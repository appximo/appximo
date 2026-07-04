package middleware

import (
	"net/http"

	chimiddleware "github.com/go-chi/chi/v5/middleware"
)

// SelectiveCompress is chi's Compress middleware with a per-request skip — the
// same shape as the response cache's path bypass (pkg/cache): most requests
// flow through the compressor, the skipped ones reach the next handler with
// the ResponseWriter UNWRAPPED.
//
// Why it exists (FILES-BENCH finding, fixed in FILES-FIX-SENDFILE): chi's
// compressResponseWriter wraps EVERY response — even content types it never
// compresses — and does not implement io.ReaderFrom, so http.ServeContent's
// io.CopyN could not reach TCPConn.ReadFrom and file downloads fell back to a
// userspace copy loop instead of sendfile zero-copy (measured: 0 sendfile
// calls, 53% of nginx throughput at ~5.5× the CPU/byte). Skipping the wrapper
// on byte-serving routes restores the kernel fast path — and skipping
// compression on binary blobs is correct on its own terms (the FILES-V2
// investigation: never compress images/video/binaries).
//
// skip is consulted per request; everything else keeps the exact
// chimiddleware.Compress behavior (level + compressible content types).
func SelectiveCompress(skip func(r *http.Request) bool, level int, types ...string) func(http.Handler) http.Handler {
	compress := chimiddleware.Compress(level, types...)
	return func(next http.Handler) http.Handler {
		compressed := compress(next)
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if skip(r) {
				next.ServeHTTP(w, r) // naked writer — io.ReaderFrom (sendfile) preserved
				return
			}
			compressed.ServeHTTP(w, r)
		})
	}
}
