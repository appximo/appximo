//go:build e2e

// Package e2e_test holds the S38 end-to-end client scenarios (build tag `e2e`).
//
// These tests drive the REAL middleware chain (TenantMiddleware → RequestLogger
// tap → JWT → RBAC → generated REST routes) against a REAL Postgres booted once
// via testcontainers (shared across the suite by TestMain). They use httpexpect
// for fluent assertions and the official prometheus/testutil comparator to assert
// the engine's real metric series.
//
// Design notes / engine truths exercised here (verified against the source, not
// assumed — see context-docs/TESTING_PLAN.md "Hallazgos S37"):
//   - Metrics are `appitools_`-prefixed; there is NO `security_blocked_total`. The
//     attack scenario asserts appitools_requests_total{status="401"} instead.
//   - The engine uses its own SpanTracker (pkg/observability/span.go), NOT OTel.
//     The DIAN scenario captures RequestTap.Spans through a span-recording tap.
//   - helpers.BuildObservableServer wires a JS+webhook-less HookRunner and drops
//     RequestTap.Spans, so the DIAN (spans) and webhook (dispatcher) scenarios use
//     the local builders below — same chain, with the extra wiring they need.
package e2e_test

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"log"
	"net/http/httptest"
	"os"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/gavv/httpexpect/v2"
	chi "github.com/go-chi/chi/v5"
	chimiddleware "github.com/go-chi/chi/v5/middleware"
	jwtlib "github.com/golang-jwt/jwt/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"github.com/rs/zerolog"

	"github.com/miguelangel/appitools/pkg/auth"
	"github.com/miguelangel/appitools/pkg/codegen"
	"github.com/miguelangel/appitools/pkg/db"
	"github.com/miguelangel/appitools/pkg/extensions"
	"github.com/miguelangel/appitools/pkg/logging"
	"github.com/miguelangel/appitools/pkg/observability"
	"github.com/miguelangel/appitools/pkg/rbac"
	"github.com/miguelangel/appitools/pkg/schema"
	"github.com/miguelangel/appitools/pkg/tenant"
	"github.com/miguelangel/appitools/tests/helpers"
)

// testPool is the suite-wide Postgres pool. One container is booted in TestMain
// and shared by every scenario; each scenario registers its own uniquely-named
// tenant so rows and metric series never collide.
var testPool *pgxpool.Pool

func TestMain(m *testing.M) {
	logging.Log = zerolog.Nop() // silence request logs; the taps still fire.
	ctx := context.Background()
	pool, cleanup, err := helpers.StartPostgres(ctx)
	if err != nil {
		log.Fatalf("e2e: start postgres: %v", err)
	}
	if err := helpers.ApplyControlPlane(ctx, pool); err != nil {
		cleanup()
		log.Fatalf("e2e: apply control plane: %v", err)
	}
	testPool = pool
	code := m.Run()
	cleanup()
	os.Exit(code)
}

// newE2EServer registers tenantID with the named fixture schema and returns an
// observable in-process server (the real middleware chain + Prometheus tap) plus
// its live *Metrics. It reuses tests/helpers/server.go end-to-end.
func newE2EServer(t *testing.T, schemaName, tenantID string) (*httptest.Server, *observability.Metrics, *schema.APISchema) {
	t.Helper()
	s := helpers.FixtureSchema(t, schemaName)
	helpers.RegisterTenant(t, testPool, tenantID, s)
	srv, metrics := helpers.BuildObservableServer(t, s, testPool)
	return srv, metrics, s
}

// newHTTPExpect returns an httpexpect client bound to srv whose every request
// carries the tenant's Host subdomain (so TenantMiddleware resolves the schema).
// The per-request Authorization header is set by each test (roles differ).
func newHTTPExpect(t *testing.T, srv *httptest.Server, tenantID string) *httpexpect.Expect {
	t.Helper()
	e := httpexpect.WithConfig(httpexpect.Config{
		BaseURL:  srv.URL,
		Client:   srv.Client(),
		Reporter: httpexpect.NewRequireReporter(t), // fail-fast, testify-backed
	})
	// Go's http client takes the virtual host from Request.Host, which httpexpect's
	// WithHost sets — the subdomain TenantMiddleware reads. Builder applies it to
	// every request issued from the returned Expect.
	return e.Builder(func(req *httpexpect.Request) {
		req.WithHost(tenantID + ".localhost")
	})
}

