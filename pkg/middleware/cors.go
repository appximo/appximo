package middleware

import (
	"net/http"
	"strconv"
	"strings"
)

// CORSConfig configures cross-origin resource sharing for the browser-facing
// data-plane routes (/api, /auth, /graphql, /openapi). CORS is INFRASTRUCTURE
// configuration of the instance — which browser origins may call this engine —
// NOT part of the schema (the schema describes the data model, identical for every
// tenant; the allowed origins are a deployment decision). The zero value (no
// AllowedOrigins) disables CORS entirely: the engine emits no Access-Control-*
// header and short-circuits no preflight — the safe default. An operator opts in
// explicitly by listing origins (APPXIMO_CORS_ORIGINS), so a browser SPA on
// another origin never works by accident, only by configuration.
type CORSConfig struct {
	// AllowedOrigins is the exact origin allowlist (scheme + host + optional port,
	// e.g. "https://app.example.com"). The single literal "*" allows ANY origin.
	AllowedOrigins []string
	// AllowedMethods is echoed in the preflight Access-Control-Allow-Methods.
	// Empty ⇒ GET, POST, PUT, PATCH, DELETE, OPTIONS.
	AllowedMethods []string
	// AllowedHeaders is echoed in the preflight Access-Control-Allow-Headers.
	// Empty ⇒ Authorization, Content-Type.
	AllowedHeaders []string
	// ExposedHeaders lists response headers a browser script may read
	// (Access-Control-Expose-Headers). Empty ⇒ none beyond the CORS-safelisted set.
	ExposedHeaders []string
	// AllowCredentials, when true, sends Access-Control-Allow-Credentials: true so a
	// browser may send cookies / Authorization. Per the Fetch spec a credentialed
	// response may NOT use the literal "*" origin, so when AllowCredentials is set
	// and AllowedOrigins is "*", the request's Origin is REFLECTED instead.
	AllowCredentials bool
	// MaxAge bounds how long (seconds) a browser may cache a preflight result.
	// 0 ⇒ 600 (10 minutes). Negative ⇒ no Access-Control-Max-Age header.
	MaxAge int
}

// Enabled reports whether any origin is configured. A CORSConfig with no origins
// is a no-op and the engine does not wire the middleware at all.
func (c CORSConfig) Enabled() bool { return len(c.AllowedOrigins) > 0 }

// corsScopedPrefixes are the request paths CORS applies to: the routes a public
// browser client consumes. The control plane (:9090), /admin, /metrics and /debug
// are operation surfaces (same-origin or machine callers), never opened to a
// public browser origin — they are intentionally excluded.
func corsScoped(path string) bool {
	switch {
	case path == "/graphql",
		strings.HasPrefix(path, "/api/"),
		strings.HasPrefix(path, "/auth/"),
		// "/openapi." (not a bare "/openapi" prefix) so it matches exactly the spec
		// routes /openapi.json and /openapi.yaml and never a future /openapi-*
		// route — the same separator discipline as /api/ and /auth/.
		strings.HasPrefix(path, "/openapi."):
		return true
	default:
		return false
	}
}

// CORS returns a middleware that emits cross-origin headers for allowed origins
// and answers preflight OPTIONS requests directly. ALL per-request choices are
// precomputed here at boot (origin set, header strings) — the request path only
// does a map lookup and a few header writes, and a request WITHOUT an Origin header
// (every server-to-server / curl / mobile call) returns on the first line untouched,
// so non-browser traffic pays effectively nothing.
//
// Placement: this MUST run BEFORE the tenant, response-cache, JWT and RBAC
// middlewares. (1) A preflight carries no credentials and no tenant, so it must be
// answered before JWT (which would 401 it) and before the tenant resolver (which
// could 400/500 a bare Host). (2) The response cache stores only status, body,
// Content-Type and ETag — never Access-Control-* — so a per-request Allow-Origin
// set by THIS outer middleware is never cached and replayed to a different origin.
func CORS(cfg CORSConfig) func(http.Handler) http.Handler {
	allowAny := false
	originSet := make(map[string]struct{}, len(cfg.AllowedOrigins))
	for _, o := range cfg.AllowedOrigins {
		o = strings.TrimSpace(o)
		if o == "" {
			continue
		}
		if o == "*" {
			allowAny = true
			continue
		}
		originSet[o] = struct{}{}
	}

	methods := strings.Join(cfg.AllowedMethods, ", ")
	if methods == "" {
		methods = "GET, POST, PUT, PATCH, DELETE, OPTIONS"
	}
	headers := strings.Join(cfg.AllowedHeaders, ", ")
	if headers == "" {
		headers = "Authorization, Content-Type"
	}
	exposed := strings.Join(cfg.ExposedHeaders, ", ")
	maxAge := ""
	switch {
	case cfg.MaxAge == 0:
		maxAge = "600"
	case cfg.MaxAge > 0:
		maxAge = strconv.Itoa(cfg.MaxAge)
	}
	credentials := cfg.AllowCredentials

	// resolveOrigin returns the value for Access-Control-Allow-Origin and whether
	// the origin is allowed. Literal "*" is only emitted when credentials are off
	// (the Fetch spec forbids "*" with credentials — there the origin is reflected).
	resolveOrigin := func(origin string) (string, bool) {
		if allowAny {
			if credentials {
				return origin, true
			}
			return "*", true
		}
		if _, ok := originSet[origin]; ok {
			return origin, true
		}
		return "", false
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Get("Origin")
			if origin == "" || !corsScoped(r.URL.Path) {
				next.ServeHTTP(w, r)
				return
			}
			preflight := r.Method == http.MethodOptions

			allowOrigin, ok := resolveOrigin(origin)
			if !ok {
				// Disallowed origin: emit NO CORS headers (the browser blocks the read).
				// A preflight is still answered 204 so the disallowed actual request is
				// never even sent; a simple request flows through to be handled, but its
				// response carries no Allow-Origin so the browser withholds it.
				if preflight {
					w.WriteHeader(http.StatusNoContent)
					return
				}
				next.ServeHTTP(w, r)
				return
			}

			h := w.Header()
			h.Set("Access-Control-Allow-Origin", allowOrigin)
			if allowOrigin != "*" {
				// The response varies by Origin — caches/proxies must not serve one
				// origin's response to another.
				h.Add("Vary", "Origin")
			}
			if credentials {
				h.Set("Access-Control-Allow-Credentials", "true")
			}

			if preflight {
				h.Set("Access-Control-Allow-Methods", methods)
				h.Set("Access-Control-Allow-Headers", headers)
				if maxAge != "" {
					h.Set("Access-Control-Max-Age", maxAge)
				}
				h.Add("Vary", "Access-Control-Request-Method")
				h.Add("Vary", "Access-Control-Request-Headers")
				w.WriteHeader(http.StatusNoContent)
				return
			}

			if exposed != "" {
				h.Set("Access-Control-Expose-Headers", exposed)
			}
			next.ServeHTTP(w, r)
		})
	}
}
