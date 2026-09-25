package integration_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/appximo/appximo/pkg/ask"
	"github.com/appximo/appximo/pkg/askspend"
	"github.com/appximo/appximo/pkg/codegen"
	"github.com/appximo/appximo/pkg/controlplane"
	"github.com/appximo/appximo/pkg/outbox"
	"github.com/appximo/appximo/pkg/schema"
	"github.com/jackc/pgx/v5/pgxpool"
)

// VOZ-ESCRITURAS-S1: voice WRITES against a REAL engine + Postgres. The model
// is scripted at the HTTP seam; everything else is the engine: the plan is
// validated against the role's vocabulary, the dictated name is matched
// through the engine's own search, the confirmation is asked, and the
// confirmed write runs through the transaction cores — RBAC, validation, the
// state-machine guard, the outbox event — exactly as an API write would.

func agendaVozSchema(t *testing.T) *schema.APISchema {
	t.Helper()
	raw, err := os.ReadFile("../../examples/model-lab/agenda-voz.json")
	if err != nil {
		t.Fatalf("read example: %v", err)
	}
	s, err := schema.LoadFromBytes(raw)
	if err != nil {
		t.Fatalf("parse example: %v", err)
	}
	return s
}

func setupAskWrites(t *testing.T, fm *fakeAnthropic) (*httptest.Server, *pgxpool.Pool, func(role, uid string) string, func()) {
	t.Helper()
	if testing.Short() {
		t.Skip("ask: skipping in -short mode")
	}
	t.Setenv("ANTHROPIC_API_KEY", "sk-test-not-a-real-key")
	t.Setenv("ANTHROPIC_BASE_URL", fm.server(t).URL)
	t.Setenv("APPXIMO_SUMMARY_TIMEZONE", "")
	t.Setenv("APPXIMO_ASK_WRITES", "")
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
	s := agendaVozSchema(t)
	if _, err := controlplane.RegisterTenant(context.Background(), pool, controlplane.RegisterRequest{
		TenantID: tenantID, DisplayName: "Agenda", Email: "g@g.com", Plan: "free", Schema: s,
	}); err != nil {
		cleanPG()
		t.Fatalf("register tenant: %v", err)
	}
	rt := &codegen.AskRuntime{Ledger: askspend.New(askspend.Defaults, pool, time.UTC, nil, "Agenda"), Cache: ask.NewPlanCache(100, time.Hour), Pending: ask.NewPendingStore()}
	rest := httptest.NewServer(buildDPWithAsk(s, pool, tenantID+".localhost", rt))
	return rest, pool, genToken, func() { rest.Close(); cleanPG() }
}

func outboxCount(t *testing.T, pool *pgxpool.Pool, topic string) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(), "SELECT count(*) FROM public.outbox WHERE topic = $1", topic).Scan(&n); err != nil {
		t.Fatalf("outbox count: %v", err)
	}
	return n
}