// mintJWT signs a normal 24h HS256 token for role/tenant (delegates to the same
// auth.GenerateToken cmd_serve.go uses, via the helpers package).
func mintJWT(t *testing.T, tenantID, role string) string {
	t.Helper()
	return helpers.GenToken(t, role, "00000000-0000-0000-0000-000000000001", tenantID)
}

// mintExpiredJWT signs a correctly-HS256-signed token whose exp is in the past.
// The signature is valid; only expiry should cause the 401 (proves exp is enforced,
// not that a bad signature was rejected).
func mintExpiredJWT(t *testing.T, tenantID, role string) string {
	t.Helper()
	c := auth.Claims{
		UserID:   "00000000-0000-0000-0000-000000000001",
		Role:     role,
		TenantID: tenantID,
		RegisteredClaims: jwtlib.RegisteredClaims{
			ExpiresAt: jwtlib.NewNumericDate(time.Now().Add(-1 * time.Hour)),
			IssuedAt:  jwtlib.NewNumericDate(time.Now().Add(-2 * time.Hour)),
		},
	}
	tok, err := jwtlib.NewWithClaims(jwtlib.SigningMethodHS256, c).SignedString([]byte(helpers.JWTSecret))
	if err != nil {
		t.Fatalf("mint expired jwt: %v", err)
	}
	return tok
}

// mintAlgNoneJWT builds an UNSIGNED `alg:none` token by hand. The engine pins
// HS256 (auth.ValidateToken), so this must be rejected with 401 — the classic
// alg-confusion bypass attempt.
func mintAlgNoneJWT(t *testing.T, tenantID, role string) string {
	t.Helper()
	c := auth.Claims{
		UserID:   "00000000-0000-0000-0000-000000000001",
		Role:     role,
		TenantID: tenantID,
		RegisteredClaims: jwtlib.RegisteredClaims{
			ExpiresAt: jwtlib.NewNumericDate(time.Now().Add(1 * time.Hour)),
		},
	}
	tok, err := jwtlib.NewWithClaims(jwtlib.SigningMethodNone, c).
		SignedString(jwtlib.UnsafeAllowNoneSignatureType)
	if err != nil {
		t.Fatalf("mint alg:none jwt: %v", err)
	}
	return tok
}

// ── metric assertions ─────────────────────────────────────────────────────────

// sumCounter gathers from g and returns the summed value of every series of the
// named counter family whose labels are a superset of `labels`.
func sumCounter(t *testing.T, g prometheus.Gatherer, name string, labels map[string]string) float64 {
	t.Helper()
	mfs, err := g.Gather()
	if err != nil {
		t.Fatalf("gather %s: %v", name, err)
	}
	var total float64
	for _, mf := range mfs {
		if mf.GetName() != name {
			continue
		}
		for _, m := range mf.GetMetric() {
			if !labelsMatch(m.GetLabel(), labels) {
				continue
			}
			total += m.GetCounter().GetValue()
		}
	}
	return total
}

