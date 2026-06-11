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

-- S47: registered servers for remote ops (metrics / bench / deploy). The SSH
-- private key is referenced by key_path on THIS box's filesystem — never stored
-- here. host_key is the TOFU-pinned public host key (authorized_keys format),
-- recorded on first connect and required to match on every later connect.
-- admin_key_env names the devhub-process env var holding that server's engine
-- admin key (secrets live in the process env, not in SQLite).
CREATE TABLE IF NOT EXISTS servers (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    name          TEXT UNIQUE NOT NULL,      -- "58-prod", "105-dev"
    host          TEXT NOT NULL,
    port          INTEGER DEFAULT 22,
    ssh_user      TEXT DEFAULT 'root',
    key_path      TEXT NOT NULL,             -- path on the devhub box
    engine_port   INTEGER DEFAULT 8080,
    admin_key_env TEXT,
    is_production INTEGER DEFAULT 0,
    bench_tenant  TEXT DEFAULT 'acme',       -- tenant the bench token is minted for
    host_key      TEXT,                      -- TOFU, first connect
    secrets_path  TEXT DEFAULT '/root/.appitools-secrets', -- remote file fetch-admin-key reads
    start_script  TEXT DEFAULT '/tmp/start_prod.sh',
    binary_path   TEXT DEFAULT '/root/appitools/appitools',
    log_path      TEXT DEFAULT '/tmp/appitools.log',
    created_at    DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS deploys (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    server_id   INTEGER NOT NULL REFERENCES servers(id),
    sha         TEXT NOT NULL,
    status      TEXT NOT NULL,               -- running | success | failed
    detail      TEXT,
    started_at  DATETIME DEFAULT CURRENT_TIMESTAMP,
    finished_at DATETIME
);

CREATE INDEX IF NOT EXISTS idx_deploys_server ON deploys(server_id);

-- S47b: audit trail of secret USAGE (operation + when). Values never land here
-- — they live age-encrypted in /root/.devhub/secrets.age, outside SQLite.
CREATE TABLE IF NOT EXISTS secret_access (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    server_id  INTEGER NOT NULL,
    operation  TEXT NOT NULL,     -- 'metrics_scrape', 'fetch_from_server', 'manual_set', 'rotate', ...
    ts         DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_secret_access_server ON secret_access(server_id);

INSERT OR IGNORE INTO baselines (name, rps, notes) VALUES
('nestjs-v10-no-auth', 1092, 'NestJS colapsó a 1092 RPS en benchmark S34. Sin JWT/RBAC/multi-tenant.'),
('appitools-slo', 2000, 'SLO: p95<15ms, error_rate<1% @ 2000 RPS');
