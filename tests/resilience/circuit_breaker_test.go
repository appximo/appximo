//go:build resilience

// Package resilience holds chaos/resilience tests (build tag `resilience`). They
// inject real failures (network latency via toxiproxy, process shutdown under load)
// against the REAL middleware chain + a REAL Postgres, so claims like "the circuit
// breaker opens" become executed evidence rather than code inspection.
//
// circuit_breaker_test.go verifies the gobreaker wired into db.NewTenantDB
// (pkg/db/tenant.go → resilience.NewQueryBreaker). Toxiproxy sits between the
// engine's pool and Postgres; injecting 6s of latency (> the 5s query timeout)
// forces query failures, which must (a) open the breaker after ≥10 requests at
// ≥60% failure, turning subsequent 5xx into IMMEDIATE 503s (not 5s timeouts), and
// (b) recover to 200 once the latency is removed and the 8s open→half-open window
// elapses.
//
// NOTE on the real breaker config (verified in pkg/resilience/circuitbreaker.go):
// MaxRequests=2, Timeout=8s (open→half-open), ReadyToTrip = Requests≥10 AND
// failure-ratio≥0.6. PRIMER's "10 fallos/recovery 30s" is approximate — the code
// is a ratio over ≥10 requests with an 8s recovery window.
package resilience

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"
	"time"

	toxiproxy "github.com/Shopify/toxiproxy/v2"
	toxiclient "github.com/Shopify/toxiproxy/v2/client"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/rs/zerolog"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/appximo/appximo/pkg/db"
	"github.com/appximo/appximo/tests/helpers"
)

func TestCircuitBreakerOpensUnderPostgresLatency(t *testing.T) {
	ctx := context.Background()

	// 1. Real Postgres. Capture BOTH the direct conn string (for setup, never
	//    throttled) and the raw host:port (the toxiproxy upstream).
	directConnStr, pgHostPort, pgCleanup := startPostgresRaw(t, ctx)
	defer pgCleanup()

	// Control-plane + tenant provisioning over a DIRECT pool, so setup is fast and
	// never affected by the toxic we add later.
	setupPool, err := db.NewPool(ctx, directConnStr)
	if err != nil {
		t.Fatalf("setup pool: %v", err)
	}
	defer setupPool.Close()
	if err := helpers.ApplyControlPlane(ctx, setupPool); err != nil {
		t.Fatalf("apply control plane: %v", err)
	}
	s := helpers.FixtureSchema(t, "logistics_schema.json")
	const tenantID = "cbtenant"
	helpers.RegisterTenant(t, setupPool, tenantID, s)

	// 2. In-process toxiproxy: API server + a proxy (engine → proxy → Postgres).
	proxy := startToxiproxy(t, pgHostPort)

	// 3. Engine pool pointed at the PROXY, so every query traverses toxiproxy and
	//    the per-TenantDB circuit breaker (db.NewTenantDB inside BuildObservableServer).
	proxyConnStr := replaceHost(t, directConnStr, proxy.Listen)
	enginePool, err := db.NewPool(ctx, proxyConnStr)
	if err != nil {
		t.Fatalf("engine pool (via proxy): %v", err)
	}
	defer enginePool.Close()

	srv, _ := helpers.BuildObservableServer(t, s, enginePool)
	token := helpers.GenToken(t, "super_admin", "00000000-0000-0000-0000-000000000001", tenantID)

	// 4. Baseline: a normal request through the proxy works.
	if status, _ := doGuidesGET(t, srv, tenantID, token); status != http.StatusOK {
		t.Fatalf("baseline GET /api/guides: want 200, got %d", status)
	}

	// 5. Inject 6s downstream latency (> the 5s query timeout) so every query fails.
	if _, err := proxy.AddToxic("latency", "latency", "downstream", 1.0,
		toxiclient.Attributes{"latency": 6000, "jitter": 0}); err != nil {
		t.Fatalf("add latency toxic: %v", err)
	}

	// 6. Drive enough failing requests to trip the breaker (need ≥10 at ≥60% fail).
	//    Fire them concurrently so the ~5s-each timeouts overlap instead of summing.
	fireConcurrent(t, srv, tenantID, token, 15)

	// The breaker must now be OPEN: a further request returns 503 IMMEDIATELY
	// (breaker short-circuit), not after a 5s query timeout. Poll briefly to absorb
	// the tiny window where the last concurrent failures are still settling counts.
	if !eventuallyFastStatus(t, srv, tenantID, token, http.StatusServiceUnavailable, 2*time.Second, 5*time.Second) {
		t.Fatal("circuit breaker did not open: no immediate 503 after 15 failing requests")
	}
	t.Log("circuit breaker OPEN: immediate 503 (< 2s), not a 5s query timeout")

	// 7. Remove the toxic — Postgres is healthy again.
	if err := proxy.RemoveToxic("latency"); err != nil {
		t.Fatalf("remove latency toxic: %v", err)
	}

	// 8/9. After the 8s open→half-open window, probes succeed and the breaker
	//      closes → 200 again. Poll up to 25s (8s window + half-open probes + slack).
	if !eventuallyStatus(t, srv, tenantID, token, http.StatusOK, 25*time.Second) {
		t.Fatal("circuit breaker did not recover to 200 after latency removed + recovery window")
	}
	t.Log("circuit breaker RECOVERED: requests return 200 after the toxic was removed")
}

