package summary

// The digest's memory (VOZ-DELTA-S1, ADR-034): ONE row per (tenant, role, day)
// in public.summary_snapshots — the attention counts the digest saw that day,
// the level, and what the scheduled send decided. It is deliberately NOT a
// history: the delta needs exactly one baseline (the most recent row from a
// previous day), the heartbeat needs one integer (how many scheduled runs in a
// row stayed silent), and both fit in the last two rows. Older rows are pruned
// on every upsert. A minute of Postgres a year would not notice it.

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// EnsureSnapshotTable creates public.summary_snapshots idempotently (boot).
func EnsureSnapshotTable(ctx context.Context, pool *pgxpool.Pool) error {
	_, err := pool.Exec(ctx, `
CREATE TABLE IF NOT EXISTS public.summary_snapshots (
    tenant_id     TEXT        NOT NULL,
    role          TEXT        NOT NULL,
    day           DATE        NOT NULL,
    taken_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    level         TEXT        NOT NULL,
    attention     JSONB       NOT NULL,
    scheduled_at  TIMESTAMPTZ,
    decision      TEXT,
    silent_streak INT         NOT NULL DEFAULT 0,
    PRIMARY KEY (tenant_id, role, day)
);`)
	if err != nil {
		return fmt.Errorf("summary: ensure snapshot table: %w", err)
	}
	return nil
}

// ResourceAttention is what a snapshot remembers per resource.
type ResourceAttention struct {
	States   map[string]int64 `json:"states"`
	Total    int64            `json:"total"`
	Inferred bool             `json:"inferred,omitempty"`
}

// Snapshot is one row.
type Snapshot struct {
	TenantID     string
	Role         string
	Day          string // YYYY-MM-DD
	TakenAt      time.Time
	Level        string
	Attention    map[string]ResourceAttention
	ScheduledAt  *time.Time // when the scheduled digest last evaluated on this day (nil = never that day)
	Decision     string     // "sent:<reason>" | "silent" (scheduled runs only)
	SilentStreak int        // consecutive scheduled runs that stayed silent, up to and including this day
}

// LoadBaseline returns the most recent snapshot from a day BEFORE today for
// (tenant, role), or nil when none exists (the first digest ever).
func LoadBaseline(ctx context.Context, pool *pgxpool.Pool, tenant, role, today string) (*Snapshot, error) {
	return loadOne(ctx, pool, `SELECT tenant_id, role, day::text, taken_at, level, attention, scheduled_at, decision, silent_streak
		FROM public.summary_snapshots WHERE tenant_id=$1 AND role=$2 AND day < $3::date ORDER BY day DESC LIMIT 1`, tenant, role, today)
}

// LoadDay returns today's row (nil when the digest has not run today).
func LoadDay(ctx context.Context, pool *pgxpool.Pool, tenant, role, day string) (*Snapshot, error) {
	return loadOne(ctx, pool, `SELECT tenant_id, role, day::text, taken_at, level, attention, scheduled_at, decision, silent_streak
		FROM public.summary_snapshots WHERE tenant_id=$1 AND role=$2 AND day = $3::date`, tenant, role, day)
}

func loadOne(ctx context.Context, pool *pgxpool.Pool, q string, args ...any) (*Snapshot, error) {
	var s Snapshot
	var att []byte
	err := pool.QueryRow(ctx, q, args...).Scan(&s.TenantID, &s.Role, &s.Day, &s.TakenAt, &s.Level, &att, &s.ScheduledAt, &s.Decision, &s.SilentStreak)
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("summary: load snapshot: %w", err)
	}
	if err := json.Unmarshal(att, &s.Attention); err != nil {
		return nil, fmt.Errorf("summary: decode snapshot: %w", err)
	}
	return &s, nil
}

// Upsert writes today's row and prunes everything older than the baseline
// day (keeping at most two rows per tenant/role: today and yesterday's
// baseline). The scheduled fields are written only when scheduled is true,
// so a manual `resumen` never overwrites what the morning run decided.
func Upsert(ctx context.Context, pool *pgxpool.Pool, s *Snapshot, scheduled bool, baselineDay string) error {
	att, err := json.Marshal(s.Attention)
	if err != nil {
		return err
	}
	if scheduled {
		_, err = pool.Exec(ctx, `INSERT INTO public.summary_snapshots (tenant_id, role, day, taken_at, level, attention, scheduled_at, decision, silent_streak)
			VALUES ($1,$2,$3::date,now(),$4,$5,now(),$6,$7)
			ON CONFLICT (tenant_id, role, day) DO UPDATE SET taken_at=now(), level=EXCLUDED.level, attention=EXCLUDED.attention,
			  scheduled_at=now(), decision=EXCLUDED.decision, silent_streak=EXCLUDED.silent_streak`,
			s.TenantID, s.Role, s.Day, s.Level, att, s.Decision, s.SilentStreak)
	} else {
		_, err = pool.Exec(ctx, `INSERT INTO public.summary_snapshots (tenant_id, role, day, taken_at, level, attention, silent_streak)
			VALUES ($1,$2,$3::date,now(),$4,$5,$6)
			ON CONFLICT (tenant_id, role, day) DO UPDATE SET taken_at=now(), level=EXCLUDED.level, attention=EXCLUDED.attention`,
			s.TenantID, s.Role, s.Day, s.Level, att, s.SilentStreak)
	}
	if err != nil {
		return fmt.Errorf("summary: upsert snapshot: %w", err)
	}
	if baselineDay != "" {
		_, _ = pool.Exec(ctx, `DELETE FROM public.summary_snapshots WHERE tenant_id=$1 AND role=$2 AND day < $3::date`, s.TenantID, s.Role, baselineDay)
	}
	return nil
}

// SnapshotOf captures the attention counts of ORDERED facts.
func SnapshotOf(tenant, role, day, level string, facts []Facts) *Snapshot {
	s := &Snapshot{TenantID: tenant, Role: role, Day: day, Level: level, Attention: map[string]ResourceAttention{}}
	for _, f := range facts {
		if !f.HasState {
			continue
		}
		states := map[string]int64{}
		for k, v := range f.Attention {
			states[k] = v
		}
		s.Attention[f.Resource] = ResourceAttention{States: states, Total: f.AttentionTotal, Inferred: f.AttentionInferred}
	}
	return s
}

// ApplyBaseline fills each fact's Prev* fields from the baseline snapshot.
// A resource absent from the baseline has HasPrev=false (it entered the
// digest today); a nil baseline leaves every fact without a comparison.
func ApplyBaseline(facts []Facts, base *Snapshot) {
	if base == nil {
		return
	}
	for i := range facts {
		if !facts[i].HasState {
			continue
		}
		ra, ok := base.Attention[facts[i].Resource]
		facts[i].HasPrev = true
		if !ok {
			facts[i].Prev = map[string]int64{}
			continue
		}
		facts[i].Prev = map[string]int64{}
		for k, v := range ra.States {
			facts[i].Prev[k] = v
		}
		facts[i].PrevTotal = ra.Total
	}
}
