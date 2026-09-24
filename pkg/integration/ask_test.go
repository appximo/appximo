package integration_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/appximo/appximo/pkg/ask"
	"github.com/appximo/appximo/pkg/askspend"
	"github.com/appximo/appximo/pkg/codegen"
	"github.com/appximo/appximo/pkg/controlplane"
	"github.com/appximo/appximo/pkg/observability"
	"github.com/appximo/appximo/pkg/outbox"
	"github.com/appximo/appximo/pkg/schema"
)

// VOZ-PREGUNTAS-S1: POST /api/ask against a REAL engine + Postgres, with the
// model faked at the HTTP seam (ANTHROPIC_BASE_URL → a scripted /v1/messages)
// so the whole chain is exercised: JWT → RBAC → vocabulary → plan → validation
// → name resolution through the engine's own ?search= → the aggregate/list
// builders with the role's row condition → the composed reply. The model's
// answers are scripted; the NUMBERS come from the database.

type fakeAnthropic struct {
	mu      sync.Mutex
	replies []string
	calls   int
	seen    []string // the user turns, to assert what the model was told
	systems []string
}

func (f *fakeAnthropic) server(t *testing.T) *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/messages" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if r.Header.Get("x-api-key") == "" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		var req struct {
			System   []struct{ Text string } `json:"system"`
			Messages []struct{ Role, Content string }
		}
		json.NewDecoder(r.Body).Decode(&req) //nolint:errcheck
		f.mu.Lock()
		if len(req.System) > 0 {
			f.systems = append(f.systems, req.System[0].Text)
		}
		if len(req.Messages) > 0 {
			f.seen = append(f.seen, req.Messages[len(req.Messages)-1].Content)
		}
		i := f.calls
		f.calls++
		if i >= len(f.replies) {
			i = len(f.replies) - 1
		}
		text := f.replies[i]
		f.mu.Unlock()
		json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
			"content":     []map[string]any{{"type": "text", "text": text}},
			"stop_reason": "end_turn",
			"usage":       map[string]any{"input_tokens": 1200, "output_tokens": 45},
		})
	}))
	t.Cleanup(srv.Close)
	return srv
}

const (
	askU1 = "11111111-1111-1111-1111-111111111111"
	askU2 = "22222222-2222-2222-2222-222222222222"
)

func askSchema() *schema.APISchema {
	return &schema.APISchema{
		Schema: "https://appximo.com/schema/v1", Version: "1", Name: "Óptica Ver Bien",
		Resources: map[string]schema.ResourceSchema{
			"optometras": {Fields: map[string]schema.FieldDef{"nombre": {Type: "string", Required: true}, "apellido": {Type: "string"}}},
			"citas": {Aliases: []string{"turnos"}, Fields: map[string]schema.FieldDef{
				"motivo":       {Type: "string", Required: true},
				"optometra_id": {Type: "uuid", Relation: "optometras"},
				"user_id":      {Type: "uuid"},
				"valor_cents":  {Type: "int64"},
				"estado": {Type: "string", Enum: []string{"pendiente", "confirmada", "atendida", "cancelada"}, Default: "pendiente",
					Aliases: map[string][]string{"pendiente": {"por atender"}},
					StateMachine: &schema.StateMachine{Initial: []string{"pendiente"}, Pending: []string{"pendiente"},
						Transitions: map[string][]string{"pendiente": {"confirmada", "cancelada"}, "confirmada": {"atendida", "cancelada"}, "atendida": {}, "cancelada": {}}}},
				"creado_en": {Type: "time", Auto: schema.AutoCreate},
			}},
			"secretos": {Fields: map[string]schema.FieldDef{"nota": {Type: "string"}}},
		},
		RBAC: schema.RBACPolicy{Roles: map[string]schema.RolePolicy{
			"super_admin": {Resources: json.RawMessage(`"*"`), Actions: []string{"*"}},
			"owner": {
				Resources:  json.RawMessage(`["citas", "optometras"]`),
				Actions:    []string{"read", "create"},
				Conditions: &schema.Condition{Field: "user_id", Op: "eq", Val: "$user_id"},
			},
		}},
	}
}

