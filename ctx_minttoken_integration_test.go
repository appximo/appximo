//go:build integration

// FRESH-AGENT-GAPS-S1: Ctx.MintToken — the missing half of Ctx.CreateUser. A
// custom registration endpoint can now create the identity AND mint its
// session in one handler, and the minted token must be accepted by the
// GENERATED routes exactly like a /auth/login token (same claims shape, same
// signing path, same secret — acceptance by construction). The two refusals
// are pinned too: empty userID (the CLI-token footgun) and an undeclared role
// (deny-by-default would otherwise turn it into an unexplained 403).
package appximo

import (
	"fmt"
	"io"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/appximo/appximo/pkg/schema"
	"github.com/appximo/appximo/tests/helpers"
)

func newMintTokenApp(t *testing.T) *httptest.Server {
	t.Helper()
	app, err := New(Config{
		SchemaPath: filepath.Join(helpers.RepoRoot(), "examples", "quickstart", "schema.json"),
		DSN:        itConnStr,
		JWTSecret:  helpers.JWTSecret,
		AdminKey:   helpers.AdminKey,
		Env:        "test",
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { app.pool.Close() })

	// The registration endpoint the backend-spec teaches: create the user and
	// hand back a working session — the engine's own /auth/signup contract,
	// now reachable from a custom handler.
	mustRegister(t, app, Route{Method: "POST", Path: "/api/_register", Public: true, Handler: func(ctx Ctx) error {
		var body struct {
			Email    string `json:"email"`
			Password string `json:"password"`
		}
		if err := ctx.Bind(&body); err != nil {
			return ctx.Error(400, "invalid body", err)
		}
		user, err := ctx.CreateUser(body.Email, body.Password, "admin")
		if err != nil {
			return ctx.Error(422, err.Error(), err)
		}
		tok, err := ctx.MintToken(user.ID, user.Role)
		if err != nil {
			return ctx.Error(500, "mint failed", err)
		}
		return ctx.JSON(201, map[string]any{"user_id": user.ID, "token": tok})
	}})

	// The refusal probes, exercised through a real handler.
	mustRegister(t, app, Route{Method: "POST", Path: "/api/_mint_bad", Public: true, Handler: func(ctx Ctx) error {
		var body struct {
			UserID string `json:"user_id"`
			Role   string `json:"role"`
		}
		if err := ctx.Bind(&body); err != nil {
			return ctx.Error(400, "invalid body", err)
		}
		if _, err := ctx.MintToken(body.UserID, body.Role); err != nil {
			return ctx.Error(422, err.Error(), err)
		}
		return ctx.JSON(200, map[string]any{"ok": true})
	}})

	srv := httptest.NewServer(app.buildRouter(app.bootSurface()))
	t.Cleanup(srv.Close)
	return srv
}

func TestCtxMintToken_SessionWorksOnGeneratedRoutes(t *testing.T) {
	s, err := schema.LoadFromFile(filepath.Join(helpers.RepoRoot(), "examples", "quickstart", "schema.json"))
	if err != nil {
		t.Fatalf("load schema: %v", err)
	}
	helpers.RegisterTenant(t, itPool, "minttok", s)
	srv := newMintTokenApp(t)

	// 1. Register through the custom endpoint → identity + session in one call.
	resp := do(t, srv, "POST", "/api/_register", "minttok.localhost", "",
		`{"email":"mint@example.com","password":"a-strong-password"}`)
	if resp.StatusCode != 201 {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("register: %d %s", resp.StatusCode, b)
	}
	out := decode(t, resp)
	tok, _ := out["token"].(string)
	if tok == "" {
		t.Fatalf("no token in %v", out)
	}

	// 2. The minted token works on a GENERATED route like any login session.
	resp = do(t, srv, "POST", "/api/tasks", "minttok.localhost", tok, `{"title":"minted session works"}`)
	if resp.StatusCode != 201 {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("generated route with minted token: want 201, got %d %s", resp.StatusCode, b)
	}

	// 3. Refusals: empty userID, undeclared role — named, at mint time.
	for _, tc := range []struct{ body, want string }{
		{`{"user_id":"","role":"admin"}`, "non-empty userID"},
		{fmt.Sprintf(`{"user_id":%q,"role":"ghost"}`, out["user_id"]), "role not declared"},
	} {
		resp := do(t, srv, "POST", "/api/_mint_bad", "minttok.localhost", "", tc.body)
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != 422 || !strings.Contains(string(b), tc.want) {
			t.Errorf("mint refusal %s: want 422 containing %q, got %d %s", tc.body, tc.want, resp.StatusCode, b)
		}
	}
}
