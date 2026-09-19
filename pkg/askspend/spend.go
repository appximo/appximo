// Package askspend is the WALLET GUARD of the question path (VOZ-SIN-IA-S1):
// every model call a question makes is billed, so the engine keeps a ledger
// per tenant and day, applies two caps — a per-minute cap on MODEL calls
// (human pace, not a runaway loop) and a DAILY cap in money — and tells the
// owner at 80 % of the daily cap through the same alerter every other alert
// uses. At the cap the MODEL is switched off for the rest of the day; the
// deterministic parser, the plan cache and the fixed commands keep answering
// — degradation, never a dead channel.
//
// The ledger is persisted (public.ask_spend, one row per tenant and day) so a
// restart cannot reset the day's spend, and it is what /admin/ask and the
// appximo_ask_* gauges read — the owner sees what a question costs without
// opening the provider's console.
package askspend

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"
	zlog "github.com/rs/zerolog/log"

	"github.com/appximo/appximo/pkg/observability"
)

// Config is the wallet guard's knobs.
type Config struct {
	// PerMinute caps MODEL calls per tenant per minute. Default 6: an owner
	// asking by voice needs several seconds per question; six a minute is a
	// human at full speed, thirty was a script. Parser/cache answers cost
	// nothing and are not counted here (they are ordinary reads, bounded by
	// the tenant rate limiter like any GET).
	PerMinute int
	// DailyUSD caps the model spend per tenant per day (in the declared
	// timezone). Default 0.50 — about 150 model questions at the measured
	// ≈ $0.003, ten times a heavy human day. 0 disables the daily cap (an
	// explicit decision, never a typo: a non-number refuses to boot).
	DailyUSD float64
	// AlertPct is the share of DailyUSD at which ONE alert per tenant per
	// day goes out ("80 % del techo, van US$ 0,40"). Default 80; 0 disables.
	AlertPct int
	// UserDailyUSD caps the model spend per USER (the JWT subject) per day
	// (VOZ-TRAZABILIDAD-S1). It COMPOSES with DailyUSD: whichever is reached
	// first wins; at the user cap only that user degrades to parser/cache/
	// fixed commands, the rest of the tenant goes on. Default 0 = off — a
	// single-owner app must not meet a second, silent ceiling; an operator
	// with several people asking turns it on.
	UserDailyUSD float64
	// Trace adds to the human TEXT of every reply who answered (parser /
	// cache / model), how long it took, what it cost, and — when the parser
	// fell through — why. Never to `speech` (a voice that reads "tres
	// centavos" every time wears out in two days). Default off: it is for
	// whoever administers, not for a shop owner.
	Trace bool
	// HistoryDays is the retention of public.ask_history (one row per
	// question). Default 30; 0 disables the history entirely.
	HistoryDays int
	// HistoryText decides what of the question TEXT the history keeps:
	// "redacted" (default — the proper names the engine identified are
	// replaced by [nombre]: the shape the parser needs to learn from, without
	// the person), "full", or "none" (the plan only). A-53 discipline: no IP
	// is ever stored, and personal data is opt-in, not default.
	HistoryText string
}

// Defaults are what an unset environment means.
var Defaults = Config{PerMinute: 6, DailyUSD: 0.50, AlertPct: 80, UserDailyUSD: 0, Trace: false, HistoryDays: 30, HistoryText: "redacted"}

const (
	envPerMinute    = "APPXIMO_ASK_PER_MINUTE"
	envDailyUSD     = "APPXIMO_ASK_DAILY_USD"
	envAlertPct     = "APPXIMO_ASK_ALERT_PCT"
	envUserDailyUSD = "APPXIMO_ASK_DAILY_USD_PER_USER"
	envTrace        = "APPXIMO_ASK_TRACE"
	envHistoryDays  = "APPXIMO_ASK_HISTORY_DAYS"
	envHistoryText  = "APPXIMO_ASK_HISTORY_TEXT"
)