// setupAsk boots the engine WITH the fake model (env set before the router
// is built — the model client is created at BuildRouter time).
func setupAsk(t *testing.T, fm *fakeAnthropic) (*httptest.Server, func(role, uid string) string, func()) {
	t.Helper()
	if testing.Short() {
		t.Skip("ask: skipping in -short mode")
	}
	if fm != nil {
		t.Setenv("ANTHROPIC_API_KEY", "sk-test-not-a-real-key")
		t.Setenv("ANTHROPIC_BASE_URL", fm.server(t).URL)
	} else {
		t.Setenv("ANTHROPIC_API_KEY", "")
	}
	// The zone stays the process's (UTC in CI): the sibling summary tests
	// compare against Postgres's current_date, and the zone arithmetic itself
	// is pinned in pkg/ask (TestPeriod_Windows, TestAnswer_Clear…).
	t.Setenv("APPXIMO_SUMMARY_TIMEZONE", "")
	pool, cleanPG := startPG(t)
	applyControlPlane(t, pool)
	if err := outbox.EnsureTable(context.Background(), pool); err != nil {
		cleanPG()
		t.Fatalf("ensure outbox: %v", err)
	}
	s := askSchema()
	if _, err := controlplane.RegisterTenant(context.Background(), pool, controlplane.RegisterRequest{
		TenantID: tenantID, DisplayName: "Óptica", Email: "g@g.com", Plan: "free", Schema: s,
	}); err != nil {
		cleanPG()
		t.Fatalf("register tenant: %v", err)
	}
	rest := httptest.NewServer(buildDP(s, pool, tenantID+".localhost"))
	return rest, genToken, func() { rest.Close(); cleanPG() }
}

func seedAsk(t *testing.T, rest *httptest.Server, super string) (anaID, luisID string) {
	ana := dpDo(t, rest, "POST", "/api/optometras", super, map[string]any{"nombre": "Ana", "apellido": "Gómez"}, http.StatusCreated)
	luis := dpDo(t, rest, "POST", "/api/optometras", super, map[string]any{"nombre": "Luis", "apellido": "Gómez"}, http.StatusCreated)
	dpDo(t, rest, "POST", "/api/optometras", super, map[string]any{"nombre": "Carlos", "apellido": "Mesa"}, http.StatusCreated)
	idOf := func(m map[string]any) string {
		if d, ok := m["data"].(map[string]any); ok {
			id, _ := d["id"].(string)
			return id
		}
		id, _ := m["id"].(string)
		return id
	}
	anaID, luisID = idOf(ana), idOf(luis)
	if anaID == "" || luisID == "" {
		t.Fatalf("seed: no ids in %v / %v", ana, luis)
	}
	for _, r := range []map[string]any{
		{"motivo": "control", "optometra_id": anaID, "user_id": askU1, "valor_cents": 8000000},
		{"motivo": "lentes", "optometra_id": anaID, "user_id": askU1, "valor_cents": 5000000},
		{"motivo": "examen", "optometra_id": luisID, "user_id": askU2, "valor_cents": 8000000},
	} {
		dpDo(t, rest, "POST", "/api/citas", super, r, http.StatusCreated)
	}
	return anaID, luisID
}

func TestAsk_ClearQuestionCountsFromTheDatabase(t *testing.T) {
	fm := &fakeAnthropic{replies: []string{`{"kind":"count","resource":"citas","period":{"range":"today"}}`}}
	rest, tok, done := setupAsk(t, fm)
	defer done()
	super := tok("super_admin", superID)
	seedAsk(t, rest, super)

	got := dpDo(t, rest, "POST", "/api/ask", super, map[string]any{"q": "how many citas do we have today"}, http.StatusOK)
	if got["kind"] != "answer" || got["number"] != float64(3) {
		t.Fatalf("want answer 3 from the DB, got %v", got)
	}
	if text, _ := got["text"].(string); !strings.HasPrefix(text, "<b>3</b> citas") {
		t.Errorf("text leads with the number: %q", text)
	}
	if got["cost_usd"].(float64) <= 0 || got["model"] != "claude-haiku-4-5" {
		t.Errorf("cost + model accounted: %v %v", got["cost_usd"], got["model"])
	}
	// The model saw the vocabulary and the question — never a row.
	fm.mu.Lock()
	sys := fm.systems[0]
	fm.mu.Unlock()
	if !strings.Contains(sys, "- citas:") || strings.Contains(sys, "control") || strings.Contains(sys, askU1) {
		t.Errorf("the model gets vocabulary, not data:\n%.600s", sys)
	}
}

