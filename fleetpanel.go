package appitools

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"net/http"
	"sort"
	"sync/atomic"
	"time"
)

// MT-STRUCT-S5 — the unified fleet console: ONE face over the N in-process
// apps of `fleet serve`. It lives at the PROCESS level (the unmatched-Host
// handler, next to /healthz), a level ABOVE the per-app surfaces — each app
// keeps its own /admin, /editor, /docs on its domain, and the console links
// into them. Read-only aggregation + navigation: it holds NO write path into
// any app; operations on an app go through that app's own authenticated
// surface underneath (the S3 isolation is not bypassable from here).
//
// Auth taxonomy (decided in S5): the console is gated by the FLEET-OPERATOR
// key (manifest `operator_key` / APPITOOLS_FLEET_OPERATOR_KEY) — the server
// owner's credential, distinct by validation from every app's ADMIN_KEY and
// JWT_SECRET. A missing or wrong key is a UNIFORM 404 (anti-fingerprinting,
// the signed-files pattern), and an empty key disables the console entirely
// (safe by default). Holding one app's credentials grants nothing here;
// holding the fleet key grants nothing on any app's API.
//
// Observability by (app, tenant): S3 gave each app its OWN obs stack keyed by
// tenant, so the (app, tenant) dimension exists STRUCTURALLY — this console
// namespaces each app's per-tenant snapshots under the app, with zero
// re-keying of pkg/observability and zero hot-path change. Scale note: memory
// is one ring/histogram set per (app × tenant) — the same per-tenant cost as
// single-engine, multiplied by apps (documented in docs/FLEET.md).
type fleetPanel struct {
	operatorKey string
	version     string
	apps        []*panelApp
}

// panelApp is the console's read-only view of one in-process app.
type panelApp struct {
	name        string
	domains     []string
	schemaName  string
	controlPort int
	app         *App
	swaps       atomic.Int64 // successful hot-swaps since boot (S4 deploys)
}

// panelAppJSON is one row of GET /fleet/api/apps.
type panelAppJSON struct {
	Name        string                    `json:"name"`
	SchemaName  string                    `json:"schema_name"`
	Domains     []string                  `json:"domains"`
	ControlPort int                       `json:"control_port"`
	Resources   []string                  `json:"resources"`
	HotSwaps    int64                     `json:"hot_swaps"`
	Tenants     []panelTenantJSON         `json:"tenants"`
	Obs         map[string]panelTenantObs `json:"obs"` // tenant id → latency snapshot
}

type panelTenantJSON struct {
	ID          string `json:"id"`
	DisplayName string `json:"display_name"`
}

// panelTenantObs is the (app, tenant) latency snapshot the console renders.
type panelTenantObs struct {
	P50USUncached float64 `json:"p50_us_uncached"`
	P95USUncached float64 `json:"p95_us_uncached"`
	Requests      int64   `json:"requests"`
}

// register mounts the console on the process-level mux (next to the health
// probes). Everything is behind requireOperator; with no key configured the
// routes answer the same 404 as any unmatched path.
func (p *fleetPanel) register(mux *http.ServeMux) {
	mux.HandleFunc("GET /fleet", p.requireOperator(p.handleConsole))
	mux.HandleFunc("GET /fleet/api/apps", p.requireOperator(p.handleApps))
}

// requireOperator gates a console handler behind the fleet-operator key
// (X-Fleet-Key header or ?key=). Any failure — no key configured, missing,
// wrong — is the SAME 404 the process handler gives unknown paths, so the
// console's existence is not fingerprintable without the credential.
func (p *fleetPanel) requireOperator(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		key := r.Header.Get("X-Fleet-Key")
		if key == "" {
			key = r.URL.Query().Get("key")
		}
		if p.operatorKey == "" || subtle.ConstantTimeCompare([]byte(key), []byte(p.operatorKey)) != 1 {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusNotFound)
			w.Write([]byte(`{"error":"unknown app domain"}` + "\n")) //nolint:errcheck
			return
		}
		next(w, r)
	}
}

