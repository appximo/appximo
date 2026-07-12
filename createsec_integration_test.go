//go:build integration

// FASE3-SEC-V1 integration tests (Hallazgo 2): the generated CREATE path now
// enforces the role's row-level RBAC condition + field allowlist — the SAME
// enforcement read/update/delete already apply — closing the mass-assignment hole
// where a row-scoped role could POST a record attributed to ANOTHER principal.
// Both surfaces are covered: REST POST and the GraphQL create mutation, which share
// codegen.EnforceCreateRBAC via the codegen.RunInsert core. Reuses the shared
// Postgres container + control plane from TestMain in library_integration_test.go.
package appitools

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/miguelangel/appitools/pkg/schema"
	"github.com/miguelangel/appitools/tests/helpers"
)

const (
	csecAlice = "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
	csecBob   = "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb"
	csecAdmin = "11111111-1111-1111-1111-111111111111"
	csecLim   = "cccccccc-cccc-cccc-cccc-cccccccccccc"
)

func createSecFixturePath() string {
	return filepath.Join(helpers.RepoRoot(), "tests", "fixtures", "schemas", "createsec.json")
}

func newCreateSecApp(t *testing.T) *httptest.Server {
	t.Helper()
	app, err := New(Config{
		SchemaPath: createSecFixturePath(),
		DSN:        itConnStr,
		JWTSecret:  helpers.JWTSecret,
		AdminKey:   helpers.AdminKey,
		Env:        "test",
	})
	if err != nil {
		t.Fatalf("New(createsec): %v", err)
	}
	t.Cleanup(func() { app.pool.Close() })
	srv := httptest.NewServer(app.buildRouter(app.bootSurface()))
	t.Cleanup(srv.Close)
	return srv
}

func loadCreateSecSchema(t *testing.T) *schema.APISchema {
	t.Helper()
	s, err := schema.LoadFromFile(createSecFixturePath())
	if err != nil {
		t.Fatalf("load createsec fixture: %v", err)
	}
	return s
}

// csecRow reads the stored owner_id + secret_note for a record id (direct DB read,
// bypassing RBAC field filtering, to prove what was actually persisted).
func csecRow(t *testing.T, tenant, id string) (ownerID, secretNote string) {
	t.Helper()
	var owner, note *string
	err := itPool.QueryRow(context.Background(),
		"SELECT owner_id::text, secret_note FROM tenant_"+tenant+".records WHERE id=$1", id).Scan(&owner, &note)
	if err != nil {
		t.Fatalf("read stored record %s: %v", id, err)
	}
	if owner != nil {
		ownerID = *owner
	}
	if note != nil {
		secretNote = *note
	}
	return ownerID, secretNote
}

// gqlReq POSTs a GraphQL query to /graphql with the tenant Host + bearer token and
// returns the decoded response (GraphQL always answers HTTP 200; check errors[]).
func gqlReq(t *testing.T, srv *httptest.Server, host, token, query string) map[string]any {
	t.Helper()
	body, _ := json.Marshal(map[string]any{"query": query})
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/graphql", bytes.NewReader(body))
	req.Host = host
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("graphql request: %v", err)
	}
	defer resp.Body.Close()
	var m map[string]any
	json.NewDecoder(resp.Body).Decode(&m) //nolint:errcheck
	return m
}

func gqlHasError(result map[string]any) bool {
	errs, _ := result["errors"].([]any)
	return len(errs) > 0
}

// ── REST ──────────────────────────────────────────────────────────────────────