func TestAskWrite_CreateConfirmsThenWritesThroughTheEngine(t *testing.T) {
	fm := &fakeAnthropic{replies: []string{
		`{"kind":"create","resource":"tareas","data":{"titulo":"Llamar a Fabián para arreglar el techo","persona_id":{"match":"Fabian"},"prioridad":"urgente","vence_en":"tomorrow"}}`,
	}}
	rest, pool, tok, done := setupAskWrites(t, fm)
	defer done()
	dueno := tok("dueno", askU1)
	fab := dpDo(t, rest, "POST", "/api/personas", dueno, map[string]any{"nombre": "Fabián Gómez", "telefono": "300"}, http.StatusCreated)
	dpDo(t, rest, "POST", "/api/personas", dueno, map[string]any{"nombre": "Marta Ruiz"}, http.StatusCreated)
	fabID := fab["id"].(string)

	// A VERBLESS sentence: the fixed form (AGENDA-ASISTENTE-S1) settles
	// «anota …» without the model; this test pins the MODEL's write path.
	got := dpDo(t, rest, "POST", "/api/ask", dueno, map[string]any{"q": "Llamar a Fabián para arreglar el techo, urgente, para mañana"}, http.StatusOK)
	if got["kind"] != "confirm" || got["pending_id"] == nil || got["stage"] != "confirm" {
		t.Fatalf("want a confirmation, got %v", got)
	}
	text := got["text"].(string)
	for _, want := range []string{"Voy a crear", "Fabián Gómez", "urgente", "mañana", "¿Confirmas?"} {
		if !strings.Contains(text, want) {
			t.Errorf("confirmation lacks %q: %s", want, text)
		}
	}
	if !strings.Contains(fm.systems[0], "WRITES —") || !strings.Contains(fm.systems[0], "tareas: [may create+update]") {
		t.Errorf("the model was not told the write forms / abilities")
	}
	// Nothing written yet: the table is empty, the outbox has no tareas.created.
	if rows := dpList(t, rest, "/api/tareas", dueno); len(rows) != 0 {
		t.Fatalf("NOTHING may be written before the confirmation; got %d rows", len(rows))
	}
	if n := outboxCount(t, pool, "tareas.created"); n != 0 {
		t.Fatalf("no event before the confirmation, got %d", n)
	}
	// Confirm by id (the button door).
	pendingID := got["pending_id"]
	got = dpDo(t, rest, "POST", "/api/ask", dueno, map[string]any{"pending_id": pendingID, "answer": "sí"}, http.StatusOK)
	if got["kind"] != "written" || got["source"] != "confirm" || got["cost_usd"] != float64(0) {
		t.Fatalf("want written at zero cost, got %v", got)
	}
	rows := dpList(t, rest, "/api/tareas", dueno)
	if len(rows) != 1 || rows[0]["persona_id"] != fabID || rows[0]["prioridad"] != "urgente" || rows[0]["estado"] != "pendiente" || rows[0]["titulo"] != "Llamar a Fabián para arreglar el techo" {
		t.Fatalf("the row the engine wrote: %v", rows)
	}
	if v, _ := rows[0]["vence_en"].(string); !strings.HasPrefix(v, time.Now().UTC().AddDate(0, 0, 1).Format("2006-01-02")) {
		t.Errorf("vence_en resolved to tomorrow (engine day): %v", rows[0]["vence_en"])
	}
	if n := outboxCount(t, pool, "tareas.created"); n != 1 {
		t.Fatalf("the create emits its outbox event like an API POST, got %d", n)
	}
	// A second confirmation of the same id executes nothing (the pending is gone).
	got = dpDo(t, rest, "POST", "/api/ask", dueno, map[string]any{"pending_id": pendingID, "answer": "sí"}, http.StatusOK)
	if got["kind"] != "expired" {
		t.Fatalf("a resolved pending cannot be replayed: %v", got)
	}
	if rows := dpList(t, rest, "/api/tareas", dueno); len(rows) != 1 {
		t.Fatalf("replay must not write: %d rows", len(rows))
	}
}

func TestAskWrite_ReadOnlyRoleIsRefusedByRBAC_NeverByWording(t *testing.T) {
	// The demo role (read-only) never even gets the write forms: the parser
	// refuses the write verb deterministically (no model call) — and had a
	// plan reached the executor, prepareTxOp would 403 it.
	fm := &fakeAnthropic{replies: []string{`{"kind":"create","resource":"tareas","data":{"titulo":"x"}}`}}
	rest, _, tok, done := setupAskWrites(t, fm)
	defer done()
	demo := tok("demo", askU2)
	got := dpDo(t, rest, "POST", "/api/ask", demo, map[string]any{"q": "anota llamar a Fabián mañana"}, http.StatusOK)
	if got["kind"] != "write_refused" || !strings.Contains(got["text"].(string), "solo <b>leo</b>") {
		t.Fatalf("read-only role: %v", got)
	}
	if fm.calls != 0 {
		t.Fatalf("no model call for a read-only role's write verb")
	}
	if rows := dpList(t, rest, "/api/tareas", demo); len(rows) != 0 {
		t.Fatalf("nothing written: %d", len(rows))
	}
	// A confirm by id from another identity is "nothing".
	got = dpDo(t, rest, "POST", "/api/ask", demo, map[string]any{"pending_id": "deadbeef00000000", "answer": "sí"}, http.StatusOK)
	if got["kind"] != "expired" {
		t.Fatalf("foreign/unknown id: %v", got)
	}
}

