package observability

import (
	"context"
	"time"
)

// Layer 4 — the database.
//
// 4a, the CLIENT side, is always observable: pgxpool.Stat() is an in-memory
// read of the pool's own counters. It is passed in as a func so this package
// never imports pgx. EmptyAcquireCount (a goroutine asked and the pool was
// empty) and EmptyAcquireWaitTime are the two signals the pool_exhausted rule
// reads; AcquiredConns == MaxConns with IdleConns == 0 is saturation.
//
// 4b, the SERVER side, is read ONLY when the database is declared local: a
// remote Postgres's RAM/CPU/IO are not observable from the app, by design,
// and the section says so instead of showing zeros.

// PoolStat is the subset of pgxpool.Stat the collector reads (pgx v5 names).
type PoolStat struct {
	MaxConns                int32
	TotalConns              int32
	AcquiredConns           int32
	IdleConns               int32
	ConstructingConns       int32
	AcquireCount            int64
	AcquireDuration         time.Duration
	EmptyAcquireCount       int64
	EmptyAcquireWaitTime    time.Duration
	CanceledAcquireCount    int64
	NewConnsCount           int64
	MaxLifetimeDestroyCount int64
	MaxIdleDestroyCount     int64
}

// DBServerProbe runs the server-side statement (pg_stat_database +
// pg_database_size + pg_stat_activity counts) and fills out. It MUST bound
// its own connection acquire (the engine's implementation uses a 250 ms
// timeout on pool.Acquire and returns an error the collector reports as
// "skipped: pool busy") — the probe never competes with requests for a
// connection.
type DBServerProbe func(ctx context.Context, out *DBServerStats) error

type dbReader struct {
	stat   func() PoolStat
	probe  DBServerProbe
	local  bool
	every  time.Duration
	last   PoolStat
	primed bool
	// server-side memory: the last successful probe is re-published between
	// probes (with its probed_at), so the card never flickers to zero.
	server     DBServerStats
	lastProbe  time.Time
	probeErr   string
	probeCount int
}

func newDBReader(stat func() PoolStat, probe DBServerProbe, local bool, every time.Duration) *dbReader {
	r := &dbReader{stat: stat, probe: probe, local: local, every: every}
	if !local {
		r.server = DBServerStats{Observable: false, Reason: "database is not on this host — its internals are not observable from the app (by design)"}
	} else if probe == nil {
		r.server = DBServerStats{Observable: false, Reason: "no server probe wired"}
	}
	return r
}

func (r *dbReader) readClient() {
	if r.stat != nil {
		r.last = r.stat()
		r.primed = true
	}
}

func (r *dbReader) fillClient(st *DBClientStats) {
	if r.stat == nil {
		return
	}
	s := r.stat()
	st.MaxConns, st.TotalConns, st.AcquiredConns, st.IdleConns, st.ConstructingConns =
		s.MaxConns, s.TotalConns, s.AcquiredConns, s.IdleConns, s.ConstructingConns
	st.AcquireCount = s.AcquireCount
	st.AcquireDurationMs = float64(s.AcquireDuration) / float64(time.Millisecond)
	st.EmptyAcquireCount = s.EmptyAcquireCount
	st.EmptyAcquireWaitMs = float64(s.EmptyAcquireWaitTime) / float64(time.Millisecond)
	st.CanceledAcquireCount = s.CanceledAcquireCount
	st.NewConnsCount = s.NewConnsCount
	st.MaxLifetimeDestroy = s.MaxLifetimeDestroyCount
	st.MaxIdleDestroy = s.MaxIdleDestroyCount
	if r.primed {
		st.AcquireDelta = max64(0, s.AcquireCount-r.last.AcquireCount)
		st.EmptyAcquireDelta = max64(0, s.EmptyAcquireCount-r.last.EmptyAcquireCount)
		if d := s.EmptyAcquireWaitTime - r.last.EmptyAcquireWaitTime; d > 0 {
			st.EmptyAcquireWaitDelta = float64(d) / float64(time.Millisecond)
		}
		if d := s.AcquireDuration - r.last.AcquireDuration; d > 0 {
			st.AcquireWaitDeltaMs = float64(d) / float64(time.Millisecond)
		}
		st.CanceledAcquireDelta = max64(0, s.CanceledAcquireCount-r.last.CanceledAcquireCount)
	}
	st.Saturated = s.MaxConns > 0 && s.AcquiredConns >= s.MaxConns && s.IdleConns == 0
	r.last = s
	r.primed = true
}

// fillServer publishes the last probe and runs a new one when due. The probe
// runs on THIS goroutine (the collector's), bounded by a context: a slow
// database makes the tick late, never a request.
func (r *dbReader) fillServer(st *DBServerStats, now time.Time) {
	if !r.local || r.probe == nil {
		*st = r.server
		return
	}
	if r.lastProbe.IsZero() || now.Sub(r.lastProbe) >= r.every {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		var fresh DBServerStats
		err := r.probe(ctx, &fresh)
		cancel()
		r.lastProbe = now
		r.probeCount++
		if err != nil {
			r.probeErr = err.Error()
			// Keep the previous numbers, say why this one was skipped.
			r.server.Reason = "skipped: " + r.probeErr
		} else {
			fresh.Observable = true
			fresh.ProbedAt = now.UnixMilli()
			if fresh.BlksHit+fresh.BlksRead > 0 {
				fresh.CacheHitRatio = float64(fresh.BlksHit) / float64(fresh.BlksHit+fresh.BlksRead)
			}
			r.server = fresh
			r.probeErr = ""
		}
	}
	*st = r.server
}
