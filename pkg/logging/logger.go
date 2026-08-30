package logging

import (
	"bytes"
	"context"
	"io"
	"net"
	"net/http"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/appximo/appximo/pkg/observability"
	"github.com/appximo/appximo/pkg/tenant"
	"github.com/go-chi/chi/v5"
	chimiddleware "github.com/go-chi/chi/v5/middleware"
	"github.com/rs/zerolog"
)

// clientIP extracts the client IP: X-Real-IP, then the first X-Forwarded-For
// entry, then the host part of RemoteAddr.
func clientIP(r *http.Request) string {
	if ip := r.Header.Get("X-Real-IP"); ip != "" {
		return ip
	}
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		if i := strings.IndexByte(xff, ','); i >= 0 {
			return strings.TrimSpace(xff[:i])
		}
		return strings.TrimSpace(xff)
	}
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return host
	}
	return r.RemoteAddr
}

// Log is the package-level structured logger. Call Init before using.
var Log zerolog.Logger

// sensitiveFieldRe matches JSON string values for sensitive field names.
// Replacements use constant literal [REDACTED] to prevent log exfiltration.
var sensitiveFieldRe = regexp.MustCompile(
	`("(?:token|password|secret|authorization)"\s*:\s*)"[^"]*"`,
)

// sensitiveNames are the field names sensitiveFieldRe can match. A log line that
// contains none of them as a substring cannot match the regex, so we skip the
// scan-and-allocate entirely. This is the common case — request logs deliberately
// omit the Authorization header — and the per-line regex showed up on the hot path.
var sensitiveNames = [][]byte{
	[]byte("token"), []byte("password"), []byte("secret"), []byte("authorization"),
}

func mightContainSensitive(p []byte) bool {
	for _, n := range sensitiveNames {
		if bytes.Contains(p, n) {
			return true
		}
	}
	return false
}

// RedactWriter wraps an io.Writer and scrubs sensitive JSON field values before
// they reach the underlying writer. Applied to every structured log line.
type RedactWriter struct {
	w io.Writer
}

// NewRedactWriter wraps w with sensitive-field redaction.
func NewRedactWriter(w io.Writer) RedactWriter {
	return RedactWriter{w: w}
}

func (r RedactWriter) Write(p []byte) (int, error) {
	out := p
	// Only pay for the regex (a full scan plus an allocation) when a sensitive
	// field name is actually present. ReplaceAll always allocates, even on no
	// match, so gating it removes that cost from every clean log line.
	if mightContainSensitive(p) {
		out = sensitiveFieldRe.ReplaceAll(p, []byte(`$1"[REDACTED]"`))
	}
	if _, err := r.w.Write(out); err != nil {
		return 0, err
	}
	return len(p), nil // report original length so zerolog does not treat it as a short write
}

// defaultWriter is where Init sends structured log output. os.Stdout is the
// historical (12-factor) default and stays untouched for `serve` and library
// consumers. `appximo up --json` redirects it to stderr BEFORE booting the
// engine, because its stdout must carry exactly one JSON object (the C1 rule:
// machine commands keep byte-clean stdout).
var defaultWriter io.Writer = os.Stdout

// SetDefaultWriter redirects where subsequent Init calls send log output.
// Call it before the engine boots (appximo.New re-runs Init).
func SetDefaultWriter(w io.Writer) { defaultWriter = w }

// Init configures the global logger for the given environment.
// All output passes through RedactWriter to strip sensitive field values.
func Init(env string) {
	base := RedactWriter{w: defaultWriter}
	if env == "development" {
		Log = zerolog.New(
			zerolog.ConsoleWriter{Out: base}).
			With().Timestamp().Logger()
	} else {
		Log = zerolog.New(base).
			With().Timestamp().Logger()
	}
	// zerolog.Ctx(ctx) outside a request (no logger in the context) must fall
	// back to THIS logger, never to a silent one: the RBAC deny line, the
	// route error line and the webhook lines all go through the context
	// logger so they carry trace_id when there is one — and still print when
	// there is not (OBSERVABILIDAD-ERRORES-S1).
	zerolog.DefaultContextLogger = &Log
}

// RequestTap carries the per-request facts a downstream consumer (Prometheus
// metrics, the per-tenant ring buffer) needs, so RequestLogger stays the single
// measurement point. Route is the chi route pattern (e.g. "/api/{entity}"),
// preferred over the raw Path for bounded metric/label cardinality.
type RequestTap struct {
	TenantID   string
	Method     string
	Path       string
	Route      string
	Status     int
	StartUS    int64 // request start, unix microseconds
	DurationUS int64
	FromCache  bool
	TraceID    string                      // 16-hex request trace id (also in X-Trace-ID)
	Spans      []observability.Span        // per-stage breakdown from the SpanTracker
	ErrMsg     string                      // error message for an errored request ("" otherwise)
	Capture    *observability.ErrorCapture // symbolized stack for a 500 (nil otherwise)
	IP         string                      // client IP (X-Real-IP / X-Forwarded-For / RemoteAddr)
	UserAgent  string                      // raw User-Agent header
	Headers    map[string]string           // filtered request headers (persisted traces only)
	FullURL    string                      // scheme://host/path?query (persisted traces only)
	// OBSERVABILIDAD-ERRORES-S1: who (from the JWT, populated for EVERY trace,
	// not only captured 500s), the failed statement the driver noted, and the
	// redacted request body when APPXIMO_TRACE_BODY is on.
	UserID string
	Role   string
	SQL    string
	Body   string
}

