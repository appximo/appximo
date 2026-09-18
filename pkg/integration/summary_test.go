package integration_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/appximo/appximo/pkg/controlplane"
	"github.com/appximo/appximo/pkg/outbox"
	"github.com/appximo/appximo/pkg/schema"
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

func setupSummaryVisual(t *testing.T) (*httptest.Server, func(role, uid string) string, func()) {
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
	// Accept: image/png is the other door.
	req, _ := http.NewRequest(http.MethodGet, rest.URL+"/api/summary", nil)
	req.Header.Set("Authorization", "Bearer "+super)
	req.Header.Set("Accept", "image/png")
	req.Host = tenantID + ".localhost"
	resp, err := rest.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.Header.Get("Content-Type") != "image/png" {
		t.Errorf("Accept: image/png must render the image, got %s", resp.Header.Get("Content-Type"))
	}
}
