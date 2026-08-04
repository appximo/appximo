package platformadmin

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/appximo/appximo/pkg/controlplane"
	"github.com/appximo/appximo/pkg/migration"
	"github.com/appximo/appximo/pkg/schema"
	"github.com/appximo/appximo/pkg/schemahistory"
)

// fakeCP is a controlplane.Service stub: the deploy handlers only delegate to it,
// so a fake lets us test routing/auth/parse/error-surfacing without a database.
type fakeCP struct {
	preview    *migration.Preview
	outcome    *migration.ApplyOutcome
	gotSchema  *schema.APISchema
	gotApplied []string
	previewErr error
	applyErr   error
	getErr     error

	// history + rollback (VERSION-S1)
	histPage           *schemahistory.Page
	histVersion        *schemahistory.Version
	rollbackRes        *controlplane.RollbackResult
	histErr            error
	rollbackErr        error
	gotRollbackVersion int
	gotRollbackDrops   []string
}

func (f *fakeCP) Register(context.Context, controlplane.RegisterRequest) (*controlplane.Tenant, error) {
	return nil, nil
}
func (f *fakeCP) GetByID(context.Context, string) (*controlplane.Tenant, error) { return nil, nil }
func (f *fakeCP) UpdateSchema(context.Context, string, *schema.APISchema) error { return nil }
func (f *fakeCP) UpdateSchemaApproved(_ context.Context, _ string, _ *schema.APISchema, approved []string) (*migration.ApplyOutcome, error) {
	f.gotApplied = approved
	return f.outcome, f.applyErr
}
func (f *fakeCP) PreviewSchema(context.Context, string, *schema.APISchema, []string) (*migration.Preview, error) {
	return f.preview, f.previewErr
}
func (f *fakeCP) GetSchema(context.Context, string) (*schema.APISchema, error) {
	return f.gotSchema, f.getErr
}
func (f *fakeCP) ListSchemaHistory(context.Context, string, int, int) (*schemahistory.Page, error) {
	if f.histErr != nil {
		return nil, f.histErr
	}
	if f.histPage != nil {
		return f.histPage, nil
	}
	return &schemahistory.Page{}, nil
}
func (f *fakeCP) GetSchemaVersion(context.Context, string, int) (*schemahistory.Version, error) {
	if f.histErr != nil {
		return nil, f.histErr
	}
	if f.histVersion == nil {
		return nil, schemahistory.ErrVersionNotFound
	}
	return f.histVersion, nil
}
func (f *fakeCP) RollbackSchema(_ context.Context, _ string, version int, approved []string) (*controlplane.RollbackResult, error) {
	f.gotRollbackVersion = version
	f.gotRollbackDrops = approved
	return f.rollbackRes, f.rollbackErr
}

const validSchemaJSON = `{"$schema":"https://appximo.com/schema/v1","version":"1","name":"todo-api","resources":{"tasks":{"fields":{"title":{"type":"string","required":true}}}}}`

func deployService(cp controlplane.Service, adminKey string) http.Handler {
	s := NewService(nil, nil, cp, nil, Config{JWTSecret: unitSecret})
	r := chi.NewRouter()
	s.Register(r, nil, adminKey)
	return r
}

func req(t *testing.T, h http.Handler, method, path, body, adminKey string) *httptest.ResponseRecorder {
	t.Helper()
	rr := httptest.NewRecorder()
	rq := httptest.NewRequest(method, path, strings.NewReader(body))
	if adminKey != "" {
		rq.Header.Set("X-Admin-Key", adminKey)
	}
	h.ServeHTTP(rr, rq)
	return rr
}

func TestDeployRoutesRequireAuth(t *testing.T) {
	h := deployService(&fakeCP{}, "secret-key")
	for _, tc := range []struct{ method, path, body string }{
		{http.MethodGet, "/admin/tenants/acme/schema", ""},
		{http.MethodPut, "/admin/tenants/acme/schema", `{"schema":` + validSchemaJSON + `}`},
	} {
		if rr := req(t, h, tc.method, tc.path, tc.body, ""); rr.Code != http.StatusForbidden {
			t.Fatalf("%s %s without auth: got %d, want 403", tc.method, tc.path, rr.Code)
		}
	}
}

