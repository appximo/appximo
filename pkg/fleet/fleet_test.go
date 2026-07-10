package fleet

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// ── manifest ────────────────────────────────────────────────────────────────

func writeManifest(t *testing.T, dir string, m map[string]any) string {
	t.Helper()
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, "fleet.json")
	if err := os.WriteFile(p, b, 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func baseApp(t *testing.T, dir, name, secret string) map[string]any {
	t.Helper()
	schema := filepath.Join(dir, name+"-schema.json")
	if err := os.WriteFile(schema, []byte(`{}`), 0o644); err != nil {
		t.Fatal(err)
	}
	return map[string]any{
		"name":    name,
		"schema":  name + "-schema.json", // relative — must resolve vs manifest dir
		"domains": []string{name + ".local"},
		"env": map[string]string{
			"DATABASE_URL": "postgres://u:p@localhost/" + name,
			"JWT_SECRET":   secret,
			"ADMIN_KEY":    "k-" + name,
		},
	}
}

func TestLoadManifestValid(t *testing.T) {
	dir := t.TempDir()
	p := writeManifest(t, dir, map[string]any{
		"apps": []any{baseApp(t, dir, "crm", strings.Repeat("a", 32)), baseApp(t, dir, "shop", strings.Repeat("b", 32))},
	})
	m, err := LoadManifest(p)
	if err != nil {
		t.Fatalf("valid manifest rejected: %v", err)
	}
	if m.Listen != ":8080" || m.StatusAddr != "127.0.0.1:9601" {
		t.Fatalf("defaults not applied: %+v", m)
	}
	if !filepath.IsAbs(m.Apps[0].Schema) {
		t.Fatalf("schema path not resolved: %s", m.Apps[0].Schema)
	}
	if m.Apps[0].MergedEnv()["ADMIN_KEY"] != "k-crm" {
		t.Fatalf("env not merged")
	}
}

func TestLoadManifestRejections(t *testing.T) {
	dir := t.TempDir()
	secret := strings.Repeat("s", 32)

	cases := []struct {
		name    string
		mutate  func(apps []any) []any
		wantErr string
	}{
		{"duplicate name", func(apps []any) []any {
			dup := baseApp(t, dir, "crm", strings.Repeat("x", 32))
			return append(apps, dup)
		}, "duplicate app name"},
		{"duplicate domain", func(apps []any) []any {
			b := baseApp(t, dir, "other", strings.Repeat("y", 32))
			b["domains"] = []string{"crm.local"}
			return append(apps, b)
		}, "domain \"crm.local\" claimed by both"},
		{"shared jwt secret", func(apps []any) []any {
			return append(apps, baseApp(t, dir, "twin", secret))
		}, "share the same JWT_SECRET"},
		{"missing required env", func(apps []any) []any {
			b := baseApp(t, dir, "noenv", strings.Repeat("z", 32))
			delete(b["env"].(map[string]string), "JWT_SECRET")
			return append(apps, b)
		}, "JWT_SECRET is required"},
		{"missing schema file", func(apps []any) []any {
			b := baseApp(t, dir, "ghost", strings.Repeat("w", 32))
			b["schema"] = "does-not-exist.json"
			return append(apps, b)
		}, "does-not-exist.json"},
		{"bad name", func(apps []any) []any {
			b := baseApp(t, dir, "badname", strings.Repeat("v", 32))
			b["name"] = "Bad Name"
			return append(apps, b)
		}, "must match"},
		{"unknown key", func(apps []any) []any {
			b := baseApp(t, dir, "unk", strings.Repeat("u", 32))
			b["portt"] = 1 // typo must be rejected, not silently ignored
			return append(apps, b)
		}, "unknown field"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			apps := []any{baseApp(t, dir, "crm", secret)}
			p := writeManifest(t, dir, map[string]any{"apps": tc.mutate(apps)})
			_, err := LoadManifest(p)
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("want error containing %q, got %v", tc.wantErr, err)
			}
		})
	}
}

