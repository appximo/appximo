package askspend

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	zlog "github.com/rs/zerolog/log"
)

// History is the question log (VOZ-TRAZABILIDAD-S1): one row per question —
// what was asked (as the retention policy allows), who answered it (parser /
// cache / model), what it cost, how long it took, the plan, and why the
// parser fell through. It is what makes "en qué se me va la plata" answerable:
// the most expensive phrases, the most repeated, the ones that need the model
// (VOZ-9's real number lives here, not in the lab corpus).
//
// Written OFF the answer path: Record hands the row to a buffered channel and
// one goroutine inserts; a full buffer drops the row with a log line (the
// answer is never delayed by the history). Retention is days (HistoryDays),
// pruned hourly and at boot — never an unbounded log. No IP is ever stored
// (A-53); the question text follows HistoryText (redacted by default).
type History struct {
	pool *pgxpool.Pool
	cfg  Config
	ch   chan Entry
	// dropped counts rows the buffer could not take (visible on /admin/ask).
	mu      sync.Mutex
	dropped int
	written int
}

// Entry is one question's row.
type Entry struct {
	At         time.Time
	Tenant     string
	Role       string
	UserID     string
	Question   string // already shaped by HistoryText (redacted/full/"")
	Source     string // parser | cache | model | ""
	Kind       string // answer | unclear | ambiguous | not_found | write_refused | capped | …
	Resource   string
	CostUSD    float64
	LatencyMS  int64
	ModelMS    int64
	Plan       any
	Fallback   string // the parser's reason when it fell through
	CacheHit   bool
	Corrected  bool
	ModelCalls int
}

// EnsureHistoryTable creates public.ask_history idempotently (boot).
func EnsureHistoryTable(ctx context.Context, pool *pgxpool.Pool) error {
	_, err := pool.Exec(ctx, `
CREATE TABLE IF NOT EXISTS public.ask_history (
    id            BIGSERIAL PRIMARY KEY,
    at            TIMESTAMPTZ NOT NULL DEFAULT now(),
    tenant_id     TEXT        NOT NULL,
    role          TEXT        NOT NULL DEFAULT '',
    user_id       TEXT        NOT NULL DEFAULT '',
    question      TEXT        NOT NULL DEFAULT '',
    question_hash TEXT        NOT NULL DEFAULT '',
    source        TEXT        NOT NULL DEFAULT '',
    kind          TEXT        NOT NULL DEFAULT '',
    resource      TEXT        NOT NULL DEFAULT '',
    cost_usd      DOUBLE PRECISION NOT NULL DEFAULT 0,
    latency_ms    BIGINT      NOT NULL DEFAULT 0,
    model_ms      BIGINT      NOT NULL DEFAULT 0,
    model_calls   INT         NOT NULL DEFAULT 0,
    plan          JSONB,
    fallback      TEXT        NOT NULL DEFAULT '',
    cache_hit     BOOLEAN     NOT NULL DEFAULT false,
    corrected     BOOLEAN     NOT NULL DEFAULT false
);
CREATE INDEX IF NOT EXISTS ask_history_tenant_at ON public.ask_history (tenant_id, at DESC);`)
	if err != nil {
		return fmt.Errorf("askspend: ensure history table: %w", err)
	}
	return nil
}

// NewHistory builds the log; Run starts the writer.
func NewHistory(cfg Config, pool *pgxpool.Pool) *History {
	return &History{pool: pool, cfg: cfg, ch: make(chan Entry, 1024)}
}

// Enabled reports whether questions are being logged.
func (h *History) Enabled() bool { return h != nil && h.pool != nil && h.cfg.HistoryDays > 0 }

// QuestionHash is the stable key a question is grouped by (normalized text).
func QuestionHash(normalized string) string {
	sum := sha256.Sum256([]byte(normalized))
	return hex.EncodeToString(sum[:8])
}

// Record queues one row; it never blocks the caller.
func (h *History) Record(e Entry) {
	if !h.Enabled() {
		return
	}
	select {
	case h.ch <- e:
	default:
		h.mu.Lock()
		h.dropped++
		n := h.dropped
		h.mu.Unlock()
		if n == 1 || n%100 == 0 {
			zlog.Warn().Int("dropped", n).Msg("askspend: history buffer full — rows dropped (the answer path is never delayed by the log)")
		}
	}
}

// Run is the writer + the hourly pruner. It returns when ctx is done, after
// draining what is queued.
func (h *History) Run(ctx context.Context) {
	if !h.Enabled() {
		return
	}
	h.prune(ctx)
	tick := time.NewTicker(time.Hour)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			h.drain()
			return
		case <-tick.C:
			h.prune(ctx)
		case e := <-h.ch:
			h.insert(context.Background(), e)
		}
	}
}

func (h *History) drain() {
	for {
		select {
		case e := <-h.ch:
			h.insert(context.Background(), e)
		default:
			return
		}
	}
}

