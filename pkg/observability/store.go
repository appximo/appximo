package observability

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite" // CGO-free SQLite driver, registered as "sqlite"
)

// defaultObsDBPath is where the observability store lives when OBS_DB_PATH (or the
// OpenStore arg) is empty. It is a PERSISTENT, standard Linux application-data path
// (not /tmp) so the trace/snapshot history survives a process or container restart
// out of the box — the same root the file store uses (/var/lib/appitools). Its
// parent directory is created on open; if it cannot be created or written OpenStore
// falls back to an ephemeral temp file and logs a WARNING (observability is
// best-effort — a bad path never crashes the engine).
const defaultObsDBPath = "/var/lib/appitools/obs.db"

// retentionDays bounds how long snapshots are kept; Prune drops anything older.
const retentionDays = 7

// SlowTraceThresholdUS is the per-request duration above which a trace is
// persisted to slow_traces (Phase 1: a fixed 50ms, not a per-tenant p95).
const SlowTraceThresholdUS = 50_000

// PersistErrors, when true, persists every error trace (status >= 400) regardless
// of latency, so a fast 401/403/422/500 is still captured for debugging.
const PersistErrors = true

// ShouldPersistTrace reports whether a request's trace should be written to
// slow_traces. A trace is persisted when it is either slow (DurUS above the
// threshold) or — when PersistErrors — an error response (Status >= 400).
//
// (A third heuristic, "any span with dur_us == 0", was considered for detecting a
// prematurely-cut pipeline but rejected: sub-microsecond stages such as the RBAC
// map lookup legitimately round to 0µs, so it would persist almost every request.
// "How far the pipeline got" is instead conveyed by which spans are present —
// error paths now mark their own stage.)
func ShouldPersistTrace(s Sample) bool {
	if s.DurUS > SlowTraceThresholdUS {
		return true
	}
	if PersistErrors && s.Status >= 400 {
		return true
	}
	return false
}

// Snapshot is one persisted point-in-time observation for a tenant.
type Snapshot struct {
	TenantID   string  `json:"tenant_id"`
	TS         int64   `json:"ts"` // unix seconds
	P50US      int64   `json:"p50_us"`
	P95US      int64   `json:"p95_us"`
	ErrorRatio float64 `json:"error_ratio"`
	BurnRate   float64 `json:"burn_rate"`
	SLOStatus  string  `json:"slo_status"`
}

// ObsStore persists observability snapshots to a local SQLite file (modernc, no CGO).
type ObsStore struct {
	db   *sql.DB
	path string // resolved on-disk location (after default/fallback resolution)
}

// Path returns the resolved on-disk location the store opened. It may differ from
// the requested path when an unwritable directory forced the ephemeral fallback.
func (s *ObsStore) Path() string { return s.path }