func TestEnvFileMergeAndOverride(t *testing.T) {
	dir := t.TempDir()
	envFile := filepath.Join(dir, "crm.env")
	os.WriteFile(envFile, []byte("# comment\nexport DATABASE_URL=postgres://from-file\nJWT_SECRET=\"filesecret-0123456789012345678901\"\nADMIN_KEY=filekey\n"), 0o600) //nolint:errcheck
	app := baseApp(t, dir, "crm", "ignored")
	app["env_file"] = "crm.env"
	app["env"] = map[string]string{"ADMIN_KEY": "override-wins"} // partial override
	p := writeManifest(t, dir, map[string]any{"apps": []any{app}})
	m, err := LoadManifest(p)
	if err != nil {
		t.Fatal(err)
	}
	env := m.Apps[0].MergedEnv()
	if env["DATABASE_URL"] != "postgres://from-file" {
		t.Fatalf("env_file not loaded: %v", env)
	}
	if env["JWT_SECRET"] != "filesecret-0123456789012345678901" {
		t.Fatalf("quoted value not unquoted: %q", env["JWT_SECRET"])
	}
	if env["ADMIN_KEY"] != "override-wins" {
		t.Fatalf("explicit env must override env_file, got %q", env["ADMIN_KEY"])
	}
}

// ── proxy ───────────────────────────────────────────────────────────────────

// twoAppProxy builds a proxy over two httptest backends that echo which app
// answered and the Host they saw (the tenant-carrying header must survive).
func twoAppProxy(t *testing.T) (*Proxy, func()) {
	t.Helper()
	mk := func(name string) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprintf(w, "%s|%s|%s", name, r.Host, r.URL.Path) //nolint:errcheck
		}))
	}
	crm, shop := mk("crm"), mk("shop")
	port := func(u string) int {
		parsed, _ := url.Parse(u)
		var p int
		fmt.Sscanf(parsed.Port(), "%d", &p) //nolint:errcheck
		return p
	}
	mf := &Manifest{Apps: []AppSpec{
		{Name: "crm", Domains: []string{"crm.example.com"}},
		{Name: "shop", Domains: []string{"shop.example.com", "tienda.local"}},
	}}
	ports := map[string]int{"crm": port(crm.URL), "shop": port(shop.URL)}
	p, err := NewProxy(mf, func(n string) (int, bool) { v, ok := ports[n]; return v, ok })
	if err != nil {
		t.Fatal(err)
	}
	return p, func() { crm.Close(); shop.Close() }
}

func TestProxyRoutesByHostAndPreservesIt(t *testing.T) {
	p, done := twoAppProxy(t)
	defer done()
	srv := httptest.NewServer(p)
	defer srv.Close()

	get := func(host, path string) (int, string) {
		req, _ := http.NewRequest("GET", srv.URL+path, nil)
		req.Host = host
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close() //nolint:errcheck
		var b strings.Builder
		buf := make([]byte, 512)
		for {
			n, err := resp.Body.Read(buf)
			b.Write(buf[:n])
			if err != nil {
				break
			}
		}
		return resp.StatusCode, b.String()
	}

	// Exact domain → its app.
	if code, body := get("crm.example.com", "/api/x"); code != 200 || !strings.HasPrefix(body, "crm|crm.example.com|/api/x") {
		t.Fatalf("exact match failed: %d %q", code, body)
	}
	// TENANT subdomain routes to the app AND the Host survives verbatim (the
	// engine's tenant middleware depends on it).
	if code, body := get("acme.shop.example.com", "/api/y"); code != 200 || body != "shop|acme.shop.example.com|/api/y" {
		t.Fatalf("subdomain match / Host preservation failed: %d %q", code, body)
	}
	// Second domain of the same app.
	if code, body := get("tienda.local", "/"); code != 200 || !strings.HasPrefix(body, "shop|") {
		t.Fatalf("alias domain failed: %d %q", code, body)
	}
	// Host with port — port stripped before matching.
	if code, body := get("crm.example.com:8443", "/z"); code != 200 || !strings.HasPrefix(body, "crm|") {
		t.Fatalf("host:port match failed: %d %q", code, body)
	}
	// Unknown domain → clean 404, never a fallthrough to some app.
	if code, body := get("nobody.example.org", "/api/x"); code != 404 || !strings.Contains(body, "unknown app domain") {
		t.Fatalf("unknown domain must 404: %d %q", code, body)
	}
}