// ClaimsExtractor reads (user_id, role) off a request context. Set by the app
// at boot (auth.ClaimsFromCtx) — logging cannot import auth without a cycle.
var ClaimsExtractor func(ctx context.Context) (userID, role string)

// TraceBodyEnvVar opts request BODIES into persisted error traces. Off by
// default: a body can carry passwords, tokens or personal data — the trade-off
// is written in docs/BACKEND_SPEC_LLM.md §3.9. When on, at most TraceBodyMax
// bytes are kept and sensitive JSON fields are redacted before persistence.
const (
	TraceBodyEnvVar = "APPXIMO_TRACE_BODY"
	TraceBodyMax    = 4 << 10
)

// traceBodyOn is read once at package init (an env knob, not a hot-path read).
var traceBodyOn = func() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(TraceBodyEnvVar))) {
	case "1", "on", "true", "yes":
		return true
	}
	return false
}()

// RedactBody replaces the values of sensitive JSON keys and caps the length.
func RedactBody(b []byte) string {
	if len(b) > TraceBodyMax {
		b = b[:TraceBodyMax]
	}
	return string(sensitiveFieldRe.ReplaceAll(b, []byte(`${1}"[Filtered]"`)))
}

// bodyTee captures the first TraceBodyMax bytes of a body as it is read.
type bodyTee struct {
	io.ReadCloser
	buf bytes.Buffer
}

func (t *bodyTee) Read(p []byte) (int, error) {
	n, err := t.ReadCloser.Read(p)
	if n > 0 && t.buf.Len() < TraceBodyMax {
		room := TraceBodyMax - t.buf.Len()
		if n < room {
			room = n
		}
		t.buf.Write(p[:room])
	}
	return n, err
}

// FromCtx returns the request-scoped logger (carries trace_id). Falls back to
// the global logger outside a request.
func FromCtx(ctx context.Context) *zerolog.Logger {
	if ctx == nil { // a test-built ctx; zerolog.Ctx(nil) would dereference it
		return &Log
	}
	return zerolog.Ctx(ctx)
}

// requestFullURL reconstructs the request URL: scheme (from X-Forwarded-Proto,
// else http) + host + path + query.
func requestFullURL(r *http.Request) string {
	scheme := "http"
	if p := r.Header.Get("X-Forwarded-Proto"); p != "" {
		scheme = p
	}
	return scheme + "://" + r.Host + r.URL.RequestURI()
}

