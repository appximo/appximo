package api

import (
	"bufio"
	"bytes"
	"context"
	"database/sql"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/miguelangel/appitools/tools/devhub/sshx"
	"github.com/miguelangel/appitools/tools/devhub/stats"

	_ "modernc.org/sqlite" // CGO-free SQLite driver, registered as "sqlite"
)

// benchDB is the process-wide handle to the DevHub statistics store. It is set
// once by InitBenchDB at startup; *sql.DB is safe for concurrent use.
var (
	benchDB   *sql.DB
	benchOnce sync.Once
)

// InitBenchDB opens (creating if needed) the SQLite store at
// <repoDir>/tools/devhub/db/devhub.db and applies the embedded schema. The
// schema is idempotent (CREATE TABLE IF NOT EXISTS / INSERT OR IGNORE), so it is
// safe to run on every boot. schemaSQL is the contents of db/schema.sql, passed
// in from the main package (which can embed it — the api package cannot reach a
// sibling directory with go:embed).
func InitBenchDB(repoDir, schemaSQL string) error {
	dbPath := filepath.Join(repoDir, "tools", "devhub", "db", "devhub.db")
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		return fmt.Errorf("mkdir db dir: %w", err)
	}
	// busy_timeout avoids spurious SQLITE_BUSY under the reader/writer mix; WAL
	// lets the API read while a run is being imported; foreign_keys honours the
	// ON DELETE CASCADE from run_datapoints.
	dsn := "file:" + dbPath + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return fmt.Errorf("open sqlite: %w", err)
	}
	if err := db.Ping(); err != nil {
		return fmt.Errorf("ping sqlite: %w", err)
	}
	if _, err := db.Exec(schemaSQL); err != nil {
		return fmt.Errorf("apply schema: %w", err)
	}
	benchOnce.Do(func() { benchDB = db })
	return nil
}

// ── k6 NDJSON parsing ────────────────────────────────────────────────────────

// k6Point is the subset of a k6 `--out json` line we care about. k6 emits NDJSON
// (one object per line): "Metric" lines define a metric, "Point" lines are
// samples. We only read http_req_duration Points; data.value is already in ms.
type k6Point struct {
	Metric string `json:"metric"`
	Type   string `json:"type"`
	Data   struct {
		Value float64           `json:"value"`
		Tags  map[string]string `json:"tags"`
	} `json:"data"`
}

// ParseK6JSON streams a k6 NDJSON file line by line (never loading the whole
// file — it can be >100MB) and returns the http_req_duration latencies in ms for
// successful requests, plus the error rate. A request is "successful" when its
// expected_response tag is not "false" (k6 tags failed responses
// expected_response:"false"); the error rate is failed / total over all
// http_req_duration points.
func ParseK6JSON(path string) (latencies []float64, errorRate float64, err error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, 0, err
	}
	defer f.Close() //nolint:errcheck

	durTag := []byte(`"http_req_duration"`)
	pointTag := []byte(`"type":"Point"`)

	sc := bufio.NewScanner(f)
	// k6 lines are small, but guard against the occasional long one.
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)

	var total, failed int
	for sc.Scan() {
		line := sc.Bytes()
		// Cheap prefilter: skip the many Metric-definition and other-metric lines
		// before paying for a JSON unmarshal.
		if !bytes.Contains(line, durTag) || !bytes.Contains(line, pointTag) {
			continue
		}
		var p k6Point
		if jsonErr := json.Unmarshal(line, &p); jsonErr != nil {
			continue
		}
		if p.Metric != "http_req_duration" || p.Type != "Point" {
			continue
		}
		total++
		if er, ok := p.Data.Tags["expected_response"]; ok && er == "false" {
			failed++
			continue
		}
		latencies = append(latencies, p.Data.Value)
	}
	if scErr := sc.Err(); scErr != nil {
		return nil, 0, scErr
	}
	if total > 0 {
		errorRate = float64(failed) / float64(total)
	}
	return latencies, errorRate, nil
}

// ── persistence ──────────────────────────────────────────────────────────────

