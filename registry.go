package appitools

import (
	"net/http"
	"strings"
	"sync/atomic"
)

// MT-STRUCT-S2 — the in-process app registry, the Option-B foundation
// (docs/design/MT-STRUCT.md §9 Stage 2).
//
// An APP is one schema compiled into one API surface (router + GraphQL + RBAC
// + OpenAPI — exactly what buildRouter produces today); the registry maps a
// request's Host to the app that serves it. In S2 the registry holds ONE app —
// the boot schema — and every Host resolves to it, so behavior is identical to
// pre-registry: the layer exists, costs ~nothing (benched), and gives S3
// (N apps + per-app middleware) and S4 (per-app hot-swap) their seam.
//
// Read path is LOCK-FREE by construction: both fields are atomic.Pointer
// loads, never a mutex — the S4 hot-swap will publish a new map/app by pointer
// swap while in-flight requests keep the one they resolved.
//
// Host-parse coordination (deliberate): the dispatch parses Host to answer
// "which APP?" (suffix walk over registered domains) and the tenant middleware
// — inside the app — keeps parsing it to answer "which TENANT?" (first
// label). Sharing one parse via context.WithValue would cost an allocation +
// escape per request (~50 B), an order of magnitude MORE than the second
// zero-alloc parse (~14 ns, measured in the design). Two cheap reads beat one
// expensive share; the clean ordering is app first, tenant second.
type Registry struct {
	// domains maps a lowercase registered domain to its app. A Host matches a
	// domain exactly or as any subdomain of it (tenant labels stay free:
	// acme.crm.example.com → app of crm.example.com, tenant acme). Empty in S2.
	domains atomic.Pointer[map[string]*compiledApp]
	// def is the app serving any Host no domain claims. In S2 this is THE app
	// (the boot schema), preserving today's "any Host reaches the engine"
	// contract exactly. Never nil.
	def atomic.Pointer[compiledApp]
}

// compiledApp is one schema compiled into a servable API surface. In S2 the
// handler is the whole data-plane router buildRouter produces; S3 grows this
// struct into the per-app unit (own config/secret/policy/OpenAPI).
type compiledApp struct {
	name    string
	handler http.Handler
}

// NewRegistry builds a registry serving def for every unmatched Host, plus the
// optional domain table (keys must be lowercase hostnames). def must not be
// nil — S2 always has the boot app.
func NewRegistry(def *compiledApp, domains map[string]*compiledApp) *Registry {
	if def == nil {
		panic("appitools: NewRegistry requires a default app")
	}
	if domains == nil {
		domains = map[string]*compiledApp{}
	}
	r := &Registry{}
	r.def.Store(def)
	r.domains.Store(&domains)
	return r
}

// Resolve returns the app owning host (a request Host header, port allowed).
// Zero allocations on every path except the never-in-practice "uppercase Host
// that also matches a registered domain" retry. The suffix walk makes the
// LONGEST registered domain win (api.crm.example.com prefers crm.example.com
// over example.com) in O(labels) map probes.
func (r *Registry) Resolve(host string) *compiledApp {
	if m := *r.domains.Load(); len(m) != 0 {
		h := trimHostPort(host)
		for retried := false; ; {
			for s := h; ; {
				if app, ok := m[s]; ok {
					return app
				}
				i := strings.IndexByte(s, '.')
				if i < 0 {
					break
				}
				s = s[i+1:]
			}
			// Host headers are case-insensitive; the table is lowercase. Only
			// a mixed-case Host pays the ToLower alloc, once.
			if retried || h == strings.ToLower(h) {
				break
			}
			h = strings.ToLower(h)
			retried = true
		}
	}
	return r.def.Load()
}

// ServeHTTP dispatches the request to its app — the ONLY hot-path addition of
// S2 (benched: see docs/design/MT-STRUCT.md Stage 2).
func (r *Registry) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	r.Resolve(req.Host).handler.ServeHTTP(w, req)
}

// trimHostPort strips an optional :port (and IPv6 brackets) without
// allocating. Mirrors the tolerant parse net/http itself applies to Host.
func trimHostPort(h string) string {
	if strings.HasPrefix(h, "[") {
		if i := strings.IndexByte(h, ']'); i >= 0 {
			return h[1:i]
		}
		return h
	}
	if i := strings.IndexByte(h, ':'); i >= 0 {
		return h[:i]
	}
	return h
}
