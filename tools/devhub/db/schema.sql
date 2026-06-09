PRAGMA journal_mode = WAL;
PRAGMA foreign_keys = ON;

CREATE TABLE IF NOT EXISTS benchmark_runs (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    created_at   DATETIME DEFAULT CURRENT_TIMESTAMP,
    label        TEXT    NOT NULL,
    target_rps   INTEGER,
    duration_s   INTEGER,
    n_requests   INTEGER,
    p50_ms       REAL,
    p95_ms       REAL,
    p99_ms       REAL,
    error_rate   REAL,
    cv           REAL,
    is_baseline  INTEGER DEFAULT 0,
    notes        TEXT
);

CREATE TABLE IF NOT EXISTS run_datapoints (
    run_id      INTEGER NOT NULL REFERENCES benchmark_runs(id) ON DELETE CASCADE,
    seq         INTEGER NOT NULL,
    latency_ms  REAL    NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_dp_run ON run_datapoints(run_id);

CREATE TABLE IF NOT EXISTS comparisons (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    created_at  DATETIME DEFAULT CURRENT_TIMESTAMP,
    run_a_id    INTEGER NOT NULL REFERENCES benchmark_runs(id),
    run_b_id    INTEGER NOT NULL REFERENCES benchmark_runs(id),
    u_statistic REAL,
    p_value     REAL,
    ci_lower_ms REAL,
    ci_upper_ms REAL,
    significant INTEGER DEFAULT 0,
    direction   TEXT,
    delta_pct   REAL
);

CREATE TABLE IF NOT EXISTS baselines (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    name       TEXT UNIQUE NOT NULL,
    p95_ms     REAL,
    p99_ms     REAL,
    error_rate REAL,
    rps        REAL,
    notes      TEXT
);

INSERT OR IGNORE INTO baselines (name, rps, notes) VALUES
('nestjs-v10-no-auth', 1092, 'NestJS colapsó a 1092 RPS en benchmark S34. Sin JWT/RBAC/multi-tenant.'),
('appitools-slo', 2000, 'SLO: p95<15ms, error_rate<1% @ 2000 RPS');
