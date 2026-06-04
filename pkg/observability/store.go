package observability

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	_ "modernc.org/sqlite" // CGO-free SQLite driver, registered as "sqlite"
)

// defaultObsDBPath is used when OBS_DB_PATH (or the OpenStore arg) is empty, so the
// engine persists observability snapshots without any extra configuration.
const defaultObsDBPath = "/tmp/obs.db"

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
	db *sql.DB
}

// OpenStore opens (creating if needed) the SQLite store at path and ensures the
// schema exists. An empty path falls back to defaultObsDBPath.
func OpenStore(path string) (*ObsStore, error) {
	if path == "" {
		path = defaultObsDBPath
	}
	// busy_timeout avoids "database is locked" under brief contention; MaxOpenConns(1)
	// serializes all access so concurrent Flush/History never race on the connection.
	dsn := fmt.Sprintf("file:%s?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)", path)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open obs db %q: %w", path, err)
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
			spans_json TEXT,               -- JSON array of {name, dur_us}
			PRIMARY KEY (trace_id)
		);
		CREATE INDEX IF NOT EXISTS idx_slow_tenant_ts ON slow_traces(tenant_id, ts DESC);
	`); err != nil {
		db.Close() //nolint:errcheck
		return nil, fmt.Errorf("init obs schema: %w", err)
	}
	// Migrate slow_traces tables created before the status column existed. The
	// ALTER errors with "duplicate column name" once applied, which we ignore.
	_, _ = db.Exec(`ALTER TABLE slow_traces ADD COLUMN status INTEGER NOT NULL DEFAULT 0`)
	return &ObsStore{db: db}, nil
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
	_, err = s.db.Exec(
		`INSERT OR REPLACE INTO slow_traces (trace_id, tenant_id, ts, route, total_us, status, spans_json)
			VALUES (?, ?, ?, ?, ?, ?, ?)`,
		tv.TraceID, tenantID, tv.TS, tv.Route, tv.TotalUS, tv.Status, string(spansJSON),
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
		`SELECT trace_id, ts, route, total_us, status, spans_json
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
		var spansJSON string
		var status int
		if err := rows.Scan(&tv.TraceID, &tv.TS, &tv.Route, &tv.TotalUS, &status, &spansJSON); err != nil {
			return nil, fmt.Errorf("scan slow trace: %w", err)
		}
		tv.Status = uint16(status)
		if spansJSON != "" {
			_ = json.Unmarshal([]byte(spansJSON), &tv.Spans)
		}
		out = append(out, tv)
	}
	return out, rows.Err()
}

// Prune deletes snapshots and slow traces older than the retention window.
func (s *ObsStore) Prune() error {
	cutoffSec := time.Now().Unix() - int64(retentionDays)*86400
	if _, err := s.db.Exec(`DELETE FROM obs_snapshots WHERE ts < ?`, cutoffSec); err != nil {
		return fmt.Errorf("prune snapshots: %w", err)
	}
	// slow_traces.ts is in microseconds.
	if _, err := s.db.Exec(`DELETE FROM slow_traces WHERE ts < ?`, cutoffSec*1_000_000); err != nil {
		return fmt.Errorf("prune slow traces: %w", err)
	}
	return nil
}

// Close closes the underlying database.
func (s *ObsStore) Close() error {
	return s.db.Close()
}
