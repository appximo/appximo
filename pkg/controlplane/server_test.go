package controlplane_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/miguelangel/appitools/pkg/controlplane"
	"github.com/miguelangel/appitools/pkg/migration"
	"github.com/miguelangel/appitools/pkg/schema"
	"github.com/miguelangel/appitools/pkg/schemahistory"
)

const testAdminKey = "test-admin-key-abc123"

// ── in-memory mock service ────────────────────────────────────────────────────

type inMemService struct {
	tenants map[string]*controlplane.Tenant
	schemas map[string]*schema.APISchema

	// Recorded inputs of the last UpdateSchemaApproved / PreviewSchema call, so a
	// handler test can assert the dry_run / approved_drops fields were wired through.
	lastApprovedDrops []string
	lastPreviewDrops  []string
	previewCalled     bool
	// updateCalled records whether the APPLY path ran, so a dry-run test can
	// assert it did not — which is the actual contract, and is not provable by
	// comparing the stored schema (the fixture seeds the same one).
	updateCalled bool
	// Optional override: when set, PreviewSchema returns this (so a test can shape the
	// dry-run response). Otherwise a minimal empty preview is returned.
	previewResult *migration.Preview
}

func newInMemService() *inMemService {
	return &inMemService{
		tenants: make(map[string]*controlplane.Tenant),
		schemas: make(map[string]*schema.APISchema),
	}
}

func (m *inMemService) Register(_ context.Context, req controlplane.RegisterRequest) (*controlplane.Tenant, error) {
	if _, exists := m.tenants[req.TenantID]; exists {
		return nil, fmt.Errorf("tenant %q already exists: %w", req.TenantID, controlplane.ErrAlreadyExists)
	}
	// Minimal ID validation — mirrors what RegisterTenant does.
	if len(req.TenantID) < 2 {
		return nil, fmt.Errorf("invalid tenant id: %w", controlplane.ErrInvalidInput)
	}
	t := &controlplane.Tenant{
		ID:          req.TenantID,
		PGSchema:    "tenant_" + req.TenantID,
		DisplayName: req.DisplayName,
		Email:       req.Email,
		Plan:        req.Plan,
		CreatedAt:   time.Now(),
	}
	m.tenants[req.TenantID] = t
	if req.Schema != nil {
		m.schemas[req.TenantID] = req.Schema
	}
	return t, nil
}

func (m *inMemService) GetByID(_ context.Context, id string) (*controlplane.Tenant, error) {
	t, ok := m.tenants[id]
	if !ok {
		return nil, fmt.Errorf("tenant %q: %w", id, controlplane.ErrNotFound)
	}
	return t, nil
}

func (m *inMemService) UpdateSchema(ctx context.Context, id string, s *schema.APISchema) error {
	_, err := m.UpdateSchemaApproved(ctx, id, s, nil)
	return err
}

func (m *inMemService) UpdateSchemaApproved(_ context.Context, id string, s *schema.APISchema, approvedDrops []string) (*migration.ApplyOutcome, error) {
	if _, ok := m.tenants[id]; !ok {
		return nil, fmt.Errorf("tenant %q: %w", id, controlplane.ErrNotFound)
	}
	m.schemas[id] = s
	m.lastApprovedDrops = approvedDrops
	m.updateCalled = true
	// Echo the approvals as "applied" so a handler test can assert wiring.
	return &migration.ApplyOutcome{AppliedDrops: approvedDrops}, nil
}

func (m *inMemService) PreviewSchema(_ context.Context, id string, _ *schema.APISchema, approvedDrops []string) (*migration.Preview, error) {
	if _, ok := m.tenants[id]; !ok {
		return nil, fmt.Errorf("tenant %q: %w", id, controlplane.ErrNotFound)
	}
	m.previewCalled = true
	m.lastPreviewDrops = approvedDrops
	if m.previewResult != nil {
		return m.previewResult, nil
	}
	return &migration.Preview{PGSchema: "tenant_" + id, Empty: true}, nil
}

func (m *inMemService) GetSchema(_ context.Context, id string) (*schema.APISchema, error) {
	if _, ok := m.tenants[id]; !ok {
		return nil, fmt.Errorf("tenant %q: %w", id, controlplane.ErrNotFound)
	}
	return m.schemas[id], nil
}
func (m *inMemService) ListSchemaHistory(context.Context, string, int, int) (*schemahistory.Page, error) {
	return &schemahistory.Page{}, nil
}
func (m *inMemService) GetSchemaVersion(context.Context, string, int) (*schemahistory.Version, error) {
	return nil, schemahistory.ErrVersionNotFound
}
func (m *inMemService) RollbackSchema(context.Context, string, int, []string) (*controlplane.RollbackResult, error) {
	return nil, schemahistory.ErrVersionNotFound
}

// ── helpers ───────────────────────────────────────────────────────────────────

func newTestServer(svc controlplane.Service) *httptest.Server {
	return httptest.NewServer(controlplane.NewControlPlaneRouter(svc, testAdminKey))
}