func TestAsk_RowScopedRoleCountsOnlyItsOwn_AndNeverTheHiddenResource(t *testing.T) {
	// The security provocation: the owner U1 asks the same question the
	// super-admin asked. It gets ITS 2, not 3; and `secretos` is neither in its
	// vocabulary nor executable even if the model names it.
	fm := &fakeAnthropic{replies: []string{
		`{"kind":"count","resource":"citas","period":{"range":"today"}}`,
		`{"kind":"count","resource":"secretos"}`,
		`{"kind":"count","resource":"secretos"}`,
	}}
	rest, tok, done := setupAsk(t, fm)
	defer done()
	super := tok("super_admin", superID)
	seedAsk(t, rest, super)
	dpDo(t, rest, "POST", "/api/secretos", super, map[string]any{"nota": "x"}, http.StatusCreated)

	owner := tok("owner", askU1)
	got := dpDo(t, rest, "POST", "/api/ask", owner, map[string]any{"q": "how many citas do I have today"}, http.StatusOK)
	if got["kind"] != "answer" || got["number"] != float64(2) {
		t.Fatalf("owner U1 must count ONLY its own 2, got %v", got)
	}
	fm.mu.Lock()
	sys := fm.systems[0]
	fm.mu.Unlock()
	if strings.Contains(sys, "secretos") {
		t.Errorf("a resource the role cannot read must not be in its vocabulary")
	}
	got = dpDo(t, rest, "POST", "/api/ask", owner, map[string]any{"q": "cuenta los secretos"}, http.StatusOK)
	if got["kind"] != "unclear" || got["number"] != nil {
		t.Fatalf("a plan over a hidden resource must be refused, got %v", got)
	}
}

func TestAsk_MangledNameResolvedThroughTheEnginesSearch(t *testing.T) {
	fm := &fakeAnthropic{replies: []string{`{"kind":"count","resource":"citas","filters":[{"field":"optometra_id","op":"eq","match":"Ana Gomes"}]}`}}
	rest, tok, done := setupAsk(t, fm)
	defer done()
	super := tok("super_admin", superID)
	seedAsk(t, rest, super)
	got := dpDo(t, rest, "POST", "/api/ask", super, map[string]any{"q": "cuántas citas tiene la doctora Ana Gomes"}, http.StatusOK)
	if got["kind"] != "answer" || got["number"] != float64(2) {
		t.Fatalf("want Ana's 2, got %v", got)
	}
	if text, _ := got["text"].(string); !strings.Contains(text, "Entendí «Ana Gomes» como <b>Ana Gómez</b>") {
		t.Errorf("the resolved name must be said: %q", text)
	}
}

func TestAsk_AmbiguousAndMissingNames(t *testing.T) {
	fm := &fakeAnthropic{replies: []string{
		`{"kind":"count","resource":"citas","filters":[{"field":"optometra_id","op":"eq","match":"Gómez"}]}`,
		`{"kind":"count","resource":"citas","filters":[{"field":"optometra_id","op":"eq","match":"Wilfredo Pacheco"}]}`,
	}}
	rest, tok, done := setupAsk(t, fm)
	defer done()
	super := tok("super_admin", superID)
	seedAsk(t, rest, super)
	got := dpDo(t, rest, "POST", "/api/ask", super, map[string]any{"q": "citas de Gómez"}, http.StatusOK)
	if got["kind"] != "ambiguous" || !strings.Contains(got["text"].(string), "Ana Gómez") || !strings.Contains(got["text"].(string), "Luis Gómez") {
		t.Fatalf("want ambiguous listing both, got %v", got)
	}
	got = dpDo(t, rest, "POST", "/api/ask", super, map[string]any{"q": "citas de Wilfredo Pacheco"}, http.StatusOK)
	if got["kind"] != "not_found" || got["number"] != nil {
		t.Fatalf("want not_found without a number, got %v", got)
	}
}

func TestAsk_WriteRefusedAndListAndGroup(t *testing.T) {
	// Only the list question reaches the (scripted) model: the write verb,
	// the group-by and the sum are settled by the parser (VOZ-SIN-IA-S1).
	fm := &fakeAnthropic{replies: []string{
		`{"kind":"list","resource":"citas","filters":[{"field":"estado","op":"eq","value":"pendiente"}],"limit":2}`,
	}}
	rest, tok, done := setupAsk(t, fm)
	defer done()
	super := tok("super_admin", superID)
	seedAsk(t, rest, super)
	got := dpDo(t, rest, "POST", "/api/ask", super, map[string]any{"q": "borrá las citas viejas"}, http.StatusOK)
	if got["kind"] != "write_refused" {
		t.Fatalf("want write_refused, got %v", got)
	}
	got = dpDo(t, rest, "POST", "/api/ask", super, map[string]any{"q": "show me two citas still pendientes"}, http.StatusOK)
	if got["kind"] != "answer" || got["number"] != float64(3) || !strings.Contains(got["text"].(string), "y 1 más") {
		t.Fatalf("list: total 3, 2 shown, got %v", got)
	}
	got = dpDo(t, rest, "POST", "/api/ask", super, map[string]any{"q": "citas por estado"}, http.StatusOK)
	if got["kind"] != "answer" || got["groups"] == nil {
		t.Fatalf("group_by: %v", got)
	}
	got = dpDo(t, rest, "POST", "/api/ask", super, map[string]any{"q": "cuánto suman las citas"}, http.StatusOK)
	if got["kind"] != "answer" || got["number"] != float64(21000000) || !strings.Contains(got["text"].(string), "$ 210.000") {
		t.Fatalf("sum of cents formatted as pesos: %v", got)
	}
}

