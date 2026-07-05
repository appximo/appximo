package appitools

import (
	"net/http"
	"strings"
	"sync"
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

	// writeMu serializes MUTATIONS of the domain table (SwapApp/AddApp/
	// RemoveApp), never the reads. Deploys/adds/removes are rare and must not
	// interleave (two concurrent copy-on-writes would lose one), but the read
	// path (Resolve) never takes it — it only atomic-loads the published map.
	writeMu sync.Mutex
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

// swapDomains is the copy-on-write core of every table mutation (MT-STRUCT-S4):
// under writeMu it clones the current map, applies mutate to the clone, and
// atomic-stores the clone. Readers (Resolve) that loaded the OLD map keep
// serving it consistently — a published map is NEVER mutated in place, so an
// in-flight request that resolved app X finishes on X, and only requests that
// load the NEW map see the change. Lock-free reads are preserved.
func (r *Registry) swapDomains(mutate func(m map[string]*compiledApp)) {
	r.writeMu.Lock()
	defer r.writeMu.Unlock()
	cur := *r.domains.Load()
	next := make(map[string]*compiledApp, len(cur)+1)
	for k, v := range cur {
		next[k] = v
	}
	mutate(next)
	r.domains.Store(&next)
}

// SwapApp atomically replaces the app served for each of domains with app —
// the per-app HOT-SWAP (MT-STRUCT-S4): a deploy recompiles ONE app's router
// from its new schema and publishes it here, leaving every OTHER app's entry
// byte-identical (their pointers are copied unchanged into the new map). No
// process restart, no effect on the other apps, lock-free reads throughout.
func (r *Registry) SwapApp(domains []string, app *compiledApp) {
	r.swapDomains(func(m map[string]*compiledApp) {
		for _, d := range domains {
			m[strings.ToLower(d)] = app
		}
	})
}

// AddApp registers a NEW app on domains (hot add — a domain not previously
// served starts routing to app). Same copy-on-write semantics as SwapApp.
func (r *Registry) AddApp(domains []string, app *compiledApp) { r.SwapApp(domains, app) }

// RemoveApp unregisters domains (hot remove — those Hosts fall back to the
// default/unmatched app). Same copy-on-write semantics.
func (r *Registry) RemoveApp(domains []string) {
	r.swapDomains(func(m map[string]*compiledApp) {
		for _, d := range domains {
			delete(m, strings.ToLower(d))
		}
	})
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