func adminReq(method, url string, body any) *http.Request {
	var buf bytes.Buffer
	if body != nil {
		json.NewEncoder(&buf).Encode(body)
	}
	req, _ := http.NewRequest(method, url, &buf)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Admin-Key", testAdminKey)
	return req
}

func validSchema() *schema.APISchema {
	return &schema.APISchema{
		Schema:  "https://appitools.dev/schema/v1",
		Version: "1",
		Name:    "test-api",
		Resources: map[string]schema.ResourceSchema{
			"items": {
				Fields: map[string]schema.FieldDef{
					"name":   {Type: "string", Required: true},
					"status": {Type: "string"},
				},
			},
		},
		RBAC: schema.RBACPolicy{},
	}
}

// ── tests ─────────────────────────────────────────────────────────────────────

func TestControlPlane_CreateTenant_Success(t *testing.T) {
	srv := newTestServer(newInMemService())
	defer srv.Close()

	body := map[string]any{
		"tenant_id":    "acme",
		"display_name": "Acme Corp",
		"email":        "admin@acme.com",
		"plan":         "free",
		"schema":       validSchema(),
	}
	resp, err := srv.Client().Do(adminReq(http.MethodPost, srv.URL+"/tenants", body))
	if err != nil {
		t.Fatalf("POST /tenants: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201, got %d", resp.StatusCode)
	}
	var got map[string]any
	json.NewDecoder(resp.Body).Decode(&got)
	if got["id"] != "acme" {
		t.Errorf("id: got %v, want acme", got["id"])
	}
	if got["pg_schema"] != "tenant_acme" {
		t.Errorf("pg_schema: got %v, want tenant_acme", got["pg_schema"])
	}
}

func TestControlPlane_CreateTenant_Duplicate(t *testing.T) {
	svc := newInMemService()
	srv := newTestServer(svc)
	defer srv.Close()

	body := map[string]any{
		"tenant_id": "dup",
		"schema":    validSchema(),
	}
	// First registration.
	resp, _ := srv.Client().Do(adminReq(http.MethodPost, srv.URL+"/tenants", body))
	resp.Body.Close()

	// Second registration — must be 409.
	resp, err := srv.Client().Do(adminReq(http.MethodPost, srv.URL+"/tenants", body))
	if err != nil {
		t.Fatalf("POST /tenants: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("expected 409, got %d", resp.StatusCode)
	}
}

func TestControlPlane_CreateTenant_NoAdminKey(t *testing.T) {
	srv := newTestServer(newInMemService())
	defer srv.Close()

	// No X-Admin-Key header.
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/tenants",
		bytes.NewBufferString(`{"tenant_id":"x","schema":{}}`))
	req.Header.Set("Content-Type", "application/json")

	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("POST /tenants: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", resp.StatusCode)
	}
}

