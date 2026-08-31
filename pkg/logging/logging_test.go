package logging_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/appximo/appximo/pkg/logging"
	"github.com/appximo/appximo/pkg/observability"
	"github.com/go-chi/chi/v5"
)

func TestRedactWriter_RedactsToken(t *testing.T) {
	var buf bytes.Buffer
	rw := logging.NewRedactWriter(&buf)
	input := []byte(`{"level":"info","token":"supersecret","msg":"ok"}`)
	rw.Write(input) //nolint:errcheck

	out := buf.String()
	if strings.Contains(out, "supersecret") {
		t.Errorf("token value must be redacted; got: %s", out)
	}
	if !strings.Contains(out, "[REDACTED]") {
		t.Errorf("expected [REDACTED] in output; got: %s", out)
	}
}

func TestRedactWriter_RedactsPassword(t *testing.T) {
	var buf bytes.Buffer
	rw := logging.NewRedactWriter(&buf)
	rw.Write([]byte(`{"password":"hunter2","user":"alice"}`)) //nolint:errcheck

	out := buf.String()
	if strings.Contains(out, "hunter2") {
		t.Errorf("password must be redacted; got: %s", out)
	}
}

func TestRedactWriter_RedactsSecret(t *testing.T) {
	var buf bytes.Buffer
	rw := logging.NewRedactWriter(&buf)
	rw.Write([]byte(`{"secret":"my-hmac-key","event":"hook"}`)) //nolint:errcheck

	out := buf.String()
	if strings.Contains(out, "my-hmac-key") {
		t.Errorf("secret must be redacted; got: %s", out)
	}
}

func TestRedactWriter_RedactsAuthorization(t *testing.T) {
	var buf bytes.Buffer
	rw := logging.NewRedactWriter(&buf)
	rw.Write([]byte(`{"authorization":"Bearer eyJhbGciOiJIUzI1NiJ9.x.y"}`)) //nolint:errcheck

	out := buf.String()
	if strings.Contains(out, "eyJhbGciOiJIUzI1NiJ9") {
		t.Errorf("authorization value must be redacted; got: %s", out)
	}
}

func TestRedactWriter_PreservesNonSensitiveFields(t *testing.T) {
	var buf bytes.Buffer
	rw := logging.NewRedactWriter(&buf)
	input := `{"tenant_id":"acme","method":"GET","status":200}`
	rw.Write([]byte(input)) //nolint:errcheck

	out := buf.String()
	if !strings.Contains(out, "acme") {
		t.Errorf("non-sensitive field 'tenant_id' must not be redacted; got: %s", out)
	}
	if !strings.Contains(out, "200") {
		t.Errorf("non-sensitive field 'status' must not be redacted; got: %s", out)
	}
}

func TestRedactWriter_ReportsOriginalLength(t *testing.T) {
	var buf bytes.Buffer
	rw := logging.NewRedactWriter(&buf)
	input := []byte(`{"token":"abc","msg":"test"}`)
	n, err := rw.Write(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n != len(input) {
		t.Errorf("Write must report original len=%d, got %d", len(input), n)
	}
}

// io.Discard-backed benchmarks isolate RedactWriter's own cost. A typical request
// log line carries no sensitive field, so it must take the fast path (no regex,
// no allocation); a line that does carry one still gets redacted.
var benchCleanLine = []byte(`{"level":"info","tenant_id":"10","method":"GET","path":"/api/guides","status":200,"duration_ms":1,"request_id":"abc123","message":"request"}` + "\n")
var benchSensitiveLine = []byte(`{"level":"info","msg":"auth","token":"eyJhbGciOiJIUzI1Niis.payload.sig","status":200}` + "\n")

func BenchmarkRedactWriter_CleanLine(b *testing.B) {
	rw := logging.NewRedactWriter(io.Discard)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rw.Write(benchCleanLine) //nolint:errcheck
	}
}

func BenchmarkRedactWriter_SensitiveLine(b *testing.B) {
	rw := logging.NewRedactWriter(io.Discard)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rw.Write(benchSensitiveLine) //nolint:errcheck
	}
}