// ── supervisor ──────────────────────────────────────────────────────────────

// fakeEngine writes a shell script the supervisor spawns instead of the real
// binary. It ignores the serve args, appends a boot marker, and sleeps.
func fakeEngine(t *testing.T, dir string, sleepSecs string) string {
	t.Helper()
	script := filepath.Join(dir, "fake-engine.sh")
	// exec so the sleep replaces the shell — SIGTERM lands on the process the
	// supervisor tracks, like the real engine.
	body := "#!/bin/sh\necho boot >> \"" + filepath.Join(dir, "boots.log") + "\"\nexec sleep " + sleepSecs + "\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	return script
}

func supFor(t *testing.T, dir, bin string) *Supervisor {
	t.Helper()
	schema := filepath.Join(dir, "s.json")
	os.WriteFile(schema, []byte("{}"), 0o644) //nolint:errcheck
	mf := &Manifest{
		DataDir: filepath.Join(dir, "data"),
		Apps: []AppSpec{{
			Name: "one", Schema: schema, Domains: []string{"one.local"},
			mergedEnv: map[string]string{"DATABASE_URL": "x", "JWT_SECRET": "y", "ADMIN_KEY": "z"},
		}},
	}
	s := NewSupervisor(mf, bin)
	s.baseBackoff = 20 * time.Millisecond // fast restarts for the test
	s.bootstrap = func(context.Context, string) error { return nil }
	return s
}

func TestSupervisorRestartsOnExitButNotOnStop(t *testing.T) {
	dir := t.TempDir()
	// A "crashing engine": exits after 0.1 s → the supervisor must respawn it.
	s := supFor(t, dir, fakeEngine(t, dir, "0.1"))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := s.Start(ctx); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if s.Status()[0].Restarts >= 2 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if got := s.Status()[0].Restarts; got < 2 {
		t.Fatalf("crashing app not restarted: restarts=%d", got)
	}

	// Deliberate stop: no auto-restart afterwards.
	if err := s.StopApp("one"); err != nil {
		t.Fatal(err)
	}
	base := s.Status()[0].Restarts
	time.Sleep(300 * time.Millisecond)
	st := s.Status()[0]
	if st.Running || st.Restarts != base {
		t.Fatalf("stopped app must stay stopped: %+v", st)
	}

	// StartApp brings it back.
	if err := s.StartApp("one"); err != nil {
		t.Fatal(err)
	}
	if st := s.Status()[0]; !st.Running {
		t.Fatalf("started app not running: %+v", st)
	}
	s.Shutdown()
}

