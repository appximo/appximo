package integration_test

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	chi "github.com/go-chi/chi/v5"
	"github.com/rs/zerolog"

	"github.com/appximo/appximo/pkg/auth"
	"github.com/appximo/appximo/pkg/cache"
	"github.com/appximo/appximo/pkg/codegen"
	"github.com/appximo/appximo/pkg/db"
	"github.com/appximo/appximo/pkg/events"
	"github.com/appximo/appximo/pkg/extensions"
	gqlhandler "github.com/appximo/appximo/pkg/graphql"
	"github.com/appximo/appximo/pkg/logging"
	"github.com/appximo/appximo/pkg/observability"
	rbacpkg "github.com/appximo/appximo/pkg/rbac"
	"github.com/appximo/appximo/pkg/schema"
	"github.com/appximo/appximo/pkg/tenant"
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
			ErrMsg: tp.ErrMsg, ErrorCapture: tp.Capture,
		}
		rings.Record(tp.TenantID, sample)
		if store != nil && observability.ShouldPersistTrace(sample) {
			tv := observability.TraceView{
				TraceID: tp.TraceID, TS: tp.StartUS, Route: tp.Route,
				TotalUS: sample.DurUS, Status: uint16(tp.Status),
				Spans:  append([]observability.Span(nil), tp.Spans...),
				ErrMsg: tp.ErrMsg,
			}
			if tp.Capture != nil {
				tv.Stack = tp.Capture.Stack
				if tv.ErrMsg == "" {
					tv.ErrMsg = tp.Capture.ErrMsg
				}
			}
			_ = store.SaveSlowTrace(tp.TenantID, tv)
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
	// GraphQL on the SAME traced chain (ENG-56): its resolvers answer 200 +
	// errors[] by contract, and a server-class resolver failure must still
	// reach the trace as a 500 through the logger's logical status.
	var rbacPolicy rbacpkg.Policy
	_ = json.Unmarshal(policyJSON, &rbacPolicy)
	inner.Handle("/graphql", gqlhandler.BuildHandler(s, pool, hr, &rbacPolicy, events.NewHub(0), false))
	inner.Mount("/", codegen.BuildRouter(s, pool, hr, nil, nil))

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
		Schema: "https://appximo.com/schema/v1", Version: "1", Name: "tracing",
		Resources: map[string]schema.ResourceSchema{
			"guides": {
				Fields: map[string]schema.FieldDef{
					"code": {Type: "string"}, "status": {Type: "string"},
				},
				Hooks: map[string]schema.HookConfig{
					// reject:true → clean 422 (error-persist test). boom:true → infinite
					// loop: the 80ms watchdog interrupts it, which is the one JS outcome
					// that surfaces as a hook EXECUTION error → a genuine 500 with a
					// captured stack (a thrown JS exception maps to Proceed:false → 422
					// by design, and an unknown column is a 422 too since FIX 7 — the
					// 500-persistence test below needs a real engine-side failure).
					"before_create": {Type: "js", Script: `if (data.reject) { result.proceed = false; result.error = "rejected by test"; } if (data.boom || data.status == "boom") { while (true) {} }`},
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

	// Warmup (populates cache + pool). APP-PODER-S1: a MISS publishes the
	// engine's stage durations as Server-Timing (the query stage included), so
	// a client can show "the query took N ms" honestly.
	warm := get("/api/guides?per_page=5")
	warm.Body.Close()
	if st := warm.Header.Get("Server-Timing"); !strings.Contains(st, "query;dur=") || !strings.Contains(st, "app;dur=") {
		t.Fatalf("a cache MISS must carry Server-Timing with query and app durations, got %q", st)
	}

	// FAST request — cache HIT, well under 50ms.
	respFast := get("/api/guides?per_page=5")
	tidFast := respFast.Header.Get("X-Trace-ID")
	respFast.Body.Close()
	// Whether this second read is a HIT depends on the fixture's cache policy;
	// whichever it is, Server-Timing tells the truth about it.
	if st := respFast.Header.Get("Server-Timing"); respFast.Header.Get("X-Cache") == "HIT" {
		if st != `cache;desc="hit"` {
			t.Fatalf("a cache HIT must say Server-Timing: cache;desc=\"hit\" (no query ran), got %q", st)
		}
	} else if !strings.Contains(st, "query;dur=") {
		t.Fatalf("a served read must carry its query duration, got %q", st)
	}

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

	// 8a. An unknown column on a write is a CLIENT error since FIX 7: 422 with
	// rule unknown_field (SQLSTATE 42703 classified in WriteDBError), never a
	// masked 500 — and therefore persisted without a stack.
	bodyUnk, _ := json.Marshal(map[string]any{"code": "EUNK", "status": "pending", "no_such_column": "x"})
	reqUnk, _ := http.NewRequest(http.MethodPost, srv.URL+"/api/guides", bytes.NewReader(bodyUnk))
	reqUnk.Header.Set("Authorization", "Bearer "+token)
	reqUnk.Header.Set("Content-Type", "application/json")
	respUnk, err := srv.Client().Do(reqUnk)
	if err != nil {
		t.Fatalf("POST unknown column: %v", err)
	}
	tidUnk := respUnk.Header.Get("X-Trace-ID")
	unkBody, _ := io.ReadAll(respUnk.Body)
	respUnk.Body.Close()
	if respUnk.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("unknown column must be 422 unknown_field (FIX 7), got %d: %s", respUnk.StatusCode, unkBody)
	}
	if !bytes.Contains(unkBody, []byte("unknown_field")) {
		t.Errorf("422 body must carry rule unknown_field, got: %s", unkBody)
	}
	if !persisted(tidUnk) {
		t.Errorf("422 unknown-field trace %s should be persisted (status >= 400)", tidUnk)
	}

	// 8b. A real 500 (the before_create hook THROWS) must be persisted WITH a
	// stack trace.
	body500, _ := json.Marshal(map[string]any{"code": "E500", "status": "pending", "boom": true})
	req500, _ := http.NewRequest(http.MethodPost, srv.URL+"/api/guides", bytes.NewReader(body500))
	req500.Header.Set("Authorization", "Bearer "+token)
	req500.Header.Set("Content-Type", "application/json")
	resp500, err := srv.Client().Do(req500)
	if err != nil {
		t.Fatalf("POST 500: %v", err)
	}
	tid500 := resp500.Header.Get("X-Trace-ID")
	st500 := resp500.StatusCode
	resp500.Body.Close()
	if st500 != http.StatusInternalServerError {
		t.Fatalf("expected 500 (hook throws), got %d", st500)
	}

	var tv500 observability.TraceView
	if !eventually(2*time.Second, func() bool {
		s, _ := store.SlowTraces(tenantID, 24)
		for _, tv := range s {
			if tv.TraceID == tid500 {
				tv500 = tv
				return true
			}
		}
		return false
	}) {
		t.Fatalf("500 trace %s was not persisted", tid500)
	}
	if tv500.Status != 500 {
		t.Errorf("persisted 500 status = %d", tv500.Status)
	}
	if len(tv500.Stack) == 0 {
		t.Errorf("500 trace must carry a stack; got none")
	}
	if tv500.ErrMsg == "" {
		t.Errorf("500 trace must carry an error message")
	}

	// 8c. The SAME failure through GraphQL (ENG-56): the wire says 200 +
	// errors[] (the contract), but the trace must persist as a 500 with the
	// stack, the message and a fingerprint — the panel sees it like REST's.
	gqlBody, _ := json.Marshal(map[string]any{"query": `mutation { createGuide(input: {code: "G500", status: "boom"}) { id } }`})
	reqG, _ := http.NewRequest(http.MethodPost, srv.URL+"/graphql", bytes.NewReader(gqlBody))
	reqG.Header.Set("Authorization", "Bearer "+token)
	reqG.Header.Set("Content-Type", "application/json")
	respG, err := srv.Client().Do(reqG)
	if err != nil {
		t.Fatalf("POST /graphql: %v", err)
	}
	tidG := respG.Header.Get("X-Trace-ID")
	var gqlRes map[string]any
	_ = json.NewDecoder(respG.Body).Decode(&gqlRes)
	respG.Body.Close()
	if respG.StatusCode != http.StatusOK {
		t.Fatalf("GraphQL answers 200 by contract, got %d", respG.StatusCode)
	}
	if errs, _ := gqlRes["errors"].([]any); len(errs) == 0 {
		t.Fatalf("expected errors[] from the hook that never returns, got %v", gqlRes)
	}
	var tvG observability.TraceView
	foundG := false
	if sg, err := store.SlowTraces(tenantID, 24); err == nil {
		for _, tv := range sg {
			if tv.TraceID == tidG {
				tvG, foundG = tv, true
			}
		}
	}
	if !foundG {
		t.Fatalf("GraphQL resolver 500 trace %s was not persisted (ENG-56)", tidG)
	}
	if tvG.Status != 500 {
		t.Errorf("GraphQL resolver failure persisted with status %d, want 500 (logical status; the wire was 200)", tvG.Status)
	}
	if len(tvG.Stack) == 0 {
		t.Errorf("GraphQL resolver 500 must carry a stack; got none")
	}
	if !strings.Contains(tvG.ErrMsg, "graphql resolver (HTTP 200 on the wire)") {
		t.Errorf("GraphQL resolver 500 message must say the wire was 200; got %q", tvG.ErrMsg)
	}
	if !strings.Contains(logBuf.String(), `"wire_status":200`) {
		t.Errorf("the request line must carry wire_status=200 beside status=500; log:\n%s", func() string {
			l := logBuf.String()
			if len(l) > 1200 {
				return l[len(l)-1200:]
			}
			return l
		}())
	}

	// The 401 and 422 traces must have NO stack (client errors, not bugs).
	s2, _ := store.SlowTraces(tenantID, 24)
	for _, tv := range s2 {
		if (tv.TraceID == tid401 || tv.TraceID == tid422 || tv.TraceID == tidUnk) && len(tv.Stack) != 0 {
			t.Errorf("4xx trace %s must not have a stack, got %d frames", tv.TraceID, len(tv.Stack))
		}
	}
}
