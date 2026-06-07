// Package benchmark measures data-plane throughput against a real PostgreSQL
// instance. Run with:
//
//	go test ./pkg/benchmark/... -bench=. -benchmem -benchtime=10s -count=3
package benchmark_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"sort"
	"sync"
	"testing"
	"time"

	chi "github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/miguelangel/appitools/pkg/auth"
	"github.com/miguelangel/appitools/pkg/codegen"
	"github.com/miguelangel/appitools/pkg/controlplane"
	"github.com/miguelangel/appitools/pkg/db"
	"github.com/miguelangel/appitools/pkg/extensions"
	rbacpkg "github.com/miguelangel/appitools/pkg/rbac"
	"github.com/miguelangel/appitools/pkg/schema"
	"github.com/miguelangel/appitools/pkg/tenant"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

// ── shared infrastructure (started once per test binary) ─────────────────────

var (
	benchPool   *pgxpool.Pool
	benchSrv    *httptest.Server     // single-tenant data plane
	benchToken  string               // super_admin JWT
	benchSrvMap [10]*httptest.Server // 10-tenant servers for multi-tenant benchmark
	benchTokens [10]string
	benchOnce   sync.Once
)

const benchSecret = "bench-jwt-secret"

func benchSchema() *schema.APISchema {
	return &schema.APISchema{
		Schema:  "https://appitools.dev/schema/v1",
		Version: "1",
		Name:    "bench",
		Resources: map[string]schema.ResourceSchema{
			"guides": {
				Fields: map[string]schema.FieldDef{
					"code":       {Type: "string", Required: true},
					"status":     {Type: "string"},
					"created_at": {Type: "time", Auto: true},
				},
				Hooks: map[string]schema.HookConfig{
					"before_create": {
						Type:   "js",
						Script: `if (!data.code) { result.proceed = false; result.error = "code required"; }`,
					},
				},
			},
		},
		RBAC: schema.RBACPolicy{Roles: map[string]schema.RolePolicy{
			"admin": {Resources: json.RawMessage(`"*"`), Actions: []string{"*"}},
		}},
	}
}

func setupBench() {
	ctx := context.Background()

	ctr, err := tcpostgres.Run(ctx, "postgres:16-alpine",
		tcpostgres.WithDatabase("bench"),
		tcpostgres.WithUsername("bench"),
		tcpostgres.WithPassword("bench"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").WithOccurrence(2),
		),
	)
	if err != nil {
		fmt.Fprintln(os.Stderr, "benchmark: start postgres:", err)
		os.Exit(1)
	}

	connStr, _ := ctr.ConnectionString(ctx, "sslmode=disable")
	pool, err := db.NewPool(ctx, connStr)
	if err != nil {
		fmt.Fprintln(os.Stderr, "benchmark: pool:", err)
		os.Exit(1)
	}
	benchPool = pool

	// Apply control plane schema.
	sql, _ := os.ReadFile("../../migrations/001_control_plane.sql")
	benchPool.Exec(ctx, string(sql))

	// ── Single-tenant benchmark setup ────────────────────────────────────────
	s := benchSchema()
	controlplane.RegisterTenant(ctx, benchPool, controlplane.RegisterRequest{
		TenantID: "benchone", DisplayName: "Bench", Email: "b@b.com", Plan: "free",
		Schema: s,
	})

	// Seed 1000 rows for list benchmark.
	for i := 0; i < 1000; i++ {
		benchPool.Exec(ctx,
			`INSERT INTO tenant_benchone.guides (code, status) VALUES ($1, 'ok')`,
			fmt.Sprintf("GU-%04d", i))
	}

	benchSrv = buildServer(s, benchPool, "benchone")
	benchToken, _ = auth.GenerateToken(auth.Claims{Role: "admin", TenantID: "benchone"}, benchSecret)

	// ── 10-tenant benchmark setup ─────────────────────────────────────────────
	for i := 0; i < 10; i++ {
		tid := fmt.Sprintf("benchtenant%02d", i)
		ts := benchSchema()
		controlplane.RegisterTenant(ctx, benchPool, controlplane.RegisterRequest{
			TenantID: tid, DisplayName: tid, Email: tid + "@b.com", Plan: "free",
			Schema: ts,
		})
		// 100 rows each.
		for j := 0; j < 100; j++ {
			benchPool.Exec(ctx,
				fmt.Sprintf(`INSERT INTO tenant_%s.guides (code,status) VALUES ($1,'ok')`, tid),
				fmt.Sprintf("GU-%04d", j))
		}
		benchSrvMap[i] = buildServer(ts, benchPool, tid)
		benchTokens[i], _ = auth.GenerateToken(auth.Claims{Role: "admin", TenantID: tid}, benchSecret)
	}
}