func TestSupervisorShutdownStopsAll(t *testing.T) {
	dir := t.TempDir()
	s := supFor(t, dir, fakeEngine(t, dir, "30"))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := s.Start(ctx); err != nil {
		t.Fatal(err)
	}
	if st := s.Status()[0]; !st.Running || st.PID == 0 {
		t.Fatalf("app not running after Start: %+v", st)
	}
	s.Shutdown()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if !s.Status()[0].Running {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("app still running after Shutdown: %+v", s.Status()[0])
}

// The fleet-operator key is a level ABOVE the apps: it must never coincide
// with any app's ADMIN_KEY or JWT_SECRET (MT-STRUCT-S5).
func TestOperatorKeyMustDifferFromAppCredentials(t *testing.T) {
	dir := t.TempDir()
	app := baseApp(t, dir, "crm", strings.Repeat("s", 32))
	m := map[string]any{"operator_key": "k-crm", "apps": []any{app}} // == crm ADMIN_KEY
	p := writeManifest(t, dir, m)
	if _, err := LoadManifest(p); err == nil || !strings.Contains(err.Error(), "operator_key must differ") {
		t.Fatalf("want operator_key collision error, got %v", err)
	}
	m["operator_key"] = "a-distinct-fleet-operator-key"
	p = writeManifest(t, dir, m)
	mf, err := LoadManifest(p)
	if err != nil || mf.OperatorKey != "a-distinct-fleet-operator-key" {
		t.Fatalf("distinct operator key must load: %v", err)
	}
}

// FLEET-CONSOLE-S2: the unified operator identity — the email may live in the
// manifest, the password ONLY in the environment; declaring the email without
// the password env must fail the load loudly (never a silently-off feature).
func TestLoadManifestOperatorAdmin(t *testing.T) {
	dir := t.TempDir()
	p := writeManifest(t, dir, map[string]any{
		"operator_admin_email": "op@fleet.local",
		"apps":                 []any{baseApp(t, dir, "crm", strings.Repeat("a", 32))},
	})

	t.Setenv("APPITOOLS_FLEET_ADMIN_PASSWORD", "")
	if _, err := LoadManifest(p); err == nil || !strings.Contains(err.Error(), "APPITOOLS_FLEET_ADMIN_PASSWORD") {
		t.Fatalf("email without password env must fail actionably, got: %v", err)
	}

	t.Setenv("APPITOOLS_FLEET_ADMIN_PASSWORD", "a-strong-passphrase")
	m, err := LoadManifest(p)
	if err != nil {
		t.Fatalf("valid operator admin rejected: %v", err)
	}
	email, pass := m.OperatorAdmin()
	if email != "op@fleet.local" || pass != "a-strong-passphrase" {
		t.Fatalf("OperatorAdmin() = %q/%q", email, pass)
	}

	// Env fallback for the email too (APPITOOLS_FLEET_ADMIN_EMAIL).
	p2 := writeManifest(t, dir, map[string]any{
		"apps": []any{baseApp(t, dir, "crm", strings.Repeat("a", 32))},
	})
	t.Setenv("APPITOOLS_FLEET_ADMIN_EMAIL", "env@fleet.local")
	m2, err := LoadManifest(p2)
	if err != nil {
		t.Fatalf("env-fallback email rejected: %v", err)
	}
	if e, _ := m2.OperatorAdmin(); e != "env@fleet.local" {
		t.Fatalf("email env fallback = %q", e)
	}
}

// FLEET-DB-ASSIST: db_instances resolve their admin DSN from env (fail-loud when
// the declared env var is unset), and the console-facing view never leaks it.
func TestLoadManifestDBInstances(t *testing.T) {
	dir := t.TempDir()
	m := map[string]any{
		"db_instances": []any{
			map[string]any{"name": "local", "label": "Local PG", "admin_dsn_env": "TEST_DB_LOCAL_ADMIN"},
		},
		"apps": []any{baseApp(t, dir, "crm", strings.Repeat("a", 32))},
	}
	p := writeManifest(t, dir, m)

	// Declared but unwired → fail loud.
	t.Setenv("TEST_DB_LOCAL_ADMIN", "")
	if _, err := LoadManifest(p); err == nil || !strings.Contains(err.Error(), "TEST_DB_LOCAL_ADMIN") {
		t.Fatalf("declared-but-unwired instance must fail loud, got: %v", err)
	}

	// Wired → resolves, and the safe view exposes name/label/can_create but NOT the DSN.
	t.Setenv("TEST_DB_LOCAL_ADMIN", "postgres://admin:secret@localhost:5432/postgres")
	mf, err := LoadManifest(p)
	if err != nil {
		t.Fatalf("valid db_instance rejected: %v", err)
	}
	inst := mf.DBInstanceByName("local")
	if inst == nil || inst.AdminDSN() != "postgres://admin:secret@localhost:5432/postgres" {
		t.Fatalf("admin DSN not resolved: %+v", inst)
	}
	safe := mf.SafeDBInstances()
	if len(safe) != 1 || safe[0].Name != "local" || !safe[0].CanCreateDB {
		t.Fatalf("safe view wrong: %+v", safe)
	}
	// The safe view is a distinct type with no DSN field — a compile-time guarantee
	// the secret can't be marshaled to the browser; assert the label round-trips.
	if safe[0].Label != "Local PG" {
		t.Fatalf("safe label = %q", safe[0].Label)
	}
}
