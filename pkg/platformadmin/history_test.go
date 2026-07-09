package platformadmin

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/miguelangel/appitools/pkg/controlplane"
	"github.com/miguelangel/appitools/pkg/migration"
	"github.com/miguelangel/appitools/pkg/schema"
	"github.com/miguelangel/appitools/pkg/schemahistory"
)

// The history + rollback routes are thin delegates (VERSION-S1): these tests
// pin the routing, auth, parameter validation, the dry-run vs apply dispatch,
// and the error taxonomy (unknown version → 404, engine error → actionable 422)
// against the fake control plane. The real append/list/rollback semantics are
// covered by the schemahistory integration test and the live acceptance run.

func TestHistoryRoutesRequireAuth(t *testing.T) {
	h := deployService(&fakeCP{}, "secret-key")
	for _, tc := range []struct{ method, path, body string }{
		{http.MethodGet, "/admin/tenants/acme/schema/history", ""},
		{http.MethodGet, "/admin/tenants/acme/schema/history/1", ""},
		{http.MethodPost, "/admin/tenants/acme/schema/rollback", `{"version":1}`},
		// Flow-test routes (FLOWTEST-S1) share the same platform gate.
		{http.MethodGet, "/admin/tenants/acme/flows", ""},
		{http.MethodPost, "/admin/tenants/acme/flows", `{"flow":{"name":"x","steps":[]}}`},
		{http.MethodPost, "/admin/tenants/acme/flows/run", ""},
		{http.MethodGet, "/admin/tenants/acme/flows/runs", ""},
	} {
		if rr := req(t, h, tc.method, tc.path, tc.body, ""); rr.Code != http.StatusForbidden {
			t.Fatalf("%s %s without auth: got %d, want 403", tc.method, tc.path, rr.Code)
		}
	}
}

