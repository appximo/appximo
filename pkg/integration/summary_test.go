package integration_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/appximo/appximo/pkg/controlplane"
	"github.com/appximo/appximo/pkg/outbox"
	"github.com/appximo/appximo/pkg/schema"
	"github.com/appximo/appximo/pkg/summary"
)

// VOZ-ESCALON1-S1: GET /api/summary — the owner-language daily digest, generic
// from the schema, deterministic, RBAC-scoped. These tests prove: today's
// created count, pending-by-state from the state machine, the row-scoped role
// sees ONLY its own, empty-with-dignity, and the census view.
const (
	sumU1 = "11111111-1111-1111-1111-111111111111"
	sumU2 = "22222222-2222-2222-2222-222222222222"
)

func sumSchema() *schema.APISchema {
	return &schema.APISchema{
		Schema:  "https://appximo.com/schema/v1",
		Version: "1",
		Name:    "Mi Tienda",
		Resources: map[string]schema.ResourceSchema{
			"pedidos": {
				Fields: map[string]schema.FieldDef{
					"titulo":    {Type: "string", Required: true},
					"creado_en": {Type: "time", Auto: schema.AutoCreate},
					"user_id":   {Type: "uuid"},
					"estado": {
						Type: "string", Enum: []string{"pendiente", "pagado", "cancelado"}, Default: "pendiente",
						StateMachine: &schema.StateMachine{
							Initial: []string{"pendiente"},
							Transitions: map[string][]string{
								"pendiente": {"pagado", "cancelado"},
								"pagado":    {},
								"cancelado": {},
							},
						},
					},
				},
			},
			// A resource the row-scoped role cannot read at all — it must never
			// appear in that role's digest.
			"secretos": {
				Fields: map[string]schema.FieldDef{
					"nota":      {Type: "string"},
					"creado_en": {Type: "time", Auto: schema.AutoCreate},
				},
			},
		},
		RBAC: schema.RBACPolicy{Roles: map[string]schema.RolePolicy{
			"super_admin": {Resources: json.RawMessage(`"*"`), Actions: []string{"*"}},
			"owner": {
				Resources:  json.RawMessage(`["pedidos"]`),
				Actions:    []string{"read", "create"},
				Conditions: &schema.Condition{Field: "user_id", Op: "eq", Val: "$user_id"},
			},
		}},
	}
}

func setupSummary(t *testing.T) (*httptest.Server, func(role, uid string) string, func()) {
	t.Helper()
	if testing.Short() {
		t.Skip("summary: skipping in -short mode")
	}
	pool, cleanPG := startPG(t)
	applyControlPlane(t, pool)
	if err := outbox.EnsureTable(context.Background(), pool); err != nil {
		cleanPG()
		t.Fatalf("ensure outbox: %v", err)
	}
	s := sumSchema()
	if _, err := controlplane.RegisterTenant(context.Background(), pool, controlplane.RegisterRequest{
		TenantID: tenantID, DisplayName: "Mi Tienda", Email: "g@g.com", Plan: "free", Schema: s,
	}); err != nil {
		cleanPG()
		t.Fatalf("register tenant: %v", err)
	}
	rest := httptest.NewServer(buildDP(s, pool, tenantID+".localhost"))
	return rest, genToken, func() { rest.Close(); cleanPG() }
}

func summaryText(t *testing.T, rest *httptest.Server, path, token string) string {
	t.Helper()
	got := dpDo(t, rest, "GET", path, token, nil, http.StatusOK)
	text, _ := got["text"].(string)
	if text == "" {
		t.Fatalf("summary %s: empty text (%v)", path, got)
	}
	return text
}