// ConfigFromEnv reads the knobs, FAIL-FAST: a value that is not a number, a
// negative, a per-minute of 0 or an alert percentage outside 0..100 refuses
// to boot naming the variable — a cap that is silently ignored is worse than
// no cap.
func ConfigFromEnv() (Config, error) {
	c := Defaults
	if v := strings.TrimSpace(os.Getenv(envPerMinute)); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 {
			return c, fmt.Errorf("appximo: %s=%q must be a positive integer (model questions per tenant per minute; default %d)", envPerMinute, v, Defaults.PerMinute)
		}
		c.PerMinute = n
	}
	if v := strings.TrimSpace(os.Getenv(envDailyUSD)); v != "" {
		f, err := strconv.ParseFloat(v, 64)
		if err != nil || f < 0 {
			return c, fmt.Errorf("appximo: %s=%q must be a number of US dollars ≥ 0 (the daily model spend cap per tenant; 0 disables; default %.2f)", envDailyUSD, v, Defaults.DailyUSD)
		}
		c.DailyUSD = f
	}
	if v := strings.TrimSpace(os.Getenv(envAlertPct)); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 || n > 100 {
			return c, fmt.Errorf("appximo: %s=%q must be an integer 0..100 (percent of the daily cap that triggers the alert; 0 disables; default %d)", envAlertPct, v, Defaults.AlertPct)
		}
		c.AlertPct = n
	}
	if v := strings.TrimSpace(os.Getenv(envUserDailyUSD)); v != "" {
		f, err := strconv.ParseFloat(v, 64)
		if err != nil || f < 0 {
			return c, fmt.Errorf("appximo: %s=%q must be a number of US dollars ≥ 0 (the daily model spend cap per USER; 0 = off, the default)", envUserDailyUSD, v)
		}
		c.UserDailyUSD = f
	}
	if v := strings.ToLower(strings.TrimSpace(os.Getenv(envTrace))); v != "" {
		switch v {
		case "on", "true", "1", "yes":
			c.Trace = true
		case "off", "false", "0", "no":
			c.Trace = false
		default:
			return c, fmt.Errorf("appximo: %s=%q must be on or off (adds who answered, latency and cost to every reply's text; default off)", envTrace, v)
		}
	}
	if v := strings.TrimSpace(os.Getenv(envHistoryDays)); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 || n > 3650 {
			return c, fmt.Errorf("appximo: %s=%q must be an integer 0..3650 (days the question history is kept; 0 disables; default %d)", envHistoryDays, v, Defaults.HistoryDays)
		}
		c.HistoryDays = n
	}
	if v := strings.ToLower(strings.TrimSpace(os.Getenv(envHistoryText))); v != "" {
		switch v {
		case "redacted", "full", "none":
			c.HistoryText = v
		default:
			return c, fmt.Errorf("appximo: %s=%q must be redacted, full or none (what of the question text the history keeps; default redacted)", envHistoryText, v)
		}
	}
	return c, nil
}

// Day is one tenant's row for one day.
type Day struct {
	Tenant     string  `json:"tenant"`
	Day        string  `json:"day"`
	Questions  int     `json:"questions"`
	ModelCalls int     `json:"model_calls"`
	USD        float64 `json:"usd"`
	Parser     int     `json:"parser"`
	Cache      int     `json:"cache"`
	Alerted    bool    `json:"alerted"`
	Capped     bool    `json:"capped"`
}

// Ledger keeps today's rows in memory, writes them through to Postgres, and
// decides whether the model may be called.
type Ledger struct {
	cfg     Config
	pool    *pgxpool.Pool // nil = memory only (tests)
	loc     *time.Location
	alerter observability.Alerter
	appName string
	now     func() time.Time

	mu      sync.Mutex
	days    map[string]*Day          // tenant → today's row (dropped when the day changes)
	users   map[string]*Day          // tenant|user → today's row (the per-user cap)
	minutes map[string]*minuteWindow // tenant → model calls this minute
	loaded  map[string]bool
	hist    *History // nil = no history
}

type minuteWindow struct {
	start time.Time
	n     int
}

// New builds a ledger. pool may be nil (no persistence); alerter may be nil.
func New(cfg Config, pool *pgxpool.Pool, loc *time.Location, alerter observability.Alerter, appName string) *Ledger {
	if loc == nil {
		loc = time.Local
	}
	return &Ledger{cfg: cfg, pool: pool, loc: loc, alerter: alerter, appName: appName, now: time.Now,
		days: map[string]*Day{}, users: map[string]*Day{}, minutes: map[string]*minuteWindow{}, loaded: map[string]bool{}}
}

