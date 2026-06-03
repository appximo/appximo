package observability

import (
	"database/sql"
	"fmt"
	"time"

	_ "modernc.org/sqlite" // CGO-free SQLite driver, registered as "sqlite"
)

// defaultObsDBPath is used when OBS_DB_PATH (or the OpenStore arg) is empty, so the
// engine persists observability snapshots without any extra configuration.
const defaultObsDBPath = "/tmp/obs.db"

// retentionDays bounds how long snapshots are kept; Prune drops anything older.
const retentionDays = 7

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
	`); err != nil {
		db.Close() //nolint:errcheck
		return nil, fmt.Errorf("init obs schema: %w", err)
	}
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

// Prune deletes snapshots older than the retention window.
func (s *ObsStore) Prune() error {
	cutoff := time.Now().Unix() - int64(retentionDays)*86400
	if _, err := s.db.Exec(`DELETE FROM obs_snapshots WHERE ts < ?`, cutoff); err != nil {
		return fmt.Errorf("prune snapshots: %w", err)
	}
	return nil
}

// Close closes the underlying database.
func (s *ObsStore) Close() error {
	return s.db.Close()
}
