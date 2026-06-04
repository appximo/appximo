package integration_test

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	chi "github.com/go-chi/chi/v5"
	"github.com/rs/zerolog"

	"github.com/miguelangel/appitools/pkg/auth"
	"github.com/miguelangel/appitools/pkg/cache"
	"github.com/miguelangel/appitools/pkg/codegen"
	"github.com/miguelangel/appitools/pkg/db"
	"github.com/miguelangel/appitools/pkg/extensions"
	"github.com/miguelangel/appitools/pkg/logging"
	"github.com/miguelangel/appitools/pkg/observability"
	rbacpkg "github.com/miguelangel/appitools/pkg/rbac"
	"github.com/miguelangel/appitools/pkg/schema"
	"github.com/miguelangel/appitools/pkg/tenant"
)

// safeBuf is a mutex-guarded buffer: the server goroutine writes log lines after
// the client's Do() returns, so reads from the test goroutine must synchronize.
type safeBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *safeBuf) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *safeBuf) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// eventually polls cond until it returns true or the timeout elapses.
func eventually(timeout time.Duration, cond func() bool) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return cond()
}

// buildTracedStack mirrors the cmd_serve middleware chain relevant to tracing
// (RequestLogger → cache → JWT → RBAC → router) plus a tap that records to the
// ring and persists slow traces — but SYNCHRONOUSLY, for deterministic asserts.
// A test sleep middleware forces a request over the 50ms slow threshold.
func buildTracedStack(pool *db.TenantDB, rings *observability.Rings, store *observability.ObsStore, s *schema.APISchema) http.Handler {
	policyJSON, _ := json.Marshal(s.RBAC)
	hr := extensions.NewHookRunner(extensions.NewJSSandbox())
	rc := cache.New(5 * time.Second) // no role gate → caches every role

	tap := func(tp logging.RequestTap) {
		if tp.TenantID == "" {
			return
		}
		var traceID [8]byte
		if b, err := hex.DecodeString(tp.TraceID); err == nil && len(b) >= 8 {
			copy(traceID[:], b[:8])
		}
		var spans [8]observability.Span
		n := len(tp.Spans)
		if n > 8 {
			n = 8
		}
		copy(spans[:], tp.Spans[:n])
		sample := observability.Sample{
			Start: tp.StartUS, DurUS: int32(tp.DurationUS), Route: rings.RouteID(tp.Route),
			Status: uint16(tp.Status), TraceID: traceID, Spans: spans, NSpans: uint8(n),
		}
		rings.Record(tp.TenantID, sample)
		if store != nil && observability.ShouldPersistTrace(sample) {
			_ = store.SaveSlowTrace(tp.TenantID, observability.TraceView{
				TraceID: tp.TraceID, TS: tp.StartUS, Route: tp.Route,
				TotalUS: sample.DurUS, Status: uint16(tp.Status),
				Spans: append([]observability.Span(nil), tp.Spans...),
			})
		}
	}

	inner := chi.NewMux()
	inner.Use(tenant.TenantMiddleware) // resolves tenant from Host (set by the wrapper below)
	inner.Use(logging.RequestLogger(nil, nil, tap))
	inner.Use(func(next http.Handler) http.Handler { // test-only slow injector
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Query().Get("slow") == "1" {
				time.Sleep(70 * time.Millisecond)
			}
			next.ServeHTTP(w, r)
		})
	})
	inner.Use(rc.Middleware)
	inner.Use(auth.JWTMiddleware(jwtSecret))
	inner.Use(rbacpkg.RBACMiddleware(policyJSON))
	inner.Mount("/", codegen.BuildRouter(s, pool, hr))

	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		req.Host = tenantID + ".localhost"
		inner.ServeHTTP(w, req)
	})
}