func TestAsk_DisabledWithoutKey_AndBadBody(t *testing.T) {
	rest, tok, done := setupAsk(t, nil)
	defer done()
	super := tok("super_admin", superID)
	// A shape the parser cannot settle needs the model → 503 naming the variable
	// (a parser-able shape answers 200 without a key: TestAsk_NoKey_ParserStillAnswers).
	got := dpDo(t, rest, "POST", "/api/ask", super, map[string]any{"q": "cuánto vendimos esta semana"}, http.StatusServiceUnavailable)
	if got["error"] != "ask_disabled" || !strings.Contains(got["message"].(string), "ANTHROPIC_API_KEY") {
		t.Fatalf("no key → 503 ask_disabled naming the variable, got %v", got)
	}
}

func TestAsk_BadBodyWithKey(t *testing.T) {
	fm := &fakeAnthropic{replies: []string{`{"kind":"unclear"}`}}
	rest, tok, done := setupAsk(t, fm)
	defer done()
	super := tok("super_admin", superID)
	dpDo(t, rest, "POST", "/api/ask", super, map[string]any{"question": "x"}, http.StatusBadRequest)
	dpDo(t, rest, "POST", "/api/ask", super, map[string]any{"q": strings.Repeat("a", 501)}, http.StatusBadRequest)
	// Anonymous: the handler is the deny point (no rbac.public here → 401 at
	// the JWT stage; with a public block the reserved $public role is denied
	// by the handler with a 403 — pinned by the binary-diff corpus).
	dpDo(t, rest, "POST", "/api/ask", "", map[string]any{"q": "x"}, http.StatusUnauthorized)
}

// ── VOZ-SIN-IA-S1: the parser, the plan cache and the daily cap, against Postgres ──

type recAlerter struct {
	mu   sync.Mutex
	sent []observability.Alert
}

func (r *recAlerter) Send(_ context.Context, a observability.Alert) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sent = append(r.sent, a)
	return nil
}

// buildDPWithAsk is buildDP plus the per-app ask runtime (what app.go installs).
func buildDPWithAsk(s *schema.APISchema, pool *pgxpool.Pool, host string, rt *codegen.AskRuntime) http.Handler {
	inner := buildDP(s, pool, host)
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		inner.ServeHTTP(w, req.WithContext(codegen.WithAskRuntime(req.Context(), rt)))
	})
}

func setupAskRuntime(t *testing.T, fm *fakeAnthropic, cfg askspend.Config, al observability.Alerter) (*httptest.Server, *pgxpool.Pool, *codegen.AskRuntime, func(role, uid string) string, func()) {
	t.Helper()
	if testing.Short() {
		t.Skip("ask: skipping in -short mode")
	}
	if fm != nil {
		t.Setenv("ANTHROPIC_API_KEY", "sk-test-not-a-real-key")
		t.Setenv("ANTHROPIC_BASE_URL", fm.server(t).URL)
	} else {
		t.Setenv("ANTHROPIC_API_KEY", "")
	}
	t.Setenv("APPXIMO_SUMMARY_TIMEZONE", "")
	pool, cleanPG := startPG(t)
	applyControlPlane(t, pool)
	if err := outbox.EnsureTable(context.Background(), pool); err != nil {
		cleanPG()
		t.Fatalf("ensure outbox: %v", err)
	}
	if err := askspend.EnsureTable(context.Background(), pool); err != nil {
		cleanPG()
		t.Fatalf("ensure ask_spend: %v", err)
	}
	s := askSchema()
	if _, err := controlplane.RegisterTenant(context.Background(), pool, controlplane.RegisterRequest{
		TenantID: tenantID, DisplayName: "Óptica", Email: "g@g.com", Plan: "free", Schema: s,
	}); err != nil {
		cleanPG()
		t.Fatalf("register tenant: %v", err)
	}
	rt := &codegen.AskRuntime{Ledger: askspend.New(cfg, pool, time.UTC, al, "Óptica"), Cache: ask.NewPlanCache(100, time.Hour)}
	rest := httptest.NewServer(buildDPWithAsk(s, pool, tenantID+".localhost", rt))
	return rest, pool, rt, genToken, func() { rest.Close(); cleanPG() }
}

