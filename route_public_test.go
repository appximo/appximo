package appximo

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/appximo/appximo/pkg/auth"
	"github.com/appximo/appximo/pkg/rbac"
	"github.com/appximo/appximo/pkg/schema"
)

// LIBRARY-EXTEND-S1: Route.Public validation + the exact-match middleware skip.

func TestRegister_PublicRouteValidation(t *testing.T) {
	s := tasksSchema()

	t.Run("public route with literal path registers", func(t *testing.T) {
		app := &App{schema: s}
		if err := app.Register(Route{Method: "POST", Path: "/api/_register", Public: true, Handler: noopHandler}); err != nil {
			t.Fatalf("expected public /api/_register to register, got: %v", err)
		}
	})

	t.Run("public route with chi params rejected", func(t *testing.T) {
		app := &App{schema: s}
		err := app.Register(Route{Method: "POST", Path: "/api/_invite/{code}", Public: true, Handler: noopHandler})
		if err == nil || !strings.Contains(err.Error(), "literal path") {
			t.Fatalf("expected literal-path error, got: %v", err)
		}
	})

	t.Run("public + RequireRole rejected", func(t *testing.T) {
		app := &App{schema: s}
		err := app.Register(Route{Method: "POST", Path: "/api/_x", Public: true, RequireRole: "admin", Handler: noopHandler})
		if err == nil || !strings.Contains(err.Error(), "mutually exclusive") {
			t.Fatalf("expected mutual-exclusion error, got: %v", err)
		}
	})
}

func TestPublicRoutePaths_NilWhenNonePublic(t *testing.T) {
	app := &App{schema: tasksSchema()}
	if err := app.Register(Route{Method: "POST", Path: "/api/_echo", Handler: noopHandler}); err != nil {
		t.Fatal(err)
	}
	if m := app.publicRoutePaths(); m != nil {
		t.Fatalf("no public routes ⇒ nil map (middlewares must pay zero), got %v", m)
	}
	if err := app.Register(Route{Method: "POST", Path: "/api/_register", Public: true, Handler: noopHandler}); err != nil {
		t.Fatal(err)
	}
	m := app.publicRoutePaths()
	if !m["POST /api/_register"] || len(m) != 1 {
		t.Fatalf("expected exactly {POST /api/_register}, got %v", m)
	}
}

// TestJWTMiddleware_PublicExactMatchOnly proves the skip is method+path EXACT:
// the marked route passes with no token, while its siblings — same path other
// method, prefix-extended path, any other /api route — still 401.
func TestJWTMiddleware_PublicExactMatchOnly(t *testing.T) {
	isPublic := func(method, path string) bool { return method == "POST" && path == "/api/_register" }
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if c := auth.ClaimsFromCtx(r.Context()); c != nil {
			t.Error("a tokenless public request must carry NO claims")
		}
		w.WriteHeader(http.StatusOK)
	})
	h := auth.JWTMiddlewareWithPublic("test-secret-test-secret-test-secret!", isPublic)(next)

	cases := []struct {
		method, path string
		want         int
	}{
		{"POST", "/api/_register", http.StatusOK},            // the marked route
		{"GET", "/api/_register", http.StatusUnauthorized},   // other method
		{"POST", "/api/_register2", http.StatusUnauthorized}, // no prefix widening
		{"POST", "/api/_register/x", http.StatusUnauthorized},
		{"POST", "/api/tasks", http.StatusUnauthorized}, // deny-by-default intact
	}
	for _, c := range cases {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(c.method, c.path, nil))
		if rec.Code != c.want {
			t.Errorf("%s %s: want %d, got %d", c.method, c.path, c.want, rec.Code)
		}
	}
}

// TestRBACMiddleware_PublicPassthrough proves the RBAC path enforcement passes
// a marked public route through (an anonymous caller has no role — enforcement
// would deny-by-default it) while every other /api path stays enforced.
func TestRBACMiddleware_PublicPassthrough(t *testing.T) {
	policyJSON := []byte(`{"roles":{"admin":{"resources":"*","actions":["*"]}}}`)
	isPublic := func(method, path string) bool { return method == "POST" && path == "/api/_register" }
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	h := rbac.RBACMiddlewareWithPublic(policyJSON, isPublic)(next)

	// The public route passes with no identity at all.
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("POST", "/api/_register", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("public route: want 200, got %d", rec.Code)
	}
	// Any other /api path without a role stays deny-by-default.
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("POST", "/api/tasks", nil))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("non-public route without role: want 403, got %d", rec.Code)
	}
}