func buildServer(s *schema.APISchema, pool *pgxpool.Pool, host string) *httptest.Server {
	policyJSON, _ := json.Marshal(s.RBAC)
	tdb := db.NewTenantDB(pool)
	hr := extensions.NewHookRunner(extensions.NewJSSandbox())

	r := chi.NewMux()
	r.Use(tenant.TenantMiddleware)
	r.Use(auth.JWTMiddleware(benchSecret))
	r.Use(rbacpkg.RBACMiddleware(policyJSON))
	r.Mount("/", codegen.BuildRouter(s, tdb, hr, nil))

	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		req.Host = host + ".localhost"
		r.ServeHTTP(w, req)
	}))
}

func TestMain(m *testing.M) {
	os.Exit(m.Run())
}

// ── benchmarks ────────────────────────────────────────────────────────────────

// BenchmarkListHandler_NoContention measures concurrent GET /api/guides
// against a 1000-row table with 50 parallel goroutines.
func BenchmarkListHandler_NoContention(b *testing.B) {
	if testing.Short() {
		b.Skip("benchmark needs Docker (testcontainers); skipped in -short")
	}
	benchOnce.Do(setupBench)

	b.SetParallelism(50)
	b.ReportAllocs()

	latencies := make([]time.Duration, 0, b.N)
	var mu sync.Mutex

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			start := time.Now()
			req, _ := http.NewRequest("GET", benchSrv.URL+"/api/guides", nil)
			req.Header.Set("Authorization", "Bearer "+benchToken)
			resp, err := benchSrv.Client().Do(req)
			if err != nil {
				b.Error(err)
				continue
			}
			io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			mu.Lock()
			latencies = append(latencies, time.Since(start))
			mu.Unlock()
		}
	})

	reportPercentiles(b, latencies)

	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	b.ReportMetric(float64(ms.HeapAlloc)/1024/1024, "heap_MB")
}

// BenchmarkCreateHandler_WithHook measures POST /api/guides with an active
// JS before_create hook (Goja sandbox overhead).
func BenchmarkCreateHandler_WithHook(b *testing.B) {
	if testing.Short() {
		b.Skip("benchmark needs Docker (testcontainers); skipped in -short")
	}
	benchOnce.Do(setupBench)

	b.SetParallelism(20)
	b.ReportAllocs()

	var counter int64
	var mu sync.Mutex
	latencies := make([]time.Duration, 0, b.N)

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			mu.Lock()
			counter++
			code := fmt.Sprintf("GU-BNC-%d", counter)
			mu.Unlock()

			body, _ := json.Marshal(map[string]any{"code": code, "status": "pending"})
			start := time.Now()
			req, _ := http.NewRequest("POST", benchSrv.URL+"/api/guides", bytes.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Authorization", "Bearer "+benchToken)
			resp, err := benchSrv.Client().Do(req)
			if err != nil {
				b.Error(err)
				continue
			}
			io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			latencies = append(latencies, time.Since(start))
		}
	})

	reportPercentiles(b, latencies)
}

// BenchmarkMultiTenant_10Tenants measures throughput with 10 concurrent
// tenants, verifying that SET LOCAL search_path does not introduce contention.
func BenchmarkMultiTenant_10Tenants(b *testing.B) {
	if testing.Short() {
		b.Skip("benchmark needs Docker (testcontainers); skipped in -short")
	}
	benchOnce.Do(setupBench)

	b.SetParallelism(10)
	b.ReportAllocs()

	var i int64
	var mu sync.Mutex
	latencies := make([]time.Duration, 0, b.N)

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			mu.Lock()
			idx := int(i) % 10
			i++
			mu.Unlock()

			start := time.Now()
			req, _ := http.NewRequest("GET", benchSrvMap[idx].URL+"/api/guides", nil)
			req.Header.Set("Authorization", "Bearer "+benchTokens[idx])
			resp, err := benchSrvMap[idx].Client().Do(req)
			if err != nil {
				b.Error(err)
				continue
			}
			io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			latencies = append(latencies, time.Since(start))
		}
	})

	reportPercentiles(b, latencies)
}

// ── helpers ───────────────────────────────────────────────────────────────────

func reportPercentiles(b *testing.B, latencies []time.Duration) {
	b.Helper()
	if len(latencies) == 0 {
		return
	}
	sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })
	n := len(latencies)

	p50 := latencies[n*50/100]
	p95 := latencies[n*95/100]
	p99 := latencies[n*99/100]

	b.ReportMetric(float64(p50.Microseconds()), "µs/p50")
	b.ReportMetric(float64(p95.Microseconds()), "µs/p95")
	b.ReportMetric(float64(p99.Microseconds()), "µs/p99")
	b.ReportMetric(float64(b.N)/b.Elapsed().Seconds(), "req/s")
}