func TestSummary_TodayCreatedAndPending(t *testing.T) {
	rest, tok, done := setupSummary(t)
	defer done()
	super := tok("super_admin", superID)

	// 2 rows for U1, 1 for U2 — all created "today", all pending (initial).
	for _, r := range []map[string]any{
		{"titulo": "uno", "user_id": sumU1},
		{"titulo": "dos", "user_id": sumU1},
		{"titulo": "tres", "user_id": sumU2},
	} {
		dpDo(t, rest, "POST", "/api/pedidos", super, r, http.StatusCreated)
	}

	text := summaryText(t, rest, "/api/summary", super)
	if !strings.Contains(text, "Mi Tienda") {
		t.Errorf("digest must name the app; got:\n%s", text)
	}
	if !strings.Contains(text, "🆕 3 nuevos") {
		t.Errorf("want 3 nuevos; got:\n%s", text)
	}
	// pending: 3 in "pendiente" (the schema's own word, not translated)
	if !strings.Contains(text, "pendiente: 3") {
		t.Errorf("want 'pendiente: 3'; got:\n%s", text)
	}
}

func TestSummary_RowScopedRoleSeesOnlyOwn(t *testing.T) {
	rest, tok, done := setupSummary(t)
	defer done()
	super := tok("super_admin", superID)
	for _, r := range []map[string]any{
		{"titulo": "uno", "user_id": sumU1},
		{"titulo": "dos", "user_id": sumU1},
		{"titulo": "tres", "user_id": sumU2},
	} {
		dpDo(t, rest, "POST", "/api/pedidos", super, r, http.StatusCreated)
	}

	// owner U1 must count only its OWN 2 rows, and never see `secretos`.
	text := summaryText(t, rest, "/api/summary", tok("owner", sumU1))
	if !strings.Contains(text, "🆕 2 nuevos") {
		t.Errorf("owner U1 must see exactly its 2 rows; got:\n%s", text)
	}
	if strings.Contains(text, "secretos") {
		t.Errorf("owner must NOT see a resource it cannot read; got:\n%s", text)
	}
	if !strings.Contains(text, "pendiente: 2") {
		t.Errorf("owner pending must be its own 2; got:\n%s", text)
	}
}

func TestSummary_EmptyWithDignity(t *testing.T) {
	rest, tok, done := setupSummary(t)
	defer done()
	// No rows created.
	text := summaryText(t, rest, "/api/summary", tok("super_admin", superID))
	if !strings.Contains(text, "Sin movimiento hoy") {
		t.Errorf("an empty day must say 'Sin movimiento hoy'; got:\n%s", text)
	}
}

func TestSummary_CensusView(t *testing.T) {
	rest, tok, done := setupSummary(t)
	defer done()
	super := tok("super_admin", superID)
	for _, r := range []map[string]any{
		{"titulo": "uno", "user_id": sumU1},
		{"titulo": "dos", "user_id": sumU1},
	} {
		dpDo(t, rest, "POST", "/api/pedidos", super, r, http.StatusCreated)
	}
	text := summaryText(t, rest, "/api/summary?view=census", super)
	if !strings.Contains(text, "Estado de Mi Tienda") {
		t.Errorf("census must be an 'Estado' view; got:\n%s", text)
	}
	if !strings.Contains(text, "pedidos") || !strings.Contains(text, "2") {
		t.Errorf("census must show pedidos total 2; got:\n%s", text)
	}
}

func TestSummary_AnonymousDenied(t *testing.T) {
	rest, _, done := setupSummary(t)
	defer done()
	req, _ := http.NewRequest(http.MethodGet, rest.URL+"/api/summary", nil)
	req.Header.Set("Content-Type", "application/json")
	resp, err := rest.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	// An anonymous caller is denied — 401 (this harness's JWT middleware) or 403
	// (the handler's own anonymous deny, which the real engine reaches because it
	// lets tokenless requests fall through to RBAC for rbac.public support). Never
	// a 200 empty digest.
	if resp.StatusCode != http.StatusUnauthorized && resp.StatusCode != http.StatusForbidden {
		t.Errorf("tokenless /api/summary must be denied (401/403), got %d", resp.StatusCode)
	}
}

