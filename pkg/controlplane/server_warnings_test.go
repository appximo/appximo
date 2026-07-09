package controlplane_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/miguelangel/appitools/pkg/controlplane"
	"github.com/miguelangel/appitools/pkg/migration"
	"github.com/miguelangel/appitools/pkg/schema"
	"github.com/miguelangel/appitools/pkg/schemahistory"
)

// stubService satisfies controlplane.Service without a database — these tests
// exercise the HTTP layer's warning contract, not persistence.
type stubService struct{}

func (stubService) Register(_ context.Context, req controlplane.RegisterRequest) (*controlplane.Tenant, error) {
	return &controlplane.Tenant{ID: req.TenantID, PGSchema: "tenant_" + req.TenantID}, nil
}
func (stubService) GetByID(context.Context, string) (*controlplane.Tenant, error) { return nil, nil }
func (stubService) UpdateSchema(context.Context, string, *schema.APISchema) error { return nil }
func (stubService) UpdateSchemaApproved(context.Context, string, *schema.APISchema, []string) (*migration.ApplyOutcome, error) {
	return &migration.ApplyOutcome{}, nil
}
func (stubService) PreviewSchema(context.Context, string, *schema.APISchema, []string) (*migration.Preview, error) {
	return &migration.Preview{}, nil
}
func (stubService) GetSchema(context.Context, string) (*schema.APISchema, error) { return nil, nil }
func (stubService) ListSchemaHistory(context.Context, string, int, int) (*schemahistory.Page, error) {
	return &schemahistory.Page{}, nil
}
func (stubService) GetSchemaVersion(context.Context, string, int) (*schemahistory.Version, error) {
	return nil, schemahistory.ErrVersionNotFound
}
func (stubService) RollbackSchema(context.Context, string, int, []string) (*controlplane.RollbackResult, error) {
	return nil, schemahistory.ErrVersionNotFound
}

const indexedSchema = `{
	"$schema": "s", "version": "1", "name": "t",
	"resources": {
		"tasks": {
			"fields": {"title": {"type": "string"}},
			"indexes": [{"fields": ["title"]}]
		}
	},
	"rbac": {"roles": {"admin": {"resources": "*", "actions": ["*"]}}}
}`

// `indexes` are now MATERIALIZED at tenant migration (BUGS-V1), so a schema
// declaring them must be ACCEPTED with NO "not yet applied" warning — the index
// DDL runs at registration. (The schema must still be loadable/valid.)
func TestSchemaWithIndexesNoLongerWarns(t *testing.T) {
	router := controlplane.NewControlPlaneRouter(stubService{}, "k")

	do := func(method, path, body string) (*httptest.ResponseRecorder, map[string]any) {
		t.Helper()
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("X-Admin-Key", "k")
		router.ServeHTTP(rec, req)
		var out map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatalf("%s %s: body not JSON: %s", method, path, rec.Body.String())
		}
		return rec, out
	}

	assertNoWarn := func(label string, rec *httptest.ResponseRecorder, wantStatus int) {
		t.Helper()
		if rec.Code != wantStatus {
			t.Fatalf("%s: status %d, want %d (%s)", label, rec.Code, wantStatus, rec.Body.String())
		}
		if strings.Contains(rec.Body.String(), "warnings") {
			t.Errorf("%s: indexes are applied now — must NOT warn, got: %s", label, rec.Body.String())
		}
	}

	rec, out := do(http.MethodPut, "/tenants/acme/schema", `{"schema":`+indexedSchema+`}`)
	assertNoWarn("PUT schema", rec, http.StatusOK)
	if out["status"] != "migration_queued" {
		t.Errorf("PUT must keep its status field, got: %v", out["status"])
	}

	rec, out = do(http.MethodPost, "/tenants", `{"tenant_id":"acme","schema":`+indexedSchema+`}`)
	assertNoWarn("register", rec, http.StatusCreated)
	if out["id"] != "acme" {
		t.Errorf("register must keep the tenant record fields, got: %s", rec.Body.String())
	}
}