// RunSummary is the row written to benchmark_runs plus its id.
type RunSummary struct {
	RunID     int64   `json:"run_id"`
	Label     string  `json:"label"`
	TargetRPS int     `json:"target_rps"`
	DurationS int     `json:"duration_s"`
	N         int     `json:"n_requests"`
	P50       float64 `json:"p50_ms"`
	P95       float64 `json:"p95_ms"`
	P99       float64 `json:"p99_ms"`
	ErrorRate float64 `json:"error_rate"`
	CV        float64 `json:"cv"`
}

// batchRows is the number of datapoint rows per multi-VALUES INSERT. 5000*3 =
// 15000 bound parameters, well under SQLite's 32766 limit, and all batches run
// inside one transaction so a run imports atomically and fast.
const batchRows = 5000

// SaveRun computes the summary percentiles/CV from latencies and persists the
// run plus every datapoint (batched). Returns the stored summary with its id.
func SaveRun(label string, rps, durationS int, latencies []float64, errorRate float64) (*RunSummary, error) {
	if benchDB == nil {
		return nil, errors.New("bench DB not initialized")
	}
	sum := &RunSummary{
		Label:     label,
		TargetRPS: rps,
		DurationS: durationS,
		N:         len(latencies),
		P50:       stats.Percentile(latencies, 50),
		P95:       stats.Percentile(latencies, 95),
		P99:       stats.Percentile(latencies, 99),
		ErrorRate: errorRate,
		CV:        stats.CV(latencies),
	}

	ctx := context.Background()
	tx, err := benchDB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback() //nolint:errcheck // no-op after a successful Commit

	res, err := tx.ExecContext(ctx,
		`INSERT INTO benchmark_runs
		   (label, target_rps, duration_s, n_requests, p50_ms, p95_ms, p99_ms, error_rate, cv)
		 VALUES (?,?,?,?,?,?,?,?,?)`,
		sum.Label, sum.TargetRPS, sum.DurationS, sum.N, sum.P50, sum.P95, sum.P99, sum.ErrorRate, sum.CV)
	if err != nil {
		return nil, err
	}
	runID, err := res.LastInsertId()
	if err != nil {
		return nil, err
	}
	sum.RunID = runID

	for start := 0; start < len(latencies); start += batchRows {
		end := start + batchRows
		if end > len(latencies) {
			end = len(latencies)
		}
		var sb strings.Builder
		sb.WriteString("INSERT INTO run_datapoints (run_id, seq, latency_ms) VALUES ")
		args := make([]any, 0, (end-start)*3)
		for i := start; i < end; i++ {
			if i > start {
				sb.WriteByte(',')
			}
			sb.WriteString("(?,?,?)")
			args = append(args, runID, i, latencies[i])
		}
		if _, err := tx.ExecContext(ctx, sb.String(), args...); err != nil {
			return nil, err
		}
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return sum, nil
}

// loadDatapoints reads all latencies for a run in insertion order.
func loadDatapoints(runID int64) ([]float64, error) {
	rows, err := benchDB.Query(`SELECT latency_ms FROM run_datapoints WHERE run_id = ? ORDER BY seq`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck
	var out []float64
	for rows.Next() {
		var v float64
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// rejectOutliers drops the IQR (Tukey-fence) outliers from x.
func rejectOutliers(x []float64) []float64 {
	idx := stats.IQROutliers(x)
	if len(idx) == 0 {
		return x
	}
	skip := make(map[int]bool, len(idx))
	for _, i := range idx {
		skip[i] = true
	}
	out := make([]float64, 0, len(x)-len(idx))
	for i, v := range x {
		if !skip[i] {
			out = append(out, v)
		}
	}
	return out
}

// runIDsForLabel returns the run ids in a label "group". The protocol stores its
// runs as "<label>#<i>", so a run matches the group base when its label equals
// base or starts with base+"#". The match is done in Go (not SQL LIKE) so labels
// containing the LIKE wildcards _ or % cannot mis-match.
func runIDsForLabel(base string) ([]int64, error) {
	rows, err := benchDB.Query(`SELECT id, label FROM benchmark_runs ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck
	prefix := base + "#"
	var ids []int64
	for rows.Next() {
		var id int64
		var label string
		if err := rows.Scan(&id, &label); err != nil {
			return nil, err
		}
		if label == base || strings.HasPrefix(label, prefix) {
			ids = append(ids, id)
		}
	}
	return ids, rows.Err()
}

// pooledGroup loads every run in a label group, rejects each run's IQR outliers
// independently, and pools the survivors into one sample. It also returns each
// run's raw p50 (the same value stored in benchmark_runs.p50_ms, so the caller
// can compute the between-run CV) and the number of non-empty runs.
func pooledGroup(base string) (pooled, p50s []float64, nRuns int, err error) {
	ids, err := runIDsForLabel(base)
	if err != nil {
		return nil, nil, 0, err
	}
	for _, id := range ids {
		x, err := loadDatapoints(id)
		if err != nil {
			return nil, nil, 0, err
		}
		if len(x) == 0 {
			continue
		}
		p50s = append(p50s, stats.Percentile(x, 50))
		pooled = append(pooled, rejectOutliers(x)...)
		nRuns++
	}
	return pooled, p50s, nRuns, nil
}

// CompareResult is the outcome of CompareRuns, mirroring the comparisons table
// (plus min_effect_ms, which is reported for transparency but not persisted).
type CompareResult struct {
	RunA        int64   `json:"run_a"`
	RunB        int64   `json:"run_b"`
	U           float64 `json:"u"`
	PValue      float64 `json:"p_value"`
	CILowerMs   float64 `json:"ci_lower_ms"`
	CIUpperMs   float64 `json:"ci_upper_ms"`
	MinEffectMs float64 `json:"min_effect_ms"`
	Significant bool    `json:"significant"`
	Direction   string  `json:"direction"`
	DeltaPct    float64 `json:"delta_pct"`
}

// classify computes the statistical verdict for two already-loaded latency
// samples (no DB), rejecting each sample's IQR outliers first. It is the
// per-run-rejection wrapper around verdictOf used by CompareRuns.
func classify(a, b []float64) CompareResult {
	return verdictOf(rejectOutliers(a), rejectOutliers(b))
}

// verdictOf runs Mann-Whitney U and the bootstrap median-difference CI on two
// latency samples that have ALREADY had their outliers handled, computes the p95
// delta %, and classifies the result requiring BOTH statistical and practical
// significance.
//
// Statistical significance alone (p<0.05) is not enough: at large n Mann-Whitney
// detects µs-scale host noise as a "regression" (see the S42 1-vCPU artifact). A
// real change must also move the median by more than minEffect — the larger of
// 0.5ms (absolute floor for sub-ms latencies) or 3% of median(A).
//
// It does NOT reject outliers itself: classify rejects per run before calling
// here, and CompareGroups rejects per run before pooling — re-rejecting a pooled
// distribution would discard legitimate points from the slower runs.
func verdictOf(a, b []float64) CompareResult {
	u, p := stats.MannWhitneyU(a, b)
	lo, hi := stats.BootstrapMedianDiffCI(a, b)

	p95a := stats.Percentile(a, 95)
	p95b := stats.Percentile(b, 95)
	deltaPct := 0.0
	if p95a != 0 {
		deltaPct = (p95b - p95a) / p95a * 100
	}

	medA := stats.Median(a)
	medB := stats.Median(b)
	minEffect := math.Max(0.5, 0.03*medA)
	practicallySignificant := math.Abs(medB-medA) > minEffect

	significant := p < 0.05 && practicallySignificant
	direction := "no_change"
	if significant {
		if medB < medA {
			direction = "improvement"
		} else if medB > medA {
			direction = "regression"
		}
	}

	return CompareResult{
		U: u, PValue: p,
		CILowerMs: lo, CIUpperMs: hi,
		MinEffectMs: minEffect,
		Significant: significant,
		Direction:   direction,
		DeltaPct:    deltaPct,
	}
}

// CompareRuns loads both runs' datapoints, classifies the comparison (see
// classify) and persists it. Returns the verdict.
func CompareRuns(runA, runB int64) (*CompareResult, error) {
	if benchDB == nil {
		return nil, errors.New("bench DB not initialized")
	}
	a, err := loadDatapoints(runA)
	if err != nil {
		return nil, err
	}
	b, err := loadDatapoints(runB)
	if err != nil {
		return nil, err
	}
	if len(a) == 0 || len(b) == 0 {
		return nil, fmt.Errorf("run has no datapoints (run_a=%d:%d points, run_b=%d:%d points)", runA, len(a), runB, len(b))
	}

	res := classify(a, b)
	res.RunA = runA
	res.RunB = runB

	sigInt := 0
	if res.Significant {
		sigInt = 1
	}
	if _, err := benchDB.Exec(
		`INSERT INTO comparisons
		   (run_a_id, run_b_id, u_statistic, p_value, ci_lower_ms, ci_upper_ms, significant, direction, delta_pct)
		 VALUES (?,?,?,?,?,?,?,?,?)`,
		runA, runB, res.U, res.PValue, res.CILowerMs, res.CIUpperMs, sigInt, res.Direction, res.DeltaPct); err != nil {
		return nil, err
	}
	return &res, nil
}

// GroupCompareResult is the verdict for a label-vs-label comparison. It carries
// the same statistical fields as CompareResult plus group metadata: how many
// runs each label pooled, the pooled datapoint counts, and each group's
// between-run CV (the run-to-run reproducibility of the p50). This is the
// defensible "N runs vs M runs" comparison, as opposed to single run vs run.
type GroupCompareResult struct {
	LabelA         string  `json:"label_a"`
	LabelB         string  `json:"label_b"`
	ServerA        string  `json:"server_a"` // parsed from the "{server}--" label prefix
	ServerB        string  `json:"server_b"`
	NRunsA         int     `json:"n_runs_a"`
	NRunsB         int     `json:"n_runs_b"`
	NA             int     `json:"n_a"` // pooled datapoints after per-run IQR rejection
	NB             int     `json:"n_b"`
	CVBetweenRunsA float64 `json:"cv_between_runs_a"`
	CVBetweenRunsB float64 `json:"cv_between_runs_b"`
	U              float64 `json:"u"`
	PValue         float64 `json:"p_value"`
	CILowerMs      float64 `json:"ci_lower_ms"`
	CIUpperMs      float64 `json:"ci_upper_ms"`
	MinEffectMs    float64 `json:"min_effect_ms"`
	Significant    bool    `json:"significant"`
	Direction      string  `json:"direction"`
	DeltaPct       float64 `json:"delta_pct"`
}

// CompareGroups pools every run of label A and label B (IQR-rejecting each run
// first) and runs the same verdict (Mann-Whitney + bootstrap CI + practical
// min-effect) on the two pools. Comparing a label against itself yields no_change
// (identical pools → zero median shift). Not persisted: the comparisons table is
// keyed on single run ids.
func CompareGroups(labelA, labelB string) (*GroupCompareResult, error) {
	if benchDB == nil {
		return nil, errors.New("bench DB not initialized")
	}
	pooledA, p50A, nRunsA, err := pooledGroup(labelA)
	if err != nil {
		return nil, err
	}
	pooledB, p50B, nRunsB, err := pooledGroup(labelB)
	if err != nil {
		return nil, err
	}
	if len(pooledA) == 0 || len(pooledB) == 0 {
		return nil, fmt.Errorf("empty group (label_a=%q: %d runs/%d points, label_b=%q: %d runs/%d points)",
			labelA, nRunsA, len(pooledA), labelB, nRunsB, len(pooledB))
	}

	res := verdictOf(pooledA, pooledB)
	return &GroupCompareResult{
		LabelA: labelA, LabelB: labelB,
		ServerA: serverOfLabel(labelA), ServerB: serverOfLabel(labelB),
		NRunsA: nRunsA, NRunsB: nRunsB,
		NA: len(pooledA), NB: len(pooledB),
		CVBetweenRunsA: stats.CV(p50A),
		CVBetweenRunsB: stats.CV(p50B),
		U:              res.U,
		PValue:         res.PValue,
		CILowerMs:      res.CILowerMs,
		CIUpperMs:      res.CIUpperMs,
		MinEffectMs:    res.MinEffectMs,
		Significant:    res.Significant,
		Direction:      res.Direction,
		DeltaPct:       res.DeltaPct,
	}, nil
}

// ── HTTP handlers ────────────────────────────────────────────────────────────

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v) //nolint:errcheck
}

func writeErr(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

// BenchImportHandler — POST /api/bench/import
// body: {"path":"/tmp/k6-run.json","label":"...","rps":500,"duration":30}
func BenchImportHandler(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Path     string `json:"path"`
		Label    string `json:"label"`
		RPS      int    `json:"rps"`
		Duration int    `json:"duration"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if req.Path == "" || req.Label == "" {
		writeErr(w, http.StatusBadRequest, "path and label are required")
		return
	}
	latencies, errorRate, err := ParseK6JSON(req.Path)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "parse k6 json: "+err.Error())
		return
	}
	if len(latencies) == 0 {
		writeErr(w, http.StatusBadRequest, "no http_req_duration datapoints found in "+req.Path)
		return
	}
	sum, err := SaveRun(req.Label, req.RPS, req.Duration, latencies, errorRate)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "save run: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, sum)
}

// serverOfLabel extracts the "{server}--" prefix the remote protocol puts on
// labels (S47). Labels without the prefix predate the registry — they all ran
// against this box, so they report as "105-dev".
func serverOfLabel(label string) string {
	if i := strings.Index(label, "--"); i > 0 {
		return label[:i]
	}
	return "105-dev"
}

// BenchRunsHandler — GET /api/bench/runs → list benchmark_runs (no datapoints).
func BenchRunsHandler(w http.ResponseWriter, r *http.Request) {
	rows, err := benchDB.Query(
		`SELECT id, created_at, label, target_rps, duration_s, n_requests,
		        p50_ms, p95_ms, p99_ms, error_rate, cv
		   FROM benchmark_runs ORDER BY id DESC`)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer rows.Close() //nolint:errcheck

	type run struct {
		ID        int64   `json:"id"`
		CreatedAt string  `json:"created_at"`
		Label     string  `json:"label"`
		Server    string  `json:"server"` // parsed from the "{server}--" label prefix
		TargetRPS int     `json:"target_rps"`
		DurationS int     `json:"duration_s"`
		N         int     `json:"n_requests"`
		P50       float64 `json:"p50_ms"`
		P95       float64 `json:"p95_ms"`
		P99       float64 `json:"p99_ms"`
		ErrorRate float64 `json:"error_rate"`
		CV        float64 `json:"cv"`
	}
	out := []run{}
	for rows.Next() {
		var x run
		if err := rows.Scan(&x.ID, &x.CreatedAt, &x.Label, &x.TargetRPS, &x.DurationS,
			&x.N, &x.P50, &x.P95, &x.P99, &x.ErrorRate, &x.CV); err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		x.Server = serverOfLabel(x.Label)
		out = append(out, x)
	}
	writeJSON(w, http.StatusOK, out)
}

// BenchExportHandler — GET /api/bench/export?prefix=pub-
// Streams every run whose label starts with the prefix as CSV (raw evidence
// for published benchmarks; error_rate included because the saturation
// criterion depends on it).
func BenchExportHandler(w http.ResponseWriter, r *http.Request) {
	if benchDB == nil {
		writeErr(w, http.StatusServiceUnavailable, "bench DB not initialized")
		return
	}
	prefix := r.URL.Query().Get("prefix")
	if prefix == "" {
		writeErr(w, http.StatusBadRequest, "prefix query param is required")
		return
	}
	rows, err := benchDB.Query(
		`SELECT id, label, created_at, target_rps, duration_s, n_requests,
		        p50_ms, p95_ms, p99_ms, error_rate, cv
		   FROM benchmark_runs
		  WHERE label LIKE ? ESCAPE '\'
		  ORDER BY id`,
		strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(prefix)+"%")
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer rows.Close() //nolint:errcheck

	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	cw := csv.NewWriter(w)
	cw.Write([]string{"run_id", "label", "created_at", "target_rps", "duration_s", //nolint:errcheck
		"n", "p50_ms", "p95_ms", "p99_ms", "error_rate", "cv"})
	f := func(v float64) string { return strconv.FormatFloat(v, 'f', -1, 64) }
	for rows.Next() {
		var (
			id               int64
			label, createdAt string
			rps, durS, n     int
			p50, p95, p99    float64
			errRate, cv      float64
		)
		if err := rows.Scan(&id, &label, &createdAt, &rps, &durS, &n,
			&p50, &p95, &p99, &errRate, &cv); err != nil {
			return // headers already sent; truncate rather than corrupt the CSV
		}
		cw.Write([]string{strconv.FormatInt(id, 10), label, createdAt, //nolint:errcheck
			strconv.Itoa(rps), strconv.Itoa(durS), strconv.Itoa(n),
			f(p50), f(p95), f(p99), f(errRate), f(cv)})
	}
	cw.Flush()
}

// BenchRunHistogramHandler — GET /api/bench/runs/{id}/histogram
// Returns 1ms-wide buckets aggregated in SQL (never ships raw datapoints).
func BenchRunHistogramHandler(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid run id")
		return
	}
	rows, err := benchDB.Query(
		`SELECT CAST(latency_ms AS INTEGER) AS ms, COUNT(*) AS c
		   FROM run_datapoints WHERE run_id = ?
		  GROUP BY ms ORDER BY ms`, id)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer rows.Close() //nolint:errcheck

	type bucket struct {
		Ms    int64 `json:"ms"`
		Count int64 `json:"count"`
	}
	buckets := []bucket{}
	for rows.Next() {
		var b bucket
		if err := rows.Scan(&b.Ms, &b.Count); err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		buckets = append(buckets, b)
	}
	writeJSON(w, http.StatusOK, map[string]any{"run_id": id, "bucket_width_ms": 1, "buckets": buckets})
}

// RunStats is the five-number boxplot summary of a run plus the tail percentiles
// and IQR-outlier count. Computed in Go (SQLite has no native percentile) and
// cached: a run's datapoints are immutable, so the summary never changes.
type RunStats struct {
	RunID       int64   `json:"run_id"`
	Min         float64 `json:"min"`
	Q1          float64 `json:"q1"`
	Median      float64 `json:"median"`
	Q3          float64 `json:"q3"`
	Max         float64 `json:"max"`
	P95         float64 `json:"p95"`
	P99         float64 `json:"p99"`
	N           int     `json:"n"`
	OutliersIQR int     `json:"outliers_iqr"`
}

// statsCache memoizes RunStats by run_id. Two concurrent first-requests for the
// same run may both compute (harmless: identical result, last write wins) — a
// run's datapoints never change, so no invalidation is needed.
var (
	statsCache   = map[int64]*RunStats{}
	statsCacheMu sync.Mutex
)

// runStats loads a run's datapoints and computes (or returns the cached)
// quartile summary.
func runStats(runID int64) (*RunStats, error) {
	statsCacheMu.Lock()
	cached, ok := statsCache[runID]
	statsCacheMu.Unlock()
	if ok {
		return cached, nil
	}

	x, err := loadDatapoints(runID)
	if err != nil {
		return nil, err
	}
	if len(x) == 0 {
		return nil, fmt.Errorf("run %d has no datapoints", runID)
	}
	min, max := x[0], x[0]
	for _, v := range x {
		if v < min {
			min = v
		}
		if v > max {
			max = v
		}
	}
	s := &RunStats{
		RunID:       runID,
		Min:         min,
		Q1:          stats.Percentile(x, 25),
		Median:      stats.Percentile(x, 50),
		Q3:          stats.Percentile(x, 75),
		Max:         max,
		P95:         stats.Percentile(x, 95),
		P99:         stats.Percentile(x, 99),
		N:           len(x),
		OutliersIQR: len(stats.IQROutliers(x)),
	}
	statsCacheMu.Lock()
	statsCache[runID] = s
	statsCacheMu.Unlock()
	return s, nil
}

// BenchRunStatsHandler — GET /api/bench/runs/{id}/stats
// Quartile/boxplot summary for one run (cached). Never ships raw datapoints.
func BenchRunStatsHandler(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid run id")
		return
	}
	s, err := runStats(id)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, s)
}

