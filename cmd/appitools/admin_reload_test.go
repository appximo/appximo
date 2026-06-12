package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/miguelangel/appitools/pkg/auth"
	"github.com/miguelangel/appitools/pkg/cache"
	"github.com/miguelangel/appitools/pkg/controlplane"
	"github.com/miguelangel/appitools/pkg/observability"
	"github.com/miguelangel/appitools/pkg/schema"
	"github.com/miguelangel/appitools/pkg/tenant"
)

// mockCPService is an in-memory controlplane.Service for unit-testing reloadHandler
// without a database. The Service interface documents that all methods are safe to
// mock — only GetByID and GetSchema are exercised by the reload path.
type mockCPService struct {
	getByID   func(ctx context.Context, id string) (*controlplane.Tenant, error)
	getSchema func(ctx context.Context, id string) (*schema.APISchema, error)
}

func (m *mockCPService) Register(context.Context, controlplane.RegisterRequest) (*controlplane.Tenant, error) {
	return nil, nil
}
func (m *mockCPService) GetByID(ctx context.Context, id string) (*controlplane.Tenant, error) {
	return m.getByID(ctx, id)
}
func (m *mockCPService) UpdateSchema(context.Context, string, *schema.APISchema) error { return nil }
func (m *mockCPService) GetSchema(ctx context.Context, id string) (*schema.APISchema, error) {
	return m.getSchema(ctx, id)
}

// reloadTestRouter wires the handler exactly as cmd_serve.go does: behind the
// X-Admin-Key gate and a chi route with the {id} URL param, so the test covers the
// real auth + param-extraction path, not just the bare handler.
func reloadTestRouter(svc controlplane.Service, rc *cache.ResponseCache, sc *tenant.SchemaCache, adminKey string) http.Handler {
	return reloadTestRouterWithBoot(svc, rc, sc, adminKey, &schema.APISchema{})
}

func reloadTestRouterWithBoot(svc controlplane.Service, rc *cache.ResponseCache, sc *tenant.SchemaCache, adminKey string, boot *schema.APISchema) http.Handler {
	r := chi.NewRouter()
	r.Method(http.MethodPost, "/admin/tenants/{id}/reload",
		observability.AdminAuth(adminKey, reloadHandler(svc, rc, sc, boot)))
	return r
}

func okTenant(id string) func(context.Context, string) (*controlplane.Tenant, error) {
	return func(_ context.Context, _ string) (*controlplane.Tenant, error) {
		return &controlplane.Tenant{ID: id, PGSchema: "tenant_" + id}, nil
	}
}