// OpenStore opens (creating if needed) the SQLite store at path and ensures the
// schema exists. An empty path selects defaultObsDBPath (a persistent location).
// The parent directory is created if missing; if it cannot be created or written,
// the store falls back to an ephemeral temp file so observability still works — a
// bad path is logged as a WARNING, never a boot failure. A resolved path on an
// ephemeral filesystem (/tmp or a tmpfs) is also logged, so the operator knows the
// history will not survive a restart.
func OpenStore(path string) (*ObsStore, error) {
	resolved, warning := planObsDBPath(path)
	if warning != "" {
		log.Printf("WARNING: %s", warning)
	}
	// busy_timeout avoids "database is locked" under brief contention; MaxOpenConns(1)
	// serializes all access so concurrent Flush/History never race on the connection.
	dsn := fmt.Sprintf("file:%s?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)", resolved)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open obs db %q: %w", resolved, err)
	}
	db.SetMaxOpenConns(1)

	if _, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS obs_snapshots (
			tenant_id   TEXT    NOT NULL,
			ts          INTEGER NOT NULL,
			p50_us      INTEGER,
			p95_us      INTEGER,
			error_ratio REAL,
			burn_rate   REAL,
			slo_status  TEXT,
			PRIMARY KEY (tenant_id, ts)
		);
		CREATE INDEX IF NOT EXISTS idx_tenant_ts ON obs_snapshots(tenant_id, ts DESC);

		CREATE TABLE IF NOT EXISTS slow_traces (
			trace_id   TEXT    NOT NULL,
			tenant_id  TEXT    NOT NULL,
			ts         INTEGER NOT NULL,   -- request start, unix microseconds
			route      TEXT,
			total_us   INTEGER,
			status     INTEGER NOT NULL DEFAULT 0, -- HTTP status (so errors are visible/colored)
			err_msg    TEXT,               -- error message for 4xx/5xx ("" otherwise)
			stack_json TEXT,               -- JSON array of StackFrame, 500s only
			ip         TEXT,               -- client IP
			user_agent TEXT,               -- raw User-Agent
			browser    TEXT,               -- parsed browser
			os         TEXT,               -- parsed OS
			country    TEXT,               -- ISO country code (GeoLite2)
			method     TEXT,               -- HTTP method (for curl reconstruction)
			full_url   TEXT,               -- scheme://host/path?query
			headers_json TEXT,             -- filtered request headers (map)
			spans_json TEXT,               -- JSON array of {name, dur_us}
			PRIMARY KEY (trace_id)
		);
		CREATE INDEX IF NOT EXISTS idx_slow_tenant_ts ON slow_traces(tenant_id, ts DESC);
	`); err != nil {
		db.Close() //nolint:errcheck
		return nil, fmt.Errorf("init obs schema: %w", err)
	}
	// Migrate slow_traces tables created before these columns existed. Each ALTER
	// errors with "duplicate column name" once applied, which we ignore (idempotent).
	for _, col := range []string{
		`status INTEGER NOT NULL DEFAULT 0`, `err_msg TEXT`, `stack_json TEXT`,
		`ip TEXT`, `user_agent TEXT`, `browser TEXT`, `os TEXT`, `country TEXT`,
		`method TEXT`, `full_url TEXT`, `headers_json TEXT`,
	} {
		_, _ = db.Exec(`ALTER TABLE slow_traces ADD COLUMN ` + col)
	}
	return &ObsStore{db: db, path: resolved}, nil
}

// resolveObsDBPath maps an empty requested path to the persistent default; a
// non-empty path is honored verbatim. Pure (no filesystem access).
func resolveObsDBPath(requested string) string {
	if requested == "" {
		return defaultObsDBPath
	}
	return requested
}

// planObsDBPath resolves where the observability SQLite store should live and
// ensures its parent directory exists and is writable. An empty requested path
// selects the persistent default (defaultObsDBPath). If the chosen directory
// cannot be created or written, it falls back to an ephemeral file under the
// system temp dir so the store still opens — observability is best-effort and a
// bad path must never crash the engine. The returned warning (empty for a healthy
// persistent location) is meant to be logged by the caller.
func planObsDBPath(requested string) (path string, warning string) {
	path = resolveObsDBPath(requested)
	if err := ensureWritableDir(filepath.Dir(path)); err != nil {
		fallback := filepath.Join(os.TempDir(), "appitools-obs.db")
		_ = ensureWritableDir(filepath.Dir(fallback)) // best effort; the temp dir normally exists
		return fallback, fmt.Sprintf(
			"observability store: cannot use %q (%v); falling back to ephemeral %q — "+
				"history will NOT survive restarts; set OBS_DB_PATH to a writable persistent path",
			path, err, fallback)
	}
	if isEphemeralPath(path) {
		return path, fmt.Sprintf(
			"observability store at %s is ephemeral — history will NOT survive restarts; "+
				"set OBS_DB_PATH to a persistent path for production", path)
	}
	return path, ""
}

// ensureWritableDir creates dir (and any parents) and verifies a file can actually
// be created inside it. MkdirAll alone is not enough: it can succeed on a directory
// that is then unwritable (restrictive perms, a read-only mount), so a temp-file
// probe confirms real writability. The probe is created and removed immediately.
func ensureWritableDir(dir string) error {
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return err
	}
	probe, err := os.CreateTemp(dir, ".obswrite-*")
	if err != nil {
		return err
	}
	name := probe.Name()
	_ = probe.Close()
	_ = os.Remove(name)
	return nil
}

// isEphemeralPath reports whether path lives on storage whose contents do not
// survive a reboot or container restart: anything under /tmp (commonly cleared by
// systemd-tmpfiles or backed by a container tmpfs), or a detected RAM-backed
// filesystem (tmpfs/ramfs). The /tmp check is a pure string test (so a path can be
// classified before it exists); the tmpfs probe is best-effort and Linux-only
// (see isTmpfs).
func isEphemeralPath(path string) bool {
	abs := filepath.Clean(path)
	if a, err := filepath.Abs(path); err == nil {
		abs = a
	}
	if abs == "/tmp" || strings.HasPrefix(abs, "/tmp/") {
		return true
	}
	// The file itself may not exist yet, so probe its parent directory (created by
	// ensureWritableDir before this is reached on the open path).
	return isTmpfs(filepath.Dir(abs))
}

// Flush writes (or replaces) one snapshot for the given tenant.
func (s *ObsStore) Flush(tenantID string, snap Snapshot) error {
	_, err := s.db.Exec(
		`INSERT OR REPLACE INTO obs_snapshots
			(tenant_id, ts, p50_us, p95_us, error_ratio, burn_rate, slo_status)
			VALUES (?, ?, ?, ?, ?, ?, ?)`,
		tenantID, snap.TS, snap.P50US, snap.P95US, snap.ErrorRatio, snap.BurnRate, snap.SLOStatus,
	)
	if err != nil {
		return fmt.Errorf("flush snapshot: %w", err)
	}
	return nil
}

// History returns the tenant's snapshots from the last `hours`, newest first.
func (s *ObsStore) History(tenantID string, hours int) ([]Snapshot, error) {
	cutoff := time.Now().Unix() - int64(hours)*3600
	rows, err := s.db.Query(
		`SELECT tenant_id, ts, p50_us, p95_us, error_ratio, burn_rate, slo_status
			FROM obs_snapshots
			WHERE tenant_id = ? AND ts > ?
			ORDER BY ts DESC`,
		tenantID, cutoff,
	)
	if err != nil {
		return nil, fmt.Errorf("query history: %w", err)
	}
	defer rows.Close()

	out := []Snapshot{}
	for rows.Next() {
		var snap Snapshot
		if err := rows.Scan(&snap.TenantID, &snap.TS, &snap.P50US, &snap.P95US,
			&snap.ErrorRatio, &snap.BurnRate, &snap.SLOStatus); err != nil {
			return nil, fmt.Errorf("scan history row: %w", err)
		}
		out = append(out, snap)
	}
	return out, rows.Err()
}

// SaveSlowTrace persists a single slow request's trace (id, route, total, span
// breakdown). tv.TS is the request start in unix microseconds, consistent with
// the ring's Sample.Start. Designed to be called asynchronously from the request
// path so it never blocks the response.
func (s *ObsStore) SaveSlowTrace(tenantID string, tv TraceView) error {
	spansJSON, err := json.Marshal(tv.Spans)
	if err != nil {
		return fmt.Errorf("marshal spans: %w", err)
	}
	// stack_json is stored only when there is a stack (500s); "" otherwise.
	stackJSON := ""
	if len(tv.Stack) > 0 {
		b, mErr := json.Marshal(tv.Stack)
		if mErr != nil {
			return fmt.Errorf("marshal stack: %w", mErr)
		}
		stackJSON = string(b)
	}
	headersJSON := ""
	if len(tv.Headers) > 0 {
		if b, mErr := json.Marshal(tv.Headers); mErr == nil {
			headersJSON = string(b)
		}
	}
	_, err = s.db.Exec(
		`INSERT OR REPLACE INTO slow_traces
			(trace_id, tenant_id, ts, route, total_us, status, err_msg, stack_json,
			 ip, user_agent, browser, os, country, method, full_url, headers_json, spans_json)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		tv.TraceID, tenantID, tv.TS, tv.Route, tv.TotalUS, tv.Status, tv.ErrMsg, stackJSON,
		tv.IP, tv.UserAgent, tv.Browser, tv.OS, tv.Country, tv.Method, tv.FullURL, headersJSON, string(spansJSON),
	)
	if err != nil {
		return fmt.Errorf("save slow trace: %w", err)
	}
	return nil
}