func (h *History) insert(ctx context.Context, e Entry) {
	wctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	var plan []byte
	if e.Plan != nil {
		plan, _ = json.Marshal(e.Plan)
	}
	hash := ""
	if e.Question != "" {
		hash = QuestionHash(strings.ToLower(strings.TrimSpace(e.Question)))
	}
	_, err := h.pool.Exec(wctx, `INSERT INTO public.ask_history
		(at, tenant_id, role, user_id, question, question_hash, source, kind, resource, cost_usd, latency_ms, model_ms, model_calls, plan, fallback, cache_hit, corrected)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17)`,
		e.At, e.Tenant, e.Role, e.UserID, e.Question, hash, e.Source, e.Kind, e.Resource, e.CostUSD, e.LatencyMS, e.ModelMS, e.ModelCalls, nullableJSON(plan), e.Fallback, e.CacheHit, e.Corrected)
	if err != nil {
		zlog.Warn().Err(err).Msg("askspend: could not write the question history row")
		return
	}
	h.mu.Lock()
	h.written++
	h.mu.Unlock()
}

func nullableJSON(b []byte) any {
	if len(b) == 0 {
		return nil
	}
	return b
}

func (h *History) prune(ctx context.Context) {
	wctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	tag, err := h.pool.Exec(wctx, `DELETE FROM public.ask_history WHERE at < now() - ($1::int * interval '1 day')`, h.cfg.HistoryDays)
	if err != nil {
		zlog.Warn().Err(err).Msg("askspend: history prune failed")
		return
	}
	if tag.RowsAffected() > 0 {
		zlog.Info().Int64("rows", tag.RowsAffected()).Int("days", h.cfg.HistoryDays).Msg("askspend: history pruned")
	}
}

// Stats reports rows written and dropped since boot.
func (h *History) Stats() (written, dropped int) {
	if h == nil {
		return 0, 0
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.written, h.dropped
}

// ── the three lists that matter (+ the real parser share) ──

// Phrase is one aggregated question.
type Phrase struct {
	Question string  `json:"question"`
	Count    int     `json:"count"`
	CostUSD  float64 `json:"cost_usd"`
	Source   string  `json:"source,omitempty"`
	Fallback string  `json:"fallback,omitempty"`
	LastAt   string  `json:"last_at,omitempty"`
}

// Share is the real split by source over a window.
type Share struct {
	Questions int     `json:"questions"`
	Parser    int     `json:"parser"`
	Cache     int     `json:"cache"`
	Model     int     `json:"model"`
	Other     int     `json:"other"` // capped / disabled / unavailable — no plan
	CostUSD   float64 `json:"cost_usd"`
	ParserPct float64 `json:"parser_pct"`
}

// TopCost lists the phrases that cost the most (model questions), last N days.
func (h *History) TopCost(ctx context.Context, tenant string, days, n int) ([]Phrase, error) {
	return h.phrases(ctx, tenant, days, n, `source = 'model' AND cost_usd > 0`, `sum(cost_usd) DESC, count(*) DESC`)
}

// TopRepeated lists the most repeated phrases, last N days.
func (h *History) TopRepeated(ctx context.Context, tenant string, days, n int) ([]Phrase, error) {
	return h.phrases(ctx, tenant, days, n, `question <> ''`, `count(*) DESC, sum(cost_usd) DESC`)
}

// ModelFallbacks lists the phrases that went to the model, with the parser's
// reason — what the parser should learn next.
func (h *History) ModelFallbacks(ctx context.Context, tenant string, days, n int) ([]Phrase, error) {
	return h.phrases(ctx, tenant, days, n, `source = 'model'`, `count(*) DESC, max(at) DESC`)
}

func (h *History) phrases(ctx context.Context, tenant string, days, n int, where, order string) ([]Phrase, error) {
	if !h.Enabled() {
		return nil, nil
	}
	if n <= 0 {
		n = 10
	}
	if days <= 0 {
		days = h.cfg.HistoryDays
	}
	rows, err := h.pool.Query(ctx, `SELECT question, count(*), sum(cost_usd), max(source), max(fallback), to_char(max(at), 'YYYY-MM-DD HH24:MI')
		FROM public.ask_history
		WHERE tenant_id = $1 AND at >= now() - ($2::int * interval '1 day') AND `+where+`
		GROUP BY question_hash, question ORDER BY `+order+` LIMIT $3`, tenant, days, n)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Phrase
	for rows.Next() {
		var p Phrase
		var c int64
		if err := rows.Scan(&p.Question, &c, &p.CostUSD, &p.Source, &p.Fallback, &p.LastAt); err != nil {
			return nil, err
		}
		p.Count = int(c)
		out = append(out, p)
	}
	return out, rows.Err()
}

// ShareFor computes the real split by source for a tenant over the last N days.
func (h *History) ShareFor(ctx context.Context, tenant string, days int) (Share, error) {
	var s Share
	if !h.Enabled() {
		return s, nil
	}
	if days <= 0 {
		days = h.cfg.HistoryDays
	}
	rows, err := h.pool.Query(ctx, `SELECT source, count(*), sum(cost_usd) FROM public.ask_history
		WHERE tenant_id = $1 AND at >= now() - ($2::int * interval '1 day') GROUP BY source`, tenant, days)
	if err != nil {
		return s, err
	}
	defer rows.Close()
	for rows.Next() {
		var src string
		var c int64
		var usd float64
		if err := rows.Scan(&src, &c, &usd); err != nil {
			return s, err
		}
		n := int(c)
		s.Questions += n
		s.CostUSD += usd
		switch src {
		case "parser":
			s.Parser += n
		case "cache":
			s.Cache += n
		case "model":
			s.Model += n
		default:
			s.Other += n
		}
	}
	if s.Questions > 0 {
		s.ParserPct = 100 * float64(s.Parser) / float64(s.Questions)
	}
	return s, rows.Err()
}
