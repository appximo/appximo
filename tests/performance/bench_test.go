//go:build !short

// Package performance holds reproducible, CPU-bound Go benchmarks for CI
// regression tracking (benchmark-action → gh-pages). They are deliberately
// Docker-free and deterministic so run-to-run variance is low enough for an
// alert threshold to be meaningful — the noisy DB-backed throughput benchmarks
// live in pkg/benchmark (testcontainers) and are NOT what we trend here.
//
// What we trend is the per-request overhead the ENGINE adds: JWT validation, RBAC
// evaluation, and the full auth+routing+serialize hot path. Postgres latency is
// out of our code's control, so the GET-list benchmark stubs the DB read with a
// fixed in-memory result and measures everything around it.
package performance

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	chi "github.com/go-chi/chi/v5"

	"github.com/appximo/appximo/pkg/auth"
	"github.com/appximo/appximo/pkg/rbac"
	"github.com/appximo/appximo/pkg/schema"
	"github.com/appximo/appximo/pkg/tenant"
)

const benchSecret = "bench-jwt-secret"

// benchSchema is a minimal single-resource schema with a full-access role, used to
// build the RBAC policy and the request chain without loading a fixture (the
// helpers package is build-tagged and unavailable in the default lane).
func benchSchema() *schema.APISchema {
	return &schema.APISchema{
		Schema:  "https://appximo.com/schema/v1",
		Version: "1",
		Name:    "bench",
		Resources: map[string]schema.ResourceSchema{
			"guides": {Fields: map[string]schema.FieldDef{
				"code":   {Type: "string", Required: true},
				"status": {Type: "string"},
			}},
		},
		RBAC: schema.RBACPolicy{Roles: map[string]schema.RolePolicy{
			"super_admin": {Resources: json.RawMessage(`"*"`), Actions: []string{"*"}},
		}},
	}
}

func benchPolicyJSON(b *testing.B) []byte {
	b.Helper()
	pj, err := json.Marshal(benchSchema().RBAC)
	if err != nil {
		b.Fatalf("marshal rbac: %v", err)
	}
	return pj
}

func benchToken(b *testing.B) string {
	b.Helper()
	tok, err := auth.GenerateToken(auth.Claims{
		UserID: "00000000-0000-0000-0000-000000000001", Role: "super_admin", TenantID: "bench",
	}, benchSecret)
	if err != nil {
		b.Fatalf("generate token: %v", err)
	}
	return tok
}

// BenchmarkJWTValidation measures the cost of parsing + verifying one HS256 token
// (the per-request auth cost when the claims cache misses).
func BenchmarkJWTValidation(b *testing.B) {
	token := benchToken(b)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := auth.ValidateToken(token, benchSecret); err != nil {
			b.Fatalf("validate: %v", err)
		}
	}
}

// BenchmarkRBACCheck measures one policy evaluation (role → resource/action).
func BenchmarkRBACCheck(b *testing.B) {
	var policy rbac.Policy
	if err := json.Unmarshal(benchPolicyJSON(b), &policy); err != nil {
		b.Fatalf("unmarshal policy: %v", err)
	}
	evalCtx := rbac.EvalContext{Role: "super_admin", UserID: "00000000-0000-0000-0000-000000000001"}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if res := policy.Evaluate(evalCtx, "guides", "read"); !res.Allowed {
			b.Fatal("expected allowed")
		}
	}
}

// BenchmarkGETListHandler measures the full per-request engine overhead for a list
// GET — TenantMiddleware → JWT → RBAC → routing → JSON serialization — with the DB
// read stubbed by a fixed in-memory result, so the number reflects our code, not
// Postgres. This is the realistic hot-path cost a regression would show up in.
func BenchmarkGETListHandler(b *testing.B) {
	policyJSON := benchPolicyJSON(b)
	token := benchToken(b)

	// A canned list payload mirroring the real list envelope ({"data":[...],"meta":{}}).
	rows := make([]map[string]any, 0, 20)
	for i := 0; i < 20; i++ {
		rows = append(rows, map[string]any{
			"id":   "00000000-0000-0000-0000-0000000000" + twoHex(i),
			"code": "GU-" + twoHex(i), "status": "in_transit",
			"origin": "BOG", "destination": "MED",
		})
	}
	payload := map[string]any{"data": rows, "meta": map[string]any{"page": 1}}

	r := chi.NewMux()
	r.Use(tenant.TenantMiddleware)
	r.Use(auth.JWTMiddleware(benchSecret))
	r.Use(rbac.RBACMiddleware(policyJSON))
	r.Get("/api/guides", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(payload) //nolint:errcheck
	})

	srv := httptest.NewServer(r)
	defer srv.Close()
	client := srv.Client()

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		req, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/guides", nil)
		req.Host = "bench.localhost"
		req.Header.Set("Authorization", "Bearer "+token)
		resp, err := client.Do(req)
		if err != nil {
			b.Fatalf("request: %v", err)
		}
		io.Copy(io.Discard, resp.Body) //nolint:errcheck
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			b.Fatalf("want 200, got %d", resp.StatusCode)
		}
	}
}

// twoHex renders 0..255 as a two-char hex string for stable synthetic ids.
func twoHex(n int) string {
	const hexd = "0123456789abcdef"
	return string([]byte{hexd[(n>>4)&0xf], hexd[n&0xf]})
}