func TestAsk_ParserAnswersWithoutTheModel_AndRBAC(t *testing.T) {
	fm := &fakeAnthropic{replies: []string{`{"kind":"unclear","reason":"should not be called"}`}}
	rest, _, rt, tok, done := setupAskRuntime(t, fm, askspend.Defaults, nil)
	defer done()
	super := tok("super_admin", superID)
	seedAsk(t, rest, super)

	got := dpDo(t, rest, "POST", "/api/ask", super, map[string]any{"q": "cuántas citas hay"}, http.StatusOK)
	if got["kind"] != "answer" || got["source"] != "parser" || got["number"] != float64(3) || got["cost_usd"] != float64(0) {
		t.Fatalf("parser must count 3 from the DB with no model: %v", got)
	}
	fm.mu.Lock()
	calls := fm.calls
	fm.mu.Unlock()
	if calls != 0 {
		t.Fatalf("the model must not have been called: %d", calls)
	}
	// The same question as the row-scoped owner: ITS 2 — the parser plan runs
	// through the same executor, the same row condition.
	got = dpDo(t, rest, "POST", "/api/ask", tok("owner", askU1), map[string]any{"q": "cuántas citas hay"}, http.StatusOK)
	if got["source"] != "parser" || got["number"] != float64(2) {
		t.Fatalf("owner must get ITS 2 via the parser: %v", got)
	}
	// A hidden resource is not in the owner's vocabulary: the parser is not
	// sure, the (scripted) model says unclear.
	got = dpDo(t, rest, "POST", "/api/ask", tok("owner", askU1), map[string]any{"q": "cuántos secretos hay"}, http.StatusOK)
	if got["kind"] != "unclear" || got["source"] != "model" {
		t.Fatalf("hidden resource → unclear via the model: %v", got)
	}
	// A write verb is refused by the parser, without a call.
	got = dpDo(t, rest, "POST", "/api/ask", super, map[string]any{"q": "borrá las citas de ayer"}, http.StatusOK)
	if got["kind"] != "write_refused" || got["source"] != "parser" {
		t.Fatalf("write refused by the parser: %v", got)
	}
	// The ledger counted: 3 parser answers, 1 model call.
	rows := rt.Ledger.Today()
	if len(rows) != 1 || rows[0].Parser != 3 || rows[0].ModelCalls != 1 || rows[0].Questions != 4 {
		t.Fatalf("ledger: %+v", rows)
	}
}

func TestAsk_DailyCapDegradesAlertsAndSurvivesRestart(t *testing.T) {
	fm := &fakeAnthropic{replies: []string{`{"kind":"count","resource":"citas","filters":[{"field":"estado","op":"eq","value":"pendiente"}]}`}}
	al := &recAlerter{}
	// A cap ONE model call can cross (the fake model bills 1200 in / 45 out ≈ $0.0014).
	cfg := askspend.Config{PerMinute: 100, DailyUSD: 0.001, AlertPct: 50}
	rest, pool, rt, tok, done := setupAskRuntime(t, fm, cfg, al)
	defer done()
	super := tok("super_admin", superID)
	seedAsk(t, rest, super)

	// 1st model question: allowed, answered, crosses the cap → capped + alerts.
	got := dpDo(t, rest, "POST", "/api/ask", super, map[string]any{"q": "which citas are still waiting for confirmation"}, http.StatusOK)
	if got["kind"] != "answer" || got["source"] != "model" {
		t.Fatalf("first model question: %v", got)
	}
	spend, _ := got["spend"].(map[string]any)
	if spend == nil || spend["daily_cap_usd"] != float64(0.001) || spend["day_model_calls"] != float64(1) {
		t.Fatalf("spend block: %v", got["spend"])
	}
	al.mu.Lock()
	kinds := []string{}
	for _, a := range al.sent {
		kinds = append(kinds, a.Kind)
	}
	al.mu.Unlock()
	if strings.Join(kinds, ",") != "ask_spend_warning,ask_spend_capped" {
		t.Fatalf("alerts: %v", kinds)
	}
	// 2nd model question: the model is OFF for the day → capped, no call.
	got = dpDo(t, rest, "POST", "/api/ask", super, map[string]any{"q": "which citas are still waiting for confirmation, again"}, http.StatusOK)
	if got["kind"] != "capped" || !strings.Contains(got["text"].(string), "techo diario") {
		t.Fatalf("capped reply: %v", got)
	}
	fm.mu.Lock()
	calls := fm.calls
	fm.mu.Unlock()
	if calls != 1 {
		t.Fatalf("no second model call: %d", calls)
	}
	// The parser keeps answering (degradation, not a dead channel)…
	got = dpDo(t, rest, "POST", "/api/ask", super, map[string]any{"q": "cuántas citas hay"}, http.StatusOK)
	if got["kind"] != "answer" || got["source"] != "parser" || got["number"] != float64(3) {
		t.Fatalf("parser under the cap: %v", got)
	}
	// …and so does the plan cache: the FIRST question again → cache, fresh data.
	dpDo(t, rest, "POST", "/api/citas", super, map[string]any{"motivo": "extra", "user_id": askU1}, http.StatusCreated)
	got = dpDo(t, rest, "POST", "/api/ask", super, map[string]any{"q": "Which citas are still waiting for confirmation?"}, http.StatusOK)
	if got["kind"] != "answer" || got["source"] != "cache" || got["number"] != float64(4) {
		t.Fatalf("cache under the cap with fresh data: %v", got)
	}
	// Persisted: the row exists, and a NEW ledger (a restart) still sees the cap.
	var usd float64
	var capped bool
	if err := pool.QueryRow(context.Background(), `SELECT usd, capped FROM public.ask_spend WHERE tenant_id=$1 AND day=current_date`, tenantID).Scan(&usd, &capped); err != nil || !capped || usd < 0.001 {
		t.Fatalf("persisted row: usd=%v capped=%v err=%v", usd, capped, err)
	}
	fresh := askspend.New(cfg, pool, time.UTC, nil, "Óptica")
	if v := fresh.Allow(context.Background(), tenantID, ""); v.ModelOK || v.Reason != "capped" {
		t.Fatalf("a restarted ledger must remember the cap: %+v", v)
	}
	_ = rt
}