func TestAdminReloadTenant(t *testing.T) {
	const adminKey = "test-admin-key"

	t.Run("happy path → 200 with reloaded_at", func(t *testing.T) {
		sc := tenant.NewSchemaCache()
		rc := cache.New(time.Second)
		fresh := &schema.APISchema{Name: "logistics", Version: "2"}
		svc := &mockCPService{
			getByID:   okTenant("10"),
			getSchema: func(context.Context, string) (*schema.APISchema, error) { return fresh, nil },
		}

		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/admin/tenants/10/reload", nil)
		req.Header.Set("X-Admin-Key", adminKey)
		reloadTestRouter(svc, rc, sc, adminKey).ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d (%s)", rec.Code, rec.Body.String())
		}
		var body struct {
			OK         bool   `json:"ok"`
			TenantID   string `json:"tenant_id"`
			ReloadedAt string `json:"reloaded_at"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		if !body.OK || body.TenantID != "10" {
			t.Fatalf("unexpected body: %+v", body)
		}
		if _, err := time.Parse(time.RFC3339, body.ReloadedAt); err != nil {
			t.Fatalf("reloaded_at %q is not RFC3339: %v", body.ReloadedAt, err)
		}
		// Warm reload populated the schema cache with the fresh DB schema.
		if got, ok := sc.Get("10"); !ok || got != fresh {
			t.Fatalf("schema cache not warmed with fresh schema: got=%v ok=%v", got, ok)
		}
	})

	t.Run("stored-schema hooks differ from boot → 200 with warning", func(t *testing.T) {
		sc := tenant.NewSchemaCache()
		rc := cache.New(time.Second)
		boot := &schema.APISchema{Resources: map[string]schema.ResourceSchema{
			"tasks": {Fields: map[string]schema.FieldDef{"title": {Type: "string"}}},
		}}
		stored := &schema.APISchema{Resources: map[string]schema.ResourceSchema{
			"tasks": {
				Fields: map[string]schema.FieldDef{"title": {Type: "string"}},
				Hooks: map[string]schema.HookConfig{
					"after_create": {Type: "webhook", URL: "https://x.example", HMACSecretEnv: "HOOK_SECRET"},
				},
			},
		}}
		svc := &mockCPService{
			getByID:   okTenant("10"),
			getSchema: func(context.Context, string) (*schema.APISchema, error) { return stored, nil },
		}

		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/admin/tenants/10/reload", nil)
		req.Header.Set("X-Admin-Key", adminKey)
		reloadTestRouterWithBoot(svc, rc, sc, adminKey, boot).ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d (%s)", rec.Code, rec.Body.String())
		}
		var body struct {
			OK       bool     `json:"ok"`
			Warnings []string `json:"warnings"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		if !body.OK || len(body.Warnings) != 1 ||
			!strings.Contains(body.Warnings[0], "tasks") ||
			!strings.Contains(body.Warnings[0], "restart") {
			t.Fatalf("expected hook-drift warning naming tasks + restart, got: %s", rec.Body.String())
		}
	})

	t.Run("identical hooks → no warning key", func(t *testing.T) {
		sc := tenant.NewSchemaCache()
		rc := cache.New(time.Second)
		same := &schema.APISchema{Resources: map[string]schema.ResourceSchema{
			"tasks": {Fields: map[string]schema.FieldDef{"title": {Type: "string"}}},
		}}
		svc := &mockCPService{
			getByID:   okTenant("10"),
			getSchema: func(context.Context, string) (*schema.APISchema, error) { return same, nil },
		}

		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/admin/tenants/10/reload", nil)
		req.Header.Set("X-Admin-Key", adminKey)
		reloadTestRouterWithBoot(svc, rc, sc, adminKey, same).ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", rec.Code)
		}
		if strings.Contains(rec.Body.String(), "warnings") {
			t.Fatalf("no warning expected for identical hooks, got: %s", rec.Body.String())
		}
	})

	t.Run("unknown tenant → 404", func(t *testing.T) {
		sc := tenant.NewSchemaCache()
		rc := cache.New(time.Second)
		svc := &mockCPService{
			getByID:   func(context.Context, string) (*controlplane.Tenant, error) { return nil, controlplane.ErrNotFound },
			getSchema: func(context.Context, string) (*schema.APISchema, error) { return nil, controlplane.ErrNotFound },
		}

		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/admin/tenants/nonexistent/reload", nil)
		req.Header.Set("X-Admin-Key", adminKey)
		reloadTestRouter(svc, rc, sc, adminKey).ServeHTTP(rec, req)

		if rec.Code != http.StatusNotFound {
			t.Fatalf("expected 404, got %d (%s)", rec.Code, rec.Body.String())
		}
		if _, ok := sc.Get("nonexistent"); ok {
			t.Fatal("schema cache must not be populated for an unknown tenant")
		}
	})

	t.Run("missing/invalid admin key → 401", func(t *testing.T) {
		sc := tenant.NewSchemaCache()
		rc := cache.New(time.Second)
		svc := &mockCPService{
			getByID:   okTenant("10"),
			getSchema: func(context.Context, string) (*schema.APISchema, error) { return &schema.APISchema{}, nil },
		}
		router := reloadTestRouter(svc, rc, sc, adminKey)

		for _, key := range []string{"", "wrong-key"} {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/admin/tenants/10/reload", nil)
			if key != "" {
				req.Header.Set("X-Admin-Key", key)
			}
			router.ServeHTTP(rec, req)
			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("key=%q: expected 401, got %d", key, rec.Code)
			}
		}
	})

	t.Run("schema load failure → 500 without leaking DB details", func(t *testing.T) {
		sc := tenant.NewSchemaCache()
		rc := cache.New(time.Second)
		svc := &mockCPService{
			getByID: okTenant("10"),
			getSchema: func(context.Context, string) (*schema.APISchema, error) {
				return nil, context.DeadlineExceeded // stand-in for a DB error
			},
		}

		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/admin/tenants/10/reload", nil)
		req.Header.Set("X-Admin-Key", adminKey)
		reloadTestRouter(svc, rc, sc, adminKey).ServeHTTP(rec, req)

		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("expected 500, got %d", rec.Code)
		}
		if got := rec.Body.String(); !jsonHasGenericError(t, got) {
			t.Fatalf("500 body should carry a generic error, got %q", got)
		}
	})

	// The real "next request reflects the change" guarantee: reload evicts the
	// tenant's response cache so an identical GET that was previously a HIT becomes
	// a MISS and re-runs the backend (where REST's SELECT * picks up new columns).
	t.Run("reload evicts response cache → next request is a fresh MISS", func(t *testing.T) {
		sc := tenant.NewSchemaCache()
		rc := cache.New(5 * time.Second)
		const token = "reload-test-token"
		auth.SetCachedClaims(token, &auth.Claims{Role: "super_admin", TenantID: "10"})

		calls := 0
		backend := rc.Middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			calls++
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"data":[]}`)) //nolint:errcheck
		}))
		get := func() *httptest.ResponseRecorder {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, "/api/users", nil)
			req.Header.Set("Authorization", "Bearer "+token)
			req = req.WithContext(tenant.WithContext(req.Context(),
				&tenant.TenantCtx{ID: "10", PGSchema: "tenant_10"}))
			backend.ServeHTTP(rec, req)
			return rec
		}

		get()        // MISS — populates cache (calls=1)
		hit := get() // HIT — served from cache (calls stays 1)
		if calls != 1 || hit.Header().Get("X-Cache") != "HIT" {
			t.Fatalf("precondition failed: calls=%d x-cache=%q", calls, hit.Header().Get("X-Cache"))
		}

		// Reload the tenant.
		svc := &mockCPService{
			getByID: okTenant("10"),
			getSchema: func(context.Context, string) (*schema.APISchema, error) {
				return &schema.APISchema{Name: "logistics"}, nil
			},
		}
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/admin/tenants/10/reload", nil)
		req.Header.Set("X-Admin-Key", adminKey)
		reloadTestRouter(svc, rc, sc, adminKey).ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("reload expected 200, got %d", rec.Code)
		}

		miss := get() // after eviction → MISS, backend runs again (calls=2)
		if calls != 2 {
			t.Fatalf("expected backend re-run after reload (calls=2), got %d", calls)
		}
		if miss.Header().Get("X-Cache") == "HIT" {
			t.Fatal("request after reload must not be served from the stale cache")
		}
	})
}

func jsonHasGenericError(t *testing.T, body string) bool {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(body), &m); err != nil {
		return false
	}
	msg, _ := m["error"].(string)
	return msg == "reload failed"
}
