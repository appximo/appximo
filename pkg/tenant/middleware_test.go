package tenant_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/appximo/appximo/pkg/tenant"
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

// "ACME.localhost" → 200, resolving to the SAME tenant as "acme.localhost".
//
// This assertion is INVERTED from the original, deliberately (ADR-024). RFC 9110
// §4.2.3 makes the host case-insensitive and DNS resolves both spellings to the
// same server, so the old 400 rejected a legal request for a real tenant. The
// test now pins the property that makes folding safe rather than merely lenient:
// the upper-case host must resolve to the SAME id and the SAME Postgres schema,
// never to a second, parallel tenant namespace.
func TestMiddleware_UppercaseTenantFoldsToSameTenant(t *testing.T) {
	rec, tc := runMiddleware("ACME.localhost")
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for an upper-case host (RFC 9110 §4.2.3), got %d", rec.Code)
	}
	if tc == nil {
		t.Fatal("expected TenantCtx for ACME.localhost, got nil")
	}
	if tc.ID != "acme" || tc.PGSchema != "tenant_acme" {
		t.Errorf("ACME.localhost resolved to {ID:%q PGSchema:%q}, want {acme tenant_acme} — "+
			"case folding must map onto the existing tenant, never create a parallel one",
			tc.ID, tc.PGSchema)
	}
}

// A mixed-case host folds the same way.
func TestMiddleware_MixedCaseTenantFoldsToSameTenant(t *testing.T) {
	_, tc := runMiddleware("AcMe.LocalHost")
	if tc == nil || tc.ID != "acme" {
		t.Errorf("AcMe.LocalHost did not fold to tenant acme: %+v", tc)
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

// ── MiddlewareWithBareHosts (FLEET-CONSOLE-S2, the phantom-tenant fix) ────────

func runBareMiddleware(bare []string, host string) (*httptest.ResponseRecorder, *tenant.TenantCtx) {
	var captured *tenant.TenantCtx
	req := httptest.NewRequest("GET", "/", nil)
	req.Host = host
	rec := httptest.NewRecorder()
	tenant.MiddlewareWithBareHosts(bare)(captureHandler(&captured)).ServeHTTP(rec, req)
	return rec, captured
}

// The app's OWN domain (no tenant label) passes through with NO TenantCtx —
// before this fix "erp.localhost" resolved a phantom tenant "erp" that then
// polluted observability.
func TestMiddlewareBareHosts_AppDomainIsNotATenant(t *testing.T) {
	for _, host := range []string{"erp.localhost", "erp.localhost:8080"} {
		rec, tc := runBareMiddleware([]string{"erp.localhost"}, host)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: expected 200, got %d", host, rec.Code)
		}
		if tc != nil {
			t.Errorf("%s: expected NO TenantCtx on the app's bare domain, got %+v (the phantom tenant)", host, tc)
		}
	}
}

// A SUBDOMAIN of the app domain still resolves its first label as the tenant.
func TestMiddlewareBareHosts_TenantSubdomainStillResolves(t *testing.T) {
	rec, tc := runBareMiddleware([]string{"erp.localhost"}, "acme.erp.localhost:8080")
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if tc == nil || tc.ID != "acme" || tc.PGSchema != "tenant_acme" {
		t.Errorf("expected tenant acme under the app domain, got %+v", tc)
	}
}

// A bare list with ports normalizes; other hosts behave exactly as before.
func TestMiddlewareBareHosts_OtherHostsUnchanged(t *testing.T) {
	rec, tc := runBareMiddleware([]string{"erp.localhost:8080"}, "acme.other.com")
	if rec.Code != http.StatusOK || tc == nil || tc.ID != "acme" {
		t.Errorf("non-bare host must resolve normally, got code=%d tc=%+v", rec.Code, tc)
	}
	// Empty bare == the historical middleware.
	rec2, tc2 := runBareMiddleware(nil, "acme.localhost")
	if rec2.Code != http.StatusOK || tc2 == nil || tc2.ID != "acme" {
		t.Errorf("nil bare must behave as TenantMiddleware, got code=%d tc=%+v", rec2.Code, tc2)
	}
}

// TestRequireForTenantAPI pins T3: a tenant-scoped request whose Host names no
// tenant is a NAMED 400 at the edge (it used to panic in MustFromCtx and mask
// as a 500), while tenant-agnostic surfaces pass through untouched.
func TestRequireForTenantAPI(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	h := tenant.MiddlewareWithBareHosts(nil)(tenant.RequireForTenantAPI(inner))

	cases := []struct {
		host, path string
		want       int
	}{
		{"localhost:8080", "/api/notes", http.StatusBadRequest},  // T3: named, not 500
		{"localhost:8080", "/graphql", http.StatusBadRequest},    // GraphQL too
		{"localhost:8080", "/auth/login", http.StatusBadRequest}, // auth is tenant-scoped
		{"localhost:8080", "/admin", http.StatusOK},              // tenant-agnostic passes
		{"localhost:8080", "/healthz", http.StatusOK},            // probes pass
		{"acme.localhost:8080", "/api/notes", http.StatusOK},     // a real tenant passes
	}
	for _, c := range cases {
		req := httptest.NewRequest(http.MethodGet, "http://x"+c.path, nil)
		req.Host = c.host
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != c.want {
			t.Fatalf("%s %s: status %d, want %d (body: %s)", c.host, c.path, rec.Code, c.want, rec.Body.String())
		}
		if c.want == http.StatusBadRequest && !strings.Contains(rec.Body.String(), "no tenant in host") {
			t.Fatalf("%s %s: the 400 must NAME the problem, got: %s", c.host, c.path, rec.Body.String())
		}
	}
}