// The rbac.public digest case ($public is a real role: anonymous gets a digest
// scoped to public resources/fields, never a private one) is proven LIVE by the
// binary-diff gate (scripts/binary-diff/corpus.jsonl → summary-unauth, against a
// schema that declares rbac.public on `authors`): base=403, new=200 with only
// the public surface. Replicating the engine's public-JWT chain in this harness
// would be fragile; the gate is the authoritative proof.

// ── VOZ-VISUAL-S1 ────────────────────────────────────────────────────────────
// A wider schema: a declared `summary.resources` filter (order + membership),
// a declared `state_machine.pending` (vocabulary), a flow-only resource, a
// terminal state that must never count, and the PNG door.

func sumSchemaVisual() *schema.APISchema {
	return &schema.APISchema{
		Schema:  "https://appximo.com/schema/v1",
		Version: "1",
		Name:    "Tienda Visual",
		Resources: map[string]schema.ResourceSchema{
			"ordenes": {
				Fields: map[string]schema.FieldDef{
					"titulo":    {Type: "string", Required: true},
					"creado_en": {Type: "time", Auto: schema.AutoCreate},
					"estado": {
						Type: "string", Enum: []string{"creada", "pagada", "enviada", "entregada", "cerrada"}, Default: "creada",
						StateMachine: &schema.StateMachine{
							Initial: []string{"creada"},
							Transitions: map[string][]string{
								"creada": {"pagada"}, "pagada": {"enviada"}, "enviada": {"entregada"}, "entregada": {"cerrada"}, "cerrada": {},
							},
							// The owner's queue is "pagada" (must be prepared). "creada" is
							// NOT declared pending → flow, never "sin avanzar" either.
							Pending: []string{"pagada"},
						},
					},
				},
			},
			"clientes": {Fields: map[string]schema.FieldDef{
				"nombre":    {Type: "string"},
				"creado_en": {Type: "time", Auto: schema.AutoCreate},
			}},
			// Not in summary.resources → must NEVER appear, however much it moves.
			"productos": {Fields: map[string]schema.FieldDef{
				"nombre":    {Type: "string"},
				"creado_en": {Type: "time", Auto: schema.AutoCreate},
			}},
		},
		Summary: &schema.SummaryConfig{Resources: []string{"clientes", "ordenes"}},
		RBAC: schema.RBACPolicy{Roles: map[string]schema.RolePolicy{
			"super_admin": {Resources: json.RawMessage(`"*"`), Actions: []string{"*"}},
		}},
	}
}

// lastVisualPool lets a test reach the control-plane tables the harness
// created (the digest's snapshots live in public.summary_snapshots).
var lastVisualPool *pgxpool.Pool

func integrationPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	if lastVisualPool == nil {
		t.Fatal("no pool — call setupSummaryVisual first")
	}
	return lastVisualPool
}

func setupSummaryVisual(t *testing.T) (*httptest.Server, func(role, uid string) string, func()) {
	t.Helper()
	if testing.Short() {
		t.Skip("summary: skipping in -short mode")
	}
	pool, cleanPG := startPG(t)
	lastVisualPool = pool
	applyControlPlane(t, pool)
	if err := outbox.EnsureTable(context.Background(), pool); err != nil {
		cleanPG()
		t.Fatalf("ensure outbox: %v", err)
	}
	if err := summary.EnsureSnapshotTable(context.Background(), pool); err != nil {
		cleanPG()
		t.Fatalf("ensure snapshots: %v", err)
	}
	s := sumSchemaVisual()
	if errs := schema.Validate(s); len(errs) != 0 {
		cleanPG()
		t.Fatalf("visual schema must validate: %v", errs)
	}
	if _, err := controlplane.RegisterTenant(context.Background(), pool, controlplane.RegisterRequest{
		TenantID: tenantID, DisplayName: "Tienda Visual", Email: "g@g.com", Plan: "free", Schema: s,
	}); err != nil {
		cleanPG()
		t.Fatalf("register tenant: %v", err)
	}
	rest := httptest.NewServer(buildDP(s, pool, tenantID+".localhost"))
	return rest, genToken, func() { rest.Close(); cleanPG() }
}

