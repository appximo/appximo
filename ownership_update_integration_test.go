//go:build integration

// MOTOR-AUTORIZACION-S1 — the all-doors proof of the identity-column update
// rule (ENG-45 family 1, the row give-away).
//
// THE HOLE THIS CLOSES. Create FORCES a role's row-condition column to the
// caller (EnforceCreateRBAC: `POST {"owner_id": <other>}` → 403), but update
// put the condition ONLY in the WHERE: `PATCH {"owner_id": <other>}` on an
// owned row answered 200 and handed the row to another principal, through
// REST PATCH/PUT, GraphQL, the batch transaction and Ctx.Update alike, in
// every RBAC form (per-resource, role-global, condition_actions). A PUT that
// omitted a nullable condition column wrote NULL — the row left every
// principal's scope. And a role whose allowlist excluded the column had the
// attempt DROPPED silently (200, unchanged), hiding it.
//
// The contract now (codegen.EnforceUpdateRBAC, the ONE implementation every
// update door consults): for a role whose row condition is IDENTITY-bound
// ($user_id / $external_client_id), the condition column is server-owned on
// update exactly as on create — a body that supplies it with any value other
// than the caller's (another id, null) is 403 "must match the authenticated
// principal", regardless of the field allowlist (explicit, never a silent
// drop); a full-replacement PUT that omits it keeps the caller's value; and
// re-sending the caller's own id is a no-op. A LITERAL condition (a
// visibility filter such as status = "pending") is deliberately NOT bound:
// a moderator approving a pending ticket moves it out of scope on purpose.
//
// It also pins the neighbour found by the same audit: a state-machine field
// set to null (PUT omitting it, PATCH/GraphQL/batch/Ctx sending null) used
// to reach the transition guard as a non-string and surface as a 500; it is
// a named 422 now.
package appximo

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/appximo/appximo/tests/helpers"
)

const ownershipSchemaJSON = `{
  "$schema": "https://appximo.com/schema/v1",
  "version": "1",
  "name": "ownership",
  "resources": {
    "docs": {
      "fields": {
        "title":      { "type": "string", "required": true, "minLength": 1 },
        "notes":      { "type": "text" },
        "owner_id":   { "type": "uuid" },
        "created_by": { "type": "uuid" },
        "status":     { "type": "string", "enum": ["draft", "review", "published"], "default": "draft",
                        "state_machine": { "initial": "draft", "transitions": { "draft": ["review"], "review": ["published"], "published": [] } } }
      }
    },
    "tickets": {
      "fields": {
        "title":  { "type": "string", "required": true },
        "status": { "type": "string", "enum": ["pending", "approved"], "default": "pending" }
      }
    },
    "posts": {
      "fields": {
        "title":     { "type": "string", "required": true },
        "author_id": { "type": "uuid" }
      }
    }
  },
  "rbac": {
    "roles": {
      "admin":     { "resources": "*", "actions": ["*"] },
      "owner":     { "permissions": { "docs": { "actions": ["read", "create", "update", "delete"],
                       "conditions": { "field": "owner_id", "op": "eq", "val": "$user_id" } } },
                     "routes": { "ctxupdate": { "actions": ["update"] } } },
      "creator":   { "resources": ["docs"], "actions": ["read", "create", "update"],
                     "conditions": { "field": "created_by", "op": "eq", "val": "$user_id" },
                     "routes": { "ctxupdate": { "actions": ["update"] } } },
      "limited":   { "permissions": { "docs": { "actions": ["read", "create", "update"],
                       "fields": ["id", "title", "notes", "status"],
                       "conditions": { "field": "owner_id", "op": "eq", "val": "$user_id" } } },
                     "routes": { "ctxupdate": { "actions": ["update"] } } },
      "moderator": { "permissions": { "tickets": { "actions": ["read", "update"],
                       "conditions": { "field": "status", "op": "eq", "val": "pending" } } } },
      "author":    { "permissions": { "posts": { "actions": ["read", "create", "update", "delete"],
                       "conditions": { "field": "author_id", "op": "eq", "val": "$user_id" },
                       "condition_actions": ["create", "update", "delete"] } },
                     "routes": { "ctxupdate": { "actions": ["update"] } } }
    }
  }
}`

