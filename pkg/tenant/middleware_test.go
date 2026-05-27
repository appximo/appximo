package tenant_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/miguelangel/appitools/pkg/tenant"
)

// captureHandler is a next-handler that records the TenantCtx from context.
func captureHandler(captured **tenant.TenantCtx) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*captured = tenant.FromCtx(r.Context())
		w.WriteHeader(http.StatusOK)
	})
}

func runMiddleware(host string) (*httptest.ResponseRecorder, *tenant.TenantCtx) {
	var captured *tenant.TenantCtx
	req := httptest.NewRequest("GET", "/", nil)
	req.Host = host
	rec := httptest.NewRecorder()
	tenant.TenantMiddleware(captureHandler(&captured)).ServeHTTP(rec, req)
	return rec, captured
}

// "acme.localhost:8080" → TenantCtx{ID:"acme", PGSchema:"tenant_acme"}
func TestMiddleware_ValidTenant(t *testing.T) {
	rec, tc := runMiddleware("acme.localhost:8080")
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if tc == nil {
		t.Fatal("expected TenantCtx, got nil")
	}
	if tc.ID != "acme" {
		t.Errorf("expected ID 'acme', got %q", tc.ID)
	}
	if tc.PGSchema != "tenant_acme" {
		t.Errorf("expected PGSchema 'tenant_acme', got %q", tc.PGSchema)
	}
}

// "ACME.localhost" → 400 (mayúsculas)
func TestMiddleware_UppercaseTenant(t *testing.T) {
	rec, _ := runMiddleware("ACME.localhost")
	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for uppercase tenant, got %d", rec.Code)
	}
}

// "a-.localhost" → 400 (termina en guión)
func TestMiddleware_TrailingHyphen(t *testing.T) {
	rec, _ := runMiddleware("a-.localhost")
	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for trailing-hyphen tenant, got %d", rec.Code)
	}
}

// "localhost:8080" → pasa sin error, TenantCtx nil en context
func TestMiddleware_RootHost(t *testing.T) {
	rec, tc := runMiddleware("localhost:8080")
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for root host, got %d", rec.Code)
	}
	if tc != nil {
		t.Errorf("expected nil TenantCtx for root host, got %+v", tc)
	}
}

// "x.localhost" → 400 (menos de 2 chars)
func TestMiddleware_SingleCharTenant(t *testing.T) {
	rec, _ := runMiddleware("x.localhost")
	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for single-char tenant, got %d", rec.Code)
	}
}
