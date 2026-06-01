package logging

import (
	"net/http"
	"os"
	"time"

	chimiddleware "github.com/go-chi/chi/v5/middleware"
	"github.com/rs/zerolog"
	"github.com/miguelangel/appitools/pkg/tenant"
)

// Log is the package-level structured logger. Call Init before using.
var Log zerolog.Logger

// Init configures the global logger for the given environment.
func Init(env string) {
	if env == "development" {
		Log = zerolog.New(
			zerolog.ConsoleWriter{Out: os.Stdout}).
			With().Timestamp().Logger()
	} else {
		Log = zerolog.New(os.Stdout).
			With().Timestamp().Logger()
	}
}

// RequestLogger returns a chi-compatible middleware that logs each request with zerolog.
// record receives duration in microseconds; observe receives duration in microseconds as float64.
// Both are optional (nil-safe).
func RequestLogger(
	record func(tenantID string, durationUs int64),
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
			Log.Info().
				Str("tenant_id", tenantID).
				Str("method", r.Method).
				Str("path", r.URL.Path).
				Int("status", ww.Status()).
				Int64("duration_ms", elapsed.Milliseconds()).
				Str("request_id", chimiddleware.GetReqID(r.Context())).
				Msg("request")
			if record != nil {
				record(tenantID, elapsed.Microseconds())
			}
			if observe != nil {
				observe(tenantID, float64(elapsed.Microseconds()))
			}
		})
	}
}
