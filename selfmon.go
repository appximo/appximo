package appximo

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/appximo/appximo/pkg/observability"
)

// Self-monitoring wiring (CENTINELA-C-S1, ADR-030): the engine-side glue
// between Config/env and observability.ResourceCollector — the knobs, the
// pgxpool.Stat adapter, the "is Postgres local?" decision and the server-side
// probe. Everything here runs at boot or on the collector's goroutine; the
// request path only sees ResourceCollector.Observe from the logging tap.

// newSelfMon builds the collector from Config + env, or nil when disabled. A
// malformed knob REFUSES TO BOOT naming the variable (OPS-13 discipline: a
// value the operator wrote and the engine could not read is never silently
// replaced by a default).
func newSelfMon(cfg Config, pool *pgxpool.Pool) (*observability.ResourceCollector, error) {
	if cfg.SelfMonDisabled || strings.EqualFold(strings.TrimSpace(os.Getenv("APPXIMO_SELFMON")), "off") {
		return nil, nil
	}
	rc := observability.ResourceConfig{
		BackgroundInterval: cfg.SelfMonInterval,
		LiveInterval:       cfg.SelfMonLiveInterval,
		DBServerLocal:      pool != nil && isLocalDBHost(pool.Config().ConnConfig.Host),
	}
	var err error
	if rc.BackgroundInterval, err = envDuration("APPXIMO_SELFMON_INTERVAL", rc.BackgroundInterval); err != nil {
		return nil, err
	}
	if rc.LiveInterval, err = envDuration("APPXIMO_SELFMON_LIVE_INTERVAL", rc.LiveInterval); err != nil {
		return nil, err
	}
	if rc.BackgroundInterval != 0 && rc.BackgroundInterval < 250*time.Millisecond {
		return nil, fmt.Errorf("APPXIMO_SELFMON_INTERVAL=%s is below the 250ms floor — the collector reads runtime/metrics, cgroup files and the pool every tick; a sub-250ms cadence is a load, not a measurement", rc.BackgroundInterval)
	}
	if rc.LiveInterval != 0 && rc.LiveInterval < 250*time.Millisecond {
		return nil, fmt.Errorf("APPXIMO_SELFMON_LIVE_INTERVAL=%s is below the 250ms floor", rc.LiveInterval)
	}
	rc.Thresholds.HighP99Ms = cfg.SelfMonHighP99Ms
	if v := strings.TrimSpace(os.Getenv("APPXIMO_SELFMON_P99_MS")); v != "" {
		f, perr := strconv.ParseFloat(v, 64)
		if perr != nil || f <= 0 {
			return nil, fmt.Errorf("APPXIMO_SELFMON_P99_MS=%q is not a positive number of milliseconds (the attribution's \"slow\" floor; default 50)", v)
		}
		rc.Thresholds.HighP99Ms = f
	}
	// /sync/mutex/wait/total:seconds accumulates ONLY while the mutex profile
	// rate is > 0 (runtime/sema.go: acquiretime is stamped on a contended
	// acquire iff mutexprofilerate > 0; the total is the sum of those). A rate
	// of 1e6 stamps every contended acquire (one nanotime per contention —
	// the cost of the metric) and records ~none of them in the profile, so the
	// lock_contention rule has its input and pprof stays effectively off. An
	// operator who set a rate of their own (a pprof session) keeps it.
	if runtime.SetMutexProfileFraction(-1) == 0 {
		runtime.SetMutexProfileFraction(1_000_000)
	}
	// Layer 5 (RESILIENCIA-S1 §D): the disk under the data and the backup's
	// liveness. Watched paths come from the env the installer writes; a bare
	// `serve`/`up` with none of them set watches nothing but its own cwd.
	if h, herr := hostConfigFromEnv(); herr != nil {
		return nil, herr
	} else {
		rc.Host = h
	}
	c := observability.NewResourceCollector(rc)
	if pool != nil {
		c.SetDB(func() observability.PoolStat {
			s := pool.Stat()
			return observability.PoolStat{
				MaxConns: s.MaxConns(), TotalConns: s.TotalConns(), AcquiredConns: s.AcquiredConns(),
				IdleConns: s.IdleConns(), ConstructingConns: s.ConstructingConns(),
				AcquireCount: s.AcquireCount(), AcquireDuration: s.AcquireDuration(),
				EmptyAcquireCount: s.EmptyAcquireCount(), EmptyAcquireWaitTime: s.EmptyAcquireWaitTime(),
				CanceledAcquireCount: s.CanceledAcquireCount(), NewConnsCount: s.NewConnsCount(),
				MaxLifetimeDestroyCount: s.MaxLifetimeDestroyCount(), MaxIdleDestroyCount: s.MaxIdleDestroyCount(),
			}
		}, dbServerProbe(pool))
	}
	return c, nil
}

// envDuration reads a Go duration from env, keeping fallback when unset; a
// value that does not parse is a boot error naming the variable.
func envDuration(name string, fallback time.Duration) (time.Duration, error) {
	v := strings.TrimSpace(os.Getenv(name))
	if v == "" {
		return fallback, nil
	}
	d, err := time.ParseDuration(v)
	if err != nil || d <= 0 {
		return 0, fmt.Errorf("%s=%q is not a positive Go duration (e.g. 10s, 500ms)", name, v)
	}
	return d, nil
}

