package integration_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	chi "github.com/go-chi/chi/v5"
	"github.com/miguelangel/appitools/pkg/auth"
	"github.com/miguelangel/appitools/pkg/codegen"
	"github.com/miguelangel/appitools/pkg/db"
	"github.com/miguelangel/appitools/pkg/extensions"
	rbacpkg "github.com/miguelangel/appitools/pkg/rbac"
	"github.com/miguelangel/appitools/pkg/schema"
	"github.com/miguelangel/appitools/pkg/tenant"
)

// V3 — Webhook async. A create on a resource with an after_create webhook must:
//  1. return 201 in well under 100ms (the webhook is dispatched asynchronously
//     and must never block the response), and
//  2. still deliver the webhook out-of-band.
//
// The webhook server deliberately sleeps 1.5s; if dispatch were synchronous the
// 201 would take >1.5s.
func TestWebhookAfterCreate_AsyncNonBlocking(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: skipping in -short mode")
	}
	ctx := context.Background()
	pool, clean := startPG(t)
	defer clean()

	// Provision the tenant schema + table directly (no control plane required).
	const ddl = `
CREATE SCHEMA IF NOT EXISTS tenant_acmetest;
CREATE TABLE tenant_acmetest.guides (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  code text NOT NULL,
  status text,
  created_at timestamptz DEFAULT now()
);`
	if _, err := pool.Exec(ctx, ddl); err != nil {
		t.Fatalf("provision schema: %v", err)
	}

	const webhookDelay = 1500 * time.Millisecond
	var delivered int32
	hookSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(webhookDelay)
		atomic.AddInt32(&delivered, 1)
		w.WriteHeader(http.StatusOK)
	}))
	defer hookSrv.Close()

	s := &schema.APISchema{
		Schema: "https://appitools.dev/schema/v1", Version: "1", Name: "webhook-async",
		Resources: map[string]schema.ResourceSchema{
			"guides": {
				Fields: map[string]schema.FieldDef{
					"code":   {Type: "string", Required: true},
					"status": {Type: "string"},
				},
				Hooks: map[string]schema.HookConfig{
					"after_create": {Type: "webhook", URL: hookSrv.URL, HMACSecretEnv: "X_TEST_SECRET"},
				},
			},
		},
		RBAC: schema.RBACPolicy{Roles: map[string]schema.RolePolicy{
			"super_admin": {Resources: json.RawMessage(`"*"`), Actions: []string{"*"}},
		}},
	}

	policyJSON, _ := json.Marshal(s.RBAC)
	tdb := db.NewTenantDB(pool)
	// Insecure dispatcher so the loopback httptest webhook is reachable — the
	// production SSRF/HTTPS guards intentionally block loopback targets.
	disp := extensions.NewWebhookDispatcherOpts(
		extensions.WithInsecureTransport(&http.Client{Timeout: 10 * time.Second}),
	)
	hr := extensions.NewHookRunnerWithDispatcher(extensions.NewJSSandbox(), disp, 0)

	inner := chi.NewMux()
	inner.Use(tenant.TenantMiddleware)
	inner.Use(auth.JWTMiddleware(jwtSecret))
	inner.Use(rbacpkg.RBACMiddleware(policyJSON))
	inner.Mount("/", codegen.BuildRouter(s, tdb, hr, nil))
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		req.Host = tenantID + ".localhost"
		inner.ServeHTTP(w, req)
	}))
	defer srv.Close()

	token := genToken("super_admin", superID)
	body, _ := json.Marshal(map[string]any{"code": "GU-ASYNC-1", "status": "pending"})

	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/api/guides", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)

	start := time.Now()
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("POST /api/guides: %v", err)
	}
	latency := time.Since(start)
	resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201, got %d", resp.StatusCode)
	}
	if latency >= 100*time.Millisecond {
		t.Fatalf("201 took %v; expected <100ms (webhook must not block the response)", latency)
	}
	if d := atomic.LoadInt32(&delivered); d != 0 {
		t.Fatalf("webhook delivered before the 201 returned (delivered=%d); dispatch was not async", d)
	}
	t.Logf("201 returned in %v while webhook sleeps %v — async confirmed", latency, webhookDelay)

	// The webhook must still be delivered, asynchronously.
	deadline := time.Now().Add(webhookDelay + 3*time.Second)
	for time.Now().Before(deadline) {
		if atomic.LoadInt32(&delivered) == 1 {
			t.Log("webhook delivered asynchronously after the 201")
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("webhook was never delivered (delivered=%d)", atomic.LoadInt32(&delivered))
}