func TestAskWrite_UpdateHonorsTheStateMachineAndEmits(t *testing.T) {
	fm := &fakeAnthropic{replies: []string{
		`{"kind":"update","resource":"tareas","where":[{"field":"persona_id","op":"eq","match":"Fabian"}],"data":{"estado":"hecha"}}`,
	}}
	rest, pool, tok, done := setupAskWrites(t, fm)
	defer done()
	dueno := tok("dueno", askU1)
	fab := dpDo(t, rest, "POST", "/api/personas", dueno, map[string]any{"nombre": "Fabián Gómez"}, http.StatusCreated)
	dpDo(t, rest, "POST", "/api/tareas", dueno, map[string]any{"titulo": "Llamar a Fabián", "persona_id": fab["id"]}, http.StatusCreated)
	dpDo(t, rest, "POST", "/api/tareas", dueno, map[string]any{"titulo": "Pagar el agua"}, http.StatusCreated)

	// A parser-able question first: "¿hay alguna tarea de Fabián?" is a list.
	got := dpDo(t, rest, "POST", "/api/ask", dueno, map[string]any{"q": "hay alguna tarea de Fabián"}, http.StatusOK)
	if got["kind"] != "answer" || got["number"] != float64(1) {
		t.Fatalf("question before the update: %v", got)
	}

	got = dpDo(t, rest, "POST", "/api/ask", dueno, map[string]any{"q": "marca como hecha la tarea de Fabián"}, http.StatusOK)
	if got["kind"] != "confirm" || !strings.Contains(got["text"].(string), "pendiente → <b>hecha</b>") {
		t.Fatalf("want a confirmation naming the transition, got %v", got)
	}
	// A typed yes through the plain door (the pending is per identity).
	got = dpDo(t, rest, "POST", "/api/ask", dueno, map[string]any{"q": "dale"}, http.StatusOK)
	if got["kind"] != "written" {
		t.Fatalf("want written, got %v", got)
	}
	rows := dpList(t, rest, "/api/tareas?filter[estado][eq]=hecha", dueno)
	if len(rows) != 1 || rows[0]["titulo"] != "Llamar a Fabián" {
		t.Fatalf("the ONE row moved: %v", rows)
	}
	if n := outboxCount(t, pool, "tareas.updated"); n != 1 {
		t.Fatalf("the update emits its event, got %d", n)
	}
	// hecha is terminal: the same order is refused BEFORE any confirmation.
	fm.calls = 0
	got = dpDo(t, rest, "POST", "/api/ask", dueno, map[string]any{"q": "marca como hecha la tarea de Fabián"}, http.StatusOK)
	if got["kind"] != "answer" || !strings.Contains(got["text"].(string), "ya está en <b>hecha</b>") {
		t.Fatalf("want 'ya está así': %v", got)
	}
	// And the engine's own guard is the last word: a pending whose row moved
	// under it is refused at execution with the 422 in words.
	fm.replies = []string{`{"kind":"update","resource":"tareas","where":[{"field":"titulo","op":"partial","value":"agua"}],"data":{"estado":"cancelada"}}`}
	fm.calls = 0
	got = dpDo(t, rest, "POST", "/api/ask", dueno, map[string]any{"q": "cancela la tarea sobre el agua"}, http.StatusOK)
	if got["kind"] != "confirm" {
		t.Fatalf("want confirm: %v", got)
	}
	agua := dpList(t, rest, "/api/tareas?filter[estado][eq]=pendiente", dueno)
	dpDo(t, rest, "PATCH", "/api/tareas/"+agua[0]["id"].(string), dueno, map[string]any{"estado": "hecha"}, http.StatusOK)
	got = dpDo(t, rest, "POST", "/api/ask", dueno, map[string]any{"q": "sí"}, http.StatusOK)
	if got["kind"] != "rejected" || !strings.Contains(got["text"].(string), "No escribí nada") {
		t.Fatalf("the guard's refusal is said, never a success face: %v", got)
	}
	var b []byte
	b, _ = json.Marshal(got)
	if strings.Contains(string(b), `"written"`) {
		t.Fatalf("nothing written: %s", b)
	}
}

// ── VOZ-AHORRO-S2 Part B: the write PLAN is cached, never the result ──