// handleApps is GET /fleet/api/apps: the fleet inventory — per app, its
// domains, live resources, tenants (from ITS control-plane table, read-only)
// and per-(app, tenant) latency snapshots. Off the hot path by construction
// (a console request; the tenant list is one bounded SELECT per app).
func (p *fleetPanel) handleApps(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	out := make([]panelAppJSON, 0, len(p.apps))
	for _, pa := range p.apps {
		row := panelAppJSON{
			Name:        pa.name,
			SchemaName:  pa.schemaName,
			Domains:     pa.domains,
			ControlPort: pa.controlPort,
			HotSwaps:    pa.swaps.Load(),
			Tenants:     []panelTenantJSON{},
			Obs:         map[string]panelTenantObs{},
		}
		if res := pa.app.liveResources.Load(); res != nil {
			row.Resources = *res
		}
		// Tenants of THIS app (its own database's control-plane table).
		rows, err := pa.app.pool.Query(ctx,
			`SELECT id, display_name FROM public.tenants ORDER BY id LIMIT 500`)
		if err == nil {
			for rows.Next() {
				var t panelTenantJSON
				if rows.Scan(&t.ID, &t.DisplayName) == nil {
					row.Tenants = append(row.Tenants, t)
				}
			}
			rows.Close()
		}
		// (app, tenant) latency: this app's per-tenant histograms.
		for _, tid := range pa.app.rings.TenantIDs() {
			if fs := pa.app.hist.FullSnapshot(tid); fs != nil && fs.Uncached != nil {
				row.Obs[tid] = panelTenantObs{
					P50USUncached: fs.Uncached.P50Us,
					P95USUncached: fs.Uncached.P95Us,
					Requests:      fs.Uncached.Count,
				}
			}
		}
		out = append(out, row)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
		"fleet_apps": len(out),
		"version":    p.version,
		"apps":       out,
	})
}

// handleConsole serves the console page (self-contained HTML, the sober token
// set — light/dark). It fetches /fleet/api/apps with the same key.
func (p *fleetPanel) handleConsole(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write([]byte(fleetConsoleHTML)) //nolint:errcheck
}