func TestSchemaHistoryList(t *testing.T) {
	fake := &fakeCP{histPage: &schemahistory.Page{
		Versions: []schemahistory.Version{
			{Version: 2, Hash: "beef", Source: "deploy", CreatedAt: time.Now(), Resources: []string{"tasks", "users"}},
			{Version: 1, Hash: "cafe", Source: "register", CreatedAt: time.Now(), Resources: []string{"tasks"}},
		},
		Total: 2, Page: 1, PerPage: 50,
	}}
	rr := req(t, deployService(fake, "k"), http.MethodGet, "/admin/tenants/acme/schema/history", "", "k")
	if rr.Code != http.StatusOK {
		t.Fatalf("history list: got %d, want 200 (%s)", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	for _, want := range []string{`"total":2`, `"version":2`, `"source":"register"`, `"users"`} {
		if !strings.Contains(body, want) {
			t.Fatalf("history list body missing %q: %s", want, body)
		}
	}
}

func TestSchemaVersionGet(t *testing.T) {
	fake := &fakeCP{histVersion: &schemahistory.Version{
		Version: 3, Hash: "beef", Source: "deploy", CreatedAt: time.Now(),
		Resources: []string{"tasks"}, SchemaJSON: []byte(validSchemaJSON),
	}}
	h := deployService(fake, "k")

	rr := req(t, h, http.MethodGet, "/admin/tenants/acme/schema/history/3", "", "k")
	if rr.Code != http.StatusOK {
		t.Fatalf("get version: got %d, want 200 (%s)", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), `"tasks"`) || !strings.Contains(rr.Body.String(), `"version":3`) {
		t.Fatalf("get version body missing schema/version: %s", rr.Body.String())
	}

	// Unknown version → 404 with the specific message.
	rr = req(t, deployService(&fakeCP{}, "k"), http.MethodGet, "/admin/tenants/acme/schema/history/9", "", "k")
	if rr.Code != http.StatusNotFound || !strings.Contains(rr.Body.String(), "schema version not found") {
		t.Fatalf("unknown version: got %d %q, want 404 version-not-found", rr.Code, rr.Body.String())
	}

	// Non-numeric / non-positive version → 400.
	for _, p := range []string{"/admin/tenants/acme/schema/history/zero", "/admin/tenants/acme/schema/history/0"} {
		if rr := req(t, h, http.MethodGet, p, "", "k"); rr.Code != http.StatusBadRequest {
			t.Fatalf("%s: got %d, want 400", p, rr.Code)
		}
	}
}

func TestRollbackDryRunUsesPreviewMachinery(t *testing.T) {
	fake := &fakeCP{
		histVersion: &schemahistory.Version{Version: 1, Hash: "cafe", SchemaJSON: []byte(validSchemaJSON)},
		preview: &migration.Preview{
			PGSchema: "tenant_acme",
			Destructive: []migration.DestructiveOp{{
				Key: "tasks.notes", Kind: "column", Table: "tasks", Column: "notes",
				RowsLost: 7, TableRows: 10,
				Summary: "DROP COLUMN tasks.notes — 7 of 10 row(s) hold a value that will be permanently lost",
			}},
		},
	}
	rr := req(t, deployService(fake, "k"), http.MethodPost, "/admin/tenants/acme/schema/rollback",
		`{"version":1,"dry_run":true}`, "k")
	if rr.Code != http.StatusOK {
		t.Fatalf("rollback dry-run: got %d, want 200 (%s)", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	// The preview IS the deploy preview: the destructive drop with its measured
	// impact must ride through untouched (the honesty contract).
	for _, want := range []string{`"status":"dry_run"`, `"target_version":1`, `"rows_lost":7`, `"tasks.notes"`} {
		if !strings.Contains(body, want) {
			t.Fatalf("dry-run body missing %q: %s", want, body)
		}
	}
	if fake.gotRollbackVersion != 0 {
		t.Fatal("dry-run must NOT call RollbackSchema (applies nothing)")
	}
}

func TestRollbackApply(t *testing.T) {
	var sc schema.APISchema
	_ = json.Unmarshal([]byte(validSchemaJSON), &sc)
	fake := &fakeCP{rollbackRes: &controlplane.RollbackResult{
		Outcome:       &migration.ApplyOutcome{AppliedDrops: []string{"tasks.notes"}, GatedDrops: []string{"tasks.extra"}},
		TargetVersion: 1, NewVersion: 4, Schema: &sc,
	}}
	rr := req(t, deployService(fake, "k"), http.MethodPost, "/admin/tenants/acme/schema/rollback",
		`{"version":1,"approved_drops":["tasks.notes"]}`, "k")
	if rr.Code != http.StatusOK {
		t.Fatalf("rollback apply: got %d, want 200 (%s)", rr.Code, rr.Body.String())
	}
	if fake.gotRollbackVersion != 1 || len(fake.gotRollbackDrops) != 1 || fake.gotRollbackDrops[0] != "tasks.notes" {
		t.Fatalf("RollbackSchema got (v=%d, drops=%v), want (1, [tasks.notes])", fake.gotRollbackVersion, fake.gotRollbackDrops)
	}
	body := rr.Body.String()
	for _, want := range []string{`"status":"rolled_back"`, `"target_version":1`, `"new_version":4`,
		`"applied_drops":["tasks.notes"]`, `"gated_drops":["tasks.extra"]`, `"resources":{"tasks"`} {
		if !strings.Contains(body, want) {
			t.Fatalf("apply body missing %q: %s", want, body)
		}
	}
}

func TestRollbackErrorTaxonomy(t *testing.T) {
	// Unknown version → 404.
	rr := req(t, deployService(&fakeCP{rollbackErr: schemahistory.ErrVersionNotFound}, "k"),
		http.MethodPost, "/admin/tenants/acme/schema/rollback", `{"version":9}`, "k")
	if rr.Code != http.StatusNotFound {
		t.Fatalf("unknown version rollback: got %d, want 404", rr.Code)
	}
	// Engine failure → actionable 422, never a masked 500 (the super-admin path).
	rr = req(t, deployService(&fakeCP{rollbackErr: errFake("NOT NULL over populated data")}, "k"),
		http.MethodPost, "/admin/tenants/acme/schema/rollback", `{"version":2}`, "k")
	if rr.Code != http.StatusUnprocessableEntity || !strings.Contains(rr.Body.String(), "NOT NULL over populated data") {
		t.Fatalf("engine failure: got %d %q, want actionable 422", rr.Code, rr.Body.String())
	}
	// Missing/invalid version → 400.
	rr = req(t, deployService(&fakeCP{}, "k"), http.MethodPost, "/admin/tenants/acme/schema/rollback", `{}`, "k")
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("missing version: got %d, want 400", rr.Code)
	}
}

type errFake string

func (e errFake) Error() string { return string(e) }