// TestCreateUser_RoleValidatedBeforeAnyIO proves Ctx.CreateUser rejects a role
// the schema RBAC does not declare — before touching the store (no DB needed).
func TestCreateUser_RoleValidatedBeforeAnyIO(t *testing.T) {
	policy := &rbac.Policy{Roles: map[string]rbac.RolePolicy{"viewer": {}}}
	c := &requestCtx{eng: &engineRefs{policy: policy, minPassword: 8}}

	if _, err := c.CreateUser("a@b.com", "longenough", "ghost"); !errors.Is(err, ErrUnknownRole) {
		t.Fatalf("undeclared role: want ErrUnknownRole, got %v", err)
	}
	// Email + password rules also fire before the store (nil store never touched).
	if _, err := c.CreateUser("not-an-email", "longenough", "viewer"); !errors.Is(err, ErrInvalidEmail) {
		t.Fatalf("bad email: want ErrInvalidEmail, got %v", err)
	}
	if _, err := c.CreateUser("a@b.com", "short", "viewer"); !errors.Is(err, ErrWeakPassword) {
		t.Fatalf("short password: want ErrWeakPassword, got %v", err)
	}
}

// LIBRARY-GAPS-S2 (ENG-6): a Public route is OPTIONALLY authenticated. The
// three branches, pinned:
//
//	absent token   → 200, Claims nil (anonymous — behavior unchanged)
//	valid token    → 200, Claims POPULATED (identity as input)
//	invalid token  → 401 (garbage never silently degrades to anonymous)
func TestJWTMiddleware_PublicOptionalAuth(t *testing.T) {
	const secret = "test-secret-test-secret-test-secret!"
	isPublic := func(method, path string) bool { return method == "POST" && path == "/api/checkout" }

	var gotClaims *auth.Claims
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotClaims = auth.ClaimsFromCtx(r.Context())
		w.WriteHeader(http.StatusOK)
	})
	h := auth.JWTMiddlewareWithPublic(secret, isPublic)(next)

	do := func(authorization string) (int, *auth.Claims) {
		gotClaims = nil
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("POST", "/api/checkout", nil)
		if authorization != "" {
			req.Header.Set("Authorization", authorization)
		}
		h.ServeHTTP(rec, req)
		return rec.Code, gotClaims
	}

	// 1. Absent → anonymous.
	if code, c := do(""); code != http.StatusOK || c != nil {
		t.Errorf("absent token: code %d claims %v, want 200 + nil claims", code, c)
	}

	// 2. Valid → populated.
	tok, err := auth.GenerateToken(auth.Claims{UserID: "u-1", Role: "cliente", TenantID: "acme"}, secret)
	if err != nil {
		t.Fatal(err)
	}
	if code, c := do("Bearer " + tok); code != http.StatusOK || c == nil || c.UserID != "u-1" || c.Role != "cliente" {
		t.Errorf("valid token: code %d claims %+v, want 200 with UserID u-1 / role cliente", code, c)
	}

	// 3a. Garbage → 401 (never anonymous).
	if code, c := do("Bearer not-a-jwt"); code != http.StatusUnauthorized || c != nil {
		t.Errorf("garbage token: code %d claims %v, want 401 and the handler never invoked", code, c)
	}
	// 3b. Expired → 401.
	expired, err := auth.GenerateTokenWithTTL(auth.Claims{UserID: "u-1", Role: "cliente", TenantID: "acme"}, secret, -time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if code, _ := do("Bearer " + expired); code != http.StatusUnauthorized {
		t.Errorf("expired token: code %d, want 401", code)
	}
	// 3c. Wrong scheme → 401.
	if code, _ := do("Basic dXNlcjpwd2Q="); code != http.StatusUnauthorized {
		t.Errorf("non-Bearer Authorization: code %d, want 401", code)
	}
}

var _ = schema.APISchema{} // keep the import used if helpers move
