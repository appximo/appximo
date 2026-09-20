package codegen

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	zlog "github.com/rs/zerolog/log"

	"github.com/appximo/appximo/pkg/aigen"
	"github.com/appximo/appximo/pkg/ask"
	"github.com/appximo/appximo/pkg/askspend"
	"github.com/appximo/appximo/pkg/db"
	pkghandlers "github.com/appximo/appximo/pkg/handlers"
	"github.com/appximo/appximo/pkg/query"
	"github.com/appximo/appximo/pkg/rbac"
	"github.com/appximo/appximo/pkg/schema"
	"github.com/appximo/appximo/pkg/summary"
	"github.com/appximo/appximo/pkg/tenant"
)

// registerAskRoute mounts POST /api/ask (VOZ-PREGUNTAS-S1, ADR-033): a
// natural-language READ question — "cuántas citas tengo hoy", "qué pedidos
// están sin pagar" — answered with the engine's own numbers.
//
// The seam is deliberately narrow. A language model receives the schema's
// VOCABULARY for the asking role (resources, fields, enum members, states —
// never a row) and the question, and returns a PLAN over a closed grammar
// (pkg/ask). The engine validates every name in the plan against the schema
// and refuses what does not exist (one correction round, then "no entendí"),
// resolves proper names against the rows that exist (one → used and said
// back; several → asked; none → said), and executes the plan through the
// SAME query builders the generated REST handlers use — query.BuildQuery /
// BuildAggregate with the role's row condition and field allowlist — so a
// question can never read what a browser request by that role could not. The
// reply is a template over the result: the model never writes a number.
//
// Read-only by construction: no plan kind writes; a write intent is refused
// with the pointer to the next step (VOZ-4). Like /api/transaction and
// /api/summary it is a reserved segment the RBAC middleware passes through
// (rbac.AskRoute); this handler authorizes the resources itself.
//
// The model: pkg/aigen's raw /v1/messages transport (no new dependency),
// ANTHROPIC_API_KEY, the cheap model by default (APPXIMO_ASK_MODEL overrides),
// temperature 0, ≤ APPXIMO_ASK_TIMEOUT (8 s) per call. Without a key the
// route answers 503 `ask_disabled` naming the variable — never a silent
// failure — and the bot's help says so. APPXIMO_ASK=off disables it
// explicitly. Every answer carries tokens, an approximate USD cost and the
// wall time, so the owner sees what a question costs.
//
// Since VOZ-SIN-IA-S1 most questions never reach the model: the deterministic
// parser (pkg/ask/parser.go) answers what the schema alone can settle, the
// plan cache answers a repeated question, and the spend ledger (pkg/askspend,
// installed per app through AskRuntime) caps model calls per minute and the
// model spend per day — at the cap the model is off, the rest keeps answering.
func registerAskRoute(r chi.Router, s *schema.APISchema, tdb *db.TenantDB, policy *rbac.Policy, tw *txWriter) {
	model, modelName := askModelFromEnv()
	timeout := askTimeoutFromEnv()
	// Voice WRITES (VOZ-ESCRITURAS-S1): create + update through the
	// transaction cores, every one confirmed by the owner first. Off by
	// APPXIMO_ASK_WRITES=off; without a writer (bare BuildRouter callers)
	// the channel reads only.
	if !askWritesEnabled() {
		tw = nil
	}

	var vocabMu sync.Mutex
	vocabs := map[string]*ask.Vocabulary{} // per role: the RBAC is boot-static

	vocabFor := func(evalCtx rbac.EvalContext, appName string) *ask.Vocabulary {
		key := evalCtx.Role
		vocabMu.Lock()
		defer vocabMu.Unlock()
		if v, ok := vocabs[key]; ok {
			return v
		}
		readCheck := func(resource string) (bool, []string) {
			ev := policy.Evaluate(evalCtx, resource, "read")
			return ev.Allowed, ev.AllowedFields
		}
		var v *ask.Vocabulary
		if tw != nil {
			v = ask.BuildWithWrites(s, appName, readCheck, func(resource string) (bool, bool) {
				return policy.Evaluate(evalCtx, resource, "create").Allowed, policy.Evaluate(evalCtx, resource, "update").Allowed
			})
		} else {
			v = ask.Build(s, appName, readCheck)
		}
		vocabs[key] = v
		return v
	}

	// bindWrites gives deps the writer + the pending store for THIS caller:
	// the pending key is tenant|role|user — a pending is never visible to
	// another identity, and a role change between the plan and the
	// confirmation re-evaluates from scratch.
	bindWrites := func(deps *ask.Deps, rt *AskRuntime, tc *tenant.TenantCtx, evalCtx rbac.EvalContext, userID string) {
		if tw == nil || rt == nil || rt.Pending == nil {
			return
		}
		deps.Write = &askWriter{tw: tw, tc: tc, evalCtx: evalCtx}
		deps.Pending = rt.Pending
		if userID != "" {
			deps.PendingKey = tc.ID + "|" + evalCtx.Role + "|" + userID
		}
	}

	r.Post("/api/"+rbac.AskRoute, func(w http.ResponseWriter, req *http.Request) {
		tc := tenant.MustFromCtx(req.Context())
		evalCtx := rbac.EvalContextFromRequest(req)
		// Reserved pass-through: this handler is the deny point for an
		// anonymous caller — and, unlike /api/summary, ALSO for the reserved
		// $public role of an rbac.public block: a question spends a model
		// call, and the binary-diff gate caught an anonymous caller being
		// answered (and billed) through a schema that declares public reads.
		// A question needs an identity; public data stays readable by GET.
		if (evalCtx.Role == "" && evalCtx.UserID == "" && evalCtx.ExternalClientID == "") || evalCtx.Role == rbac.PublicRoleName {
			writeJSONErr(w, http.StatusForbidden, "forbidden: a question requires an authenticated identity (the public role may read, not ask)")
			return
		}
		body, err := io.ReadAll(io.LimitReader(req.Body, 4<<10))
		if err != nil {
			writeJSONErr(w, http.StatusBadRequest, "could not read the body")
			return
		}
		var in struct {
			Q         string `json:"q"`
			PendingID string `json:"pending_id"`
			Answer    string `json:"answer"`
		}
		if err := json.Unmarshal(body, &in); err != nil || (strings.TrimSpace(in.Q) == "" && in.PendingID == "") {
			writeJSONErr(w, http.StatusBadRequest, `body must be {"q": "<the question>"} (or {"pending_id": "...", "answer": "sí"|"no"} to resolve a pending write)`)
			return
		}
		if in.PendingID != "" && in.Q == "" {
			in.Q = in.Answer
		}
		if len([]rune(in.Q)) > 500 {
			writeJSONErr(w, http.StatusBadRequest, "the question is longer than 500 characters")
			return
		}

		appName := os.Getenv("APPXIMO_ALERT_APP_NAME")
		if appName == "" {
			appName = s.Name
		}
		exec := &askExecutor{s: s, tdb: tdb, policy: policy, evalCtx: evalCtx, tc: tc}
		ctx, cancel := context.WithTimeout(req.Context(), 2*timeout+5*time.Second)
		defer cancel()

		// The wallet guard (VOZ-SIN-IA-S1): before anything, may the MODEL be
		// called for this tenant right now? The parser and the plan cache are
		// free and always run; only a question they cannot solve meets the
		// verdict — a daily cap answers "capped" until tomorrow, a per-minute
		// cap answers "capped" for a few seconds. Neither touches the fixed
		// commands, which never come here.
		deps := ask.Deps{
			Vocab:     vocabFor(evalCtx, appName),
			Exec:      exec,
			Now:       summary.Now(),
			ModelName: modelName,
		}
		rt := AskRuntimeFromCtx(req.Context())
		verdict := askspend.Verdict{ModelOK: true}
		// The asking identity, for the per-user cap and the history: the JWT
		// subject (or the external client id) — an id, never a name.
		userID := evalCtx.UserID
		if userID == "" {
			userID = evalCtx.ExternalClientID
		}
		if rt != nil {
			if rt.Cache != nil {
				// Read plans: tenant + role + the vocabulary's fingerprint (a
				// synonym declared after a «no entendí» must cure it, not sit
				// behind a cached refusal). Write plans: the same, plus the
				// USER — an order is personal (VOZ-AHORRO-S2).
				scope := tc.ID + "|" + evalCtx.Role + "|" + deps.Vocab.Fingerprint()
				deps.Cache, deps.CacheScope = rt.Cache, scope
				if userID != "" {
					deps.CacheScopeWrite = scope + "|" + userID
				}
			}
			if rt.Ledger != nil {
				verdict = rt.Ledger.Allow(ctx, tc.ID, userID)
				deps.Trace = rt.Ledger.Config().Trace
			}
		}
		bindWrites(&deps, rt, tc, evalCtx, userID)
		switch {
		case model == nil:
			deps.ModelOff = "disabled"
		case !verdict.ModelOK:
			deps.ModelOff = verdict.Reason
		default:
			deps.Model = &boundedModel{inner: model, timeout: timeout}
		}
		var res ask.Result
		if in.PendingID != "" {
			// The id door: a confirmation (or a pick / a value) for a
			// specific pending — the Telegram buttons and a Siri shortcut
			// that kept the id. The id alone is never enough: it must be
			// this identity's.
			res = ask.Confirm(ctx, deps, in.PendingID, in.Answer)
		} else {
			res = ask.Answer(ctx, deps, in.Q)
		}
		markSpan(req, "query")

		var day askspend.Day
		if rt != nil && rt.Ledger != nil && res.Kind != "invalid" {
			calls := 0
			if res.Source == "model" {
				calls = 1
				if res.Corrected {
					calls = 2
				}
			}
			day = rt.Ledger.Record(ctx, tc.ID, userID, res.Source, calls, res.CostUSD)
			// The question history (VOZ-TRAZABILIDAD-S1): queued, never on the
			// answer path. The text follows the retention policy: redacted (the
			// proper names the plan identified → [nombre]), full, or none.
			if h := rt.Ledger.History(); h != nil && h.Enabled() {
				q := ""
				switch rt.Ledger.Config().HistoryText {
				case "full":
					q = in.Q
				case "redacted":
					q = ask.Redact(in.Q, res.Plan)
				}
				resource := ""
				if res.Plan != nil {
					resource = res.Plan.Resource
				}
				h.Record(askspend.Entry{
					At: time.Now(), Tenant: tc.ID, Role: evalCtx.Role, UserID: userID, Question: q,
					Source: res.Source, Kind: res.Kind, Resource: resource, CostUSD: res.CostUSD,
					LatencyMS: res.TotalMS, ModelMS: res.ModelMS, Plan: res.Plan, Fallback: res.Fallback,
					CacheHit: res.Source == "cache", Corrected: res.Corrected, ModelCalls: calls,
				})
			}
		}

		ev := zlog.Info()
		if res.Kind == "unavailable" {
			ev = zlog.Warn()
		}
		ev.Str("tenant", tc.ID).Str("role", evalCtx.Role).Str("kind", res.Kind).Str("source", res.Source).
			Int64("total_ms", res.TotalMS).Int64("model_ms", res.ModelMS).
			Int("in_tokens", res.Usage.InputTokens+res.Usage.CacheReadTokens+res.Usage.CacheCreationTokens).
			Int("out_tokens", res.Usage.OutputTokens).Float64("cost_usd", res.CostUSD).
			Bool("corrected", res.Corrected).Str("detail", res.Detail).Msg("ask: question answered")

		out := map[string]any{
			"kind": res.Kind, "text": res.Text, "speech": res.Speech, "headline": res.Headline, "display": res.Display,
			"understood": res.Understood, "plan": res.Plan, "groups": res.Groups,
			"usage": res.Usage, "cost_usd": res.CostUSD, "model": modelName, "source": res.Source,
			"model_ms": res.ModelMS, "total_ms": res.TotalMS, "corrected": res.Corrected,
			"fallback": res.Fallback, "fallback_es": res.FallbackES, "trace": deps.Trace,
		}
		if rt != nil && rt.Ledger != nil {
			cfg := rt.Ledger.Config()
			sp := map[string]any{"day_usd": day.USD, "day_questions": day.Questions, "day_model_calls": day.ModelCalls, "daily_cap_usd": cfg.DailyUSD, "per_minute": cfg.PerMinute}
			if cfg.UserDailyUSD > 0 {
				sp["user_daily_cap_usd"] = cfg.UserDailyUSD
				sp["user_day_usd"] = verdict.User.USD
			}
			out["spend"] = sp
		}
		status := http.StatusOK
		if res.Kind == "disabled" && res.Source == "" {
			// No key AND nothing the parser/cache could do: say it as before
			// (503 ask_disabled), so an operator probe still reads the truth.
			status = http.StatusServiceUnavailable
			out["error"] = "ask_disabled"
			out["message"] = "natural-language questions that need the model are not enabled on this app: set ANTHROPIC_API_KEY in the app's environment (and APPXIMO_ASK is not \"off\") — the deterministic parser and the fixed commands keep working"
		}
		if res.Number != nil {
			out["number"] = *res.Number
		}
		if res.Pending != nil {
			// The write waiting for the owner: its id (the confirm door), what
			// stage it is in, and when it expires. The values themselves are
			// in the text the owner reads — that text IS the contract.
			out["pending_id"] = res.Pending.ID
			out["stage"] = res.Pending.Stage
			out["expires_in"] = int(time.Until(res.Pending.Expires).Seconds())
		}
		if len(res.Written) > 0 {
			out["written"] = res.Written
		}
		if res.Kind == "written" || res.Kind == "confirm" || res.Kind == "ask_field" {
			markSpan(req, "write")
		}
		// A grouped answer ALSO comes as a picture, through the digest's own
		// renderer (the same card, same fonts, same palette) — never a new
		// drawing path.
		if len(res.Groups) >= 2 {
			if png, err := summary.Render(groupsReport(appName, res)); err == nil {
				out["png"] = base64.StdEncoding.EncodeToString(png)
			}
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		serverTiming(w, req)
		w.WriteHeader(status)
		json.NewEncoder(w).Encode(out) //nolint:errcheck
		markSpan(req, "serialize")
	})

	registerAskSpendRoute(r, s, policy)
}

// askSpendSentinel is a resource name no schema can declare (resource names
// match ^[a-z][a-z0-9_]*$): policy.Allows(role, askSpendSentinel, "read") is
// true only for a wildcard-resource, admin-grade role — the same inherited
// test the tenant observability routes use. It invents no new check.
const askSpendSentinel = "__platform.ask_spend__"

// registerAskSpendRoute mounts GET /api/ask/spend (VOZ-TRAZABILIDAD-S1): what
// the questions cost for THIS tenant — today, the month, the caps, who
// answered how many, the phrases that cost the most — as text (Telegram HTML,
// the `gasto` command) and, with ?format=png, as the digest's own census
// card (no new renderer). Admin-grade roles only: a row-scoped or listed
// role is 403 — the spend of a platform is the administrator's business, not
// a shop clerk's. The platform-wide view stays on GET /admin/ask.
func registerAskSpendRoute(r chi.Router, s *schema.APISchema, policy *rbac.Policy) {
	r.Get("/api/"+rbac.AskRoute+"/spend", func(w http.ResponseWriter, req *http.Request) {
		tc := tenant.MustFromCtx(req.Context())
		evalCtx := rbac.EvalContextFromRequest(req)
		if evalCtx.Role == "" || evalCtx.Role == rbac.PublicRoleName || !policy.Allows(evalCtx.Role, askSpendSentinel, "read") {
			writeJSONErr(w, http.StatusForbidden, "forbidden: the spend view is for an admin-grade role (wildcard resources)")
			return
		}
		rt := AskRuntimeFromCtx(req.Context())
		if rt == nil || rt.Ledger == nil {
			writeJSONErr(w, http.StatusServiceUnavailable, "the spend ledger is not installed on this app")
			return
		}
		appName := os.Getenv("APPXIMO_ALERT_APP_NAME")
		if appName == "" {
			appName = s.Name
		}
		ctx, cancel := context.WithTimeout(req.Context(), 5*time.Second)
		defer cancel()
		dig := askspend.BuildDigest(ctx, rt.Ledger, tc.ID, appName)
		if h, hm, _ := rt.Cache.Stats(); h+hm > 0 {
			dig.CacheHits, dig.CacheMisses = h, hm
		}
		markSpan(req, "query")
		if req.URL.Query().Get("format") == "png" {
			png, err := summary.Render(dig.Report())
			if err != nil {
				writeJSONErr(w, http.StatusInternalServerError, "spend image could not be rendered — the text still works")
				return
			}
			w.Header().Set("Content-Type", "image/png")
			w.Header().Set("Cache-Control", "no-store")
			serverTiming(w, req)
			w.WriteHeader(http.StatusOK)
			w.Write(png) //nolint:errcheck
			markSpan(req, "render")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		serverTiming(w, req)
		json.NewEncoder(w).Encode(dig) //nolint:errcheck
		markSpan(req, "serialize")
	})
}

// groupsReport shapes a grouped answer as a census-style Report so the
// digest renderer paints it: one row per group, the answer as the headline.
func groupsReport(appName string, res ask.Result) summary.Report {
	rep := summary.Report{AppName: appName, Census: true, Subtitle: "Pregunta", Headline: res.Headline, Level: ""}
	for _, g := range res.Groups {
		rep.Facts = append(rep.Facts, summary.Facts{Resource: g.Label + "  " + g.Text, Total: int64(g.Value), HasTotal: true, TotalText: g.Text})
	}
	return rep
}

// askExecutor runs a plan's reads with the engine's builders, scoped by the
// asking role — the exact code path of GET /api/{resource} and its
// /aggregate, minus the HTTP envelope.
type askExecutor struct {
	s       *schema.APISchema
	tdb     *db.TenantDB
	policy  *rbac.Policy
	evalCtx rbac.EvalContext
	tc      *tenant.TenantCtx
}

func (e *askExecutor) surface(ctx context.Context, resource string) (*schema.ResourceSchema, rbac.EvalResult, error) {
	res, ok := e.s.Resources[resource]
	if !ok {
		return nil, rbac.EvalResult{}, &ask.ExecError{Status: 400, Msg: "unknown resource " + resource}
	}
	ev := e.policy.Evaluate(e.evalCtx, resource, "read")
	if !ev.Allowed {
		return nil, ev, &ask.ExecError{Status: 403, Msg: "role may not read " + resource}
	}
	return readSurface(ctx, e.tc.ID, resource, &res), ev, nil
}

func (e *askExecutor) Aggregate(ctx context.Context, resource string, params url.Values) ([]map[string]any, error) {
	res, ev, err := e.surface(ctx, resource)
	if err != nil {
		return nil, err
	}
	aq, err := query.BuildAggregate(resource, res, params, ev.Condition, ev.AllowedFields)
	if err != nil {
		if errors.Is(err, query.ErrAggForbiddenField) {
			return nil, &ask.ExecError{Status: 403, Msg: err.Error()}
		}
		return nil, &ask.ExecError{Status: 400, Msg: err.Error()}
	}
	sqlStr, args := aq.SQL()
	rows, err := e.tdb.QueryDirect(ctx, e.tc.PGSchema, resource, sqlStr, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return pkghandlers.RowsToMaps(rows)
}

func (e *askExecutor) List(ctx context.Context, resource string, params url.Values) ([]map[string]any, int64, error) {
	res, ev, err := e.surface(ctx, resource)
	if err != nil {
		return nil, 0, err
	}
	qb, err := query.BuildQuery(resource, res, params, ev.Condition, ev.AllowedFields)
	if err != nil {
		if errors.Is(err, query.ErrForbiddenField) {
			return nil, 0, &ask.ExecError{Status: 403, Msg: err.Error()}
		}
		return nil, 0, &ask.ExecError{Status: 400, Msg: err.Error()}
	}
	selectQ, countQ, selectArgs, countArgs := qb.SQL()
	rows, err := e.tdb.QueryDirect(ctx, e.tc.PGSchema, resource, selectQ, selectArgs...)
	if err != nil {
		return nil, 0, err
	}
	recs, err := pkghandlers.RowsToMaps(rows)
	rows.Close()
	if err != nil {
		return nil, 0, err
	}
	total, err := e.tdb.QueryScalarDirect(ctx, e.tc.PGSchema, resource, countQ, countArgs...)
	if err != nil {
		return nil, 0, err
	}
	return recs, total, nil
}

// boundedModel caps each model call at the configured timeout: the owner is
// waiting on a phone, and the receiver's typing indicator covers the wait.
type boundedModel struct {
	inner   aigen.ModelClient
	timeout time.Duration
}

func (b *boundedModel) Complete(ctx context.Context, req aigen.Request) (aigen.Completion, error) {
	cctx, cancel := context.WithTimeout(ctx, b.timeout)
	defer cancel()
	return b.inner.Complete(cctx, req)
}

// askModelFromEnv builds the model client once at boot. nil = disabled.
func askModelFromEnv() (aigen.ModelClient, string) {
	if strings.EqualFold(strings.TrimSpace(os.Getenv("APPXIMO_ASK")), "off") {
		return nil, ""
	}
	name := strings.TrimSpace(os.Getenv("APPXIMO_ASK_MODEL"))
	if name == "" {
		name = aigen.DefaultModel
	}
	c, err := aigen.NewAnthropicClient(name)
	if err != nil {
		return nil, ""
	}
	return c, name
}

func askTimeoutFromEnv() time.Duration {
	if v := strings.TrimSpace(os.Getenv("APPXIMO_ASK_TIMEOUT")); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			return d
		}
	}
	return 8 * time.Second
}