const (
	userA = "aaaaaaaa-aaaa-4aaa-8aaa-000000000001"
	userB = "aaaaaaaa-aaaa-4aaa-8aaa-000000000002"
)

func newOwnershipApp(t *testing.T) *App {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "ownership.json")
	if err := os.WriteFile(p, []byte(ownershipSchemaJSON), 0o600); err != nil {
		t.Fatalf("write schema: %v", err)
	}
	app, err := New(Config{SchemaPath: p, DSN: itConnStr, JWTSecret: helpers.JWTSecret, AdminKey: helpers.AdminKey, Env: "test"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { app.pool.Close() })
	mustRegister(t, app, Route{Method: "PATCH", Path: "/api/ctxupdate", Handler: func(ctx Ctx) error {
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

type ownershipEnv struct {
	srv   *httptest.Server
	host  string
	admin string
}

func newOwnershipEnv(t *testing.T, tenant string) ownershipEnv {
	t.Helper()
	app := newOwnershipApp(t)
	srv := newServerFor(t, app)
	helpers.RegisterTenant(t, itPool, tenant, mustLoadSchemaBytes(t, ownershipSchemaJSON))
	return ownershipEnv{srv: srv, host: tenant + ".localhost", admin: helpers.GenToken(t, "admin", userA, tenant)}
}

func (e ownershipEnv) create(t *testing.T, token, resource, body string) string {
	t.Helper()
	resp := do(t, e.srv, http.MethodPost, "/api/"+resource, e.host, token, body)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create %s: %d %v", resource, resp.StatusCode, decode(t, resp))
	}
	return fmt.Sprint(decode(t, resp)["id"])
}

// stored reads a column as the admin (unscoped) — the effect, not the status.
func (e ownershipEnv) stored(t *testing.T, resource, id, col string) any {
	t.Helper()
	resp := do(t, e.srv, http.MethodGet, "/api/"+resource+"/"+id, e.host, e.admin, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("admin read %s/%s: %d", resource, id, resp.StatusCode)
	}
	return decode(t, resp)[col]
}

// doors enumerates every update door as (name, fire) where fire sends `data`
// for row id of resource and returns the status + decoded body. GraphQL
// answers 200 with errors[] — its status is normalised to 403/422/200 from
// the message so the doors can be asserted uniformly.
type updateDoor struct {
	name string
	fire func(t *testing.T, token, resource, id, data string) (int, map[string]any)
}

func (e ownershipEnv) doors(singular string) []updateDoor {
	rest := func(method string) func(t *testing.T, token, resource, id, data string) (int, map[string]any) {
		return func(t *testing.T, token, resource, id, data string) (int, map[string]any) {
			resp := do(t, e.srv, method, "/api/"+resource+"/"+id, e.host, token, data)
			return resp.StatusCode, decode(t, resp)
		}
	}
	return []updateDoor{
		{"REST PATCH", rest(http.MethodPatch)},
		{"batch update", func(t *testing.T, token, resource, id, data string) (int, map[string]any) {
			body := fmt.Sprintf(`{"operations":[{"op":"update","resource":%q,"id":%q,"data":%s}]}`, resource, id, data)
			resp := do(t, e.srv, http.MethodPost, "/api/transaction", e.host, token, body)
			return resp.StatusCode, decode(t, resp)
		}},
		{"Ctx.Update", func(t *testing.T, token, resource, id, data string) (int, map[string]any) {
			body := fmt.Sprintf(`{"resource":%q,"id":%q,"data":%s}`, resource, id, data)
			resp := do(t, e.srv, http.MethodPatch, "/api/ctxupdate", e.host, token, body)
			return resp.StatusCode, decode(t, resp)
		}},
		{"GraphQL update", func(t *testing.T, token, resource, id, data string) (int, map[string]any) {
			// The input travels as a VARIABLE: a null value is only expressible
			// that way (the parser takes no null literal), and it is what a real
			// client library sends.
			q := fmt.Sprintf(`mutation($input: %sUpdateInput!) { update%s(id:%q, input:$input) { id } }`, singular, singular, id)
			resp := do(t, e.srv, http.MethodPost, "/graphql", e.host, token, fmt.Sprintf(`{"query":%q,"variables":{"input":%s}}`, q, data))
			body := decode(t, resp)
			errs, _ := body["errors"].([]any)
			if len(errs) == 0 {
				return http.StatusOK, body
			}
			msg := fmt.Sprint(errs[0].(map[string]any)["message"])
			body["error"] = msg
			switch {
			case strings.Contains(msg, "must match the authenticated principal"):
				return http.StatusForbidden, body
			case strings.Contains(msg, "validation_failed") || strings.Contains(msg, "cannot be null"):
				return http.StatusUnprocessableEntity, body
			}
			return http.StatusBadRequest, body
		}},
	}
}

// TestOwnership_UpdateCannotReassign: through EVERY update door, in every RBAC
// form, the identity column is server-owned — another id or null is 403 and
// the row stays the caller's; the caller's own id is a no-op.
func TestOwnership_UpdateCannotReassign(t *testing.T) {
	e := newOwnershipEnv(t, "ownup")
	cases := []struct {
		form, role, resource, col, singular string
	}{
		{"per-resource", "owner", "docs", "owner_id", "Doc"},
		{"role-global", "creator", "docs", "created_by", "Doc"},
		{"condition_actions", "author", "posts", "author_id", "Post"},
	}
	for _, c := range cases {
		tokA := helpers.GenToken(t, c.role, userA, "ownup")
		for _, d := range e.doors(c.singular) {
			t.Run(c.form+"/"+d.name, func(t *testing.T) {
				id := e.create(t, tokA, c.resource, `{"title":"mine"}`)
				for _, val := range []string{`"` + userB + `"`, "null"} {
					st, body := d.fire(t, tokA, c.resource, id, fmt.Sprintf(`{%q:%s}`, c.col, val))
					if val == "null" && d.name == "GraphQL update" && st == http.StatusBadRequest {
						// graphql-go drops a null variable (ENG-22): the input
						// arrives EMPTY and the resolver refuses it — nothing is
						// written. Not the 403, but not a door either; pinned so a
						// parser upgrade that starts passing nulls is caught here.
						if got := fmt.Sprint(e.stored(t, c.resource, id, c.col)); got != userA {
							t.Errorf("GraphQL null reached the row: %q", got)
						}
						continue
					}
					if st != http.StatusForbidden {
						t.Errorf("%s %s=%s: want 403, got %d %v — THE ROW GIVE-AWAY IS BACK", d.name, c.col, val, st, body)
					}
					if got := fmt.Sprint(e.stored(t, c.resource, id, c.col)); got != userA {
						t.Errorf("%s: after %s=%s the stored %s is %q, want the caller's %q", d.name, c.col, val, c.col, got, userA)
					}
				}
				st, body := d.fire(t, tokA, c.resource, id, fmt.Sprintf(`{%q:%q,"title":"renamed"}`, c.col, userA))
				if st != http.StatusOK {
					t.Errorf("%s re-sending the caller's own id must be a no-op 200, got %d %v", d.name, st, body)
				}
			})
		}
	}
}

// TestOwnership_PUTKeepsTheOwner: a full replacement that omits the nullable
// condition column keeps the caller's value (it used to write NULL — the row
// left every principal's scope); one that supplies another id is 403.
func TestOwnership_PUTKeepsTheOwner(t *testing.T) {
	e := newOwnershipEnv(t, "ownput")
	tokA := helpers.GenToken(t, "owner", userA, "ownput")
	id := e.create(t, tokA, "docs", `{"title":"mine"}`)

	resp := do(t, e.srv, http.MethodPut, "/api/docs/"+id, e.host, tokA, `{"title":"replaced","status":"draft"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("PUT omitting owner_id: want 200, got %d %v", resp.StatusCode, decode(t, resp))
	}
	if got := fmt.Sprint(e.stored(t, "docs", id, "owner_id")); got != userA {
		t.Errorf("PUT omitting owner_id stored %q, want the caller's %q (self-orphan)", got, userA)
	}
	resp = do(t, e.srv, http.MethodPut, "/api/docs/"+id, e.host, tokA, `{"title":"stolen","status":"draft","owner_id":"`+userB+`"}`)
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("PUT owner_id=other: want 403, got %d %v", resp.StatusCode, decode(t, resp))
	}
}

// TestOwnership_AllowlistNeverHidesTheAttempt: a role whose field allowlist
// excludes the condition column gets an explicit 403, not a silent drop.
func TestOwnership_AllowlistNeverHidesTheAttempt(t *testing.T) {
	e := newOwnershipEnv(t, "ownlim")
	tok := helpers.GenToken(t, "limited", userA, "ownlim")
	id := e.create(t, tok, "docs", `{"title":"mine"}`)
	for _, d := range e.doors("Doc") {
		st, body := d.fire(t, tok, "docs", id, `{"owner_id":"`+userB+`","title":"x"}`)
		if st != http.StatusForbidden {
			t.Errorf("%s: allowlisted role reassigning owner_id: want an explicit 403, got %d %v (a silent drop hides the attempt)", d.name, st, body)
		}
	}
}

// TestOwnership_LiteralConditionStillMoves: a literal row condition is a
// visibility filter, not ownership — the moderator workflow keeps working.
func TestOwnership_LiteralConditionStillMoves(t *testing.T) {
	e := newOwnershipEnv(t, "ownlit")
	tok := helpers.GenToken(t, "moderator", userA, "ownlit")
	id := e.create(t, e.admin, "tickets", `{"title":"t"}`)
	resp := do(t, e.srv, http.MethodPatch, "/api/tickets/"+id, e.host, tok, `{"status":"approved"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("moderator approving a pending ticket: want 200, got %d %v", resp.StatusCode, decode(t, resp))
	}
	if got := fmt.Sprint(e.stored(t, "tickets", id, "status")); got != "approved" {
		t.Errorf("stored status %q, want approved", got)
	}
}

// TestOwnership_HiddenRowStays404: the BOLA contract is untouched — another
// principal's row is 404 through every door, with or without the column.
func TestOwnership_HiddenRowStays404(t *testing.T) {
	e := newOwnershipEnv(t, "own404")
	tokA := helpers.GenToken(t, "owner", userA, "own404")
	tokB := helpers.GenToken(t, "owner", userB, "own404")
	id := e.create(t, tokB, "docs", `{"title":"B's"}`)
	for _, d := range e.doors("Doc") {
		for _, data := range []string{`{"title":"x"}`, `{"owner_id":"` + userA + `"}`} {
			st, body := d.fire(t, tokA, "docs", id, data)
			want := http.StatusNotFound
			if d.name == "GraphQL update" {
				want = http.StatusBadRequest // "not found" error text, normalised
			}
			if st != want {
				t.Errorf("%s %s on B's row: want %d, got %d %v", d.name, data, want, st, body)
			}
		}
	}
	if got := fmt.Sprint(e.stored(t, "docs", id, "owner_id")); got != userB {
		t.Errorf("B's row changed hands: %q", got)
	}
}

// TestStateField_NullIsANamed422: a state-machine field can never be null —
// PUT omitting it and PATCH/batch/GraphQL/Ctx sending null are 422 naming the
// field (they used to reach the SQL guard as a non-string and 500).
func TestStateField_NullIsANamed422(t *testing.T) {
	e := newOwnershipEnv(t, "ownnull")
	id := e.create(t, e.admin, "docs", `{"title":"s"}`)
	resp := do(t, e.srv, http.MethodPut, "/api/docs/"+id, e.host, e.admin, `{"title":"s2"}`)
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Errorf("PUT omitting the state field: want 422, got %d %v", resp.StatusCode, decode(t, resp))
	}
	for _, d := range e.doors("Doc") {
		st, body := d.fire(t, e.admin, "docs", id, `{"status":null}`)
		if d.name == "GraphQL update" && st == http.StatusBadRequest {
			continue // graphql-go drops the null variable → "empty input", nothing written (ENG-22)
		}
		if st != http.StatusUnprocessableEntity {
			t.Errorf("%s status=null: want 422, got %d %v", d.name, st, body)
		}
	}
	if got := fmt.Sprint(e.stored(t, "docs", id, "status")); got != "draft" {
		t.Errorf("status changed to %q", got)
	}
}
