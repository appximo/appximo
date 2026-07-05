package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// The claims cache is keyed by (secret, token) — MT-STRUCT-S3. With N apps in
// one process, a token validated by app X (secret X) must NEVER be served from
// cache to app Y (secret Y): a hit would bypass Y's signature verification.
func TestClaimsCacheIsSecretScoped(t *testing.T) {
	claims := &Claims{Role: "admin", TenantID: "acme",
		RegisteredClaims: jwt.RegisteredClaims{ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour))}}
	SetCachedClaims("secret-app-x", "tok-123", claims)

	if _, ok := GetCachedClaims("secret-app-x", "tok-123"); !ok {
		t.Fatal("same-secret lookup must hit")
	}
	if _, ok := GetCachedClaims("secret-app-y", "tok-123"); ok {
		t.Fatal("CROSS-APP HOLE: token cached under secret X was served to secret Y")
	}
	if _, ok := GetCachedClaims("", "tok-123"); ok {
		t.Fatal("empty-secret lookup must not see another secret's entry")
	}
}

// End-to-end at the middleware layer: a token minted+validated by app X's
// middleware (cached there) must still be REJECTED by app Y's middleware —
// the cache must not short-circuit Y's HMAC check.
func TestJWTMiddlewareCrossAppTokenRejectedDespiteCache(t *testing.T) {
	secretX := "secret-x-0123456789012345678901234567"
	secretY := "secret-y-0123456789012345678901234567"
	tok, err := GenerateToken(Claims{Role: "admin", TenantID: "acme"}, secretX)
	if err != nil {
		t.Fatal(err)
	}

	okHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) })
	mwX := JWTMiddleware(secretX)(okHandler)
	mwY := JWTMiddleware(secretY)(okHandler)

	req := func() *http.Request {
		r := httptest.NewRequest("GET", "/api/things", nil)
		r.Header.Set("Authorization", "Bearer "+tok)
		return r
	}

	// App X validates and caches the claims.
	rec := httptest.NewRecorder()
	mwX.ServeHTTP(rec, req())
	if rec.Code != 200 {
		t.Fatalf("app X should accept its own token, got %d (%s)", rec.Code, rec.Body.String())
	}

	// App Y must reject the SAME token (different secret) — cache or not.
	rec = httptest.NewRecorder()
	mwY.ServeHTTP(rec, req())
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("CROSS-APP HOLE: app Y accepted app X's token via the claims cache (got %d)", rec.Code)
	}
}
