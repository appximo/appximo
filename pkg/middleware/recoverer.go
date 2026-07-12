package middleware

import (
	"net/http"
	"runtime/debug"
)

// recover500Body is the masked response body written when a downstream handler
// panics — the same shape every other engine error uses (a Postgres error or
// stack trace never reaches the client).
var recover500Body = []byte(`{"error":"internal error"}`)

// Recoverer returns middleware that recovers a panic in any downstream handler
// or middleware and turns it into a clean JSON 500, so ONE request's panic never
// takes the process down. It is the request-chain half of the Phase-0 safety
// model (LIBRARY-HARDEN-S1) — the goroutine half is Ctx.SafeGo, because Go's
// recover() only reaches its OWN goroutine (a bare `go func(){panic()}()` in a
// handler crashes every tenant; this middleware cannot save that — SafeGo does).
//
// onPanic (may be nil) is called with the request, the recovered value and the
// captured stack BEFORE the response is written — wire it to a metric counter
// and a structured log. It mirrors chi's Recoverer (no ResponseWriter wrapping,
// so the steady-state cost is a single deferred recover — identical to before)
// and re-panics on http.ErrAbortHandler so net/http can abort the connection.
func Recoverer(onPanic func(r *http.Request, recovered any, stack []byte)) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				rvr := recover()
				if rvr == nil {
					return
				}
				// http.ErrAbortHandler is the sentinel a handler panics with to
				// abort the connection deliberately; let net/http handle it.
				if rvr == http.ErrAbortHandler { //nolint:errorlint // sentinel comparison, matches chi
					panic(rvr)
				}
				if onPanic != nil {
					onPanic(r, rvr, debug.Stack())
				}
				// An upgraded/hijacked connection (websocket) owns the socket; do
				// not attempt an HTTP response over it.
				if r.Header.Get("Connection") == "Upgrade" {
					return
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = w.Write(recover500Body)
			}()
			next.ServeHTTP(w, r)
		})
	}
}
