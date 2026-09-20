package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/appximo/appximo/pkg/tenant"
)

const scopeSecret = "a-test-secret-of-at-least-32-characters!!"

func TestParseTTL(t *testing.T) {
	if d, err := ParseTTL("365d"); err != nil || d != 365*24*time.Hour {
		t.Fatalf("365d: %v %v", d, err)
	}
	if d, err := ParseTTL("90m"); err != nil || d != 90*time.Minute {
		t.Fatalf("90m: %v %v", d, err)
	}
	for _, bad := range []string{"", "0d", "-1h", "forever", "1.5d"} {
		if _, err := ParseTTL(bad); err == nil {
			t.Errorf("%q must be rejected", bad)
		}
	}
}

func TestClaims_PathAllowed(t *testing.T) {
	c := &Claims{Paths: []string{"/api/ask", "/api/summary", "/api/files/*"}}
	for _, ok := range []string{"/api/ask", "/api/summary", "/api/files/abc", "/api/files/"} {
		if !c.PathAllowed(ok) {
			t.Errorf("%s must be allowed", ok)
		}
	}
	for _, no := range []string{"/api/ask/spend", "/api/ordenes", "/api/summary/x", "/api/transaction", "/api/askx"} {
		if c.PathAllowed(no) {
			t.Errorf("%s must be refused", no)
		}
	}
	if !(&Claims{}).PathAllowed("/anything") {
		t.Error("no scope = every path")
	}
}

func TestMiddleware_ScopeAndRevocation(t *testing.T) {
	t.Cleanup(func() { SetRevokedTokenIDs(nil) })
	mint := func(paths []string, id string) string {
		c := Claims{UserID: "u", Role: "dueno", TenantID: "acme", Paths: paths}
		c.RegisteredClaims.ID = id
		tok, err := GenerateTokenWithTTL(c, scopeSecret, 365*24*time.Hour)
		if err != nil {
			t.Fatal(err)
		}
		return tok
	}
	mw := JWTMiddleware(scopeSecret)
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) })
	call := func(tok, path string) (int, string) {
		req := httptest.NewRequest(http.MethodGet, "http://acme.localhost"+path, nil)
		req.Header.Set("Authorization", "Bearer "+tok)
		req = req.WithContext(context.WithValue(req.Context(), tenantCtxKeyForTest(), nil))
		rec := httptest.NewRecorder()
		mw(next).ServeHTTP(rec, req)
		return rec.Code, rec.Body.String()
	}
	scoped := mint([]string{"/api/ask", "/api/summary"}, "abc123")
	if code, _ := call(scoped, "/api/ask"); code != http.StatusNoContent {
		t.Fatalf("in scope: %d", code)
	}
	if code, body := call(scoped, "/api/ordenes"); code != http.StatusUnauthorized || !strings.Contains(body, "scoped to /api/ask, /api/summary") {
		t.Fatalf("out of scope must be 401 naming the scope: %d %s", code, body)
	}
	// Revocation by id — after the claims cache already saw the token.
	SetRevokedTokenIDs([]string{"abc123"})
	if code, body := call(scoped, "/api/ask"); code != http.StatusUnauthorized || !strings.Contains(body, "revoked") {
		t.Fatalf("revoked must be 401: %d %s", code, body)
	}
	other := mint(nil, "zzz")
	if code, _ := call(other, "/api/ordenes"); code != http.StatusNoContent {
		t.Fatalf("another token keeps working: %d", code)
	}
	// The env loader is fail-fast on a malformed list.
	t.Setenv("APPXIMO_JWT_REVOKED", "abc123,,zzz")
	if _, err := RevokedFromEnv(); err == nil {
		t.Fatal("a stray comma must refuse to boot")
	}
	t.Setenv("APPXIMO_JWT_REVOKED", " abc123 , zzz ")
	if n, err := RevokedFromEnv(); err != nil || n != 2 || !IsRevoked("zzz") {
		t.Fatalf("env load: %d %v", n, err)
	}
}

// tenantCtxKeyForTest keeps the request tenant-less (the tenant-match check
// is skipped when there is no TenantCtx), so the test isolates scope+revocation.
func tenantCtxKeyForTest() any { return struct{ k string }{"no-tenant"} }

var _ = tenant.FromCtx

// BenchmarkScopeChecks measures what TOKEN-SCOPE adds per request after the
// claims cache: one nil check on Paths and one RWMutex map lookup on the id.
func BenchmarkScopeChecks(b *testing.B) {
	plain := &Claims{Role: "dueno"}
	scoped := &Claims{Role: "dueno", Paths: []string{"/api/ask", "/api/summary"}}
	scoped.RegisteredClaims.ID = "abc123"
	SetRevokedTokenIDs([]string{"other"})
	b.Run("plain-token", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			if IsRevoked(plain.ID) || !plain.PathAllowed("/api/ordenes") {
				b.Fatal("unexpected")
			}
		}
	})
	b.Run("scoped-token", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			if IsRevoked(scoped.ID) || !scoped.PathAllowed("/api/summary") {
				b.Fatal("unexpected")
			}
		}
	})
}