// SetHistory attaches the question history (nil = none).
func (l *Ledger) SetHistory(h *History) { l.hist = h }

// History returns the attached history, if any.
func (l *Ledger) History() *History { return l.hist }

// Config returns the effective knobs.
func (l *Ledger) Config() Config { return l.cfg }

// EnsureTable creates public.ask_spend idempotently (boot).
func EnsureTable(ctx context.Context, pool *pgxpool.Pool) error {
	_, err := pool.Exec(ctx, `
CREATE TABLE IF NOT EXISTS public.ask_spend (
    tenant_id   TEXT             NOT NULL,
    day         DATE             NOT NULL,
    questions   INT              NOT NULL DEFAULT 0,
    model_calls INT              NOT NULL DEFAULT 0,
    usd         DOUBLE PRECISION NOT NULL DEFAULT 0,
    parser      INT              NOT NULL DEFAULT 0,
    cache       INT              NOT NULL DEFAULT 0,
    alerted     BOOLEAN          NOT NULL DEFAULT false,
    capped      BOOLEAN          NOT NULL DEFAULT false,
    updated_at  TIMESTAMPTZ      NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, day)
);
CREATE TABLE IF NOT EXISTS public.ask_spend_user (
    tenant_id   TEXT             NOT NULL,
    user_id     TEXT             NOT NULL,
    day         DATE             NOT NULL,
    questions   INT              NOT NULL DEFAULT 0,
    model_calls INT              NOT NULL DEFAULT 0,
    usd         DOUBLE PRECISION NOT NULL DEFAULT 0,
    capped      BOOLEAN          NOT NULL DEFAULT false,
    updated_at  TIMESTAMPTZ      NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, user_id, day)
);`)
	if err != nil {
		return fmt.Errorf("askspend: ensure table: %w", err)
	}
	return nil
}

func (l *Ledger) today() string { return l.now().In(l.loc).Format("2006-01-02") }

// row returns today's row for tenant, loading it from Postgres once per day
// (a restart must not forget the morning's spend). Caller holds l.mu.
func (l *Ledger) row(ctx context.Context, tenant string) *Day {
	day := l.today()
	if d := l.days[tenant]; d != nil && d.Day == day {
		return d
	}
	d := &Day{Tenant: tenant, Day: day}
	if l.pool != nil {
		err := l.pool.QueryRow(ctx, `SELECT questions, model_calls, usd, parser, cache, alerted, capped
			FROM public.ask_spend WHERE tenant_id=$1 AND day=$2`, tenant, day).
			Scan(&d.Questions, &d.ModelCalls, &d.USD, &d.Parser, &d.Cache, &d.Alerted, &d.Capped)
		if err != nil && !strings.Contains(err.Error(), "no rows") {
			zlog.Warn().Err(err).Str("tenant", tenant).Msg("askspend: could not load today's row — counting from zero until the next write succeeds")
		}
	}
	l.days[tenant] = d
	return d
}

// userRow returns today's row for (tenant, user), loading it once per day.
// Caller holds l.mu.
func (l *Ledger) userRow(ctx context.Context, tenant, user string) *Day {
	day := l.today()
	key := tenant + "|" + user
	if d := l.users[key]; d != nil && d.Day == day {
		return d
	}
	d := &Day{Tenant: tenant, Day: day}
	if l.pool != nil {
		err := l.pool.QueryRow(ctx, `SELECT questions, model_calls, usd, capped FROM public.ask_spend_user WHERE tenant_id=$1 AND user_id=$2 AND day=$3`, tenant, user, day).
			Scan(&d.Questions, &d.ModelCalls, &d.USD, &d.Capped)
		if err != nil && !strings.Contains(err.Error(), "no rows") {
			zlog.Warn().Err(err).Str("tenant", tenant).Msg("askspend: could not load the user's row — counting from zero")
		}
	}
	l.users[key] = d
	return d
}