// fleetConsoleHTML is the unified fleet console: overview of the N apps
// (domains, resources, tenants, deploys) with per-(app, tenant) latency, and
// per-app links into that app's OWN surfaces (Studio /editor, admin /admin,
// docs) on its domain — choosing an app = entering its surface. Sober tokens
// (the 636736d set), light/dark.
const fleetConsoleHTML = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Appitools — Fleet</title>
<style>
:root{
  --bg:#fafafa;--panel:#ffffff;--border:#e4e4e7;--text:#18181b;--muted:#71717a;
  --accent:#2563eb;--ok:#16a34a;--chip:#f4f4f5;--mono:ui-monospace,SFMono-Regular,Menlo,monospace;
}
@media (prefers-color-scheme: dark){:root{
  --bg:#111113;--panel:#1a1a1d;--border:#2e2e33;--text:#e4e4e7;--muted:#8e8e96;
  --accent:#60a5fa;--ok:#4ade80;--chip:#232327;
}}
html[data-theme="dark"]{--bg:#111113;--panel:#1a1a1d;--border:#2e2e33;--text:#e4e4e7;--muted:#8e8e96;--accent:#60a5fa;--ok:#4ade80;--chip:#232327}
html[data-theme="light"]{--bg:#fafafa;--panel:#ffffff;--border:#e4e4e7;--text:#18181b;--muted:#71717a;--accent:#2563eb;--ok:#16a34a;--chip:#f4f4f5}
*{box-sizing:border-box}
body{margin:0;background:var(--bg);color:var(--text);font:14px/1.5 -apple-system,'Segoe UI',Roboto,sans-serif}
header{display:flex;align-items:center;gap:12px;padding:14px 24px;border-bottom:1px solid var(--border);background:var(--panel)}
header h1{font-size:15px;font-weight:600;margin:0}
header .sub{color:var(--muted);font-size:12.5px}
header .right{margin-left:auto;display:flex;gap:8px;align-items:center}
button.toggle{background:var(--chip);border:1px solid var(--border);color:var(--text);border-radius:6px;padding:4px 10px;font-size:12px;cursor:pointer}
main{max-width:1080px;margin:24px auto;padding:0 24px}
.grid{display:grid;gap:14px}
.card{background:var(--panel);border:1px solid var(--border);border-radius:10px;padding:16px 18px}
.card h2{margin:0;font-size:14px;font-weight:600;display:flex;align-items:center;gap:8px}
.dot{width:7px;height:7px;border-radius:50%;background:var(--ok);display:inline-block}
.meta{color:var(--muted);font-size:12.5px;margin-top:2px}
.row{display:flex;flex-wrap:wrap;gap:6px;margin-top:10px}
.chip{background:var(--chip);border:1px solid var(--border);border-radius:5px;padding:1px 8px;font-size:12px;font-family:var(--mono)}
.links{margin-top:12px;display:flex;gap:8px;flex-wrap:wrap}
.links a{color:var(--accent);text-decoration:none;font-size:12.5px;border:1px solid var(--border);border-radius:6px;padding:3px 10px}
.links a:hover{border-color:var(--accent)}
table{width:100%;border-collapse:collapse;margin-top:10px;font-variant-numeric:tabular-nums}
th{color:var(--muted);font-weight:500;font-size:11.5px;text-transform:uppercase;letter-spacing:.04em;text-align:left;padding:5px 8px;border-bottom:1px solid var(--border)}
td{padding:5px 8px;border-bottom:1px solid var(--border);font-size:12.5px}
tr:last-child td{border-bottom:none}
td.num{font-family:var(--mono)}
.empty{color:var(--muted);font-size:12.5px;padding:6px 0}
#err{color:#dc2626;padding:12px 0;display:none}
.section-title{color:var(--muted);font-size:11.5px;text-transform:uppercase;letter-spacing:.05em;margin:14px 0 0}
</style>
</head>
<body>
<header>
  <h1>Appitools · Fleet</h1>
  <span class="sub" id="summary">loading…</span>
  <div class="right"><button class="toggle" onclick="flip()">◐ theme</button></div>
</header>
<main>
  <div id="err"></div>
  <div class="grid" id="apps"></div>
</main>
<script>
const KEY = new URLSearchParams(location.search).get('key') || '';
function flip(){
  const cur = document.documentElement.getAttribute('data-theme');
  document.documentElement.setAttribute('data-theme', cur === 'dark' ? 'light' : 'dark');
}
function esc(s){const d=document.createElement('span');d.textContent=String(s);return d.innerHTML}
function ms(us){return us ? (us/1000).toFixed(2)+' ms' : '—'}
async function load(){
  try{
    const r = await fetch('/fleet/api/apps?key='+encodeURIComponent(KEY));
    if(!r.ok) throw new Error('HTTP '+r.status);
    const d = await r.json();
    document.getElementById('summary').textContent =
      d.fleet_apps + ' app' + (d.fleet_apps===1?'':'s') + ' in-process · ' + d.version;
    const root = document.getElementById('apps'); root.innerHTML='';
    for(const a of d.apps){
      const el = document.createElement('div'); el.className='card';
      const links = a.domains.map(dm =>
        '<a href="http://'+esc(dm)+'/editor" target="_blank">Studio · '+esc(dm)+'</a>'+
        '<a href="http://'+esc(dm)+'/admin" target="_blank">Admin</a>'+
        '<a href="http://'+esc(dm)+'/docs" target="_blank">Docs</a>').join('');
      const obsRows = Object.entries(a.obs).map(([t,o]) =>
        '<tr><td>'+esc(t)+'</td><td class="num">'+ms(o.p50_us_uncached)+'</td><td class="num">'+
        ms(o.p95_us_uncached)+'</td><td class="num">'+o.requests+'</td></tr>').join('');
      el.innerHTML =
        '<h2><span class="dot"></span>'+esc(a.name)+
        ' <span class="meta">'+esc(a.schema_name)+' · control :'+a.control_port+
        ' · '+a.hot_swaps+' hot-swap'+(a.hot_swaps===1?'':'s')+'</span></h2>'+
        '<div class="meta">domains: '+a.domains.map(esc).join(', ')+'</div>'+
        '<div class="section-title">Resources</div>'+
        '<div class="row">'+(a.resources||[]).map(x=>'<span class="chip">'+esc(x)+'</span>').join('')+'</div>'+
        '<div class="section-title">Tenants</div>'+
        '<div class="row">'+(a.tenants.length? a.tenants.map(t=>'<span class="chip">'+esc(t.id)+'</span>').join(''):'<span class="empty">none yet</span>')+'</div>'+
        '<div class="section-title">Latency by tenant (uncached)</div>'+
        (obsRows? '<table><tr><th>tenant</th><th>p50</th><th>p95</th><th>requests</th></tr>'+obsRows+'</table>'
                : '<div class="empty">no traffic recorded yet</div>')+
        '<div class="links">'+links+'</div>';
      root.appendChild(el);
    }
  }catch(e){
    const el=document.getElementById('err'); el.style.display='block';
    el.textContent='fleet inventory unavailable: '+e.message;
  }
}
load(); setInterval(load, 10000);
</script>
</body>
</html>`