// TestRequestLogger_TemplatesDeniedPreRouterPath proves the metric-cardinality fix
// (FASE3-SEC Hallazgo 3): a request rejected BEFORE the router resolves its pattern
// (e.g. a 403 at RBAC for DELETE /api/empleados/{uuid}) must contribute a BOUNDED
// metric label — the {id}-templated path — not the concrete UUID/id. The denied
// request carries chi's RouteContext (the outer mux injects it before the chain) but
// an EMPTY RoutePattern (routeHTTP never ran), exactly like the production path.
func TestRequestLogger_TemplatesDeniedPreRouterPath(t *testing.T) {
	cases := []struct {
		name      string
		path      string
		wantRoute string
	}{
		{"uuid id", "/api/empleados/3fa85f64-5717-4562-b3fc-2c963f66afa6", "/api/empleados/{id}"},
		{"numeric id", "/api/empleados/12345", "/api/empleados/{id}"},
		{"no id segment stays intact", "/api/empleados", "/api/empleados"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var got logging.RequestTap
			mw := logging.RequestLogger(nil, nil, func(rt logging.RequestTap) { got = rt })
			// A middleware that 403s before the router runs: routeHTTP never executes,
			// so RoutePattern is empty (chi still injected an empty RouteContext).
			denied := mw(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusForbidden)
			}))

			req := httptest.NewRequest(http.MethodDelete, tc.path, nil)
			req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, chi.NewRouteContext()))
			denied.ServeHTTP(httptest.NewRecorder(), req)

			if got.Route != tc.wantRoute {
				t.Fatalf("denied %q: Route = %q, want %q (UUID/id must be templated, not raw)", tc.path, got.Route, tc.wantRoute)
			}
		})
	}
}

// TestRequestLogger_MatchedRoutePatternWins confirms the happy path is unchanged: a
// request that reaches the router gets the chi route TEMPLATE (not the raw path, not
// a re-templated guess) — the existing behavior the fix must not disturb.
func TestRequestLogger_MatchedRoutePatternWins(t *testing.T) {
	var got logging.RequestTap
	r := chi.NewRouter()
	r.Use(logging.RequestLogger(nil, nil, func(rt logging.RequestTap) { got = rt }))
	r.Get("/api/things/{id}", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/api/things/3fa85f64-5717-4562-b3fc-2c963f66afa6", nil)
	r.ServeHTTP(httptest.NewRecorder(), req)

	if got.Route != "/api/things/{id}" {
		t.Fatalf("matched route: Route = %q, want %q (chi pattern must win)", got.Route, "/api/things/{id}")
	}
}

// TestRequestLogger_CaptureMakesTheRequestAnError pins ENG-56 (DEPLOY-FLOTA-S1):
// a handler that answers 200 on the wire but records a server-error CAPTURE on
// the span tracker (GraphQL's resolver contract) reaches the tap as a 500 —
// the status everything downstream keys on — with the wire status kept beside
// it; a plain 200 stays a 200 with no wire status divergence.
func TestRequestLogger_CaptureMakesTheRequestAnError(t *testing.T) {
	var got logging.RequestTap
	mw := logging.RequestLogger(nil, nil, func(rt logging.RequestTap) { got = rt })
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("fail") == "1" {
			if tr := observability.SpanTrackerFromCtx(r.Context()); tr != nil {
				c := observability.CaptureError(r.Context(), errors.New("graphql resolver (HTTP 200 on the wire): boom"))
				tr.SetCapture(&c)
			}
		}
		w.WriteHeader(http.StatusOK)
	}))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/graphql?fail=1", nil))
	if got.Status != http.StatusInternalServerError || got.WireStatus != http.StatusOK {
		t.Fatalf("captured 200: Status=%d WireStatus=%d, want 500/200", got.Status, got.WireStatus)
	}
	if got.Capture == nil || got.ErrMsg == "" {
		t.Fatalf("captured 200: the tap must carry the capture and the message; got capture=%v msg=%q", got.Capture != nil, got.ErrMsg)
	}
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/graphql", nil))
	if got.Status != http.StatusOK || got.WireStatus != http.StatusOK || got.Capture != nil {
		t.Fatalf("plain 200: Status=%d WireStatus=%d capture=%v, want 200/200/none", got.Status, got.WireStatus, got.Capture != nil)
	}
}
