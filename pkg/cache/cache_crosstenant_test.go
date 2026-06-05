package cache

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/miguelangel/appitools/pkg/auth"
)

// TestCache_DoesNotServeAcrossTenantToken is a regression test for the critical
// cross-tenant cache leak: the response cache runs BEFORE JWTMiddleware and a HIT
// short-circuits it, so the cache must itself verify that the validated token's
// tenant matches the host tenant (cache key). Otherwise a holder of any cacheable
// token for tenant A, replayed against Host=B, would be served tenant B's cached
// entry. This test warms tenant "victim"'s cache, then replays an "attacker"
// tenant token against Host=victim and asserts no cached data is served.
func TestCache_DoesNotServeAcrossTenantToken(t *testing.T) {
	rc := New(5 * time.Second)
	var calls int32
	handler := rc.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"secret":"victim-only-data"}`)) //nolint:errcheck
	}))

	const victimTok, attackerTok = "victim-token", "attacker-token"
	auth.SetCachedClaims(victimTok, &auth.Claims{Role: "super_admin", TenantID: "victim"})
	auth.SetCachedClaims(attackerTok, &auth.Claims{Role: "super_admin", TenantID: "attacker"})

	victimReq := func() *http.Request {
		r := withTenant(httptest.NewRequest(http.MethodGet, "/api/guides", nil), "victim")
		r.Header.Set("Authorization", "Bearer "+victimTok)
		return r
	}

	// Warm victim's cache (token tenant == host tenant), then confirm a HIT.
	handler.ServeHTTP(httptest.NewRecorder(), victimReq())
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, victimReq())
	if rec.Header().Get("X-Cache") != "HIT" {
		t.Fatalf("victim's own repeat request should HIT, got %q", rec.Header().Get("X-Cache"))
	}
	callsAfterWarm := atomic.LoadInt32(&calls)

	// Attack: attacker-tenant token replayed against Host=victim.
	attack := withTenant(httptest.NewRequest(http.MethodGet, "/api/guides", nil), "victim")
	attack.Header.Set("Authorization", "Bearer "+attackerTok)
	arec := httptest.NewRecorder()
	handler.ServeHTTP(arec, attack)

	if arec.Header().Get("X-Cache") == "HIT" {
		t.Fatal("SECURITY: attacker token was served the victim tenant's cached entry (cross-tenant leak)")
	}
	if atomic.LoadInt32(&calls) == callsAfterWarm {
		t.Fatal("SECURITY: attacker request was short-circuited by the cache without a tenant match")
	}
}
