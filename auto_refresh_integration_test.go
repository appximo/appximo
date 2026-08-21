//go:build integration

// SILENT-CORRUPTION-S1 — the auto roles, live against Postgres, through the
// THREE update paths that must agree (REST PATCH, batch /api/transaction,
// Ctx.Update): a field declared `auto:"update"` refreshes on every update
// WHATEVER its name (the Spanish `modificado_en` that used to freeze at
// creation forever), `auto:"create"` and a legacy non-updated_at auto field
// stay frozen, and the legacy literal `updated_at` keeps its documented
// refresh — now also through Ctx.Update, which used to stamp nothing.
package appximo

import (
	"fmt"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/appximo/appximo/pkg/schema"
	"github.com/appximo/appximo/tests/helpers"
)

func newAutoRolesApp(t *testing.T) *httptest.Server {
	t.Helper()
	app, err := New(Config{
		SchemaPath: filepath.Join(helpers.RepoRoot(), "examples", "model-lab", "auto-roles.json"),
		DSN:        itConnStr,
		JWTSecret:  helpers.JWTSecret,
		AdminKey:   helpers.AdminKey,
		Env:        "test",
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { app.pool.Close() })

	// A consumer's custom route driving Ctx.Update — the third update path.
	mustRegister(t, app, Route{Method: "POST", Path: "/api/_touch", Handler: func(ctx Ctx) error {
		var body struct {
			ID     string `json:"id"`
			Titulo string `json:"titulo"`
		}
		if err := ctx.Bind(&body); err != nil {
			return ctx.Error(400, "invalid body", err)
		}
		row, err := ctx.Update("tickets", body.ID, map[string]any{"titulo": body.Titulo})
		if err != nil {
			return err
		}
		if row == nil {
			return ctx.Error(404, "not found", nil)
		}
		return ctx.JSON(200, row)
	}})

	srv := httptest.NewServer(app.buildRouter(app.bootSurface()))
	t.Cleanup(srv.Close)
	return srv
}

func autoTS(t *testing.T, rec map[string]any, key string) time.Time {
	t.Helper()
	v, ok := rec[key].(string)
	if !ok || v == "" {
		t.Fatalf("field %q missing or not a timestamp in %v", key, rec)
	}
	ts, err := time.Parse(time.RFC3339Nano, v)
	if err != nil {
		t.Fatalf("field %q: %q is not a timestamp: %v", key, v, err)
	}
	return ts
}

func TestAutoRoles_RefreshAcrossAllUpdatePaths(t *testing.T) {
	s, err := schema.LoadFromFile(filepath.Join(helpers.RepoRoot(), "examples", "model-lab", "auto-roles.json"))
	if err != nil {
		t.Fatalf("load schema: %v", err)
	}
	helpers.RegisterTenant(t, itPool, "autolab", s)
	srv := newAutoRolesApp(t)
	token := helpers.GenToken(t, "admin", "u1", "autolab")

	// Create: every auto field is stamped by the column default.
	resp := do(t, srv, "POST", "/api/tickets", "autolab.localhost", token, `{"titulo":"t1"}`)
	if resp.StatusCode != 201 && resp.StatusCode != 200 {
		t.Fatalf("create: %d", resp.StatusCode)
	}
	created := decode(t, resp)
	if data, ok := created["data"].(map[string]any); ok {
		created = data
	}
	id, _ := created["id"].(string)
	if id == "" {
		t.Fatalf("no id in %v", created)
	}
	for _, k := range []string{"creado_en", "modificado_en", "created_at", "updated_at"} {
		autoTS(t, created, k)
	}

	fetch := func() map[string]any {
		t.Helper()
		resp := do(t, srv, "GET", "/api/tickets/"+id, "autolab.localhost", token, "")
		rec := decode(t, resp)
		if data, ok := rec["data"].(map[string]any); ok {
			return data
		}
		return rec
	}

	assertRefresh := func(label string, before, after map[string]any) {
		t.Helper()
		// Refreshed: the explicit "update" role (Spanish name) and the legacy
		// literal updated_at.
		for _, k := range []string{"modificado_en", "updated_at"} {
			if !autoTS(t, after, k).After(autoTS(t, before, k)) {
				t.Errorf("%s: %q did not advance (%s → %s)", label, k, before[k], after[k])
			}
		}
		// Frozen: the explicit "create" role and the legacy non-updated_at name.
		for _, k := range []string{"creado_en", "created_at"} {
			if !autoTS(t, after, k).Equal(autoTS(t, before, k)) {
				t.Errorf("%s: %q must stay frozen (%s → %s)", label, k, before[k], after[k])
			}
		}
	}

	// 1. Generated REST PATCH.
	before := fetch()
	time.Sleep(50 * time.Millisecond)
	if resp := do(t, srv, "PATCH", "/api/tickets/"+id, "autolab.localhost", token, `{"titulo":"t2"}`); resp.StatusCode != 200 {
		t.Fatalf("PATCH: %d", resp.StatusCode)
	}
	assertRefresh("REST PATCH", before, fetch())

	// 2. Batch transaction update.
	before = fetch()
	time.Sleep(50 * time.Millisecond)
	txBody := fmt.Sprintf(`{"operations":[{"op":"update","resource":"tickets","id":%q,"data":{"titulo":"t3"}}]}`, id)
	if resp := do(t, srv, "POST", "/api/transaction", "autolab.localhost", token, txBody); resp.StatusCode != 200 {
		t.Fatalf("transaction: %d", resp.StatusCode)
	}
	assertRefresh("batch transaction", before, fetch())

	// 3. Ctx.Update through the custom route — used to stamp NOTHING.
	before = fetch()
	time.Sleep(50 * time.Millisecond)
	if resp := do(t, srv, "POST", "/api/_touch", "autolab.localhost", token,
		fmt.Sprintf(`{"id":%q,"titulo":"t4"}`, id)); resp.StatusCode != 200 {
		t.Fatalf("Ctx.Update route: %d", resp.StatusCode)
	}
	assertRefresh("Ctx.Update", before, fetch())

	// 4. The auto contract is unchanged: writing an auto field by hand is the
	// same 422 read_only on every name, Spanish included.
	resp = do(t, srv, "PATCH", "/api/tickets/"+id, "autolab.localhost", token, `{"modificado_en":"2020-01-01T00:00:00Z"}`)
	if resp.StatusCode != 422 {
		t.Fatalf("hand-writing an auto field: want 422, got %d", resp.StatusCode)
	}
}