// ── helpers ─────────────────────────────────────────────────────────────────

// startPostgresRaw boots postgres:16-alpine and returns the direct connection
// string, the raw host:port (toxiproxy upstream), and a cleanup func.
func startPostgresRaw(t *testing.T, ctx context.Context) (connStr, hostPort string, cleanup func()) {
	t.Helper()
	ctr, err := tcpostgres.Run(ctx, "postgres:16-alpine",
		tcpostgres.WithDatabase("appximo_test"),
		tcpostgres.WithUsername("test"),
		tcpostgres.WithPassword("test"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").WithOccurrence(2),
		),
	)
	if err != nil {
		t.Fatalf("start postgres: %v", err)
	}
	connStr, err = ctr.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		_ = ctr.Terminate(ctx)
		t.Fatalf("connection string: %v", err)
	}
	host, err := ctr.Host(ctx)
	if err != nil {
		_ = ctr.Terminate(ctx)
		t.Fatalf("container host: %v", err)
	}
	port, err := ctr.MappedPort(ctx, "5432/tcp")
	if err != nil {
		_ = ctr.Terminate(ctx)
		t.Fatalf("mapped port: %v", err)
	}
	return connStr, net.JoinHostPort(host, port.Port()), func() { _ = ctr.Terminate(ctx) }
}

// startToxiproxy starts an in-process toxiproxy API server and creates a proxy that
// forwards a fresh local port to upstream (the Postgres host:port). It returns the
// live client proxy; t.Cleanup tears the proxy down.
func startToxiproxy(t *testing.T, upstream string) *toxiclient.Proxy {
	t.Helper()
	apiAddr := freeAddr(t)
	proxyAddr := freeAddr(t)

	server := toxiproxy.NewServer(toxiproxy.NewMetricsContainer(prometheus.NewRegistry()), zerolog.Nop())
	go func() { _ = server.Listen(apiAddr) }()

	client := toxiclient.NewClient(apiAddr)
	// Wait for the API to accept connections.
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := client.Proxies(); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("toxiproxy API did not come up within 5s")
		}
		time.Sleep(20 * time.Millisecond)
	}

	proxy, err := client.CreateProxy("appitools-pg", proxyAddr, upstream)
	if err != nil {
		t.Fatalf("create toxiproxy proxy: %v", err)
	}
	t.Cleanup(func() { _ = proxy.Delete() })
	return proxy
}

// freeAddr returns a currently-free 127.0.0.1:port string.
func freeAddr(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve free port: %v", err)
	}
	defer l.Close()
	return l.Addr().String()
}

// replaceHost rewrites the host:port of a postgres URL, keeping creds/db/params.
func replaceHost(t *testing.T, connStr, newHostPort string) string {
	t.Helper()
	u, err := url.Parse(connStr)
	if err != nil {
		t.Fatalf("parse conn string: %v", err)
	}
	u.Host = newHostPort
	return u.String()
}

// doGuidesGET issues one GET /api/guides and returns the status and elapsed time.
func doGuidesGET(t *testing.T, srv *httptest.Server, tenantID, token string) (int, time.Duration) {
	t.Helper()
	req := helpers.TenantRequest(t, srv, http.MethodGet, "/api/guides", tenantID, token)
	start := time.Now()
	resp, err := srv.Client().Do(req)
	elapsed := time.Since(start)
	if err != nil {
		// A transport error (e.g. mid-shutdown) is not a status; surface it as -1.
		return -1, elapsed
	}
	io.Copy(io.Discard, resp.Body) //nolint:errcheck
	resp.Body.Close()
	return resp.StatusCode, elapsed
}

// fireConcurrent issues n GET /api/guides requests in parallel and waits for all to
// finish. Their individual results are irrelevant — the goal is to accumulate
// failures in the breaker.
func fireConcurrent(t *testing.T, srv *httptest.Server, tenantID, token string, n int) {
	t.Helper()
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()
			doGuidesGET(t, srv, tenantID, token)
		}()
	}
	wg.Wait()
}

// eventuallyFastStatus polls until a request returns wantStatus in UNDER fastUnder
// (proving the breaker short-circuits rather than waiting on the 5s query timeout),
// or until the deadline. Returns true on success.
func eventuallyFastStatus(t *testing.T, srv *httptest.Server, tenantID, token string, wantStatus int, fastUnder, within time.Duration) bool {
	t.Helper()
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		status, elapsed := doGuidesGET(t, srv, tenantID, token)
		if status == wantStatus && elapsed < fastUnder {
			return true
		}
		time.Sleep(50 * time.Millisecond)
	}
	return false
}

// eventuallyStatus polls until a request returns wantStatus or the deadline passes.
func eventuallyStatus(t *testing.T, srv *httptest.Server, tenantID, token string, wantStatus int, within time.Duration) bool {
	t.Helper()
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		if status, _ := doGuidesGET(t, srv, tenantID, token); status == wantStatus {
			return true
		}
		time.Sleep(200 * time.Millisecond)
	}
	return false
}
