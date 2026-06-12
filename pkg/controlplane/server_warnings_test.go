package controlplane_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/miguelangel/appitools/pkg/controlplane"
	"github.com/miguelangel/appitools/pkg/schema"
)

// stubService satisfies controlplane.Service without a database — these tests
// exercise the HTTP layer's warning contract, not persistence.
type stubService struct{}

func (stubService) Register(_ context.Context, req controlplane.RegisterRequest) (*controlplane.Tenant, error) {
	return &controlplane.Tenant{ID: req.TenantID, PGSchema: "tenant_" + req.TenantID}, nil
}
func (stubService) GetByID(context.Context, string) (*controlplane.Tenant, error) { return nil, nil }
func (stubService) UpdateSchema(context.Context, string, *schema.APISchema) error { return nil }
func (stubService) GetSchema(context.Context, string) (*schema.APISchema, error)  { return nil, nil }

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

// `indexes` parses but is not applied yet (no executor) — a schema declaring
// it must be ACCEPTED with an explicit warning, never blessed in silence and
// never rejected (it has to stay loadable when the feature ships).
func TestSchemaWithIndexesWarnsOnPUTAndRegister(t *testing.T) {
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

	assertWarns := func(label string, rec *httptest.ResponseRecorder, out map[string]any, wantStatus int) {
		t.Helper()
		if rec.Code != wantStatus {
			t.Fatalf("%s: status %d, want %d (%s)", label, rec.Code, wantStatus, rec.Body.String())
		}
		warns, ok := out["warnings"].([]any)
		if !ok || len(warns) != 1 || !strings.Contains(warns[0].(string), "indexes on tasks are parsed but not yet applied") {
			t.Errorf("%s: expected the indexes warning, got: %s", label, rec.Body.String())
		}
	}

	rec, out := do(http.MethodPut, "/tenants/acme/schema", `{"schema":`+indexedSchema+`}`)
	assertWarns("PUT schema", rec, out, http.StatusOK)
	if out["status"] != "migration_queued" {
		t.Errorf("PUT must keep its status field, got: %v", out["status"])
	}

	rec, out = do(http.MethodPost, "/tenants", `{"tenant_id":"acme","schema":`+indexedSchema+`}`)
	assertWarns("register", rec, out, http.StatusCreated)
	if out["id"] != "acme" {
		t.Errorf("register must keep the tenant record fields, got: %s", rec.Body.String())
	}

	// Without indexes: no warnings key at all.
	plain := strings.Replace(indexedSchema, `"indexes": [{"fields": ["title"]}]`, `"indexes": []`, 1)
	plain = strings.Replace(plain, `,
			"indexes": []`, "", 1)
	rec, _ = do(http.MethodPut, "/tenants/acme/schema", `{"schema":`+plain+`}`)
	if rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), "warnings") {
		t.Errorf("schema without indexes must not warn: [%d] %s", rec.Code, rec.Body.String())
	}
}
