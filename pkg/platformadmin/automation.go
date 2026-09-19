package platformadmin

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/appximo/appximo/pkg/outbox"
	"github.com/appximo/appximo/pkg/resilience"
	"github.com/appximo/appximo/pkg/schema"
	"github.com/appximo/appximo/pkg/workflows"
)

// The automation subsystem's admin surface (AUTOMATIZACION-S1). The outbox was
// the engine's best-built, worst-delivered piece: transactional enqueue,
// at-least-once delivery — and no route, metric or alert. These handlers are the
// route half; /metrics (outbox.Collector) and the alerter are the other two.

// outboxRow is one pending/failed row as the admin sees it: enough to diagnose
// (topic, age, attempts, THE ERROR) without dumping business payloads by default.
type outboxRow struct {
	ID        int64           `json:"id"`
	TenantID  string          `json:"tenant_id"`
	Topic     string          `json:"topic"`
	State     string          `json:"state"`
	Attempts  int             `json:"attempts"`
	CreatedAt time.Time       `json:"created_at"`
	LastError *string         `json:"last_error,omitempty"`
	Payload   json.RawMessage `json:"payload,omitempty"`
}

// handleOutbox answers GET /admin/outbox: the queue's health snapshot (fresh —
// this is an operator asking, not a scrape), the failed rows WITH their error
// message, the oldest pending rows, and the circuit breakers' state. Platform
// token or admin key (requirePlatform).
func (s *Service) handleOutbox(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// Fresh aggregate pass (same queries as the poller, same bounded cost).
	stats, err := outbox.NewObserver(s.pool, outbox.ObserverConfig{}).Collect(ctx)
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "outbox stats unavailable: " + err.Error()})
		return
	}

	list := func(state string, limit int) ([]outboxRow, error) {
		rows, qerr := s.pool.Query(ctx, `
			SELECT id, tenant_id, topic, state, attempts, created_at, last_error, payload
			FROM public.outbox
			WHERE state = $1
			ORDER BY created_at
			LIMIT $2`, state, limit)
		if qerr != nil {
			return nil, qerr
		}
		defer rows.Close()
		var out []outboxRow
		for rows.Next() {
			var or outboxRow
			var payload []byte
			if serr := rows.Scan(&or.ID, &or.TenantID, &or.Topic, &or.State, &or.Attempts, &or.CreatedAt, &or.LastError, &payload); serr != nil {
				return nil, serr
			}
			or.Payload = append(json.RawMessage(nil), payload...)
			out = append(out, or)
		}
		return out, rows.Err()
	}

	failed, err := list("failed", 50)
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "outbox rows unavailable: " + err.Error()})
		return
	}
	pending, err := list("pending", 50)
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "outbox rows unavailable: " + err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"stats":    stats,
		"failed":   failed,  // parked rows, oldest first, each with last_error
		"pending":  pending, // oldest 50 pending rows
		"breakers": resilience.BreakerSnapshot(),
	})
}

// workflowView is one (tenant, workflow) as GET /admin/workflows reports it:
// what is declared, when it last ran and how it went, when it runs next —
// the observability the outbox never had, present from the executor's day one.
type workflowView struct {
	Tenant   string     `json:"tenant_id"`
	Workflow string     `json:"workflow"`
	Trigger  string     `json:"trigger"` // "event:<topic>" | "cron:<spec> (<tz>)"
	Overlap  string     `json:"overlap"`
	Role     string     `json:"role,omitempty"`
	NextRun  *time.Time `json:"next_run,omitempty"` // cron only
	LastRun  *runView   `json:"last_run,omitempty"`
	Failed24 int64      `json:"failed_24h"`
	Runs24   int64      `json:"runs_24h"`
}

type runView struct {
	Status     string          `json:"status"`
	StartedAt  time.Time       `json:"started_at"`
	FinishedAt *time.Time      `json:"finished_at,omitempty"`
	Error      *string         `json:"error,omitempty"`
	Detail     json.RawMessage `json:"detail,omitempty"`
}

