package cache

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/appximo/appximo/pkg/auth"
)

// rbacGate mirrors the production gate: only roles without row-level conditions
// or field restrictions (super_admin, gerente) are cacheable. operario/tercero
// (conditions) and public (fields) are not.
func rbacGate(role string) bool { return role == "super_admin" || role == "gerente" }

// whoamiHandler echoes the calling user's id, taken from the validated token, so
// a cross-user cache leak is directly visible in the response body.
func whoamiHandler(calls *int64) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(calls, 1)
		uid := "anon"
		if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
			if c, ok := auth.GetCachedClaims("", strings.TrimPrefix(h, "Bearer ")); ok {
				uid = c.UserID
			}
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"user":"` + uid + `"}`)) //nolint:errcheck
	})
}

func rbacReq(token string) *http.Request {
	r := withTenant(httptest.NewRequest(http.MethodGet, "/api/guides?per_page=20", nil), "acme")
	r.Header.Set("Authorization", "Bearer "+token)
	return r
}

func setRole(token, role, userID string) {
	auth.SetCachedClaims("", token, &auth.Claims{Role: role, UserID: userID, TenantID: "acme"})
}

//  1. Admin caches a URL; an operario requesting the same URL must NOT receive
//     the admin's response (the bypass that T3 found). It must run its own RBAC.
func TestRBACCache_OperarioNeverGetsAdminData(t *testing.T) {
	var calls int64
	rc := New(5 * time.Second)
	rc.SetRoleCacheGate(rbacGate)
	h := rc.Middleware(whoamiHandler(&calls))
	setRole("admin-tok", "super_admin", "admin1")
	setRole("op-tok", "operario", "op1")

	recA := httptest.NewRecorder()
	h.ServeHTTP(recA, rbacReq("admin-tok")) // admin caches
	if recA.Body.String() != `{"user":"admin1"}` {
		t.Fatalf("admin setup body = %q", recA.Body.String())
	}

	recO := httptest.NewRecorder()
	h.ServeHTTP(recO, rbacReq("op-tok")) // operario, same URI
	if strings.Contains(recO.Body.String(), "admin1") {
		t.Fatalf("RBAC BYPASS: operario received admin's data: %q", recO.Body.String())
	}
	if recO.Body.String() != `{"user":"op1"}` {
		t.Errorf("operario must get its own data, got %q", recO.Body.String())
	}
	if recO.Header().Get("X-Cache") == "HIT" {
		t.Error("operario must never receive a cache HIT")
	}
}

//  2. Two different admins in the same tenant share the cache (no conditions →
//     identical data for all users → one backend call).
func TestRBACCache_AdminsShare(t *testing.T) {
	var calls int64
	rc := New(5 * time.Second)
	rc.SetRoleCacheGate(rbacGate)
	h := rc.Middleware(whoamiHandler(&calls))
	setRole("a1", "super_admin", "admin1")
	setRole("a2", "super_admin", "admin2")

	h.ServeHTTP(httptest.NewRecorder(), rbacReq("a1")) // miss → cache
	rec2 := httptest.NewRecorder()
	h.ServeHTTP(rec2, rbacReq("a2")) // shared HIT

	if got := atomic.LoadInt64(&calls); got != 1 {
		t.Errorf("admins must share cache; handler calls = %d, want 1", got)
	}
	if rec2.Header().Get("X-Cache") != "HIT" {
		t.Error("second admin should get a cache HIT")
	}
}

// 3. Two different operarios must NOT share — each runs its own RBAC filter.
func TestRBACCache_OperariosDoNotShare(t *testing.T) {
	var calls int64
	rc := New(5 * time.Second)
	rc.SetRoleCacheGate(rbacGate)
	h := rc.Middleware(whoamiHandler(&calls))
	setRole("o1", "operario", "op1")
	setRole("o2", "operario", "op2")

	rec1 := httptest.NewRecorder()
	h.ServeHTTP(rec1, rbacReq("o1"))
	rec2 := httptest.NewRecorder()
	h.ServeHTTP(rec2, rbacReq("o2"))

	if got := atomic.LoadInt64(&calls); got != 2 {
		t.Errorf("operarios must not share; handler calls = %d, want 2", got)
	}
	if rec1.Body.String() != `{"user":"op1"}` || rec2.Body.String() != `{"user":"op2"}` {
		t.Errorf("each operario must get own data: o1=%q o2=%q", rec1.Body.String(), rec2.Body.String())
	}
	if rec1.Header().Get("X-Cache") == "HIT" || rec2.Header().Get("X-Cache") == "HIT" {
		t.Error("operario responses must never be HITs")
	}
}

// 4. A role with no conditions (super_admin) caches normally.
func TestRBACCache_SuperAdminCachesNormally(t *testing.T) {
	var calls int64
	rc := New(5 * time.Second)
	rc.SetRoleCacheGate(rbacGate)
	h := rc.Middleware(whoamiHandler(&calls))
	setRole("sa", "super_admin", "admin1")

	h.ServeHTTP(httptest.NewRecorder(), rbacReq("sa")) // miss
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, rbacReq("sa")) // HIT

	if got := atomic.LoadInt64(&calls); got != 1 {
		t.Errorf("super_admin should be cached; handler calls = %d, want 1", got)
	}
	if rec.Header().Get("X-Cache") != "HIT" {
		t.Error("super_admin second request should be a HIT")
	}
}