func TestGetTenantSchema(t *testing.T) {
	var sc schema.APISchema
	_ = json.Unmarshal([]byte(validSchemaJSON), &sc)
	h := deployService(&fakeCP{gotSchema: &sc}, "k")
	rr := req(t, h, http.MethodGet, "/admin/tenants/acme/schema", "", "k")
	if rr.Code != http.StatusOK {
		t.Fatalf("get schema: got %d, want 200", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), `"tasks"`) {
		t.Fatalf("get schema body missing the resource: %s", rr.Body.String())
	}

	nf := deployService(&fakeCP{getErr: controlplane.ErrNotFound}, "k")
	if rr := req(t, nf, http.MethodGet, "/admin/tenants/none/schema", "", "k"); rr.Code != http.StatusNotFound {
		t.Fatalf("get unknown tenant schema: got %d, want 404", rr.Code)
	}
}

func TestDeployDryRunPreview(t *testing.T) {
	pv := &migration.Preview{
		PGSchema: "tenant_acme",
		Apply:    []string{"ADD COLUMN tasks.due"},
		Destructive: []migration.DestructiveOp{{
			Key: "tasks.old", Kind: "column", Table: "tasks", Column: "old",
			RowsLost: 3, TableRows: 5, Approved: false, Summary: "DROP COLUMN tasks.old — 3 of 5 row(s) lost",
		}},
	}
	h := deployService(&fakeCP{preview: pv}, "k")
	rr := req(t, h, http.MethodPut, "/admin/tenants/acme/schema",
		`{"schema":`+validSchemaJSON+`,"dry_run":true}`, "k")
	if rr.Code != http.StatusOK {
		t.Fatalf("dry run: got %d, want 200 (%s)", rr.Code, rr.Body.String())
	}
	var resp struct {
		Status  string            `json:"status"`
		Preview migration.Preview `json:"preview"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Status != "dry_run" || len(resp.Preview.Destructive) != 1 || resp.Preview.Destructive[0].RowsLost != 3 {
		t.Fatalf("dry run preview not surfaced: %+v", resp)
	}
}

func TestDeployApplyWithApproval(t *testing.T) {
	cp := &fakeCP{outcome: &migration.ApplyOutcome{AppliedDrops: []string{"tasks.old"}}}
	h := deployService(cp, "k")
	rr := req(t, h, http.MethodPut, "/admin/tenants/acme/schema",
		`{"schema":`+validSchemaJSON+`,"dry_run":false,"approved_drops":["tasks.old"]}`, "k")
	if rr.Code != http.StatusOK {
		t.Fatalf("apply: got %d, want 200 (%s)", rr.Code, rr.Body.String())
	}
	if len(cp.gotApplied) != 1 || cp.gotApplied[0] != "tasks.old" {
		t.Fatalf("approved_drops not forwarded to the engine: %v", cp.gotApplied)
	}
	if !strings.Contains(rr.Body.String(), `"applied_drops"`) {
		t.Fatalf("apply response missing applied_drops: %s", rr.Body.String())
	}
}

func TestDeployApplyErrorIsActionable(t *testing.T) {
	cp := &fakeCP{applyErr: errors.New("apply migration: column \"nombre\" contains null values (NOT NULL cannot be enforced)")}
	h := deployService(cp, "k")
	rr := req(t, h, http.MethodPut, "/admin/tenants/acme/schema",
		`{"schema":`+validSchemaJSON+`}`, "k")
	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("apply error: got %d, want 422", rr.Code)
	}
	// The engine's actionable message must reach the client (not a masked generic).
	if !strings.Contains(rr.Body.String(), "NOT NULL") {
		t.Fatalf("apply error not surfaced verbatim: %s", rr.Body.String())
	}
}

func TestDeployInvalidSchemaRejected(t *testing.T) {
	h := deployService(&fakeCP{}, "k")
	// Unknown field type → schema.Validate rejects it BEFORE any engine call.
	bad := `{"schema":{"$schema":"https://appximo.com/schema/v1","version":"1","name":"x","resources":{"t":{"fields":{"f":{"type":"number"}}}}}}`
	rr := req(t, h, http.MethodPut, "/admin/tenants/acme/schema", bad, "k")
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("invalid schema: got %d, want 400 (%s)", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "errors") {
		t.Fatalf("invalid schema response missing errors list: %s", rr.Body.String())
	}
}