func labelsMatch(have []*dto.LabelPair, want map[string]string) bool {
	for k, v := range want {
		found := false
		for _, lp := range have {
			if lp.GetName() == k && lp.GetValue() == v {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

// assertMetric asserts that the summed counter value for name{labels} EXACTLY
// equals want. Use for deterministic counts (e.g. the single 403 in CRM).
func assertMetric(t *testing.T, g prometheus.Gatherer, name string, labels map[string]string, want float64) {
	t.Helper()
	got := sumCounter(t, g, name, labels)
	if got != want {
		t.Fatalf("metric %s%v = %v, want %v", name, labels, got, want)
	}
}

// assertMetricAtLeast asserts the summed counter value for name{labels} is >= want.
// Use where the exact count is not the point (e.g. "the 401 counter went up").
func assertMetricAtLeast(t *testing.T, g prometheus.Gatherer, name string, labels map[string]string, want float64) {
	t.Helper()
	got := sumCounter(t, g, name, labels)
	if got < want {
		t.Fatalf("metric %s%v = %v, want >= %v", name, labels, got, want)
	}
}

// ── span-recording server (DIAN scenario) ─────────────────────────────────────

// spanSink collects the per-request span breakdown from every RequestTap. It is
// concurrency-safe; the e2e requests are serial but the tap fires from the server
// goroutine.
type spanSink struct {
	mu      sync.Mutex
	byRoute map[string][]observability.Span
}

func newSpanSink() *spanSink { return &spanSink{byRoute: map[string][]observability.Span{}} }

func (s *spanSink) record(route string, spans []observability.Span) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := append([]observability.Span(nil), spans...)
	s.byRoute[route] = cp
}

func (s *spanSink) forRoute(route string) []observability.Span {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.byRoute[route]
}

// spanNames extracts the ordered stage names from a span slice (for log output).
func spanNames(spans []observability.Span) []string {
	names := make([]string, len(spans))
	for i, sp := range spans {
		names[i] = sp.Name
	}
	return names
}

// buildSpanServer mirrors helpers.BuildObservableServer but with a tap that also
// records RequestTap.Spans (the helper's tap drops them). It exists so the DIAN
// scenario can assert the engine's own SpanTracker recorded request stages —
// without OTel, per the S37 findings.
func buildSpanServer(t *testing.T, s *schema.APISchema, pool *pgxpool.Pool) (*httptest.Server, *observability.Metrics, *spanSink) {
	t.Helper()
	logging.Log = zerolog.Nop()

	policyJSON, err := json.Marshal(s.RBAC)
	if err != nil {
		t.Fatalf("marshal rbac: %v", err)
	}
	tdb := db.NewTenantDB(pool)
	hr := extensions.NewHookRunner(extensions.NewJSSandbox())
	metrics := observability.NewMetrics()
	rings := observability.NewRings()
	sink := newSpanSink()

	tap := func(rt logging.RequestTap) {
		if rt.TenantID == "" {
			return
		}
		metrics.ObserveRequest(rt.TenantID, rt.Method, rt.Route,
			strconv.Itoa(rt.Status), float64(rt.DurationUS)/1e6)
		rings.Record(rt.TenantID, observability.Sample{
			Start: rt.StartUS, DurUS: int32(rt.DurationUS),
			Route: rings.RouteID(rt.Route), Status: uint16(rt.Status),
		})
		metrics.SetActiveTenants(rings.Count())
		sink.record(rt.Route, rt.Spans)
	}

	r := chi.NewMux()
	r.Use(chimiddleware.RequestID)
	r.Use(tenant.TenantMiddleware)
	r.Use(logging.RequestLogger(nil, nil, tap))
	r.Use(auth.JWTMiddleware(helpers.JWTSecret))
	r.Use(rbac.RBACMiddleware(policyJSON))
	r.Use(chimiddleware.Recoverer)
	r.Mount("/", codegen.BuildRouter(s, tdb, hr, nil))

	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)
	return srv, metrics, sink
}

// buildWebhookServer mirrors the data-plane chain but wires a HookRunner with a
// webhook dispatcher (helpers.BuildObservableServer has none). disp must be an
// INSECURE-transport dispatcher when the receptor is a loopback httptest server —
// the production SSRF/HTTPS guard intentionally blocks loopback (see webhook
// scenario's SSRF sub-test, which exercises the guard directly).
func buildWebhookServer(t *testing.T, s *schema.APISchema, pool *pgxpool.Pool, disp *extensions.WebhookDispatcher) *httptest.Server {
	t.Helper()
	logging.Log = zerolog.Nop()

	policyJSON, err := json.Marshal(s.RBAC)
	if err != nil {
		t.Fatalf("marshal rbac: %v", err)
	}
	tdb := db.NewTenantDB(pool)
	hr := extensions.NewHookRunnerWithDispatcher(extensions.NewJSSandbox(), disp, 0)

	r := chi.NewMux()
	r.Use(chimiddleware.RequestID)
	r.Use(tenant.TenantMiddleware)
	r.Use(auth.JWTMiddleware(helpers.JWTSecret))
	r.Use(rbac.RBACMiddleware(policyJSON))
	r.Use(chimiddleware.Recoverer)
	r.Mount("/", codegen.BuildRouter(s, tdb, hr, nil))

	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)
	return srv
}

// hmacSHA256Hex returns the lowercase hex HMAC-SHA256 of body under secret — the
// same construction the dispatcher uses (it prefixes "sha256=").
func hmacSHA256Hex(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}
