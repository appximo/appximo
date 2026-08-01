//go:build integration

// SEC-3 — Route.Public's OPTIONAL authentication, exercised live.
//
// ENG-6 (LIBRARY-GAPS-S2) changed the auth surface: a route marked Public no
// longer means "identity ignored", it means "no token REQUIRED". Three branches
// follow from that, and until now only route_public_test.go covered them — with
// the middleware in isolation, not through a booted engine. The certification
// pass (CERTIFY-S1) could not close the gap because the pure `serve` binary
// registers no custom routes and the two live consumer apps are production
// assets.
//
// This test is the missing half: a real App, a real middleware chain, a real
// httptest listener, real HS256 tokens. It is the surface an unauthenticated
// caller reaches from the internet, so "it passes in a unit test" is not enough.
package appitools

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/miguelangel/appitools/pkg/auth"
	"github.com/miguelangel/appitools/tests/helpers"
)

// newPublicRouteApp boots an App with one PUBLIC route that reports what the
// engine resolved about the caller, and one PRIVATE route for the control.
func newPublicRouteApp(t *testing.T) *httptest.Server {
	t.Helper()
	quickstart := filepath.Join(helpers.RepoRoot(), "examples", "quickstart", "schema.json")
	app, err := New(Config{
		SchemaPath: quickstart,
		DSN:        itConnStr,
		JWTSecret:  helpers.JWTSecret,
		AdminKey:   helpers.AdminKey,
		Env:        "test",
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { app.pool.Close() })

	// The public route reports the identity the chain resolved, so the test can
	// distinguish "anonymous" from "authenticated" rather than only reading a
	// status code.
	mustRegister(t, app, Route{Method: "GET", Path: "/api/_public_probe", Public: true, Handler: func(ctx Ctx) error {
		c := ctx.Claims()
		return ctx.JSON(200, map[string]any{
			"anonymous": c.UserID == "" && c.Role == "",
			"user_id":   c.UserID,
			"role":      c.Role,
			"tenant":    ctx.Tenant(),
		})
	}})
	// Control: the SAME shape without Public. It must demand a token.
	mustRegister(t, app, Route{Method: "GET", Path: "/api/_private_probe", Handler: func(ctx Ctx) error {
		return ctx.JSON(200, map[string]any{"ok": true})
	}})

	srv := httptest.NewServer(app.buildRouter(app.bootSurface()))
	t.Cleanup(srv.Close)
	return srv
}

// expiredToken mints a structurally valid HS256 token whose exp is in the past,
// signed with the ENGINE's secret — the "my session ran out" case, which must be
// refused rather than silently downgraded to anonymous.
func expiredToken(t *testing.T, tenantID string) string {
	t.Helper()
	tok, err := auth.GenerateTokenWithTTL(auth.Claims{
		UserID: "user-1", Role: "admin", TenantID: tenantID,
	}, helpers.JWTSecret, -1*time.Hour)
	if err != nil {
		t.Fatalf("generate expired token: %v", err)
	}
	return tok
}

// foreignSecretToken is signed with a DIFFERENT secret: a forged credential.
func foreignSecretToken(t *testing.T, tenantID string) string {
	t.Helper()
	tok, err := auth.GenerateToken(auth.Claims{
		UserID: "user-1", Role: "admin", TenantID: tenantID,
	}, "a-completely-different-secret-32chars")
	if err != nil {
		t.Fatalf("generate foreign token: %v", err)
	}
	return tok
}

// TestPublicRoute_ThreeBranchesLive walks the whole contract over real HTTP.
func TestPublicRoute_ThreeBranchesLive(t *testing.T) {
	s := loadQuickstart(t)
	helpers.RegisterTenant(t, itPool, "pubalpha", s)
	helpers.RegisterTenant(t, itPool, "pubbeta", s)
	srv := newPublicRouteApp(t)
	const host = "pubalpha.localhost"

	good := helpers.GenToken(t, "admin", "user-1", "pubalpha")

	t.Run("no token at all → 200, anonymous", func(t *testing.T) {
		res := do(t, srv, "GET", "/api/_public_probe", host, "", "")
		defer res.Body.Close()
		if res.StatusCode != http.StatusOK {
			t.Fatalf("status = %d, want 200 — a public route must serve an anonymous caller", res.StatusCode)
		}
		var body map[string]any
		if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if body["anonymous"] != true {
			t.Errorf("claims should be zero for an anonymous caller, got %v", body)
		}
	})

	t.Run("valid token → 200, claims POPULATED", func(t *testing.T) {
		res := do(t, srv, "GET", "/api/_public_probe", host, good, "")
		defer res.Body.Close()
		if res.StatusCode != http.StatusOK {
			t.Fatalf("status = %d, want 200", res.StatusCode)
		}
		var body map[string]any
		if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if body["anonymous"] == true {
			t.Errorf("a VALID token on a public route must populate the claims (identity as input), got %v", body)
		}
		if body["role"] != "admin" || body["user_id"] != "user-1" {
			t.Errorf("claims not carried through: %v", body)
		}
	})

	// The third branch is the one that matters most: a caller who SENT
	// credentials and had them refused must be told, not silently downgraded to
	// anonymous. A silent downgrade is the same defect class as a dropped filter —
	// the caller believes they are authenticated and they are not.
	for _, tc := range []struct{ name, token string }{
		{"garbage token", "not.a.jwt"},
		{"wrong-secret signature", foreignSecretToken(t, "pubalpha")},
		{"expired token", expiredToken(t, "pubalpha")},
		{"foreign-tenant token", helpers.GenToken(t, "admin", "user-1", "pubbeta")},
	} {
		t.Run(tc.name+" → 401, never anonymous", func(t *testing.T) {
			res := do(t, srv, "GET", "/api/_public_probe", host, tc.token, "")
			defer res.Body.Close()
			if res.StatusCode != http.StatusUnauthorized {
				t.Errorf("status = %d, want 401 — a rejected credential must NOT degrade to anonymous access", res.StatusCode)
			}
		})
	}

	t.Run("control: the same route without Public demands a token", func(t *testing.T) {
		res := do(t, srv, "GET", "/api/_private_probe", host, "", "")
		defer res.Body.Close()
		if res.StatusCode != http.StatusUnauthorized {
			t.Errorf("status = %d, want 401 — a non-public route must require a token", res.StatusCode)
		}
	})
}

// TestPublicRoute_SkipIsExactMatch: marking one route public must not widen the
// skip to a sibling path or another method.
func TestPublicRoute_SkipIsExactMatch(t *testing.T) {
	s := loadQuickstart(t)
	helpers.RegisterTenant(t, itPool, "pubexact", s)
	srv := newPublicRouteApp(t)
	const host = "pubexact.localhost"

	// A different METHOD on the same path is not the public route.
	res := do(t, srv, "POST", "/api/_public_probe", host, "", "")
	defer res.Body.Close()
	if res.StatusCode == http.StatusOK {
		t.Errorf("POST on a route made public for GET answered 200 — the skip must be method+path exact")
	}
	// A sibling path is not the public route.
	res2 := do(t, srv, "GET", "/api/_public_probe_extra", host, "", "")
	defer res2.Body.Close()
	if res2.StatusCode == http.StatusOK {
		t.Errorf("a sibling path answered 200 — the skip must not be a prefix")
	}
}