// isLocalDBHost decides whether Postgres runs on THIS host: a loopback name/
// address or a unix socket. Only then are its pg_stat_* views read — a
// remote database's internals are declared "not observable from the app".
// A Docker-published localhost:5432 counts as local (the container shares
// the box's RAM and CPU, which is what the verdict cares about).
func isLocalDBHost(host string) bool {
	h := strings.ToLower(strings.TrimSpace(host))
	switch {
	case h == "", h == "localhost", h == "127.0.0.1", h == "::1", h == "[::1]":
		return true
	case strings.HasPrefix(h, "/"): // unix socket directory
		return true
	case strings.HasPrefix(h, "127."):
		return true
	}
	return false
}

// dbServerProbe runs the server-side statement on ONE pool connection,
// acquired with a 250 ms timeout: under pool exhaustion the probe gives up
// (reported as "skipped: pool busy") instead of competing with requests.
func dbServerProbe(pool *pgxpool.Pool) observability.DBServerProbe {
	statementsChecked, statementsPresent := false, false
	return func(ctx context.Context, out *observability.DBServerStats) error {
		actx, cancel := context.WithTimeout(ctx, 250*time.Millisecond)
		conn, err := pool.Acquire(actx)
		cancel()
		if err != nil {
			return fmt.Errorf("pool busy (acquire within 250ms failed: %v)", err)
		}
		defer conn.Release()
		const q = `SELECT pg_database_size(current_database()),
  d.blks_hit, d.blks_read, d.xact_commit, d.xact_rollback, d.deadlocks, d.temp_bytes,
  (SELECT count(*) FROM pg_stat_activity a WHERE a.datname = current_database() AND a.state = 'active'),
  (SELECT count(*) FROM pg_stat_activity a WHERE a.datname = current_database() AND a.state = 'idle in transaction'),
  (SELECT count(*) FROM pg_stat_activity a WHERE a.datname = current_database() AND a.state = 'active' AND a.wait_event IS NOT NULL),
  (SELECT count(*) FROM pg_stat_activity a WHERE a.datname = current_database())
FROM pg_stat_database d WHERE d.datname = current_database()`
		if err := conn.QueryRow(ctx, q).Scan(&out.DBSizeBytes, &out.BlksHit, &out.BlksRead, &out.XactCommit, &out.XactRollback,
			&out.Deadlocks, &out.TempBytes, &out.ActiveConns, &out.IdleInTx, &out.Waiting, &out.TotalBackends); err != nil {
			return err
		}
		if !statementsChecked {
			statementsChecked = true
			_ = conn.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM pg_extension WHERE extname = 'pg_stat_statements')`).Scan(&statementsPresent)
		}
		out.StatementsExt = statementsPresent
		return nil
	}
}

// querySpanUS returns the duration of the "query" stage of a request's spans
// (0 when it ran none) — the client-side database time the db_bound rule
// reads. A fixed-size scan over ≤ 8 spans; no allocation.
func querySpanUS(spans []observability.Span) int64 {
	var total int64
	for i := range spans {
		if spans[i].Name == "query" || spans[i].Name == "count" {
			total += int64(spans[i].DurUS)
		}
	}
	return total
}

// hostConfigFromEnv maps the installer's env onto layer 5. APPXIMO_BACKUP_DIR
// turns the backup watch on (install.sh writes it); the disk watch covers the
// files dir, the obs db's dir, the backup dir and the root — deduplicated by
// filesystem at read time. 0 disables a floor; an unparseable value refuses to
// boot, naming the variable.
func hostConfigFromEnv() (observability.HostConfig, error) {
	h := observability.HostConfig{BackupDir: strings.TrimSpace(os.Getenv("APPXIMO_BACKUP_DIR"))}
	seen := map[string]bool{}
	add := func(p string) {
		p = strings.TrimSpace(p)
		if p == "" || seen[p] {
			return
		}
		seen[p] = true
		h.DiskPaths = append(h.DiskPaths, p)
	}
	add(os.Getenv("APPXIMO_FILES_DIR"))
	if v := strings.TrimSpace(os.Getenv("OBS_DB_PATH")); v != "" {
		add(filepath.Dir(v))
	}
	add(h.BackupDir)
	if len(h.DiskPaths) > 0 {
		add("/")
	}
	var err error
	if h.BackupMaxAge, err = envDuration("APPXIMO_BACKUP_MAX_AGE", observability.DefaultBackupMaxAge); err != nil {
		return h, err
	}
	h.DiskMinFreePct = observability.DefaultDiskMinFreePct
	h.DiskMinFreeBytes = observability.DefaultDiskMinFreeBytes
	if v := strings.TrimSpace(os.Getenv("APPXIMO_DISK_MIN_FREE_PCT")); v != "" {
		f, perr := strconv.ParseFloat(v, 64)
		if perr != nil || f < 0 || f > 100 {
			return h, fmt.Errorf("APPXIMO_DISK_MIN_FREE_PCT=%q is not a percentage (0 disables the percent floor; default %.0f)", v, observability.DefaultDiskMinFreePct)
		}
		h.DiskMinFreePct = f
	}
	if v := strings.TrimSpace(os.Getenv("APPXIMO_DISK_MIN_FREE_MB")); v != "" {
		n, perr := strconv.ParseInt(v, 10, 64)
		if perr != nil || n < 0 {
			return h, fmt.Errorf("APPXIMO_DISK_MIN_FREE_MB=%q is not a non-negative integer of MiB (0 disables the byte floor; default %d)", v, observability.DefaultDiskMinFreeBytes>>20)
		}
		h.DiskMinFreeBytes = n << 20
	}
	return h, nil
}