// SlowTraces returns the tenant's persisted slow traces from the last `hours`,
// newest first (capped at 200), with spans decoded back into TraceViews.
func (s *ObsStore) SlowTraces(tenantID string, hours int) ([]TraceView, error) {
	cutoff := (time.Now().Unix() - int64(hours)*3600) * 1_000_000 // µs, matches ts unit
	rows, err := s.db.Query(
		`SELECT trace_id, ts, route, total_us, status, err_msg, stack_json,
			ip, user_agent, browser, os, country, method, full_url, headers_json, spans_json
			FROM slow_traces
			WHERE tenant_id = ? AND ts > ?
			ORDER BY ts DESC
			LIMIT 200`,
		tenantID, cutoff,
	)
	if err != nil {
		return nil, fmt.Errorf("query slow traces: %w", err)
	}
	defer rows.Close()

	out := []TraceView{}
	for rows.Next() {
		var tv TraceView
		var errMsg, stackJSON, spansJSON sql.NullString
		var ip, ua, browser, os, country, method, fullURL, headersJSON sql.NullString
		var status int
		if err := rows.Scan(&tv.TraceID, &tv.TS, &tv.Route, &tv.TotalUS, &status, &errMsg, &stackJSON,
			&ip, &ua, &browser, &os, &country, &method, &fullURL, &headersJSON, &spansJSON); err != nil {
			return nil, fmt.Errorf("scan slow trace: %w", err)
		}
		tv.Status = uint16(status)
		tv.ErrMsg = errMsg.String
		tv.IP, tv.UserAgent, tv.Browser, tv.OS, tv.Country = ip.String, ua.String, browser.String, os.String, country.String
		tv.Method, tv.FullURL = method.String, fullURL.String
		if spansJSON.String != "" {
			_ = json.Unmarshal([]byte(spansJSON.String), &tv.Spans)
		}
		if stackJSON.String != "" {
			_ = json.Unmarshal([]byte(stackJSON.String), &tv.Stack)
		}
		if headersJSON.String != "" {
			_ = json.Unmarshal([]byte(headersJSON.String), &tv.Headers)
		}
		out = append(out, tv)
	}
	return out, rows.Err()
}