func TestControlPlane_UpdateSchema_Valid(t *testing.T) {
	svc := newInMemService()
	srv := newTestServer(svc)
	defer srv.Close()

	// Seed a tenant directly.
	svc.Register(context.Background(), controlplane.RegisterRequest{ //nolint:errcheck
		TenantID: "update-me",
		Schema:   validSchema(),
	})

	updated := validSchema()
	updated.Resources["shipments"] = schema.ResourceSchema{
		Fields: map[string]schema.FieldDef{
			"tracking_code": {Type: "string", Required: true},
		},
	}

	body := map[string]any{"schema": updated}
	resp, err := srv.Client().Do(adminReq(http.MethodPut, srv.URL+"/tenants/update-me/schema", body))
	if err != nil {
		t.Fatalf("PUT /tenants/update-me/schema: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	var got map[string]string
	json.NewDecoder(resp.Body).Decode(&got)
	if got["status"] != "migration_queued" {
		t.Errorf("status: got %q, want migration_queued", got["status"])
	}
}

// TestControlPlane_UpdateSchema_DryRun verifies the PUT routes a dry-run to
// PreviewSchema (applying nothing) and returns the preview, NOT migration_queued.
func TestControlPlane_UpdateSchema_DryRun(t *testing.T) {
	svc := newInMemService()
	srv := newTestServer(svc)
	defer srv.Close()
	svc.Register(context.Background(), controlplane.RegisterRequest{TenantID: "dr", Schema: validSchema()}) //nolint:errcheck

	// Shape the preview so the handler test can assert it is passed through.
	svc.previewResult = &migration.Preview{
		PGSchema: "tenant_dr",
		Apply:    []string{"ADD COLUMN items.note text"},
		Destructive: []migration.DestructiveOp{
			{Key: "items.status", Kind: "column", Table: "items", Column: "status", RowsLost: 3, TableRows: 9},
		},
	}

	body := map[string]any{"schema": validSchema(), "dry_run": true}
	resp, err := srv.Client().Do(adminReq(http.MethodPut, srv.URL+"/tenants/dr/schema", body))
	if err != nil {
		t.Fatalf("PUT dry-run: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	if !svc.previewCalled {
		t.Errorf("dry_run must call PreviewSchema, not UpdateSchema")
	}
	// A dry-run must NOT take the apply path at all. The seeded schema is
	// validSchema, so comparing the stored resources proves nothing on its own —
	// what proves it is that the apply path never ran.
	if svc.updateCalled {
		t.Errorf("dry_run must NOT call UpdateSchema — the apply path was taken")
	}
	if svc.lastApprovedDrops != nil {
		t.Errorf("dry_run must not record approved drops, got %v", svc.lastApprovedDrops)
	}
	var got map[string]any
	json.NewDecoder(resp.Body).Decode(&got)
	if got["status"] != "dry_run" {
		t.Errorf("status: got %v, want dry_run", got["status"])
	}
	if _, ok := got["preview"]; !ok {
		t.Errorf("dry-run response must carry the preview, got: %v", got)
	}
}

// TestControlPlane_UpdateSchema_ApprovedDrops verifies the PUT threads approved_drops
// through to the apply and reflects what was applied in the response.
func TestControlPlane_UpdateSchema_ApprovedDrops(t *testing.T) {
	svc := newInMemService()
	srv := newTestServer(svc)
	defer srv.Close()
	svc.Register(context.Background(), controlplane.RegisterRequest{TenantID: "ad", Schema: validSchema()}) //nolint:errcheck

	body := map[string]any{"schema": validSchema(), "approved_drops": []string{"items.status", "gone"}}
	resp, err := srv.Client().Do(adminReq(http.MethodPut, srv.URL+"/tenants/ad/schema", body))
	if err != nil {
		t.Fatalf("PUT approved_drops: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	if len(svc.lastApprovedDrops) != 2 || svc.lastApprovedDrops[0] != "items.status" {
		t.Errorf("approved_drops not threaded through: %v", svc.lastApprovedDrops)
	}
	var got map[string]any
	json.NewDecoder(resp.Body).Decode(&got)
	if got["status"] != "migration_queued" {
		t.Errorf("status: got %v, want migration_queued", got["status"])
	}
	applied, _ := got["applied_drops"].([]any)
	if len(applied) != 2 {
		t.Errorf("response must report applied_drops, got: %v", got["applied_drops"])
	}
}

func TestControlPlane_UpdateSchema_Invalid(t *testing.T) {
	svc := newInMemService()
	srv := newTestServer(svc)
	defer srv.Close()

	svc.Register(context.Background(), controlplane.RegisterRequest{ //nolint:errcheck
		TenantID: "bad-schema",
		Schema:   validSchema(),
	})

	// Schema with invalid field type — schema.Validate() will catch this.
	invalid := &schema.APISchema{
		Schema:  "https://appitools.dev/schema/v1",
		Version: "1",
		Name:    "test",
		Resources: map[string]schema.ResourceSchema{
			"items": {
				Fields: map[string]schema.FieldDef{
					"name": {Type: "blob"}, // invalid type
				},
			},
		},
	}

	body := map[string]any{"schema": invalid}
	resp, err := srv.Client().Do(adminReq(http.MethodPut, srv.URL+"/tenants/bad-schema/schema", body))
	if err != nil {
		t.Fatalf("PUT /tenants/bad-schema/schema: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}
}

func TestControlPlane_GetTenant_Found(t *testing.T) {
	svc := newInMemService()
	srv := newTestServer(svc)
	defer srv.Close()

	svc.Register(context.Background(), controlplane.RegisterRequest{ //nolint:errcheck
		TenantID:    "get-me",
		DisplayName: "Get Me Corp",
		Plan:        "pro",
		Schema:      validSchema(),
	})

	resp, err := srv.Client().Do(adminReq(http.MethodGet, srv.URL+"/tenants/get-me", nil))
	if err != nil {
		t.Fatalf("GET /tenants/get-me: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	var got map[string]any
	json.NewDecoder(resp.Body).Decode(&got)
	if got["id"] != "get-me" {
		t.Errorf("id: got %v, want get-me", got["id"])
	}
	if got["display_name"] != "Get Me Corp" {
		t.Errorf("display_name: got %v, want Get Me Corp", got["display_name"])
	}
}

func TestControlPlane_GetTenant_NotFound(t *testing.T) {
	srv := newTestServer(newInMemService())
	defer srv.Close()

	resp, err := srv.Client().Do(adminReq(http.MethodGet, srv.URL+"/tenants/ghost", nil))
	if err != nil {
		t.Fatalf("GET /tenants/ghost: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", resp.StatusCode)
	}
}

func TestControlPlane_Health_NoAuth(t *testing.T) {
	srv := newTestServer(newInMemService())
	defer srv.Close()

	// /health must not require X-Admin-Key.
	resp, err := srv.Client().Get(srv.URL + "/health")
	if err != nil {
		t.Fatalf("GET /health: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
}
