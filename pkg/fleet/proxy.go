package fleet

import (
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
)

// Proxy is the fleet's public face: it routes by Host to the owning app's
// engine and does NOTHING else — pure transport (MVP choice documented in
// docs/FLEET.md: a single-binary httputil.ReverseProxy; production TLS/HTTP2
// termination can sit in front — Caddy/nginx — with the same domain table).
// Auth, RBAC, tenancy, rate limiting all happen in the destination engine,
// exactly as without a proxy.
//
// Matching: a request Host matches an app if it equals one of its domains or
// is a SUBDOMAIN of one (longest domain wins). Subdomain labels stay free for
// the engine's tenant resolution: acme.crm.example.com → app "crm" (here) →
// tenant "acme" (engine middleware).
type Proxy struct {
	domains  map[string]*route // lowercased domain → route
	notFound []byte
}

type route struct {
	app string
	rp  *httputil.ReverseProxy
}

// NewProxy builds the routing table from the manifest apps and their assigned
// ports. The table is immutable for the run (ports are fixed per fleet run).
func NewProxy(mf *Manifest, portOf func(name string) (int, bool)) (*Proxy, error) {
	p := &Proxy{
		domains:  map[string]*route{},
		notFound: []byte(`{"error":"unknown app domain"}` + "\n"),
	}
	for i := range mf.Apps {
		spec := &mf.Apps[i]
		port, ok := portOf(spec.Name)
		if !ok {
			return nil, fmt.Errorf("fleet: proxy: no port for app %q", spec.Name)
		}
		target, err := url.Parse(fmt.Sprintf("http://127.0.0.1:%d", port))
		if err != nil {
			return nil, err
		}
		rp := &httputil.ReverseProxy{
			Rewrite: func(pr *httputil.ProxyRequest) {
				pr.SetURL(target)
				// Preserve the inbound Host — it carries the TENANT subdomain
				// the engine's middleware resolves. The proxy must not eat it.
				pr.Out.Host = pr.In.Host
				pr.SetXForwarded()
			},
			// Immediate flush so SSE (GET /api/{r}/events) streams through.
			FlushInterval: -1,
			ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
				// The app is down/restarting: a clean 502, never a hung client.
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusBadGateway)
				fmt.Fprintf(w, `{"error":"app %s is not available"}`+"\n", spec.Name) //nolint:errcheck
			},
		}
		rt := &route{app: spec.Name, rp: rp}
		for _, d := range spec.Domains {
			p.domains[d] = rt
		}
	}
	return p, nil
}

// ServeHTTP routes by Host. Lookup walks the host's label suffixes so the
// LONGEST matching domain wins (api.crm.example.com prefers crm.example.com
// over example.com), each step one map hit — O(labels), no allocation.
func (p *Proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	host := r.Host
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	host = strings.ToLower(host)
	for h := host; h != ""; {
		if rt, ok := p.domains[h]; ok {
			rt.rp.ServeHTTP(w, r)
			return
		}
		if i := strings.IndexByte(h, '.'); i >= 0 {
			h = h[i+1:]
		} else {
			break
		}
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusNotFound)
	w.Write(p.notFound) //nolint:errcheck
}

// StatusHandler serves the fleet's internal status/control API:
//
//	GET  /fleet/status                  → {apps: [AppStatus…]}
//	POST /fleet/apps/{name}/stop        → graceful stop (no auto-restart)
//	POST /fleet/apps/{name}/start       → start a stopped app
//	POST /fleet/apps/{name}/restart     → deliberate stop+start of ONE app
//
// It binds StatusAddr (default loopback). Like the engine control plane it is
// an INTERNAL surface — never expose it publicly.
func StatusHandler(s *Supervisor) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /fleet/status", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"apps": s.Status()}) //nolint:errcheck
	})
	act := func(name string, fn func(string) error) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			app := r.PathValue("name")
			w.Header().Set("Content-Type", "application/json")
			if err := fn(app); err != nil {
				w.WriteHeader(http.StatusBadRequest)
				json.NewEncoder(w).Encode(map[string]string{"error": err.Error()}) //nolint:errcheck
				return
			}
			log.Printf("fleet: %s %q via status API", name, app)
			json.NewEncoder(w).Encode(map[string]string{"ok": name, "app": app}) //nolint:errcheck
		}
	}
	mux.HandleFunc("POST /fleet/apps/{name}/stop", act("stop", s.StopApp))
	mux.HandleFunc("POST /fleet/apps/{name}/start", act("start", s.StartApp))
	mux.HandleFunc("POST /fleet/apps/{name}/restart", act("restart", s.RestartApp))
	return mux
}