// maxSlowTraceRows bounds the slow_traces table independently of the time window.
// Every request with status >= 400 (incl. 401 floods) or > the slow threshold gets
// a row, so a sustained error flood could grow the table without bound WITHIN the
// 7-day window. Past this many rows the oldest are dropped, keeping the on-disk
// size bounded (~tens of MB) regardless of flood volume. Var only so tests can
// shrink it; never mutated at runtime.
var maxSlowTraceRows = 50_000

// Prune enforces retention: it deletes snapshots and slow traces older than the
// retention window, AND caps slow_traces at maxSlowTraceRows (oldest dropped). It
// must be called periodically, not only at startup — otherwise a long-running
// server never reclaims space.
func (s *ObsStore) Prune() error {
	cutoffSec := time.Now().Unix() - int64(retentionDays)*86400
	if _, err := s.db.Exec(`DELETE FROM obs_snapshots WHERE ts < ?`, cutoffSec); err != nil {
		return fmt.Errorf("prune snapshots: %w", err)
	}
	// slow_traces.ts is in microseconds.
	if _, err := s.db.Exec(`DELETE FROM slow_traces WHERE ts < ?`, cutoffSec*1_000_000); err != nil {
		return fmt.Errorf("prune slow traces: %w", err)
	}
	// Hard row cap: drop everything older than the newest maxSlowTraceRows. Bounds
	// disk under an error flood that stays within the time window.
	if _, err := s.db.Exec(
		`DELETE FROM slow_traces WHERE ts < (
			SELECT MIN(ts) FROM (SELECT ts FROM slow_traces ORDER BY ts DESC LIMIT ?)
		)`, maxSlowTraceRows); err != nil {
		return fmt.Errorf("cap slow traces: %w", err)
	}
	return nil
}

// Close closes the underlying database.
func (s *ObsStore) Close() error {
	return s.db.Close()
}