func TestAsk_NoKey_ParserStillAnswers(t *testing.T) {
	rest, _, _, tok, done := setupAskRuntime(t, nil, askspend.Defaults, nil)
	defer done()
	super := tok("super_admin", superID)
	seedAsk(t, rest, super)
	got := dpDo(t, rest, "POST", "/api/ask", super, map[string]any{"q": "cuántas citas confirmadas hay"}, http.StatusOK)
	if got["kind"] != "answer" || got["source"] != "parser" || got["number"] != float64(0) {
		t.Fatalf("no key, parser answers: %v", got)
	}
	got = dpDo(t, rest, "POST", "/api/ask", super, map[string]any{"q": "cuánto vendimos esta semana"}, http.StatusServiceUnavailable)
	if got["error"] != "ask_disabled" {
		t.Fatalf("no key, model question → 503 ask_disabled: %v", got)
	}
}

// ── VOZ-TRAZABILIDAD-S1: trace switch, history + redaction, per-user cap, the spend view ──

func setupAskTrace(t *testing.T, fm *fakeAnthropic, cfg askspend.Config, al observability.Alerter) (*httptest.Server, *pgxpool.Pool, *codegen.AskRuntime, *askspend.History, func(role, uid string) string, func()) {
	t.Helper()
	rest, pool, rt, tok, done := setupAskRuntime(t, fm, cfg, al)
	if err := askspend.EnsureHistoryTable(context.Background(), pool); err != nil {
		done()
		t.Fatalf("ensure history: %v", err)
	}
	h := askspend.NewHistory(cfg, pool)
	rt.Ledger.SetHistory(h)
	hctx, hcancel := context.WithCancel(context.Background())
	go h.Run(hctx)
	return rest, pool, rt, h, tok, func() { hcancel(); time.Sleep(50 * time.Millisecond); done() }
}