func TestSummary_DeclaredFilterOrderAndPending(t *testing.T) {
	rest, tok, done := setupSummaryVisual(t)
	defer done()
	super := tok("super_admin", superID)
	ids := []string{}
	for _, r := range []map[string]any{{"titulo": "a"}, {"titulo": "b"}, {"titulo": "c"}} {
		got := dpDo(t, rest, "POST", "/api/ordenes", super, r, http.StatusCreated)
		ids = append(ids, got["id"].(string))
	}
	// a → pagada (declared pending), b → pagada → enviada → entregada → cerrada (terminal)
	dpDo(t, rest, "PATCH", "/api/ordenes/"+ids[0], super, map[string]any{"estado": "pagada"}, http.StatusOK)
	for _, st := range []string{"pagada", "enviada", "entregada", "cerrada"} {
		dpDo(t, rest, "PATCH", "/api/ordenes/"+ids[1], super, map[string]any{"estado": st}, http.StatusOK)
	}
	dpDo(t, rest, "POST", "/api/clientes", super, map[string]any{"nombre": "x"}, http.StatusCreated)
	for i := 0; i < 4; i++ {
		dpDo(t, rest, "POST", "/api/productos", super, map[string]any{"nombre": "p"}, http.StatusCreated)
	}

	got := dpDo(t, rest, "GET", "/api/summary", super, nil, http.StatusOK)
	text, _ := got["text"].(string)
	// Vocabulary: the declared pending state is "espera acción"; the terminal
	// "cerrada" is never counted; "creada" (initial, but NOT declared) is flow.
	for _, want := range []string{"🔴", "1 espera acción", "(pagada: 1)", "en curso: creada: 1"} {
		if !strings.Contains(text, want) {
			t.Errorf("want %q in:\n%s", want, text)
		}
	}
	for _, never := range []string{"cerrada", "productos", "pendientes de alguien", "sin avanzar"} {
		if strings.Contains(text, never) {
			t.Errorf("must NOT contain %q:\n%s", never, text)
		}
	}
	// Order: the declared list is verbatim — clientes before ordenes.
	if strings.Index(text, "<b>clientes</b>") > strings.Index(text, "<b>ordenes</b>") {
		t.Errorf("declared order must be verbatim (clientes, ordenes):\n%s", text)
	}
	if got["level"] != "red" || got["attention_total"].(float64) != 1 {
		t.Errorf("level/attention: %v / %v", got["level"], got["attention_total"])
	}

	// Census honors the same filter + order.
	census := dpDo(t, rest, "GET", "/api/summary?view=census", super, nil, http.StatusOK)
	ctext, _ := census["text"].(string)
	if strings.Contains(ctext, "productos") || strings.Index(ctext, "clientes") > strings.Index(ctext, "ordenes") {
		t.Errorf("census must honor the declared filter and order:\n%s", ctext)
	}
}