// BenchCompareHandler — POST /api/bench/compare  body: {"run_a":1,"run_b":2}
func BenchCompareHandler(w http.ResponseWriter, r *http.Request) {
	var req struct {
		RunA int64 `json:"run_a"`
		RunB int64 `json:"run_b"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if req.RunA == 0 || req.RunB == 0 {
		writeErr(w, http.StatusBadRequest, "run_a and run_b are required")
		return
	}
	res, err := CompareRuns(req.RunA, req.RunB)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// BenchCompareGroupsHandler — POST /api/bench/compare-groups
// body: {"label_a":"baseline-x","label_b":"candidato-y"}
func BenchCompareGroupsHandler(w http.ResponseWriter, r *http.Request) {
	var req struct {
		LabelA string `json:"label_a"`
		LabelB string `json:"label_b"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if req.LabelA == "" || req.LabelB == "" {
		writeErr(w, http.StatusBadRequest, "label_a and label_b are required")
		return
	}
	res, err := CompareGroups(req.LabelA, req.LabelB)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// BenchComparisonsHandler — GET /api/bench/comparisons → comparison history.
func BenchComparisonsHandler(w http.ResponseWriter, r *http.Request) {
	rows, err := benchDB.Query(
		`SELECT id, created_at, run_a_id, run_b_id, u_statistic, p_value,
		        ci_lower_ms, ci_upper_ms, significant, direction, delta_pct
		   FROM comparisons ORDER BY id DESC`)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer rows.Close() //nolint:errcheck

	type cmp struct {
		ID          int64   `json:"id"`
		CreatedAt   string  `json:"created_at"`
		RunA        int64   `json:"run_a_id"`
		RunB        int64   `json:"run_b_id"`
		U           float64 `json:"u_statistic"`
		PValue      float64 `json:"p_value"`
		CILowerMs   float64 `json:"ci_lower_ms"`
		CIUpperMs   float64 `json:"ci_upper_ms"`
		Significant bool    `json:"significant"`
		Direction   string  `json:"direction"`
		DeltaPct    float64 `json:"delta_pct"`
	}
	out := []cmp{}
	for rows.Next() {
		var x cmp
		var sig int
		if err := rows.Scan(&x.ID, &x.CreatedAt, &x.RunA, &x.RunB, &x.U, &x.PValue,
			&x.CILowerMs, &x.CIUpperMs, &sig, &x.Direction, &x.DeltaPct); err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		x.Significant = sig != 0
		out = append(out, x)
	}
	writeJSON(w, http.StatusOK, out)
}

// Validation patterns for BenchProtocolHandler. These bound user input that is
// passed to a shell-invoked script, so they are deliberately strict allowlists:
// a label is alnum/underscore/dash only, a duration is "<digits>s".
var (
	protocolLabelRe    = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,40}$`)
	protocolDurationRe = regexp.MustCompile(`^[0-9]{1,3}s$`)
)

// protocolScripts is the EXACT allowlist for the optional "script" field: the
// two known k6 benchmark scripts, mapped to their repo-relative paths. Anything
// else — including any path-looking value — is rejected with 400; arbitrary
// paths must never reach the protocol script's argv.
var protocolScripts = map[string]string{
	"":                    "tests/performance/sustained_2krps.js", // default: read bench
	"sustained_2krps.js":  "tests/performance/sustained_2krps.js",
	"sustained_writes.js": "tests/performance/sustained_writes.js",
}

// mintRemoteBenchToken mints a bench JWT ON the target server with a fixed
// command (the JWT secret never leaves that box; only the short-lived token
// comes back, straight into the protocol child's env — never logged).
func mintRemoteBenchToken(s *RegisteredServer, tenant string) (string, error) {
	if !benchTenantRe.MatchString(tenant) {
		return "", fmt.Errorf("invalid tenant %q", tenant)
	}
	cmd := `source /root/.appitools-secrets 2>/dev/null || source /root/.appitools-secrets-dev; ` +
		`cd /root/appitools && BIN=./appitools; [ -x "$BIN" ] || BIN=./appitools-dev; ` +
		`"$BIN" token --tenant ` + tenant + ` --secret "$JWT_SECRET" --role super_admin 2>/dev/null | tail -1`
	res, err := sshx.Run(&s.Server, cmd, 20*time.Second)
	if err != nil {
		return "", err
	}
	token := strings.TrimSpace(res.Stdout)
	if res.ExitCode != 0 || token == "" {
		return "", fmt.Errorf("token mint on %s failed (exit %d)", s.Name, res.ExitCode)
	}
	return token, nil
}

var benchTenantRe = regexp.MustCompile(`^[a-z0-9_]{1,32}$`)

// BenchProtocolHandler — POST /api/bench/protocol
// body: {"runs":3,"label":"ui-test","rate":50,"duration":"15s",
//
//	"script":"sustained_writes.js","server_id":2}
//
// script is optional (default sustained_2krps.js) and must be one of the
// protocolScripts allowlist keys — never a path.
//
// server_id is optional: when set, the run targets that registered server's
// engine (TARGET_URL) and the label is prefixed "{server_name}--" so groups
// from different servers stay distinguishable yet comparable. k6 ALWAYS runs
// on this box — the loader never competes for CPU with the system under test
// (S46 methodology).
//
// Runs scripts/bench-protocol.sh and streams its stdout/stderr as SSE, ending
// with an `event: done` carrying the exit code.
//
// SECURITY: every field is validated against a strict allowlist before use, and
// the validated values are passed as SEPARATE argv elements to exec.Command —
// never concatenated into a shell string. There is no path to `sh -c` here, so
// even if a regex were loosened the values still cannot break out into the shell.
func BenchProtocolHandler(repoDir string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Runs     int    `json:"runs"`
			Label    string `json:"label"`
			Rate     int    `json:"rate"`
			Duration string `json:"duration"`
			Script   string `json:"script"`
			ServerID int64  `json:"server_id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeErr(w, http.StatusBadRequest, "invalid JSON body")
			return
		}
		if !protocolLabelRe.MatchString(req.Label) {
			writeErr(w, http.StatusBadRequest, "label must match ^[a-zA-Z0-9_-]{1,40}$")
			return
		}
		if req.Runs < 1 || req.Runs > 20 {
			writeErr(w, http.StatusBadRequest, "runs must be an integer in [1,20]")
			return
		}
		if req.Rate < 10 || req.Rate > 1000 {
			writeErr(w, http.StatusBadRequest, "rate must be an integer in [10,1000]")
			return
		}
		if !protocolDurationRe.MatchString(req.Duration) {
			writeErr(w, http.StatusBadRequest, "duration must match ^[0-9]{1,3}s$ (e.g. 15s)")
			return
		}
		scriptPath, ok := protocolScripts[req.Script]
		if !ok {
			writeErr(w, http.StatusBadRequest, "script must be one of: sustained_2krps.js, sustained_writes.js")
			return
		}

		// Optional remote target: resolve the registered server, point the
		// (always-local) loader at its engine and namespace the label.
		label := req.Label
		extraEnv := []string(nil)
		if req.ServerID > 0 {
			srv, err := LoadServer(req.ServerID)
			if err != nil {
				writeErr(w, http.StatusNotFound, err.Error())
				return
			}
			label = srv.Name + "--" + req.Label
			// TENANT_ID drives both the k6 Host header and (local case) the
			// protocol script's own token mint — each server's data lives in
			// its own tenant (the 58 uses tenant_10, the dev box tenant_acme).
			extraEnv = append(extraEnv,
				"TARGET_URL="+srv.EngineURL(),
				"TENANT_ID="+srv.BenchTenant)
			if !srv.Local() {
				token, err := mintRemoteBenchToken(srv, srv.BenchTenant)
				if err != nil {
					writeErr(w, http.StatusBadGateway, "could not mint bench token on "+srv.Name+": "+err.Error())
					return
				}
				extraEnv = append(extraEnv, "BENCH_TOKEN="+token)
			}
		}

		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "streaming not supported", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		sw := &sseWriter{w: w, flusher: flusher}
		fmt.Fprintf(w, "event: start\ndata: {\"label\":%q,\"runs\":%d,\"rate\":%d,\"duration\":%q,\"script\":%q}\n\n",
			label, req.Runs, req.Rate, req.Duration, scriptPath)
		flusher.Flush()

		// Validated values ONLY, each as its own argv element. exec.Command does
		// not invoke a shell, so the script receives them verbatim as $1..$5.
		// scriptPath comes from the protocolScripts allowlist; the label prefix
		// is a registered (name-validated) server name, never raw input.
		cmd := exec.CommandContext(r.Context(), "bash", "scripts/bench-protocol.sh",
			strconv.Itoa(req.Runs), label, strconv.Itoa(req.Rate), req.Duration, scriptPath)
		cmd.Dir = repoDir
		if len(extraEnv) > 0 {
			cmd.Env = append(os.Environ(), extraEnv...)
		}
		cmd.Stdout = sw
		cmd.Stderr = sw
		exitCode := 0
		if err := cmd.Run(); err != nil {
			if exitErr, ok := err.(*exec.ExitError); ok {
				exitCode = exitErr.ExitCode()
			} else {
				exitCode = 1
			}
		}
		fmt.Fprintf(w, "event: done\ndata: {\"exit\":%d}\n\n", exitCode)
		flusher.Flush()
	}
}
