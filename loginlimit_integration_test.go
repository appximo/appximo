//go:build integration

// ENG-47 — the login limiter's valve, proven against a real engine: with the
// default the 6th login attempt in a minute for one identity is 429; with the
// knob raised through Config (and through the env var) it passes; a
// set-but-invalid env value refuses to boot.
package appximo

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/appximo/appximo/tests/helpers"
)

const loginLimitSchemaJSON = `{
  "$schema": "https://appximo.com/schema/v1", "version": "1", "name": "loginlimit",
  "resources": { "things": { "fields": { "title": { "type": "string", "required": true } } } },
  "rbac": { "roles": { "admin": { "resources": "*", "actions": ["*"] }, "demo": { "resources": ["things"], "actions": ["read"] } } }
}`

func newLoginLimitApp(t *testing.T, perMin, burst int) *App {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "loginlimit.json")
	if err := os.WriteFile(p, []byte(loginLimitSchemaJSON), 0o600); err != nil {
		t.Fatalf("write schema: %v", err)
	}
	app, err := New(Config{SchemaPath: p, DSN: itConnStr, JWTSecret: helpers.JWTSecret, AdminKey: helpers.AdminKey, Env: "test",
		AuthLoginAttemptsPerMinute: perMin, AuthLoginBurst: burst})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { app.pool.Close() })
	return app
}

// loginCodes creates a tenant user through /admin and fires n logins with a
// WRONG password (the limiter counts attempts, not successes), returning the
// status codes in order.
func loginCodes(t *testing.T, app *App, tenant string, n int) []int {
	t.Helper()
	srv := newServerFor(t, app)
	helpers.RegisterTenant(t, itPool, tenant, mustLoadSchemaBytes(t, loginLimitSchemaJSON))
	host := tenant + ".localhost"
	email := "demo@" + tenant + ".test"
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/admin/tenants/"+tenant+"/users", strings.NewReader(fmt.Sprintf(`{"email":%q,"password":"Demo12345!","role":"demo"}`, email)))
	req.Header.Set("X-Admin-Key", helpers.AdminKey)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil || resp.StatusCode != http.StatusCreated {
		t.Fatalf("create user: %v %d", err, resp.StatusCode)
	}
	resp.Body.Close()
	codes := make([]int, 0, n)
	for i := 0; i < n; i++ {
		r := do(t, srv, http.MethodPost, "/auth/login", host, "", fmt.Sprintf(`{"email":%q,"password":"wrong"}`, email))
		codes = append(codes, r.StatusCode)
		r.Body.Close()
	}
	return codes
}

func TestLoginLimit_DefaultStillThrottlesTheSixth(t *testing.T) {
	t.Setenv("APPXIMO_AUTH_LOGIN_ATTEMPTS_PER_MINUTE", "")
	t.Setenv("APPXIMO_AUTH_LOGIN_BURST", "")
	codes := loginCodes(t, newLoginLimitApp(t, 0, 0), "llimdef", 7)
	for i, c := range codes[:5] {
		if c != http.StatusUnauthorized {
			t.Errorf("attempt %d: want 401 (wrong password, under the quota), got %d", i+1, c)
		}
	}
	if codes[5] != http.StatusTooManyRequests {
		t.Fatalf("6th attempt: want 429 with the default limiter, got %v — THE DEFAULT MOVED", codes)
	}
}

func TestLoginLimit_RaisedThroughConfigPasses(t *testing.T) {
	codes := loginCodes(t, newLoginLimitApp(t, 30, 30), "llimcfg", 12)
	for i, c := range codes {
		if c != http.StatusUnauthorized {
			t.Fatalf("attempt %d with 30/min: want 401, got %d (%v)", i+1, c, codes)
		}
	}
}

func TestLoginLimit_RaisedThroughEnvPasses(t *testing.T) {
	t.Setenv("APPXIMO_AUTH_LOGIN_ATTEMPTS_PER_MINUTE", "30")
	t.Setenv("APPXIMO_AUTH_LOGIN_BURST", "")
	codes := loginCodes(t, newLoginLimitApp(t, 0, 0), "llimenv", 12)
	for i, c := range codes {
		if c != http.StatusUnauthorized {
			t.Fatalf("attempt %d with env 30/min: want 401, got %d (%v)", i+1, c, codes)
		}
	}
}

func TestLoginLimit_InvalidEnvRefusesToBoot(t *testing.T) {
	t.Setenv("APPXIMO_AUTH_LOGIN_ATTEMPTS_PER_MINUTE", "lots")
	dir := t.TempDir()
	p := filepath.Join(dir, "loginlimit.json")
	if err := os.WriteFile(p, []byte(loginLimitSchemaJSON), 0o600); err != nil {
		t.Fatalf("write schema: %v", err)
	}
	app, err := New(Config{SchemaPath: p, DSN: itConnStr, JWTSecret: helpers.JWTSecret, AdminKey: helpers.AdminKey, Env: "test"})
	if err == nil {
		app.pool.Close()
		t.Fatal("want a boot error for APPXIMO_AUTH_LOGIN_ATTEMPTS_PER_MINUTE=lots, got a running app")
	}
	if !strings.Contains(err.Error(), "APPXIMO_AUTH_LOGIN_ATTEMPTS_PER_MINUTE") {
		t.Fatalf("the error must name the variable: %v", err)
	}
}