func TestSummary_PNGDoor(t *testing.T) {
	rest, tok, done := setupSummaryVisual(t)
	defer done()
	super := tok("super_admin", superID)
	dpDo(t, rest, "POST", "/api/ordenes", super, map[string]any{"titulo": "a"}, http.StatusCreated)

	for _, path := range []string{"/api/summary?format=png", "/api/summary?view=census&format=png"} {
		req, _ := http.NewRequest(http.MethodGet, rest.URL+path, nil)
		req.Header.Set("Authorization", "Bearer "+super)
		req.Host = tenantID + ".localhost"
		resp, err := rest.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK || resp.Header.Get("Content-Type") != "image/png" {
			t.Fatalf("%s: want 200 image/png, got %d %s: %s", path, resp.StatusCode, resp.Header.Get("Content-Type"), body)
		}
		if len(body) < 1000 || string(body[1:4]) != "PNG" {
			t.Fatalf("%s: not a PNG (%d bytes)", path, len(body))
		}
		if !strings.Contains(path, "census") && resp.Header.Get("X-Summary-Level") == "" {
			t.Errorf("%s: the level travels as a header", path)
		}
	}
	// The Accept header is deliberately NOT a door: the response cache keys by
	// URL, so two representations on one URL served each other's bytes (seen
	// live). ?format=png is the only way to the image; the default stays JSON.
	req, _ := http.NewRequest(http.MethodGet, rest.URL+"/api/summary", nil)
	req.Header.Set("Authorization", "Bearer "+super)
	req.Header.Set("Accept", "image/png")
	req.Host = tenantID + ".localhost"
	resp, err := rest.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.Header.Get("Content-Type") != "application/json" {
		t.Errorf("Accept must not switch the representation (cache safety); got %s", resp.Header.Get("Content-Type"))
	}
}

// ── VOZ-DELTA-S1 ─────────────────────────────────────────────────────────────
// The delta against yesterday and the scheduled decision, on Postgres: the
// first digest says so; a baseline moved to "yesterday" yields +N / igual que
// ayer; ?mode=scheduled decides by policy and records it; the census proves
// the last scheduled run.

