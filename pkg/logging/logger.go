package logging

import (
	"bytes"
	"io"
	"net"
	"net/http"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	chimiddleware "github.com/go-chi/chi/v5/middleware"
	"github.com/miguelangel/appitools/pkg/observability"
	"github.com/miguelangel/appitools/pkg/tenant"
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

// Init configures the global logger for the given environment.
// All output passes through RedactWriter to strip sensitive field values.
func Init(env string) {
	base := RedactWriter{w: os.Stdout}
	if env == "development" {
		Log = zerolog.New(
			zerolog.ConsoleWriter{Out: base}).
			With().Timestamp().Logger()
	} else {
		Log = zerolog.New(base).
			With().Timestamp().Logger()
	}
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
	TraceID    string               // 16-hex request trace id (also in X-Trace-ID)
	Spans      []observability.Span // per-stage breakdown from the SpanTracker
	ErrMsg     string                      // error message for an errored request ("" otherwise)
	Capture    *observability.ErrorCapture // symbolized stack for a 500 (nil otherwise)
	IP         string                      // client IP (X-Real-IP / X-Forwarded-For / RemoteAddr)
	UserAgent  string                      // raw User-Agent header
	Headers    map[string]string           // filtered request headers (persisted traces only)
	FullURL    string                      // scheme://host/path?query (persisted traces only)
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
			// Authorization header is deliberately excluded from the log event.
			Log.Info().
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
				// post-ServeHTTP yields the matched template, falling back to the path.
				route := r.URL.Path
				if rc := chi.RouteContext(r.Context()); rc != nil {
					if p := rc.RoutePattern(); p != "" {
						route = p
					}
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
				tap(RequestTap{
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
