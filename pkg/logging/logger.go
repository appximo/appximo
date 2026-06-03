package logging

import (
	"io"
	"net/http"
	"os"
	"regexp"
	"time"

	chimiddleware "github.com/go-chi/chi/v5/middleware"
	"github.com/miguelangel/appitools/pkg/tenant"
	"github.com/rs/zerolog"
)

// Log is the package-level structured logger. Call Init before using.
var Log zerolog.Logger

// sensitiveFieldRe matches JSON string values for sensitive field names.
// Replacements use constant literal [REDACTED] to prevent log exfiltration.
var sensitiveFieldRe = regexp.MustCompile(
	`("(?:token|password|secret|authorization)"\s*:\s*)"[^"]*"`,
)

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
	redacted := sensitiveFieldRe.ReplaceAll(p, []byte(`$1"[REDACTED]"`))
	if _, err := r.w.Write(redacted); err != nil {
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

// RequestLogger returns a chi-compatible middleware that logs each request with zerolog.
// The Authorization header is intentionally NOT logged — only method, path, status,
// duration, tenant_id, and request_id are recorded.
// record receives duration in microseconds and a fromCache flag (true = served from
// the response cache, detected via the X-Cache: HIT header set by the cache middleware).
// observe receives duration in microseconds as float64. Both are optional (nil-safe).
func RequestLogger(
	record func(tenantID string, durationUs int64, fromCache bool),
	observe func(tenantID string, us float64),
) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			ww := chimiddleware.NewWrapResponseWriter(w, r.ProtoMajor)
			next.ServeHTTP(ww, r)
			elapsed := time.Since(start)
			tc := tenant.FromCtx(r.Context())
			tenantID := ""
			if tc != nil {
				tenantID = tc.ID
			}
			// Authorization header is deliberately excluded from the log event.
			Log.Info().
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
		})
	}
}
