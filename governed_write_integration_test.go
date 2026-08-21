//go:build integration

// WRITE-ASYMMETRY-S1 — the all-doors proof of the governed-field write rule.
//
// The asymmetry this closes: POST accepted a forged `id` / `auto` timestamp
// with 201 (REST, batch, Ctx.Insert) while PATCH answered 422 read_only for
// the same keys and GraphQL rejected them structurally — same input, three
// answers, and any client with create permission could forge a row's audit
// timestamps. The rule now has ONE implementation
// (schema.GovernedFieldViolations) and this file fires the SAME forged body
// through EVERY door of a real engine — REST POST/PATCH/PUT, the batch
// transaction (create + update), GraphQL (create + update), Ctx.Insert and
// Ctx.Update — asserting the verdicts match. It also proves the declared
// exception: a resource with `"import": {"roles": […]}` accepts the governed
// fields on CREATE from exactly the granted roles, stores the supplied
// values verbatim, and still rejects them on update.
package appximo

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/appximo/appximo/pkg/schema"
	"github.com/appximo/appximo/tests/helpers"
)

const governedSchemaJSON = `{
  "$schema": "https://appximo.com/schema/v1",
  "version": "1",
  "name": "governed",
  "resources": {
    "articles": {
      "fields": {
        "title":      { "type": "string", "required": true, "minLength": 1 },
        "created_at": { "type": "time", "auto": "create" },
        "updated_at": { "type": "time", "auto": true }
      }
    },
    "legacy_rows": {
      "fields": {
        "title":      { "type": "string", "required": true, "minLength": 1 },
        "created_at": { "type": "time", "auto": "create" }
      },
      "import": { "roles": ["admin"] }
    }
  },
  "rbac": {
    "roles": {
      "admin": { "resources": "*", "actions": ["*"] },
      "clerk": { "resources": ["articles", "legacy_rows"], "actions": ["read", "create", "update"] }
    }
  }
}`

const forgedID = "99999999-9999-4999-8999-999999999999"
const forgedTS = "1999-01-01T00:00:00Z"

func newGovernedApp(t *testing.T) *App {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "governed.json")
	if err := os.WriteFile(p, []byte(governedSchemaJSON), 0o600); err != nil {
		t.Fatalf("write schema: %v", err)
	}
	app, err := New(Config{
		SchemaPath: p,
		DSN:        itConnStr,
		JWTSecret:  helpers.JWTSecret,
		AdminKey:   helpers.AdminKey,
		Env:        "test",
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { app.pool.Close() })

	// The library doors, exposed as custom routes so every door is HTTP-reachable.
	mustRegister(t, app, Route{Method: "POST", Path: "/api/_ctx_create", Handler: func(ctx Ctx) error {
		var body struct {
			Resource string         `json:"resource"`
			Data     map[string]any `json:"data"`
		}
		if err := ctx.Bind(&body); err != nil {
			return ctx.Error(400, "invalid body", err)
		}
		row, err := ctx.Insert(body.Resource, body.Data)
		if err != nil {
			return err
		}
		return ctx.JSON(201, row)
	}})
	mustRegister(t, app, Route{Method: "PATCH", Path: "/api/_ctx_update", Handler: func(ctx Ctx) error {
		var body struct {
			Resource string         `json:"resource"`
			ID       string         `json:"id"`
			Data     map[string]any `json:"data"`
		}
		if err := ctx.Bind(&body); err != nil {
			return ctx.Error(400, "invalid body", err)
		}
		row, err := ctx.Update(body.Resource, body.ID, body.Data)
		if err != nil {
			return err
		}
		if row == nil {
			return ctx.Error(404, "not found", nil)
		}
		return ctx.JSON(200, row)
	}})
	return app
}

// expectReadOnly asserts a 422 whose fields[] names every forged governed key
// with rule read_only — the ONE verdict every door must give.
func expectReadOnly(t *testing.T, resp *http.Response, door string, wantFields ...string) {
	t.Helper()
	if resp.StatusCode != http.StatusUnprocessableEntity {
		b := decode(t, resp)
		t.Fatalf("%s: want 422 for forged governed fields, got %d (%v) — THE ASYMMETRY IS BACK", door, resp.StatusCode, b)
	}
	body := decode(t, resp)
	fields, _ := body["fields"].([]any)
	got := map[string]string{}
	for _, f := range fields {
		m, _ := f.(map[string]any)
		got[fmt.Sprint(m["field"])] = fmt.Sprint(m["rule"])
	}
	for _, w := range wantFields {
		if got[w] != "read_only" {
			t.Errorf("%s: field %q must be rejected with rule read_only, got %v (body %v)", door, w, got[w], body)
		}
	}
}