// RequestLogger returns a chi-compatible middleware that logs each request with zerolog.
// The Authorization header is intentionally NOT logged — only method, path, status,
// duration, tenant_id, and request_id are recorded.
// record receives duration in microseconds and a fromCache flag (true = served from
// the response cache, detected via the X-Cache: HIT header set by the cache middleware).
// observe receives duration in microseconds as float64. tap receives a fully-populated
// RequestTap. All three callbacks are optional (nil-safe).
func RequestLogger(
	record func(tenantID string, durationUs int64, fromCache bool),
	observe func(tenantID string, us float64),
	tap func(RequestTap),
) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()

			// In-process tracing: a trace id + span tracker ride the context so
			// every downstream middleware/handler can Mark() its stage and emit
			// logs already tagged with trace_id. The id is also echoed to the
			// client via X-Trace-ID for correlation.
			traceID := observability.NewTraceID()
			ctx := observability.WithTraceID(r.Context(), traceID)
			ctx = observability.WithSpanTracker(ctx)
			ctx = Log.With().Str("trace_id", traceID).Logger().WithContext(ctx)
			r = r.WithContext(ctx)

			ww := chimiddleware.NewWrapResponseWriter(w, r.ProtoMajor)
			ww.Header().Set("X-Trace-ID", traceID)
			var tee *bodyTee
			if traceBodyOn && r.Body != nil && r.Body != http.NoBody {
				tee = &bodyTee{ReadCloser: r.Body}
				r.Body = tee
			}
			next.ServeHTTP(ww, r)
			elapsed := time.Since(start)

			// Close the trace: the tail since the last Mark becomes a "done" span.
			var spans []observability.Span
			var errMsg string
			var capture *observability.ErrorCapture
			if t := observability.SpanTrackerFromCtx(r.Context()); t != nil {
				spans = t.Finish()
				if t.HasError {
					errMsg = t.ErrMsg
					capture = t.Capture
				}
			}

			tc := tenant.FromCtx(r.Context())
			tenantID := ""
			if tc != nil {
				tenantID = tc.ID
			}
			// The LEVEL carries the verdict (OBSERVABILIDAD-ERRORES-S1, Parte C):
			// a 5xx is an error, and any alert an operator mounts on
			// level=error must see it. 4xx stay info — they are the caller's
			// verdict, and probes would drown a warn stream. Before this the
			// status travelled only in a field while the level said "info".
			evt := Log.Info()
			if ww.Status() >= 500 {
				evt = Log.Error()
			}
			userID, role := "", ""
			if t := observability.SpanTrackerFromCtx(r.Context()); t != nil && t.UserID != "" {
				userID, role = t.UserID, t.Role // set by the JWT middleware on the shared tracker
			} else if ClaimsExtractor != nil {
				userID, role = ClaimsExtractor(r.Context())
			}
			// Authorization header is deliberately excluded from the log event.
			evt.
				Str("user_id", userID).
				Str("role", role).
				Str("trace_id", traceID).
				Str("tenant_id", tenantID).
				Str("method", r.Method).
				Str("path", r.URL.Path).
				Int("status", ww.Status()).
				Int64("duration_ms", elapsed.Milliseconds()).
				Str("request_id", chimiddleware.GetReqID(r.Context())).
				Msg("request")
			// X-Cache: HIT is written by the response cache middleware on cache hits.
			fromCache := ww.Header().Get("X-Cache") == "HIT"
			if record != nil {
				record(tenantID, elapsed.Microseconds(), fromCache)
			}
			if observe != nil {
				observe(tenantID, float64(elapsed.Microseconds()))
			}
			if tap != nil {
				// RoutePattern is populated only after routing completes; reading it
				// post-ServeHTTP yields the matched template (e.g. /api/empleados/{id}).
				// A request REJECTED by a middleware BEFORE the router runs (e.g. a 403
				// at RBAC, a 429 at the limiter) never reaches routeHTTP, so the pattern
				// is empty — without normalization the raw path with its CONCRETE id
				// (a UUID) would become the metric label, letting by-id probing explode
				// /metrics cardinality (FASE3-SEC Hallazgo 3). When unmatched, template
				// id-like segments to {id} so the label stays bounded to real patterns.
				route := r.URL.Path
				matched := false
				if rc := chi.RouteContext(r.Context()); rc != nil {
					if p := rc.RoutePattern(); p != "" {
						route = p
						matched = true
					}
				}
				if !matched {
					route = templatePath(route)
				}
				// Headers + full URL are captured ONLY for traces that will be
				// persisted (errors or slow), using the exact persistence predicate.
				// The 200-OK hot path never iterates r.Header.
				var reqHeaders map[string]string
				var fullURL string
				if observability.ShouldPersistTrace(observability.Sample{
					DurUS:  int32(elapsed.Microseconds()),
					Status: uint16(ww.Status()),
				}) {
					reqHeaders = observability.FilterHeaders(r.Header)
					fullURL = requestFullURL(r)
				}
				var body, failedSQL string
				if t := observability.SpanTrackerFromCtx(r.Context()); t != nil && ww.Status() >= 500 {
					failedSQL = t.LastSQL
				}
				if tee != nil && reqHeaders != nil { // persisted traces only
					body = RedactBody(tee.buf.Bytes())
				}
				tap(RequestTap{
					UserID:     userID,
					Role:       role,
					SQL:        failedSQL,
					Body:       body,
					TenantID:   tenantID,
					Method:     r.Method,
					Path:       r.URL.Path,
					Route:      route,
					Status:     ww.Status(),
					StartUS:    start.UnixMicro(),
					DurationUS: elapsed.Microseconds(),
					FromCache:  fromCache,
					TraceID:    traceID,
					Spans:      spans,
					ErrMsg:     errMsg,
					Capture:    capture,
					IP:         clientIP(r),
					UserAgent:  r.Header.Get("User-Agent"),
					Headers:    reqHeaders,
					FullURL:    fullURL,
				})
			}
		})
	}
}

// idSegmentRe matches a path segment that is a concrete record id — a UUID or a
// run of digits — so it can be collapsed to the {id} placeholder chi itself would
// have produced had the request reached the router.
var idSegmentRe = regexp.MustCompile(`^([0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}|[0-9]+)$`)

// templatePath normalizes the id-bearing segments of a raw URL path to {id}, so a
// request rejected before the router (whose RoutePattern is empty) contributes a
// BOUNDED metric label instead of one carrying the concrete UUID/id. It is only
// reached on the non-routed path (denied / 404), never the 200 hot path. The
// resource segment is left intact (a finite, schema-bounded set); only the
// per-record id varies, and that is exactly what would otherwise be unbounded.
func templatePath(p string) string {
	if !strings.ContainsRune(p, '/') {
		return p
	}
	segs := strings.Split(p, "/")
	changed := false
	for i, s := range segs {
		if idSegmentRe.MatchString(s) {
			segs[i] = "{id}"
			changed = true
		}
	}
	if !changed {
		return p
	}
	return strings.Join(segs, "/")
}