func TestTracing_EndToEnd(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: skipping in -short mode")
	}
	ctx := context.Background()
	pool, clean := startPG(t)
	defer clean()

	if _, err := pool.Exec(ctx, `
CREATE SCHEMA IF NOT EXISTS tenant_acmetest;
CREATE TABLE tenant_acmetest.guides (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(), code text, status text);
INSERT INTO tenant_acmetest.guides (code, status) VALUES ('A','pending'), ('B','delivered');`); err != nil {
		t.Fatalf("provision: %v", err)
	}

	// Capture structured logs into a buffer to assert trace_id propagation.
	var logBuf safeBuf
	saved := logging.Log
	logging.Log = zerolog.New(&logBuf).With().Timestamp().Logger()
	defer func() { logging.Log = saved }()

	s := &schema.APISchema{
		Schema: "https://appitools.dev/schema/v1", Version: "1", Name: "tracing",
		Resources: map[string]schema.ResourceSchema{
			"guides": {
				Fields: map[string]schema.FieldDef{
					"code": {Type: "string"}, "status": {Type: "string"},
				},
				Hooks: map[string]schema.HookConfig{
					// Rejects when the body carries reject:true → 422 (for the error-persist test).
					"before_create": {Type: "js", Script: `if (data.reject) { result.proceed = false; result.error = "rejected by test"; }`},
				},
			},
		},
		RBAC: schema.RBACPolicy{Roles: map[string]schema.RolePolicy{
			"super_admin": {Resources: json.RawMessage(`"*"`), Actions: []string{"*"}},
		}},
	}

	rings := observability.NewRings()
	store, err := observability.OpenStore(filepath.Join(t.TempDir(), "obs.db"))
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	defer store.Close()

	srv := httptest.NewServer(buildTracedStack(db.NewTenantDB(pool), rings, store, s))
	defer srv.Close()
	token := genToken("super_admin", superID)

	get := func(path string) *http.Response {
		req, _ := http.NewRequest(http.MethodGet, srv.URL+path, nil)
		req.Header.Set("Authorization", "Bearer "+token)
		resp, err := srv.Client().Do(req)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		return resp
	}

	// Warmup (populates cache + pool); not asserted.
	get("/api/guides?per_page=5").Body.Close()

	// FAST request — cache HIT, well under 50ms.
	respFast := get("/api/guides?per_page=5")
	tidFast := respFast.Header.Get("X-Trace-ID")
	respFast.Body.Close()

	// 1. X-Trace-ID header is a 16-hex id.
	if len(tidFast) != 16 {
		t.Fatalf("X-Trace-ID = %q, want 16 hex chars", tidFast)
	}
	if _, err := hex.DecodeString(tidFast); err != nil {
		t.Fatalf("X-Trace-ID %q not hex: %v", tidFast, err)
	}

	// 2. trace_id appears in this request's logs (the tap/log run in the server
	// goroutine after Do() returns, so poll).
	if !eventually(time.Second, func() bool { return strings.Contains(logBuf.String(), tidFast) }) {
		t.Errorf("trace_id %s not found in logs:\n%s", tidFast, logBuf.String())
	}

	// 3. Spans are recorded in the ring buffer.
	if !eventually(time.Second, func() bool {
		tr := rings.RecentTraces(tenantID, 10)
		return len(tr) > 0 && len(tr[0].Spans) > 0
	}) {
		t.Fatalf("expected spans in ring, got %+v", rings.RecentTraces(tenantID, 10))
	}

	// SLOW request (>50ms via the test injector).
	respSlow := get("/api/guides?slow=1&per_page=5")
	tidSlow := respSlow.Header.Get("X-Trace-ID")
	respSlow.Body.Close()

	// 4. The slow request is persisted (poll: SaveSlowTrace runs in the tap).
	if !eventually(2*time.Second, func() bool {
		s, _ := store.SlowTraces(tenantID, 24)
		for _, tv := range s {
			if tv.TraceID == tidSlow {
				return true
			}
		}
		return false
	}) {
		t.Fatalf("slow request %s was not persisted to slow_traces", tidSlow)
	}

	slow, err := store.SlowTraces(tenantID, 24)
	if err != nil {
		t.Fatalf("SlowTraces: %v", err)
	}
	bySlow := map[string]observability.TraceView{}
	for _, tv := range slow {
		bySlow[tv.TraceID] = tv
	}
	// 4. The slow request was persisted with its spans.
	tv, ok := bySlow[tidSlow]
	if !ok {
		t.Fatalf("slow request %s not persisted; slow_traces=%d", tidSlow, len(slow))
	}
	if len(tv.Spans) == 0 {
		t.Errorf("persisted slow trace has no spans")
	}
	if tv.TotalUS <= observability.SlowTraceThresholdUS {
		t.Errorf("slow trace total_us=%d, expected >%d", tv.TotalUS, observability.SlowTraceThresholdUS)
	}
	// 5. The fast request was NOT persisted.
	if _, bad := bySlow[tidFast]; bad {
		t.Errorf("fast request %s must not be in slow_traces", tidFast)
	}

	persisted := func(tid string) bool {
		return eventually(2*time.Second, func() bool {
			s, _ := store.SlowTraces(tenantID, 24)
			for _, tv := range s {
				if tv.TraceID == tid {
					return true
				}
			}
			return false
		})
	}

	// 6. A 401 (bad token) is persisted even though it is fast (PersistErrors).
	req401, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/guides?per_page=5", nil)
	req401.Header.Set("Authorization", "Bearer not.a.valid.token")
	resp401, err := srv.Client().Do(req401)
	if err != nil {
		t.Fatalf("GET 401: %v", err)
	}
	tid401 := resp401.Header.Get("X-Trace-ID")
	st401 := resp401.StatusCode
	resp401.Body.Close()
	if st401 != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", st401)
	}
	if !persisted(tid401) {
		t.Errorf("401 trace %s should be persisted (PersistErrors)", tid401)
	}

	// 7. A 422 (before_create hook reject) is persisted even though it is fast.
	body, _ := json.Marshal(map[string]any{"code": "X", "reject": true})
	req422, _ := http.NewRequest(http.MethodPost, srv.URL+"/api/guides", bytes.NewReader(body))
	req422.Header.Set("Authorization", "Bearer "+token)
	req422.Header.Set("Content-Type", "application/json")
	resp422, err := srv.Client().Do(req422)
	if err != nil {
		t.Fatalf("POST 422: %v", err)
	}
	tid422 := resp422.Header.Get("X-Trace-ID")
	st422 := resp422.StatusCode
	resp422.Body.Close()
	if st422 != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422, got %d", st422)
	}
	if !persisted(tid422) {
		t.Errorf("422 trace %s should be persisted (PersistErrors)", tid422)
	}
}