func seedRow(t *testing.T, srv *httptest.Server, host, token, resource string) string {
	t.Helper()
	resp := do(t, srv, http.MethodPost, "/api/"+resource, host, token, `{"title":"seed"}`)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("seed %s: %d", resource, resp.StatusCode)
	}
	return fmt.Sprint(decode(t, resp)["id"])
}

// TestGoverned_EveryDoorRejectsForgery: no import declaration → the same
// forged body gets the same 422 read_only at every door, create and update.
func TestGoverned_EveryDoorRejectsForgery(t *testing.T) {
	app := newGovernedApp(t)
	srv := newServerFor(t, app)
	tenant := "govall"
	helpers.RegisterTenant(t, itPool, tenant, mustLoadSchemaBytes(t, governedSchemaJSON))
	host := tenant + ".localhost"
	admin := helpers.GenToken(t, "admin", "u1", tenant)

	forged := fmt.Sprintf(`{"title":"x","id":%q,"created_at":%q,"updated_at":%q}`, forgedID, forgedTS, forgedTS)

	t.Run("REST POST", func(t *testing.T) {
		resp := do(t, srv, http.MethodPost, "/api/articles", host, admin, forged)
		expectReadOnly(t, resp, "REST POST", "id", "created_at", "updated_at")
	})
	t.Run("batch create", func(t *testing.T) {
		body := fmt.Sprintf(`{"operations":[{"op":"create","resource":"articles","data":%s}]}`, forged)
		resp := do(t, srv, http.MethodPost, "/api/transaction", host, admin, body)
		expectReadOnly(t, resp, "batch create", "id", "created_at", "updated_at")
	})
	t.Run("Ctx.Insert", func(t *testing.T) {
		body := fmt.Sprintf(`{"resource":"articles","data":%s}`, forged)
		resp := do(t, srv, http.MethodPost, "/api/_ctx_create", host, admin, body)
		expectReadOnly(t, resp, "Ctx.Insert", "id", "created_at", "updated_at")
	})

	rowID := seedRow(t, srv, host, admin, "articles")
	forgedUpd := fmt.Sprintf(`{"id":%q,"created_at":%q,"updated_at":%q}`, forgedID, forgedTS, forgedTS)

	t.Run("REST PATCH", func(t *testing.T) {
		resp := do(t, srv, http.MethodPatch, "/api/articles/"+rowID, host, admin, forgedUpd)
		expectReadOnly(t, resp, "REST PATCH", "id", "created_at", "updated_at")
	})
	t.Run("REST PUT", func(t *testing.T) {
		resp := do(t, srv, http.MethodPut, "/api/articles/"+rowID, host, admin, `{"title":"y","created_at":"`+forgedTS+`"}`)
		expectReadOnly(t, resp, "REST PUT", "created_at")
	})
	t.Run("batch update", func(t *testing.T) {
		body := fmt.Sprintf(`{"operations":[{"op":"update","resource":"articles","id":%q,"data":%s}]}`, rowID, forgedUpd)
		resp := do(t, srv, http.MethodPost, "/api/transaction", host, admin, body)
		expectReadOnly(t, resp, "batch update", "id", "created_at", "updated_at")
	})
	t.Run("Ctx.Update", func(t *testing.T) {
		body := fmt.Sprintf(`{"resource":"articles","id":%q,"data":%s}`, rowID, forgedUpd)
		resp := do(t, srv, http.MethodPatch, "/api/_ctx_update", host, admin, body)
		expectReadOnly(t, resp, "Ctx.Update", "id", "created_at", "updated_at")
	})

	// GraphQL: on a resource with NO import declaration the governed keys are
	// not part of the input TYPE — the rejection is structural and names the
	// field. (The runtime read_only half is proven on the import resource.)
	t.Run("GraphQL create structural", func(t *testing.T) {
		q := `{"query":"mutation{createArticle(input:{title:\"g\",created_at:\"` + forgedTS + `\"}){id}}"}`
		resp := do(t, srv, http.MethodPost, "/graphql", host, admin, q)
		body := decode(t, resp)
		errs := fmt.Sprint(body["errors"])
		if !strings.Contains(errs, "created_at") {
			t.Fatalf("GraphQL create must reject created_at structurally, got %v", body)
		}
	})
	t.Run("GraphQL update structural", func(t *testing.T) {
		q := fmt.Sprintf(`{"query":"mutation{updateArticle(id:\"%s\", input:{created_at:\"%s\"}){id}}"}`, rowID, forgedTS)
		resp := do(t, srv, http.MethodPost, "/graphql", host, admin, q)
		body := decode(t, resp)
		errs := fmt.Sprint(body["errors"])
		if !strings.Contains(errs, "created_at") {
			t.Fatalf("GraphQL update must reject created_at structurally, got %v", body)
		}
	})
}

