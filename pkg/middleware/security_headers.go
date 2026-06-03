package middleware

import "net/http"

// SecurityHeaders adds defensive HTTP response headers to every response.
// Register this as the outermost middleware in the chain.
func SecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("X-XSS-Protection", "0")
		// HSTS: 1-year max-age + subdomains; no preload until the domain is HSTS-preload eligible.
		h.Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		next.ServeHTTP(w, r)
	})
}

// StrictCSP adds a restrictive Content-Security-Policy for API and GraphQL endpoints.
// Apply this to /api/* and /graphql — routes that serve JSON, never HTML.
func StrictCSP(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'")
		next.ServeHTTP(w, r)
	})
}

// PermissiveCSP sets a CSP suitable for the GraphiQL IDE playground.
// GraphiQL loads scripts and styles from CDN, so it needs a relaxed policy.
// Apply only to /graphiql in development mode.
func PermissiveCSP(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set(
			"Content-Security-Policy",
			"default-src 'self' https:; "+
				"script-src 'self' 'unsafe-inline' https:; "+
				"style-src 'self' 'unsafe-inline' https:; "+
				"img-src 'self' data: https:; "+
				"connect-src 'self'",
		)
		next.ServeHTTP(w, r)
	})
}