func TestSummary_DeltaAndScheduledDecision(t *testing.T) {
	rest, tok, done := setupSummaryVisual(t)
	defer done()
	super := tok("super_admin", superID)
	pool := integrationPool(t)

	mk := func(n int) []string {
		var ids []string
		for i := 0; i < n; i++ {
			got := dpDo(t, rest, "POST", "/api/ordenes", super, map[string]any{"titulo": "o"}, http.StatusCreated)
			ids = append(ids, got["id"].(string))
		}
		return ids
	}
	ids := mk(2)
	for _, id := range ids {
		dpDo(t, rest, "PATCH", "/api/ordenes/"+id, super, map[string]any{"estado": "pagada"}, http.StatusOK)
	}

	// The scheduled evaluation is a side effect: never a cached answer.
	sched := func() map[string]any {
		req, _ := http.NewRequest(http.MethodGet, rest.URL+"/api/summary?mode=scheduled", nil)
		req.Header.Set("Authorization", "Bearer "+super)
		req.Header.Set("Cache-Control", "no-cache")
		req.Host = tenantID + ".localhost"
		resp, err := rest.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var out map[string]any
		if err := json.NewDecoder(resp.Body).Decode(&out); err != nil || resp.StatusCode != http.StatusOK {
			t.Fatalf("scheduled: %d %v", resp.StatusCode, err)
		}
		return out
	}
	// Day 1, scheduled: first digest → changed, should_send=changes.
	d1 := sched()
	if d1["baseline"] != nil || d1["changed"] != true || d1["should_send"] != true || d1["send_reason"] != "changes" {
		t.Fatalf("day 1: %v", d1)
	}
	if !strings.Contains(d1["text"].(string), "primer resumen") {
		t.Errorf("day 1 must say it is the first: %s", d1["text"])
	}
	// The row exists for today, with the decision.
	var decision string
	if err := pool.QueryRow(context.Background(), `SELECT decision FROM public.summary_snapshots WHERE tenant_id=$1 AND role='super_admin' AND day=current_date`, tenantID).Scan(&decision); err != nil || decision != "sent:changes" {
		t.Fatalf("snapshot decision: %q err=%v", decision, err)
	}

	// Pretend a night passed: today's row becomes yesterday's, and what was
	// created "today" was created yesterday (so it is not news again).
	shift := func() {
		for _, q := range []string{
			`DELETE FROM public.summary_snapshots WHERE tenant_id=$1 AND day = current_date - 1`,
			`UPDATE public.summary_snapshots SET day = current_date - 1 WHERE tenant_id=$1 AND day=current_date`,
		} {
			if _, err := pool.Exec(context.Background(), q, tenantID); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := pool.Exec(context.Background(), `UPDATE tenant_`+tenantID+`.ordenes SET creado_en = creado_en - interval '1 day'`); err != nil {
			t.Fatal(err)
		}
	}
	shift()

	// Day 2, same state → NOT a change → silent, streak 1; the "2 esperan" is
	// worded as igual que ayer and the level drops to amber.
	d2 := sched()
	if d2["changed"] != false || d2["should_send"] != false || d2["send_reason"] != "silent" || d2["silent_streak"].(float64) != 1 || d2["level"] != "amber" {
		t.Fatalf("day 2 same state: %v", d2)
	}
	if !strings.Contains(d2["text"].(string), "igual que ayer") {
		t.Errorf("day 2 text: %s", d2["text"])
	}
	// The census says the automatic run was silent on purpose.
	c := dpDo(t, rest, "GET", "/api/summary?view=census", super, nil, http.StatusOK)
	if !strings.Contains(c["text"].(string), "callado a propósito, 1 día sin novedad") {
		t.Errorf("census must prove the silence: %s", c["text"])
	}

	// Day 3: three more orders paid today → +3, 3 llegaron hoy → red, send.
	shift()
	ids3 := mk(3)
	for _, id := range ids3 {
		dpDo(t, rest, "PATCH", "/api/ordenes/"+id, super, map[string]any{"estado": "pagada"}, http.StatusOK)
	}
	d3 := sched()
	if d3["changed"] != true || d3["should_send"] != true || d3["level"] != "red" {
		t.Fatalf("day 3: %v", d3)
	}
	for _, want := range []string{"5 esperan acción · +3 desde ayer · 3 llegaron hoy", "⏳ 5 esperan acción (+3 desde ayer · 3 llegaron hoy) (pagada: 5)"} {
		if !strings.Contains(d3["text"].(string), want) {
			t.Errorf("day 3 want %q in %s", want, d3["text"])
		}
	}

	// Day 4: everything closed → green, "ayer esperaban 5" → a change (good news).
	shift()
	for _, id := range append(ids, ids3...) {
		for _, st := range []string{"enviada", "entregada", "cerrada"} {
			dpDo(t, rest, "PATCH", "/api/ordenes/"+id, super, map[string]any{"estado": st}, http.StatusOK)
		}
	}
	d4 := sched()
	if d4["changed"] != true || d4["should_send"] != true || d4["level"] != "green" || !strings.Contains(d4["text"].(string), "ayer esperaban 5") {
		t.Fatalf("day 4 red→green: %v", d4)
	}

	// Heartbeat: quiet for 6 runs already, quiet_days default 7 → the 7th silent
	// run speaks once and resets the streak.
	shift()
	if _, err := pool.Exec(context.Background(), `UPDATE public.summary_snapshots SET silent_streak = 6, decision='silent' WHERE tenant_id=$1 AND day=current_date-1`, tenantID); err != nil {
		t.Fatal(err)
	}
	d5 := sched()
	if d5["changed"] != false || d5["should_send"] != true || d5["send_reason"] != "heartbeat" || d5["silent_streak"].(float64) != 0 || !strings.Contains(d5["text"].(string), "7 días sin novedad. Sigo acá") {
		t.Fatalf("heartbeat: %v", d5)
	}

	// Pruning: at most two rows (today + baseline) survive.
	var rows int
	pool.QueryRow(context.Background(), `SELECT count(*) FROM public.summary_snapshots WHERE tenant_id=$1`, tenantID).Scan(&rows) //nolint:errcheck
	if rows > 2 {
		t.Errorf("snapshots are not a history: %d rows", rows)
	}
}
