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
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	zlog "github.com/rs/zerolog/log"

	"github.com/appximo/appximo/pkg/aigen"
	"github.com/appximo/appximo/pkg/ask"
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
func registerAskRoute(r chi.Router, s *schema.APISchema, tdb *db.TenantDB, policy *rbac.Policy) {
	model, modelName := askModelFromEnv()
	timeout := askTimeoutFromEnv()
	limiter := newAskLimiter(askPerMinuteFromEnv())

	var vocabMu sync.Mutex
	vocabs := map[string]*ask.Vocabulary{} // per role: the RBAC is boot-static

	vocabFor := func(evalCtx rbac.EvalContext, appName string) *ask.Vocabulary {
		key := evalCtx.Role
		vocabMu.Lock()
		defer vocabMu.Unlock()
		if v, ok := vocabs[key]; ok {
			return v
		}
		v := ask.Build(s, appName, func(resource string) (bool, []string) {
			ev := policy.Evaluate(evalCtx, resource, "read")
			return ev.Allowed, ev.AllowedFields
		})
		vocabs[key] = v
		return v
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
		if model == nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusServiceUnavailable)
			json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
				"error":   "ask_disabled",
				"message": "natural-language questions are not enabled on this app: set ANTHROPIC_API_KEY in the app's environment (and APPXIMO_ASK is not \"off\") — the fixed commands (resumen/estado/ayuda) keep working",
				"kind":    "disabled",
				"text":    "Las preguntas libres no están activadas en esta app (falta la clave del modelo, ANTHROPIC_API_KEY). Los comandos fijos siguen: resumen, estado, ayuda.",
			})
			return
		}
		if !limiter.allow(tc.ID) {
			w.Header().Set("Retry-After", "10")
			writeJSONErr(w, http.StatusTooManyRequests, "too many questions for this tenant this minute (APPXIMO_ASK_PER_MINUTE) — each question costs a model call")
			return
		}
		body, err := io.ReadAll(io.LimitReader(req.Body, 4<<10))
		if err != nil {
			writeJSONErr(w, http.StatusBadRequest, "could not read the body")
			return
		}
		var in struct {
			Q string `json:"q"`
		}
		if err := json.Unmarshal(body, &in); err != nil || strings.TrimSpace(in.Q) == "" {
			writeJSONErr(w, http.StatusBadRequest, `body must be {"q": "<the question>"}`)
			return
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
		res := ask.Answer(ctx, ask.Deps{
			Vocab:     vocabFor(evalCtx, appName),
			Model:     &boundedModel{inner: model, timeout: timeout},
			Exec:      exec,
			Now:       summary.Now(),
			ModelName: modelName,
		}, in.Q)
		markSpan(req, "query")

		ev := zlog.Info()
		if res.Kind == "unavailable" {
			ev = zlog.Warn()
		}
		ev.Str("tenant", tc.ID).Str("role", evalCtx.Role).Str("kind", res.Kind).
			Int64("total_ms", res.TotalMS).Int64("model_ms", res.ModelMS).
			Int("in_tokens", res.Usage.InputTokens+res.Usage.CacheReadTokens+res.Usage.CacheCreationTokens).
			Int("out_tokens", res.Usage.OutputTokens).Float64("cost_usd", res.CostUSD).
			Bool("corrected", res.Corrected).Str("detail", res.Detail).Msg("ask: question answered")

		out := map[string]any{
			"kind": res.Kind, "text": res.Text, "speech": res.Speech, "headline": res.Headline,
			"understood": res.Understood, "plan": res.Plan, "groups": res.Groups,
			"usage": res.Usage, "cost_usd": res.CostUSD, "model": modelName,
			"model_ms": res.ModelMS, "total_ms": res.TotalMS, "corrected": res.Corrected,
		}
		if res.Number != nil {
			out["number"] = *res.Number
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
		json.NewEncoder(w).Encode(out) //nolint:errcheck
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

func askPerMinuteFromEnv() int {
	if v := strings.TrimSpace(os.Getenv("APPXIMO_ASK_PER_MINUTE")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return 30
}

// askLimiter is a per-tenant fixed window: N questions per minute. A question
// costs a model call, so an unbounded loop from one token must not be able to
// run up the bill — the limit is small and named in the 429.
type askLimiter struct {
	mu     sync.Mutex
	perMin int
	win    map[string]*askWindow
}

type askWindow struct {
	start time.Time
	n     int
}

func newAskLimiter(perMin int) *askLimiter {
	return &askLimiter{perMin: perMin, win: map[string]*askWindow{}}
}

func (l *askLimiter) allow(tenantID string) bool {
	now := time.Now()
	l.mu.Lock()
	defer l.mu.Unlock()
	w := l.win[tenantID]
	if w == nil || now.Sub(w.start) >= time.Minute {
		l.win[tenantID] = &askWindow{start: now, n: 1}
		return true
	}
	if w.n >= l.perMin {
		return false
	}
	w.n++
	return true
}