// handleWorkflows answers GET /admin/workflows: every tenant's declared
// workflows with their run health, plus the recent failed runs.
func (s *Service) handleWorkflows(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	rows, err := s.pool.Query(ctx, `SELECT id, json_schema FROM public.tenants WHERE json_schema IS NOT NULL`)
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "tenants unavailable: " + err.Error()})
		return
	}
	type wfKey struct{ tenant, wf string }
	var views []*workflowView
	byKey := map[wfKey]*workflowView{}
	for rows.Next() {
		var tenant string
		var raw []byte
		if serr := rows.Scan(&tenant, &raw); serr != nil {
			rows.Close()
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": serr.Error()})
			return
		}
		sch, lerr := schema.LoadFromBytes(raw)
		if lerr != nil || len(sch.Workflows) == 0 {
			continue
		}
		wfs, cerr := workflows.Compile(sch)
		if cerr != nil {
			views = append(views, &workflowView{Tenant: tenant, Workflow: "(broken)", Trigger: "compile error: " + cerr.Error()})
			continue
		}
		for _, wf := range wfs {
			v := &workflowView{Tenant: tenant, Workflow: wf.Name, Overlap: "skip", Role: wf.Role}
			if wf.OverlapAllow {
				v.Overlap = "allow"
			}
			if wf.TriggerType == "event" {
				v.Trigger = "event:" + wf.Topic
			} else {
				v.Trigger = "cron:" + wf.CronSpec + " (" + wf.Location.String() + ")"
			}
			views = append(views, v)
			byKey[wfKey{tenant, wf.Name}] = v
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	// Cron schedules → next_run.
	if crows, cerr := s.pool.Query(ctx, `SELECT workflow, tenant_id, next_run FROM public.workflow_cron`); cerr == nil {
		for crows.Next() {
			var wf, tenant string
			var next *time.Time
			if crows.Scan(&wf, &tenant, &next) == nil {
				if v, ok := byKey[wfKey{tenant, wf}]; ok {
					v.NextRun = next
				}
			}
		}
		crows.Close()
	}

	// Last run + 24h counters per (tenant, workflow).
	if rrows, rerr := s.pool.Query(ctx, `
		SELECT DISTINCT ON (tenant_id, workflow)
		       tenant_id, workflow, status, started_at, finished_at, error, detail
		FROM public.workflow_runs
		ORDER BY tenant_id, workflow, started_at DESC`); rerr == nil {
		for rrows.Next() {
			var tenant, wf string
			var rv runView
			var detail []byte
			if rrows.Scan(&tenant, &wf, &rv.Status, &rv.StartedAt, &rv.FinishedAt, &rv.Error, &detail) == nil {
				rv.Detail = append(json.RawMessage(nil), detail...)
				if v, ok := byKey[wfKey{tenant, wf}]; ok {
					v.LastRun = &rv
				}
			}
		}
		rrows.Close()
	}
	if srows, serr := s.pool.Query(ctx, `
		SELECT tenant_id, workflow,
		       count(*) FILTER (WHERE status = 'failed'),
		       count(*)
		FROM public.workflow_runs
		WHERE started_at > now() - interval '24 hours'
		GROUP BY tenant_id, workflow`); serr == nil {
		for srows.Next() {
			var tenant, wf string
			var failed, total int64
			if srows.Scan(&tenant, &wf, &failed, &total) == nil {
				if v, ok := byKey[wfKey{tenant, wf}]; ok {
					v.Failed24, v.Runs24 = failed, total
				}
			}
		}
		srows.Close()
	}

	// The recent failed runs, newest first — the error is the point.
	var failedRuns []map[string]any
	if frows, ferr := s.pool.Query(ctx, `
		SELECT id, tenant_id, workflow, trigger, started_at, error
		FROM public.workflow_runs
		WHERE status = 'failed'
		ORDER BY started_at DESC
		LIMIT 50`); ferr == nil {
		for frows.Next() {
			var id int64
			var tenant, wf, trigger string
			var started time.Time
			var errMsg *string
			if frows.Scan(&id, &tenant, &wf, &trigger, &started, &errMsg) == nil {
				failedRuns = append(failedRuns, map[string]any{
					"id": id, "tenant_id": tenant, "workflow": wf,
					"trigger": trigger, "started_at": started, "error": errMsg,
				})
			}
		}
		frows.Close()
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"workflows":   views,
		"failed_runs": failedRuns,
	})
}

// SetAskStats installs the reader of the question path's live state (the
// ledger's today rows, the plan cache counters, the caps) — app.go binds it;
// nil leaves /admin/ask reading only the persisted ledger.
func (s *Service) SetAskStats(fn func(ctx context.Context) map[string]any) { s.askStats = fn }

// handleAsk answers GET /admin/ask (VOZ-SIN-IA-S1): what the questions cost —
// per tenant, today and this month (from public.ask_spend, which survives a
// restart), how many were answered by the parser / the cache / the model, the
// caps in force, and the plan cache's hit rate. The owner reads what a
// question costs here, never in the provider's console.
func (s *Service) handleAsk(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	type row struct {
		Tenant     string  `json:"tenant"`
		Day        string  `json:"day"`
		Questions  int     `json:"questions"`
		ModelCalls int     `json:"model_calls"`
		USD        float64 `json:"usd"`
		Parser     int     `json:"parser"`
		Cache      int     `json:"cache"`
		Capped     bool    `json:"capped"`
	}
	query := func(sql string, args ...any) ([]row, error) {
		rows, err := s.pool.Query(ctx, sql, args...)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		var out []row
		for rows.Next() {
			var x row
			if err := rows.Scan(&x.Tenant, &x.Day, &x.Questions, &x.ModelCalls, &x.USD, &x.Parser, &x.Cache, &x.Capped); err != nil {
				return nil, err
			}
			out = append(out, x)
		}
		return out, rows.Err()
	}
	today, err := query(`SELECT tenant_id, day::text, questions, model_calls, usd, parser, cache, capped
		FROM public.ask_spend WHERE day = current_date ORDER BY tenant_id`)
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "ask spend unavailable: " + err.Error()})
		return
	}
	month, err := query(`SELECT tenant_id, to_char(date_trunc('month', current_date), 'YYYY-MM'), sum(questions)::int, sum(model_calls)::int, sum(usd), sum(parser)::int, sum(cache)::int, bool_or(capped)
		FROM public.ask_spend WHERE day >= date_trunc('month', current_date) GROUP BY tenant_id ORDER BY tenant_id`)
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "ask spend unavailable: " + err.Error()})
		return
	}
	last30, err := query(`SELECT tenant_id, day::text, questions, model_calls, usd, parser, cache, capped
		FROM public.ask_spend WHERE day >= current_date - 30 ORDER BY day DESC, tenant_id`)
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "ask spend unavailable: " + err.Error()})
		return
	}
	if today == nil {
		today = []row{}
	}
	if month == nil {
		month = []row{}
	}
	if last30 == nil {
		last30 = []row{}
	}
	out := map[string]any{"today": today, "month": month, "last_30_days": last30}
	if s.askStats != nil {
		for k, v := range s.askStats(ctx) {
			out[k] = v
		}
	}
	writeJSON(w, http.StatusOK, out)
}