// Verdict is what Allow says about calling the model for one more question.
type Verdict struct {
	ModelOK bool
	// Reason is "" | "capped" (the tenant's daily cap) | "user_capped" (the
	// user's daily cap) | "minute" (the per-minute cap).
	Reason string
	Day    Day
	// User is the asking user's row today (zero when no per-user cap).
	User Day
}

// Allow decides, BEFORE a question is answered, whether the model may be
// called for it. It never blocks the question itself: the parser and the
// cache are free and always allowed; the caller degrades when ModelOK is
// false. A "minute" verdict is a 429 for the caller (retry in seconds); a
// "capped" verdict lasts until the day changes.
func (l *Ledger) Allow(ctx context.Context, tenant, user string) Verdict {
	l.mu.Lock()
	defer l.mu.Unlock()
	d := l.row(ctx, tenant)
	v := Verdict{ModelOK: true, Day: *d}
	if l.cfg.DailyUSD > 0 && d.USD >= l.cfg.DailyUSD {
		v.ModelOK, v.Reason = false, "capped"
		return v
	}
	// The per-user cap composes: whichever is reached first wins, and only
	// this user degrades.
	if l.cfg.UserDailyUSD > 0 && user != "" {
		u := l.userRow(ctx, tenant, user)
		v.User = *u
		if u.USD >= l.cfg.UserDailyUSD {
			v.ModelOK, v.Reason = false, "user_capped"
			return v
		}
	}
	now := l.now()
	w := l.minutes[tenant]
	if w == nil || now.Sub(w.start) >= time.Minute {
		return v
	}
	if w.n >= l.cfg.PerMinute {
		v.ModelOK, v.Reason = false, "minute"
	}
	return v
}

// Record accounts one answered question: its source (parser | cache | model)
// and, for a model question, the calls it made and their cost. It updates
// the row, writes it through, and fires the 80 % / cap alerts once per day.
func (l *Ledger) Record(ctx context.Context, tenant, user, source string, modelCalls int, usd float64) Day {
	l.mu.Lock()
	d := l.row(ctx, tenant)
	d.Questions++
	var urow *Day
	if l.cfg.UserDailyUSD > 0 && user != "" {
		urow = l.userRow(ctx, tenant, user)
		urow.Questions++
		if modelCalls > 0 {
			urow.ModelCalls += modelCalls
			urow.USD += usd
		}
	}
	switch source {
	case "parser":
		d.Parser++
	case "cache":
		d.Cache++
	}
	if modelCalls > 0 {
		d.ModelCalls += modelCalls
		d.USD += usd
		now := l.now()
		w := l.minutes[tenant]
		if w == nil || now.Sub(w.start) >= time.Minute {
			w = &minuteWindow{start: now}
			l.minutes[tenant] = w
		}
		w.n++
	}
	var alerts []observability.Alert
	if l.cfg.DailyUSD > 0 {
		pct := d.USD / l.cfg.DailyUSD * 100
		if l.cfg.AlertPct > 0 && !d.Alerted && pct >= float64(l.cfg.AlertPct) {
			d.Alerted = true
			alerts = append(alerts, l.alert("ask_spend_warning", observability.LevelWarning, tenant, d, pct))
		}
		if !d.Capped && d.USD >= l.cfg.DailyUSD {
			d.Capped = true
			alerts = append(alerts, l.alert("ask_spend_capped", observability.LevelCritical, tenant, d, pct))
		}
	}
	var usnap *Day
	if urow != nil {
		if !urow.Capped && urow.USD >= l.cfg.UserDailyUSD {
			urow.Capped = true
			a := observability.Alert{TenantID: tenant, Level: observability.LevelWarning, Kind: "ask_user_capped",
				Message: fmt.Sprintf("user %s reached the per-user daily model spend cap (US$ %.3f of %.3f, %d model calls) — that user is on parser/cache/fixed commands until tomorrow; the tenant goes on", user, urow.USD, l.cfg.UserDailyUSD, urow.ModelCalls),
				Fields:  map[string]string{"user": user, "usd": fmt.Sprintf("%.3f", urow.USD), "cap": fmt.Sprintf("%.3f", l.cfg.UserDailyUSD), "model_calls": strconv.Itoa(urow.ModelCalls), "day": urow.Day}}
			alerts = append(alerts, a)
		}
		c := *urow
		usnap = &c
	}
	snap := *d
	l.mu.Unlock()

	l.persist(ctx, snap)
	if usnap != nil {
		l.persistUser(ctx, user, *usnap)
	}
	for _, a := range alerts {
		if l.alerter != nil {
			_ = l.alerter.Send(context.Background(), a)
		}
		zlog.Warn().Str("tenant", tenant).Str("kind", a.Kind).Float64("usd", snap.USD).Float64("cap", l.cfg.DailyUSD).Msg("askspend: " + a.Message)
	}
	return snap
}

