package api

import (
	"bufio"
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

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
// samples (no DB). It rejects IQR outliers from each, runs Mann-Whitney U and the
// bootstrap median-difference CI, computes the p95 delta %, and classifies the
// result requiring BOTH statistical and practical significance.
//
// Statistical significance alone (p<0.05) is not enough: at large n Mann-Whitney
// detects µs-scale host noise as a "regression" (see the S42 1-vCPU artifact). A
// real change must also move the median by more than minEffect — the larger of
// 0.5ms (absolute floor for sub-ms latencies) or 3% of median(A).
func classify(a, b []float64) CompareResult {
	a = rejectOutliers(a)
	b = rejectOutliers(b)

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
		out = append(out, x)
	}
	writeJSON(w, http.StatusOK, out)
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
