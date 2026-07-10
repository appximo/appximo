package appitools

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"net/http"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/miguelangel/appitools/pkg/fleet"
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
	// operatorAdminEmail is the unified operator identity (FLEET-CONSOLE-S2):
	// when set, this super-admin exists in EVERY app's /admin — the console
	// shows it so the operator knows one login opens each app's panel.
	operatorAdminEmail string
	// mu guards apps: the inventory handlers read it while the lifecycle
	// (FLEET-LIFECYCLE-S1) adds/removes entries. Console-only — never the
	// request hot path (the registry has its own copy-on-write discipline).
	mu   sync.RWMutex
	apps []*panelApp
	// lifecycle, when set (ServeFleet), enables the operator's app add/remove
	// actions — the ONE write surface of the console, and it writes at the
	// FLEET level (composition), never into an app (the S3 isolation holds:
	// per-app operations still go through each app's own authenticated surface).
	lifecycle *fleetLifecycle
}

// panelApp is the console's view of one in-process app.
type panelApp struct {
	name        string
	domains     []string
	schemaName  string
	controlPort int
	app         *App
	swaps       atomic.Int64       // successful hot-swaps since boot (S4 deploys)
	cancel      context.CancelFunc // stops the app's background services (hot remove)
}

func (p *fleetPanel) addApp(pa *panelApp) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.apps = append(p.apps, pa)
}

func (p *fleetPanel) removeApp(name string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for i, pa := range p.apps {
		if pa.name == name {
			p.apps = append(p.apps[:i], p.apps[i+1:]...)
			return
		}
	}
}

func (p *fleetPanel) byName(name string) *panelApp {
	p.mu.RLock()
	defer p.mu.RUnlock()
	for _, pa := range p.apps {
		if pa.name == name {
			return pa
		}
	}
	return nil
}

func (p *fleetPanel) snapshotApps() []*panelApp {
	p.mu.RLock()
	defer p.mu.RUnlock()
	out := make([]*panelApp, len(p.apps))
	copy(out, p.apps)
	return out
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
	// FLEET-LIFECYCLE-S1: the operator's app add/remove (hot, manifest-synced).
	mux.HandleFunc("POST /fleet/api/apps", p.requireOperator(p.handleAddApp))
	mux.HandleFunc("DELETE /fleet/api/apps/{name}", p.requireOperator(p.handleRemoveApp))
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

	apps := p.snapshotApps()
	out := make([]panelAppJSON, 0, len(apps))
	for _, pa := range apps {
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
		"fleet_apps":           len(out),
		"version":              p.version,
		"operator_admin_email": p.operatorAdminEmail,
		"apps":                 out,
	})
}

// appRow is the compact row for lifecycle responses (no per-tenant queries).
func (p *fleetPanel) appRow(pa *panelApp) panelAppJSON {
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
	return row
}

// addAppRequest is the POST /fleet/api/apps body (FLEET-LIFECYCLE-S1). The
// schema arrives INLINE (`schema` — the console's paste path, e.g. straight
// from Studio's Code view or an external agent) or as a server-readable path
// (`schema_path` — the CLI's flow). env carries the app's own secrets
// (DATABASE_URL / JWT_SECRET / ADMIN_KEY required, per-app by the fleet rules).
type addAppRequest struct {
	Name       string            `json:"name"`
	Domains    []string          `json:"domains"`
	Schema     json.RawMessage   `json:"schema,omitempty"`
	SchemaPath string            `json:"schema_path,omitempty"`
	Env        map[string]string `json:"env,omitempty"`
	EnvFile    string            `json:"env_file,omitempty"`
}

// handleAddApp is POST /fleet/api/apps: validate (schema via ValidateReport,
// composition via the manifest rules) → hot-add via the registry → manifest
// persisted. Errors are actionable: 422 carries the full validation report.
func (p *fleetPanel) handleAddApp(w http.ResponseWriter, r *http.Request) {
	if p.lifecycle == nil {
		writeJSONStatus(w, http.StatusServiceUnavailable, map[string]any{"error": "app lifecycle is not available in this runtime"})
		return
	}
	var req addAppRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": "invalid request body: " + err.Error()})
		return
	}
	if req.Schema == nil && req.SchemaPath == "" {
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": "provide the schema inline (schema) or as a server path (schema_path)"})
		return
	}
	spec := &fleet.AppSpec{Name: req.Name, Domains: req.Domains, Schema: req.SchemaPath, Env: req.Env, EnvFile: req.EnvFile}
	row, lerr := p.lifecycle.AddApp(spec, req.Schema)
	if lerr != nil {
		body := map[string]any{"error": lerr.msg}
		if lerr.report != nil {
			body["report"] = lerr.report
		}
		writeJSONStatus(w, lerr.status, body)
		return
	}
	writeJSONStatus(w, http.StatusCreated, map[string]any{"added": row})
}