func waitHistory(t *testing.T, pool *pgxpool.Pool, want int) {
	t.Helper()
	for i := 0; i < 100; i++ {
		var n int
		if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM public.ask_history`).Scan(&n); err == nil && n >= want {
			return
		}
		time.Sleep(30 * time.Millisecond)
	}
	t.Fatalf("history never reached %d rows", want)
}

func TestAsk_TraceSwitchHistoryAndRedaction(t *testing.T) {
	fm := &fakeAnthropic{replies: []string{`{"kind":"count","resource":"citas","filters":[{"field":"optometra_id","op":"eq","match":"Ana Gomes"}]}`}}
	cfg := askspend.Defaults
	cfg.Trace = true
	rest, pool, _, h, tok, done := setupAskTrace(t, fm, cfg, nil)
	defer done()
	super := tok("super_admin", superID)
	seedAsk(t, rest, super)

	// Parser answer: the trace says parser, zero cost; the speech does not.
	got := dpDo(t, rest, "POST", "/api/ask", super, map[string]any{"q": "cuántas citas hay"}, http.StatusOK)
	if got["trace"] != true || !strings.Contains(got["text"].(string), "⚙︎ parser") || strings.Contains(got["speech"].(string), "⚙") {
		t.Fatalf("parser trace: %v", got)
	}
	// Model answer with a proper name: the trace says why; the history keeps
	// the shape with the name redacted.
	got = dpDo(t, rest, "POST", "/api/ask", super, map[string]any{"q": "how many citas with doctor Ana Gomes"}, http.StatusOK)
	if got["source"] != "model" || got["fallback"] != "no resource named" && !strings.HasPrefix(got["fallback"].(string), "unknown word") {
		t.Fatalf("model trace: %v", got)
	}
	if !strings.Contains(got["text"].(string), "el parser pasó:") {
		t.Fatalf("the trace must say why: %v", got["text"])
	}
	waitHistory(t, pool, 2)
	var q, fb, src string
	var cost float64
	if err := pool.QueryRow(context.Background(), `SELECT question, fallback, source, cost_usd FROM public.ask_history WHERE source='model' ORDER BY at DESC LIMIT 1`).Scan(&q, &fb, &src, &cost); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(q, "Gomes") || !strings.Contains(q, "[nombre]") || fb == "" || cost <= 0 {
		t.Fatalf("history row must be redacted and carry the reason and the cost: q=%q fb=%q cost=%v", q, fb, cost)
	}
	var ip int
	_ = pool.QueryRow(context.Background(), `SELECT count(*) FROM information_schema.columns WHERE table_name='ask_history' AND column_name LIKE '%ip%'`).Scan(&ip)
	if ip != 0 {
		t.Fatal("no IP column, ever (A-53)")
	}
	// The three lists.
	if fbs, err := h.ModelFallbacks(context.Background(), tenantID, 30, 5); err != nil || len(fbs) != 1 || fbs[0].Fallback == "" {
		t.Fatalf("fallback list: %v %v", fbs, err)
	}
	share, _ := h.ShareFor(context.Background(), tenantID, 30)
	if share.Questions != 2 || share.Parser != 1 || share.Model != 1 || share.ParserPct != 50 {
		t.Fatalf("share: %+v", share)
	}
}

func TestAsk_TraceOffSaysNothing(t *testing.T) {
	rest, _, _, _, tok, done := setupAskTrace(t, nil, askspend.Defaults, nil)
	defer done()
	super := tok("super_admin", superID)
	seedAsk(t, rest, super)
	got := dpDo(t, rest, "POST", "/api/ask", super, map[string]any{"q": "cuántas citas hay"}, http.StatusOK)
	if got["trace"] != false || strings.Contains(got["text"].(string), "⚙") {
		t.Fatalf("trace off: %v", got)
	}
	// …but the machine fields are always there.
	if got["source"] != "parser" || got["cost_usd"] != float64(0) {
		t.Fatalf("machine fields: %v", got)
	}
}

func TestAsk_PerUserCapDegradesOnlyThatUser(t *testing.T) {
	fm := &fakeAnthropic{replies: []string{`{"kind":"count","resource":"citas"}`}}
	al := &recAlerter{}
	// The fake model bills ≈ $0.0014 per call: a $0.002 user cap is crossed by the
	// SECOND call of a user, never by the first.
	cfg := askspend.Config{PerMinute: 100, DailyUSD: 1, AlertPct: 80, UserDailyUSD: 0.002, HistoryDays: 30, HistoryText: "redacted"}
	rest, _, _, _, tok, done := setupAskTrace(t, fm, cfg, al)
	defer done()
	super := tok("super_admin", superID)
	seedAsk(t, rest, super)
	other := tok("super_admin", "99999999-9999-4999-8999-999999999999")
	// User 1 spends its cap on two model questions…
	for i, q := range []string{"how many citas overall", "how many citas overall, again"} {
		got := dpDo(t, rest, "POST", "/api/ask", super, map[string]any{"q": q}, http.StatusOK)
		if got["source"] != "model" {
			t.Fatalf("call %d: %v", i, got)
		}
	}
	got := dpDo(t, rest, "POST", "/api/ask", super, map[string]any{"q": "how many citas overall, third"}, http.StatusOK)
	if got["kind"] != "capped" || !strings.Contains(got["text"].(string), "tu cupo diario") {
		t.Fatalf("user capped: %v", got)
	}
	// …still answers by parser…
	got = dpDo(t, rest, "POST", "/api/ask", super, map[string]any{"q": "cuántas citas hay"}, http.StatusOK)
	if got["kind"] != "answer" || got["source"] != "parser" {
		t.Fatalf("capped user parser: %v", got)
	}
	// …while another user of the same tenant goes on to the model.
	got = dpDo(t, rest, "POST", "/api/ask", other, map[string]any{"q": "how many citas overall, other user"}, http.StatusOK)
	if got["source"] != "model" {
		t.Fatalf("other user must not be capped: %v", got)
	}
	al.mu.Lock()
	defer al.mu.Unlock()
	if len(al.sent) != 1 || al.sent[0].Kind != "ask_user_capped" || al.sent[0].Fields["user"] != superID {
		t.Fatalf("the admin is told once: %+v", al.sent)
	}
}

func TestAsk_SpendViewIsForAdminGradeRoles(t *testing.T) {
	rest, _, _, _, tok, done := setupAskTrace(t, nil, askspend.Defaults, nil)
	defer done()
	super := tok("super_admin", superID)
	seedAsk(t, rest, super)
	dpDo(t, rest, "POST", "/api/ask", super, map[string]any{"q": "cuántas citas hay"}, http.StatusOK)
	got := dpDo(t, rest, "GET", "/api/ask/spend", super, nil, http.StatusOK)
	today, _ := got["today"].(map[string]any)
	if today == nil || today["questions"] != float64(1) || today["parser"] != float64(1) || !strings.Contains(got["text"].(string), "Gasto del modelo") {
		t.Fatalf("spend view: %v", got)
	}
	// The row-scoped owner (listed resources, not wildcard) may not see it.
	dpDo(t, rest, "GET", "/api/ask/spend", tok("owner", askU1), nil, http.StatusForbidden)
	// The picture.
	req, _ := http.NewRequest("GET", rest.URL+"/api/ask/spend?format=png", nil)
	req.Header.Set("Authorization", "Bearer "+super)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 || !strings.HasPrefix(resp.Header.Get("Content-Type"), "image/png") {
		t.Fatalf("png: %d %s", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
}

// ── VOZ-AHORRO-S2: declared aliases answer from the database, without the model ──

func TestAsk_AliasesAnswerFromTheDatabase(t *testing.T) {
	fm := &fakeAnthropic{replies: []string{`{"kind":"unclear","reason":"should not be called"}`}}
	rest, _, rt, tok, done := setupAskRuntime(t, fm, askspend.Defaults, nil)
	defer done()
	super := tok("super_admin", superID)
	seedAsk(t, rest, super)

	// «turnos» is the declared alias of citas; «por atender» of estado = pendiente.
	got := dpDo(t, rest, "POST", "/api/ask", super, map[string]any{"q": "cuántos turnos hay"}, http.StatusOK)
	if got["kind"] != "answer" || got["source"] != "parser" || got["number"] != float64(3) || got["cost_usd"] != float64(0) {
		t.Fatalf("alias resource → parser count 3 from the DB: %v", got)
	}
	got = dpDo(t, rest, "POST", "/api/ask", super, map[string]any{"q": "turnos por atender"}, http.StatusOK)
	if got["kind"] != "answer" || got["source"] != "parser" || got["number"] != float64(3) {
		t.Fatalf("alias value → parser list filtered by estado = pendiente: %v", got)
	}
	// The reply speaks the schema's word, never the alias.
	if text, _ := got["text"].(string); !strings.Contains(text, "citas") || !strings.Contains(text, "estado = pendiente") {
		t.Fatalf("the reply uses the schema's own words: %v", got["text"])
	}
	// RBAC unchanged: the row-scoped owner gets ITS 2 through the alias.
	got = dpDo(t, rest, "POST", "/api/ask", tok("owner", askU1), map[string]any{"q": "cuántos turnos hay"}, http.StatusOK)
	if got["source"] != "parser" || got["number"] != float64(2) {
		t.Fatalf("owner must get ITS 2 via the alias: %v", got)
	}
	// Part C: sentences that are not questions never reach the model.
	for q, kind := range map[string]string{"sí pero mejor el viernes": "unclear", "qué puedo preguntar": "guide", "hola, buenos días": "help"} {
		got = dpDo(t, rest, "POST", "/api/ask", super, map[string]any{"q": q}, http.StatusOK)
		if got["kind"] != kind || got["source"] != "parser" || got["cost_usd"] != float64(0) {
			t.Fatalf("%q → %s at zero cost, got %v", q, kind, got)
		}
	}
	// … while a rare legitimate question still does.
	got = dpDo(t, rest, "POST", "/api/ask", super, map[string]any{"q": "cuántos empleados tenemos"}, http.StatusOK)
	if got["source"] != "model" {
		t.Fatalf("a rare question keeps the model reachable: %v", got)
	}
	fm.mu.Lock()
	calls := fm.calls
	fm.mu.Unlock()
	if calls != 1 {
		t.Fatalf("exactly one model call (the rare question), got %d", calls)
	}
	// `display` carries plain text; the JSON is what a Siri card shows.
	if d, _ := got["display"].(string); d == "" || strings.Contains(d, "<b>") {
		t.Fatalf("display: %q", d)
	}
	rows := rt.Ledger.Today()
	if len(rows) != 1 || rows[0].ModelCalls != 1 {
		t.Fatalf("ledger: %+v", rows)
	}
}