func TestAskWrite_PlanCachedNeverTheResult_AndStrayYesCostsNothing(t *testing.T) {
	fm := &fakeAnthropic{replies: []string{
		`{"kind":"create","resource":"tareas","data":{"titulo":"Pagar la luz","vence_en":"tomorrow"}}`,
	}}
	rest, pool, tok, done := setupAskWrites(t, fm)
	defer done()
	dueno := tok("dueno", askU1)
	modelCalls := func() int {
		fm.mu.Lock()
		defer fm.mu.Unlock()
		return fm.calls
	}
	// 1. The order, once: the model plans it, the owner confirms, the engine
	// writes. A VERBLESS order — «anota …» is the fixed form's (parser, US$ 0)
	// since AGENDA-ASISTENTE-S1; this test pins the MODEL's cached plan.
	got := dpDo(t, rest, "POST", "/api/ask", dueno, map[string]any{"q": "pagar la luz para mañana"}, http.StatusOK)
	if got["kind"] != "confirm" || got["source"] != "model" || modelCalls() != 1 {
		t.Fatalf("first order: %v (calls %d)", got, modelCalls())
	}
	got = dpDo(t, rest, "POST", "/api/ask", dueno, map[string]any{"q": "sí"}, http.StatusOK)
	if got["kind"] != "written" {
		t.Fatalf("first write: %v", got)
	}
	// 2. The SAME order again: the plan comes from the cache (no model call),
	// a FRESH confirmation is asked, and confirming writes a SECOND row — the
	// result was never cached.
	got = dpDo(t, rest, "POST", "/api/ask", dueno, map[string]any{"q": "Pagar la luz para mañana"}, http.StatusOK)
	if got["kind"] != "confirm" || got["source"] != "cache" || got["cost_usd"] != float64(0) || modelCalls() != 1 {
		t.Fatalf("second order must come from the plan cache: %v (calls %d)", got, modelCalls())
	}
	if !strings.Contains(got["text"].(string), "Voy a crear") {
		t.Fatalf("a fresh confirmation: %v", got["text"])
	}
	got = dpDo(t, rest, "POST", "/api/ask", dueno, map[string]any{"q": "dale"}, http.StatusOK)
	if got["kind"] != "written" {
		t.Fatalf("second write: %v", got)
	}
	if rows := dpList(t, rest, "/api/tareas", dueno); len(rows) != 2 {
		t.Fatalf("two confirmed orders are two rows, got %d", len(rows))
	}
	if n := outboxCount(t, pool, "tareas.created"); n != 2 {
		t.Fatalf("two events, got %d", n)
	}
	// 3. A third time, then a stray «sí pero…» that carries NO datum: the
	// pending is cancelled and the sentence is settled by the parser — no
	// model call, nothing written. («Sí pero mejor el viernes» carries a day
	// and is a CORRECTION since AGENDA-ASISTENTE-S1: it re-issues the
	// confirmation instead — pinned in pkg/ask.)
	got = dpDo(t, rest, "POST", "/api/ask", dueno, map[string]any{"q": "pagar la luz para mañana"}, http.StatusOK)
	if got["kind"] != "confirm" || got["source"] != "cache" {
		t.Fatalf("third order from the cache: %v", got)
	}
	got = dpDo(t, rest, "POST", "/api/ask", dueno, map[string]any{"q": "Si pero no estoy seguro"}, http.StatusOK)
	if got["kind"] != "unclear" || got["source"] != "parser" || got["cost_usd"] != float64(0) || modelCalls() != 1 {
		t.Fatalf("stray yes: %v (calls %d)", got, modelCalls())
	}
	if text, _ := got["text"].(string); !strings.Contains(text, "Cancelé la escritura") || !strings.Contains(text, "no es un <b>sí</b>") {
		t.Fatalf("the stray yes is explained: %v", got["text"])
	}
	if rows := dpList(t, rest, "/api/tareas", dueno); len(rows) != 2 {
		t.Fatalf("the stray yes wrote nothing: %d rows", len(rows))
	}
	// 4. Another user of the same role does NOT inherit the write plan.
	other := tok("dueno", askU2)
	got = dpDo(t, rest, "POST", "/api/ask", other, map[string]any{"q": "pagar la luz para mañana"}, http.StatusOK)
	if got["source"] != "model" || modelCalls() != 2 {
		t.Fatalf("another user's order is planned anew: %v (calls %d)", got, modelCalls())
	}
	dpDo(t, rest, "POST", "/api/ask", other, map[string]any{"q": "no"}, http.StatusOK)
	// 5. The alias declared on the example: «cosas» names tareas.
	got = dpDo(t, rest, "POST", "/api/ask", dueno, map[string]any{"q": "cuántas cosas hay"}, http.StatusOK)
	if got["kind"] != "answer" || got["source"] != "parser" || got["number"] != float64(2) {
		t.Fatalf("alias «cosas» → parser count 2: %v", got)
	}
}