// handleRemoveApp is DELETE /fleet/api/apps/{name}?confirm=<name>: the gated
// hot remove. The app's DATABASE IS NOT DELETED — removal is membership, not
// data destruction (re-adding with the same DATABASE_URL restores it intact).
func (p *fleetPanel) handleRemoveApp(w http.ResponseWriter, r *http.Request) {
	if p.lifecycle == nil {
		writeJSONStatus(w, http.StatusServiceUnavailable, map[string]any{"error": "app lifecycle is not available in this runtime"})
		return
	}
	name := r.PathValue("name")
	lerr, domains := p.lifecycle.RemoveApp(name, r.URL.Query().Get("confirm"))
	if lerr != nil {
		writeJSONStatus(w, lerr.status, map[string]any{"error": lerr.msg})
		return
	}
	writeJSONStatus(w, http.StatusOK, map[string]any{
		"removed": name, "domains": domains,
		"note": "the app's database was NOT deleted — re-adding it with the same DATABASE_URL restores it intact",
	})
}

func writeJSONStatus(w http.ResponseWriter, status int, body map[string]any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(body) //nolint:errcheck
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
.h2row{display:flex;align-items:center;gap:8px}
.h2row .grow{flex:1}
button.danger{background:none;border:1px solid var(--border);color:#dc2626;border-radius:6px;padding:3px 10px;font-size:12px;cursor:pointer}
button.danger:hover{border-color:#dc2626}
button.primary{background:var(--accent);border:1px solid var(--accent);color:#fff;border-radius:6px;padding:5px 14px;font-size:12.5px;cursor:pointer}
details.addapp{background:var(--panel);border:1px solid var(--border);border-radius:10px;padding:12px 18px}
details.addapp summary{cursor:pointer;font-weight:600;font-size:13.5px}
.form{display:grid;gap:8px;margin-top:12px}
.form label{font-size:11.5px;color:var(--muted);text-transform:uppercase;letter-spacing:.04em}
.form input,.form textarea{background:var(--bg);border:1px solid var(--border);border-radius:6px;color:var(--text);padding:6px 9px;font-size:12.5px;font-family:var(--mono)}
.form textarea{min-height:140px;resize:vertical}
.form .actions{display:flex;gap:8px;align-items:center}
#addmsg{font-size:12.5px}
#addmsg.bad{color:#dc2626}
#addmsg.ok{color:var(--ok)}
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
  <details class="addapp" id="addapp" style="margin-top:14px">
    <summary>Add app</summary>
    <div class="form">
      <label for="f-name">name</label>
      <input id="f-name" placeholder="optica" autocomplete="off">
      <label for="f-domains">domains (comma-separated)</label>
      <input id="f-domains" placeholder="optica.example.com" autocomplete="off">
      <label for="f-schema">schema JSON (paste — e.g. from Studio's Code view or your agent)</label>
      <textarea id="f-schema" spellcheck="false" placeholder='{ "$schema": "https://appitools.dev/schema/v1", ... }'></textarea>
      <label for="f-db">DATABASE_URL</label>
      <input id="f-db" placeholder="postgres://user:pass@localhost:5432/app_optica" autocomplete="off">
      <label for="f-jwt">JWT_SECRET (unique per app, ≥32 chars)</label>
      <input id="f-jwt" autocomplete="off">
      <label for="f-admin">ADMIN_KEY</label>
      <input id="f-admin" autocomplete="off">
      <div class="actions">
        <button class="primary" id="f-submit" onclick="addApp()">Add app (hot — no restart)</button>
        <span id="addmsg"></span>
      </div>
    </div>
  </details>
</main>
<script>
const KEY = new URLSearchParams(location.search).get('key') || '';
function flip(){
  const cur = document.documentElement.getAttribute('data-theme');
  document.documentElement.setAttribute('data-theme', cur === 'dark' ? 'light' : 'dark');
}
function esc(s){const d=document.createElement('span');d.textContent=String(s);return d.innerHTML}
function ms(us){return us ? (us/1000).toFixed(2)+' ms' : '—'}
// The fleet's public port: the console is served by the SAME listener as the
// apps, so per-app links must carry it (before this, links pointed at :80).
const PORT = location.port ? ':'+location.port : '';
async function load(){
  try{
    const r = await fetch('/fleet/api/apps?key='+encodeURIComponent(KEY));
    if(!r.ok) throw new Error('HTTP '+r.status);
    const d = await r.json();
    document.getElementById('summary').textContent =
      d.fleet_apps + ' app' + (d.fleet_apps===1?'':'s') + ' in-process · ' + d.version +
      (d.operator_admin_email ? ' · operator admin ' + d.operator_admin_email + ' (one login, every app\'s /admin)' : '');
    const root = document.getElementById('apps'); root.innerHTML='';
    for(const a of d.apps){
      const el = document.createElement('div'); el.className='card';
      const links = a.domains.map(dm =>
        '<a href="http://'+esc(dm)+PORT+'/editor" target="_blank">Studio · '+esc(dm)+'</a>'+
        '<a href="http://'+esc(dm)+PORT+'/admin" target="_blank">Admin</a>'+
        '<a href="http://'+esc(dm)+PORT+'/docs" target="_blank">Docs</a>').join('');
      const obsRows = Object.entries(a.obs).map(([t,o]) =>
        '<tr><td>'+esc(t)+'</td><td class="num">'+ms(o.p50_us_uncached)+'</td><td class="num">'+
        ms(o.p95_us_uncached)+'</td><td class="num">'+o.requests+'</td></tr>').join('');
      el.innerHTML =
        '<div class="h2row"><h2><span class="dot"></span>'+esc(a.name)+
        ' <span class="meta">'+esc(a.schema_name)+' · control :'+a.control_port+
        ' · '+a.hot_swaps+' hot-swap'+(a.hot_swaps===1?'':'s')+'</span></h2>'+
        '<span class="grow"></span>'+
        '<button class="danger" data-remove="'+esc(a.name)+'" onclick="removeApp(this.dataset.remove)">Remove</button></div>'+
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

// FLEET-LIFECYCLE-S1 — the operator's app lifecycle, over the gated API.
async function addApp(){
  const msg = document.getElementById('addmsg');
  msg.className=''; msg.textContent='';
  let schema;
  try{ schema = JSON.parse(document.getElementById('f-schema').value); }
  catch(e){ msg.className='bad'; msg.textContent='schema is not valid JSON: '+e.message; return; }
  const body = {
    name: document.getElementById('f-name').value.trim(),
    domains: document.getElementById('f-domains').value.split(',').map(s=>s.trim()).filter(Boolean),
    schema: schema,
    env: {
      DATABASE_URL: document.getElementById('f-db').value.trim(),
      JWT_SECRET:   document.getElementById('f-jwt').value.trim(),
      ADMIN_KEY:    document.getElementById('f-admin').value.trim()
    }
  };
  const btn = document.getElementById('f-submit'); btn.disabled = true;
  try{
    const r = await fetch('/fleet/api/apps?key='+encodeURIComponent(KEY), {
      method:'POST', headers:{'Content-Type':'application/json'}, body: JSON.stringify(body)
    });
    const d = await r.json();
    if(!r.ok){
      let t = d.error || ('HTTP '+r.status);
      if(d.report && d.report.errors) t += ' — ' + d.report.errors.map(e=>e.path+': '+e.message).join(' · ');
      msg.className='bad'; msg.textContent=t;
    }else{
      msg.className='ok'; msg.textContent='app "'+body.name+'" is live — serving '+body.domains.join(', ')+' (manifest persisted)';
      document.getElementById('addapp').open = false;
      for(const id of ['f-name','f-domains','f-schema','f-db','f-jwt','f-admin']) document.getElementById(id).value='';
      load();
    }
  }catch(e){ msg.className='bad'; msg.textContent=String(e); }
  btn.disabled = false;
}
async function removeApp(name){
  const typed = prompt('Remove app "'+name+'" from the fleet?\n\nIts domains stop serving immediately; the other apps are untouched. Its DATABASE is NOT deleted.\n\nType the app name to confirm:');
  if(typed !== name) return;
  try{
    const r = await fetch('/fleet/api/apps/'+encodeURIComponent(name)+'?key='+encodeURIComponent(KEY)+'&confirm='+encodeURIComponent(name), {method:'DELETE'});
    const d = await r.json();
    if(!r.ok){ alert(d.error || ('HTTP '+r.status)); return; }
    load();
  }catch(e){ alert(String(e)); }
}
</script>
</body>
</html>`