func TestCreateSec_REST_RowConditionEnforced(t *testing.T) {
	const tn = "csecrest"
	helpers.RegisterTenant(t, itPool, tn, loadCreateSecSchema(t))
	srv := newCreateSecApp(t)
	const host = tn + ".localhost"
	alice := helpers.GenToken(t, "owner", csecAlice, tn)

	// 1. owner creating with its OWN owner_id → 201, stored as alice.
	resp := do(t, srv, "POST", "/api/records", host, alice, `{"title":"mine","owner_id":"`+csecAlice+`"}`)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("own create: want 201, got %d", resp.StatusCode)
	}
	id := decode(t, resp)["id"].(string)
	if owner, _ := csecRow(t, tn, id); owner != csecAlice {
		t.Fatalf("stored owner_id = %q, want %q", owner, csecAlice)
	}

	// 2. owner OMITTING owner_id → 201, server FORCES it to the principal (alice).
	resp = do(t, srv, "POST", "/api/records", host, alice, `{"title":"forced"}`)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("omitted create: want 201, got %d", resp.StatusCode)
	}
	id2 := decode(t, resp)["id"].(string)
	if owner, _ := csecRow(t, tn, id2); owner != csecAlice {
		t.Fatalf("omitted owner_id must be forced to %q, got %q", csecAlice, owner)
	}

	// 3. owner POSTing ANOTHER principal's owner_id → 403 (mass-assignment blocked).
	resp = do(t, srv, "POST", "/api/records", host, alice, `{"title":"theirs","owner_id":"`+csecBob+`"}`)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("foreign owner_id must be 403, got %d", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestCreateSec_REST_FieldAllowlistEnforced(t *testing.T) {
	const tn = "csecallow"
	helpers.RegisterTenant(t, itPool, tn, loadCreateSecSchema(t))
	srv := newCreateSecApp(t)
	const host = tn + ".localhost"
	lim := helpers.GenToken(t, "limited", csecLim, tn)

	// "limited" allowlist = [id, title, status]; secret_note is OUTSIDE it and must
	// be dropped on create (never persisted), not error.
	resp := do(t, srv, "POST", "/api/records", host, lim, `{"title":"x","status":"open","secret_note":"leak"}`)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("limited create: want 201, got %d", resp.StatusCode)
	}
	id := decode(t, resp)["id"].(string)
	if _, note := csecRow(t, tn, id); note != "" {
		t.Fatalf("secret_note outside allowlist must be dropped, persisted %q", note)
	}
}

func TestCreateSec_REST_UnrestrictedRoleUnchanged(t *testing.T) {
	const tn = "csecadmin"
	helpers.RegisterTenant(t, itPool, tn, loadCreateSecSchema(t))
	srv := newCreateSecApp(t)
	const host = tn + ".localhost"
	admin := helpers.GenToken(t, "admin", csecAdmin, tn)

	// admin (resources:*, actions:*, no condition, no allowlist) must keep creating
	// freely — an arbitrary owner_id and secret_note both persist (the GATE-WRITE /
	// regression case: the fix must not touch the unrestricted path).
	resp := do(t, srv, "POST", "/api/records", host, admin,
		`{"title":"any","owner_id":"`+csecBob+`","secret_note":"kept"}`)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("admin create: want 201, got %d", resp.StatusCode)
	}
	id := decode(t, resp)["id"].(string)
	owner, note := csecRow(t, tn, id)
	if owner != csecBob || note != "kept" {
		t.Fatalf("admin create must persist body verbatim, got owner=%q note=%q", owner, note)
	}
}

// ── GraphQL (same enforcement, via codegen.RunInsert / EnforceCreateRBAC) ───────

func TestCreateSec_GraphQL_RowConditionEnforced(t *testing.T) {
	const tn = "csecgql"
	helpers.RegisterTenant(t, itPool, tn, loadCreateSecSchema(t))
	srv := newCreateSecApp(t)
	const host = tn + ".localhost"
	alice := helpers.GenToken(t, "owner", csecAlice, tn)

	// 1. own owner_id → ok.
	res := gqlReq(t, srv, host, alice,
		`mutation { createRecord(input: {title: "g-mine", owner_id: "`+csecAlice+`"}) { id owner_id } }`)
	if gqlHasError(res) {
		t.Fatalf("own create must succeed, errors: %v", res["errors"])
	}

	// 2. omitted owner_id → forced to alice.
	res = gqlReq(t, srv, host, alice, `mutation { createRecord(input: {title: "g-forced"}) { id owner_id } }`)
	if gqlHasError(res) {
		t.Fatalf("omitted create must succeed, errors: %v", res["errors"])
	}
	data, _ := res["data"].(map[string]any)
	rec, _ := data["createRecord"].(map[string]any)
	id, _ := rec["id"].(string)
	if owner, _ := csecRow(t, tn, id); owner != csecAlice {
		t.Fatalf("GraphQL omitted owner_id must be forced to %q, got %q", csecAlice, owner)
	}

	// 3. foreign owner_id → REJECTED (mass-assignment blocked, GraphQL error).
	res = gqlReq(t, srv, host, alice,
		`mutation { createRecord(input: {title: "g-theirs", owner_id: "`+csecBob+`"}) { id } }`)
	if !gqlHasError(res) {
		t.Fatalf("GraphQL foreign owner_id must error (mass-assignment), got data: %v", res["data"])
	}
}