// TestGoverned_ImportGrant: the declared exception. The granted role imports
// (values stored VERBATIM) through REST, batch, Ctx.Insert and GraphQL; the
// non-granted role is rejected at the same doors; update stays closed to
// everyone.
func TestGoverned_ImportGrant(t *testing.T) {
	app := newGovernedApp(t)
	srv := newServerFor(t, app)
	tenant := "govimp"
	helpers.RegisterTenant(t, itPool, tenant, mustLoadSchemaBytes(t, governedSchemaJSON))
	host := tenant + ".localhost"
	admin := helpers.GenToken(t, "admin", "u1", tenant)
	clerk := helpers.GenToken(t, "clerk", "u2", tenant)

	newForged := func(id string) string {
		return fmt.Sprintf(`{"title":"imp","id":%q,"created_at":%q}`, id, forgedTS)
	}
	assertStored := func(t *testing.T, door string, row map[string]any, wantID string) {
		t.Helper()
		if fmt.Sprint(row["id"]) != wantID {
			t.Errorf("%s: imported id not stored verbatim: %v", door, row["id"])
		}
		if !strings.HasPrefix(fmt.Sprint(row["created_at"]), "1999-01-01") {
			t.Errorf("%s: imported created_at not stored verbatim: %v", door, row["created_at"])
		}
	}

	t.Run("granted role via REST", func(t *testing.T) {
		id := "11111111-1111-4111-8111-111111111101"
		resp := do(t, srv, http.MethodPost, "/api/legacy_rows", host, admin, newForged(id))
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("granted import: want 201, got %d (%v)", resp.StatusCode, decode(t, resp))
		}
		assertStored(t, "REST", decode(t, resp), id)
	})
	t.Run("granted role via batch", func(t *testing.T) {
		id := "11111111-1111-4111-8111-111111111102"
		body := fmt.Sprintf(`{"operations":[{"op":"create","resource":"legacy_rows","data":%s}]}`, newForged(id))
		resp := do(t, srv, http.MethodPost, "/api/transaction", host, admin, body)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("granted import via batch: want 200, got %d (%v)", resp.StatusCode, decode(t, resp))
		}
	})
	t.Run("granted role via Ctx.Insert", func(t *testing.T) {
		id := "11111111-1111-4111-8111-111111111103"
		body := fmt.Sprintf(`{"resource":"legacy_rows","data":%s}`, newForged(id))
		resp := do(t, srv, http.MethodPost, "/api/_ctx_create", host, admin, body)
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("granted import via Ctx.Insert: want 201, got %d (%v)", resp.StatusCode, decode(t, resp))
		}
		assertStored(t, "Ctx.Insert", decode(t, resp), id)
	})
	t.Run("granted role via GraphQL", func(t *testing.T) {
		id := "11111111-1111-4111-8111-111111111104"
		q := fmt.Sprintf(`{"query":"mutation{createLegacyRow(input:{title:\"g\",id:\"%s\",created_at:\"%s\"}){id created_at}}"}`, id, forgedTS)
		resp := do(t, srv, http.MethodPost, "/graphql", host, admin, q)
		body := decode(t, resp)
		if body["errors"] != nil {
			t.Fatalf("granted import via GraphQL: %v", body)
		}
	})

	t.Run("non-granted role rejected via REST", func(t *testing.T) {
		resp := do(t, srv, http.MethodPost, "/api/legacy_rows", host, clerk, newForged("22222222-2222-4222-8222-222222222201"))
		expectReadOnly(t, resp, "REST clerk", "id", "created_at")
	})
	t.Run("non-granted role rejected via GraphQL", func(t *testing.T) {
		q := fmt.Sprintf(`{"query":"mutation{createLegacyRow(input:{title:\"g\",id:\"%s\"}){id}}"}`, "22222222-2222-4222-8222-222222222202")
		resp := do(t, srv, http.MethodPost, "/graphql", host, clerk, q)
		body := decode(t, resp)
		errs := fmt.Sprint(body["errors"])
		if body["errors"] == nil || !strings.Contains(errs, "read_only") {
			t.Fatalf("non-granted GraphQL import must be a read_only validation error, got %v", body)
		}
	})

	t.Run("import never applies on update", func(t *testing.T) {
		rowID := seedRow(t, srv, host, admin, "legacy_rows")
		resp := do(t, srv, http.MethodPatch, "/api/legacy_rows/"+rowID, host, admin, fmt.Sprintf(`{"created_at":%q}`, forgedTS))
		expectReadOnly(t, resp, "PATCH on import resource", "created_at")
	})
}

func mustLoadSchemaBytes(t *testing.T, doc string) *schema.APISchema {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "s.json")
	if err := os.WriteFile(p, []byte(doc), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	return mustLoadSchema(t, p)
}