func (l *Ledger) alert(kind, level, tenant string, d *Day, pct float64) observability.Alert {
	msg := fmt.Sprintf("model spend for the questions reached %.0f%% of the daily cap (US$ %.3f of %.2f, %d model calls)", pct, d.USD, l.cfg.DailyUSD, d.ModelCalls)
	if kind == "ask_spend_capped" {
		msg = fmt.Sprintf("daily model spend cap reached (US$ %.3f of %.2f, %d model calls) — the model is off until tomorrow; the parser, the plan cache and the fixed commands keep answering", d.USD, l.cfg.DailyUSD, d.ModelCalls)
	}
	return observability.Alert{
		TenantID: tenant, Level: level, Kind: kind, Message: msg,
		Fields: map[string]string{
			"usd":         fmt.Sprintf("%.3f", d.USD),
			"cap":         fmt.Sprintf("%.2f", l.cfg.DailyUSD),
			"pct":         fmt.Sprintf("%.0f", pct),
			"model_calls": strconv.Itoa(d.ModelCalls),
			"questions":   strconv.Itoa(d.Questions),
			"day":         d.Day,
		},
	}
}

func (l *Ledger) persist(ctx context.Context, d Day) {
	if l.pool == nil {
		return
	}
	wctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
	defer cancel()
	_, err := l.pool.Exec(wctx, `INSERT INTO public.ask_spend (tenant_id, day, questions, model_calls, usd, parser, cache, alerted, capped, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,now())
		ON CONFLICT (tenant_id, day) DO UPDATE SET questions=EXCLUDED.questions, model_calls=EXCLUDED.model_calls, usd=EXCLUDED.usd,
		  parser=EXCLUDED.parser, cache=EXCLUDED.cache, alerted=EXCLUDED.alerted, capped=EXCLUDED.capped, updated_at=now()`,
		d.Tenant, d.Day, d.Questions, d.ModelCalls, d.USD, d.Parser, d.Cache, d.Alerted, d.Capped)
	if err != nil {
		zlog.Warn().Err(err).Str("tenant", d.Tenant).Msg("askspend: could not persist the day's spend (kept in memory)")
	}
}

func (l *Ledger) persistUser(ctx context.Context, user string, d Day) {
	if l.pool == nil {
		return
	}
	wctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
	defer cancel()
	_, err := l.pool.Exec(wctx, `INSERT INTO public.ask_spend_user (tenant_id, user_id, day, questions, model_calls, usd, capped, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,now())
		ON CONFLICT (tenant_id, user_id, day) DO UPDATE SET questions=EXCLUDED.questions, model_calls=EXCLUDED.model_calls, usd=EXCLUDED.usd, capped=EXCLUDED.capped, updated_at=now()`,
		d.Tenant, user, d.Day, d.Questions, d.ModelCalls, d.USD, d.Capped)
	if err != nil {
		zlog.Warn().Err(err).Str("tenant", d.Tenant).Msg("askspend: could not persist the user's spend (kept in memory)")
	}
}

// Today returns today's rows for every tenant seen since boot (memory).
func (l *Ledger) Today() []Day {
	l.mu.Lock()
	defer l.mu.Unlock()
	day := l.today()
	out := make([]Day, 0, len(l.days))
	for _, d := range l.days {
		if d.Day == day {
			out = append(out, *d)
		}
	}
	return out
}

// Month sums the ledger for the current month per tenant, from Postgres
// (nil pool → only what memory has). Used by /admin/ask and the gauges.
func (l *Ledger) Month(ctx context.Context) (map[string]Day, error) {
	out := map[string]Day{}
	if l.pool == nil {
		for _, d := range l.Today() {
			out[d.Tenant] = d
		}
		return out, nil
	}
	first := l.now().In(l.loc).Format("2006-01") + "-01"
	rows, err := l.pool.Query(ctx, `SELECT tenant_id, sum(questions), sum(model_calls), sum(usd), sum(parser), sum(cache)
		FROM public.ask_spend WHERE day >= $1 GROUP BY tenant_id`, first)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var d Day
		var q, m, p, c int64
		if err := rows.Scan(&d.Tenant, &q, &m, &d.USD, &p, &c); err != nil {
			return nil, err
		}
		d.Questions, d.ModelCalls, d.Parser, d.Cache = int(q), int(m), int(p), int(c)
		d.Day = first[:7]
		out[d.Tenant] = d
	}
	return out, rows.Err()
}

// ── /metrics ──────────────────────────────────────────────────────────────

// Collector exposes the ledger as gauges: appximo_ask_spend_usd{tenant,window}
// (day|month), appximo_ask_questions{tenant,source} today (parser|cache|model),
// and the caps. Read on scrape only; the question path pays nothing for it.
type Collector struct {
	l         *Ledger
	cacheStat func() (hits, misses, size int)
	spend     *prometheus.Desc
	questions *prometheus.Desc
	capDesc   *prometheus.Desc
	cacheDesc *prometheus.Desc
}

// NewCollector wraps the ledger (and an optional plan-cache stats reader).
func NewCollector(l *Ledger, cacheStat func() (hits, misses, size int)) *Collector {
	return &Collector{l: l, cacheStat: cacheStat,
		spend:     prometheus.NewDesc("appximo_ask_spend_usd", "Model spend of the question path, in US dollars", []string{"tenant", "window"}, nil),
		questions: prometheus.NewDesc("appximo_ask_questions", "Questions answered today by source (parser = no model call, cache = plan reused, model = a paid call)", []string{"tenant", "source"}, nil),
		capDesc:   prometheus.NewDesc("appximo_ask_daily_cap_usd", "The daily model spend cap per tenant (0 = none)", nil, nil),
		cacheDesc: prometheus.NewDesc("appximo_ask_plan_cache", "Plan cache counters since boot", []string{"what"}, nil),
	}
}

func (c *Collector) Describe(ch chan<- *prometheus.Desc) {
	ch <- c.spend
	ch <- c.questions
	ch <- c.capDesc
	ch <- c.cacheDesc
}

func (c *Collector) Collect(ch chan<- prometheus.Metric) {
	ch <- prometheus.MustNewConstMetric(c.capDesc, prometheus.GaugeValue, c.l.cfg.DailyUSD)
	for _, d := range c.l.Today() {
		ch <- prometheus.MustNewConstMetric(c.spend, prometheus.GaugeValue, d.USD, d.Tenant, "day")
		ch <- prometheus.MustNewConstMetric(c.questions, prometheus.GaugeValue, float64(d.Parser), d.Tenant, "parser")
		ch <- prometheus.MustNewConstMetric(c.questions, prometheus.GaugeValue, float64(d.Cache), d.Tenant, "cache")
		ch <- prometheus.MustNewConstMetric(c.questions, prometheus.GaugeValue, float64(d.Questions-d.Parser-d.Cache), d.Tenant, "model")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if month, err := c.l.Month(ctx); err == nil {
		for tenant, d := range month {
			ch <- prometheus.MustNewConstMetric(c.spend, prometheus.GaugeValue, d.USD, tenant, "month")
		}
	}
	if c.cacheStat != nil {
		h, m, s := c.cacheStat()
		ch <- prometheus.MustNewConstMetric(c.cacheDesc, prometheus.GaugeValue, float64(h), "hits")
		ch <- prometheus.MustNewConstMetric(c.cacheDesc, prometheus.GaugeValue, float64(m), "misses")
		ch <- prometheus.MustNewConstMetric(c.cacheDesc, prometheus.GaugeValue, float64(s), "size")
	}
}
