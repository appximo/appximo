// app.js — the generic screens of backoffice-spec: menu (permission-probed),
// home (one stat tile per resource), list (sort / search / enum+state filters /
// keyset pagination), board (a kanban derived from the state machine — columns
// by lifecycle position, drag = a legal transition), form (the five rules),
// relation selects honoring x-appximo-references (and relation columns RESOLVED
// to the target's label in lists), constrained state chips, a file widget with
// the declared policy. No resource is named anywhere in this file — everything
// is derived from /openapi.json through contract.js. This is the embedded /app
// copy (ENG-38); the teaching copy consumers adapt into their own SPA lives in
// examples/backoffice-guide/web/app.js.
//
// DEMO-SHOWCASE-S1 added the chrome around the pattern: Spanish/English UI
// strings (i18n.js — browser-derived, persisted override), mobile-first
// responsive layout, light/dark themes, consumer theme tokens (theme.css),
// and an optional DEMO MODE: for roles listed in /app/ui-config.json the
// SPA simulates writes in a per-session in-memory overlay so a public demo
// stays touchable while the role's server-side RBAC stays read-only — a
// visitor's write never reaches the server, and a reload resets everything.
// APP-VITRINA-S1 rebuilt the skin on the atina design system (ink sidebar,
// one accent, Inter, tabular figures, positional lifecycle chips, drawer
// forms, toasts, skeletons, empty states with an action) — same contract,
// same five rules, same demo overlay.
import { loadContract, controlFor, isTerminal, rowLabel, titleField } from './contract.js';
import { t, lang, setLang } from './i18n.js';

const $ = (sel) => document.querySelector(sel);
const PER = 15;                    // rows per list page
const BOARD_MAX = 100;             // rows the board loads (one request, the API cap)
let token = null;
let user = null;                   // {email, role} from the login response / claims
let contract = null;
let current = null;                // selected resource (null = home)
let listState = {};                // per resource: {sort, order, search, filters, view, page, after:[], total, rows}
const probe = {};                  // resource -> {denied|error|total}
const relLabels = {};              // target resource -> Map(refValue -> label)
let uiConfig = {};                 // served at /app/ui-config.json (demo roles, …)
let demo = false;                  // demo mode: writes are simulated, never sent

// ── demo overlay (per session, in memory only — reload = reset) ─────────────
const overlay = { created: {}, patched: {}, deleted: {} };
const ov = (m, res) => (m[res] = m[res] ?? new Map());

function demoWrite(path, opts) {
  // Simulate the write locally; it never reaches the server. The demo role's
  // RBAC is read-only server-side, so even a hand-crafted request is a 403 —
  // this overlay is coherence, not the security boundary.
  const method = opts.method ?? 'GET';
  if (path === '/api/files' && method === 'POST') {
    return { file_id: crypto.randomUUID(), sha256: '', size: 0 };
  }
  const m = path.match(/^\/api\/([a-z_]+)(?:\/([^/?]+))?/);
  if (!m) return null;
  const [, res, id] = m;
  if (method === 'POST') {
    const row = { id: crypto.randomUUID(), created_at: new Date().toISOString(), ...opts.body };
    ov(overlay.created, res).set(row.id, row);
    return row;
  }
  if (method === 'PATCH' || method === 'PUT') {
    const created = ov(overlay.created, res);
    if (created.has(id)) {
      const merged = { ...created.get(id), ...opts.body };
      created.set(id, merged);
      return merged;
    }
    const prev = ov(overlay.patched, res).get(id) ?? {};
    const merged = { ...prev, ...opts.body };
    ov(overlay.patched, res).set(id, merged);
    return { id, ...merged };
  }
  if (method === 'DELETE') {
    if (ov(overlay.created, res).delete(id)) return null;
    (overlay.deleted[res] = overlay.deleted[res] ?? new Set()).add(id);
    return null;
  }
  return null;
}

function demoMergeList(res, rows, st) {
  const deleted = overlay.deleted[res] ?? new Set();
  const patched = overlay.patched[res] ?? new Map();
  const out = rows
    .filter((r) => !deleted.has(r.id))
    .map((r) => (patched.has(r.id) ? { ...r, ...patched.get(r.id) } : r));
  if (!st || (st.after ?? []).length === 0 && (st.page ?? 1) === 1) {
    let created = [...(overlay.created[res]?.values() ?? [])];
    if (st?.search) {
      const q = st.search.toLowerCase();
      created = created.filter((r) => Object.values(r).some((v) => String(v ?? '').toLowerCase().includes(q)));
    }
    for (const [k, v] of Object.entries(st?.filters ?? {})) created = created.filter((r) => String(r[k]) === String(v));
    out.unshift(...created.reverse());
  }
  return out;
}

class ApiError extends Error {
  constructor(status, body) {
    super(body?.error ?? (status === 0 ? t('err.network') : `HTTP ${status}`));
    this.status = status;
    this.fields = body?.fields ?? [];
  }
}

async function api(path, opts = {}) {
  const method = opts.method ?? 'GET';
  if (demo && method !== 'GET' && (path.startsWith('/api/'))) {
    return demoWrite(path, opts);       // simulated: never reaches the server
  }
  const headers = { ...(opts.headers ?? {}) };
  if (token) headers['Authorization'] = 'Bearer ' + token;
  let body = opts.body;
  if (body !== undefined && !(body instanceof FormData)) {
    headers['Content-Type'] = 'application/json';
    body = JSON.stringify(body);
  }
  let res;
  try {
    res = await fetch(path, { method, headers, body });
  } catch {
    throw new ApiError(0, null);
  }
  if (res.status === 204) return null;
  const isJSON = (res.headers.get('content-type') ?? '').includes('json');
  const data = isJSON ? await res.json() : null;
  if (!res.ok) {
    if (res.status === 401 && token && !path.startsWith('/auth/')) {
      token = null; demo = false;
      renderLogin(t('login.expired'));
    }
    throw new ApiError(res.status, data);
  }
  return data;
}

// ── theme (light / dark / auto) + language ───────────────────────────────────
const THEME_KEY = 'appximo.app.theme';
const store = {
  get(k) { try { return localStorage.getItem(k); } catch { return null; } },
  set(k, v) { try { if (v == null) localStorage.removeItem(k); else localStorage.setItem(k, v); } catch { /* blocked */ } },
};
function applyTheme() {
  const v = store.get(THEME_KEY);
  if (v === 'light' || v === 'dark') document.documentElement.dataset.theme = v;
  else delete document.documentElement.dataset.theme;
}
function themeTitle() {
  const v = store.get(THEME_KEY);
  return t(v === 'light' ? 'theme.light' : v === 'dark' ? 'theme.dark' : 'theme.auto');
}
function themeIcon() {
  const v = store.get(THEME_KEY);
  return v === 'light' ? ICON.sun : v === 'dark' ? ICON.moon : ICON.auto;
}
function cycleTheme() {
  const v = store.get(THEME_KEY);
  store.set(THEME_KEY, v === 'light' ? 'dark' : v === 'dark' ? null : 'light');
  applyTheme();
}
function prefButtons(extra = '') {
  return `<button id="pref-lang" class="btn btn-ghost btn-sm" title="idioma / language">${lang === 'es' ? 'EN' : 'ES'}</button>
    <button id="pref-theme" class="btn btn-ghost btn-sm btn-icon" title="${esc(themeTitle())}" aria-label="${esc(themeTitle())}">${themeIcon()}</button>${extra}`;
}
function wirePrefButtons(rerender) {
  const l = $('#pref-lang'), th = $('#pref-theme');
  if (l) l.onclick = () => { setLang(lang === 'es' ? 'en' : 'es'); rerender(); };
  if (th) th.onclick = () => { cycleTheme(); rerender(); };
}

// ── icons (inline, stroke-based — no icon font, no CDN) ─────────────────────
const svg = (d, extra = '') => `<svg class="i" viewBox="0 0 24 24" aria-hidden="true"${extra}>${d}</svg>`;
const ICON = {
  menu: svg('<path d="M4 6h16M4 12h16M4 18h16"/>'),
  search: svg('<circle cx="11" cy="11" r="7"/><path d="m20 20-3.5-3.5"/>'),
  plus: svg('<path d="M12 5v14M5 12h14"/>'),
  x: svg('<path d="M18 6 6 18M6 6l12 12"/>'),
  right: svg('<path d="m9 18 6-6-6-6"/>'),
  up: svg('<path d="m6 15 6-6 6 6"/>'),
  down: svg('<path d="m6 9 6 6 6-6"/>'),
  check: svg('<path d="m5 12 5 5L20 7"/>'),
  trash: svg('<path d="M3 6h18M8 6V4h8v2M6 6l1 14h10l1-14"/>'),
  upload: svg('<path d="M12 16V4m0 0 4 4m-4-4-4 4M4 16v3a1 1 0 0 0 1 1h14a1 1 0 0 0 1-1v-3"/>'),
  lock: svg('<rect x="4" y="11" width="16" height="10" rx="2"/><path d="M8 11V7a4 4 0 0 1 8 0v4"/>'),
  sun: svg('<circle cx="12" cy="12" r="4"/><path d="M12 2v2m0 16v2M4.9 4.9l1.4 1.4m11.4 11.4 1.4 1.4M2 12h2m16 0h2M4.9 19.1l1.4-1.4m11.4-11.4 1.4-1.4"/>'),
  moon: svg('<path d="M21 12.8A9 9 0 1 1 11.2 3a7 7 0 0 0 9.8 9.8Z"/>'),
  auto: svg('<circle cx="12" cy="12" r="9"/><path d="M12 3a9 9 0 0 1 0 18Z" fill="currentColor"/>'),
  logout: svg('<path d="M9 21H5a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h4M16 17l5-5-5-5M21 12H9"/>'),
  list: svg('<path d="M8 6h13M8 12h13M8 18h13M3 6h.01M3 12h.01M3 18h.01"/>'),
  board: svg('<rect x="3" y="3" width="5" height="18" rx="1"/><rect x="10" y="3" width="5" height="12" rx="1"/><rect x="17" y="3" width="4" height="8" rx="1"/>'),
  home: svg('<path d="m3 11 9-8 9 8v9a1 1 0 0 1-1 1h-5v-6h-6v6H4a1 1 0 0 1-1-1Z"/>'),
  inbox: svg('<path d="M22 12h-6l-2 3h-4l-2-3H2"/><path d="M5.5 5.1 2 12v6a2 2 0 0 0 2 2h16a2 2 0 0 0 2-2v-6l-3.5-6.9A2 2 0 0 0 16.8 4H7.2a2 2 0 0 0-1.7 1.1Z"/>'),
  alert: svg('<path d="M12 9v4m0 4h.01M10.3 3.9 1.8 18a2 2 0 0 0 1.7 3h17a2 2 0 0 0 1.7-3L13.7 3.9a2 2 0 0 0-3.4 0Z"/>'),
  info: svg('<circle cx="12" cy="12" r="9"/><path d="M12 8h.01M11 12h1v4h1"/>'),
  filter: svg('<path d="M3 5h18l-7 8v6l-4 2v-8L3 5Z"/>'),
  grid: svg('<rect x="3" y="3" width="7" height="7" rx="1.5"/><rect x="14" y="3" width="7" height="7" rx="1.5"/><rect x="3" y="14" width="7" height="7" rx="1.5"/><rect x="14" y="14" width="7" height="7" rx="1.5"/>'),
  eye: svg('<path d="M2 12s3.5-7 10-7 10 7 10 7-3.5 7-10 7S2 12 2 12Z"/><circle cx="12" cy="12" r="3"/>'),
  pencil: svg('<path d="M12 20h9M16.5 3.5a2.1 2.1 0 0 1 3 3L7 19l-4 1 1-4Z"/>'),
  file: svg('<path d="M14 3H6a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V9Z"/><path d="M14 3v6h6"/>'),
};

// ── auth (frontend-spec §2 — minimal) ────────────────────────────────────────
function claimsOf(tok) {
  try {
    return JSON.parse(atob(tok.split('.')[1].replace(/-/g, '+').replace(/_/g, '/')));
  } catch { return {}; }
}

async function login(email, password) {
  const r = await api('/auth/login', { method: 'POST', body: { email, password } });
  token = r.token;
  user = r.user ?? null;
  enterApp();
}
async function signup(email, password) {
  const r = await api('/auth/signup', { method: 'POST', body: { email, password } });
  token = r.token;
  user = r.user ?? null;
  enterApp();
}
function enterApp() {
  const c = claimsOf(token);
  user = { email: user?.email ?? c.email ?? '', role: user?.role ?? c.role ?? '' };
  demo = Array.isArray(uiConfig.demo_roles) && uiConfig.demo_roles.includes(user.role);
  boot().catch(showFatal);
}
function tenantLabel() {
  const h = window.location.hostname;
  return h.includes('.') ? h.split('.')[0] : h;
}

// The engine resolves the tenant from the Host subdomain. Opening /app on a
// bare host (localhost, an IP) means every API call will get the named 400 —
// say so BEFORE the first failed login, with the exact URL shape that works.
function tenantHint() {
  const h = window.location.hostname;
  if (h.includes('.')) return '';
  const host = esc(h) + (window.location.port ? ':' + window.location.port : '');
  return `<div class="banner info">${ICON.info}<div>${t('login.tenantHint', { host })}</div></div>`;
}

// ENG-46: the consumer's return bar (text + ONE link) from /app/ui-config.json.
// Rendered as text nodes — never markup — so the seam cannot inject HTML.
function bannerHTML() {
  const b = uiConfig.banner;
  if (!b || !b.text) return '';
  const link = b.href ? `<a href="${esc(b.href)}">${esc(b.text)}</a>` : `<span>${esc(b.text)}</span>`;
  return `<div class="consumer-bar">${link}</div>`;
}

function renderLogin(msg = '') {
  document.title = 'App — Appximo';
  document.documentElement.lang = lang;
  document.body.classList.remove('drawer-open');
  const title = contract ? contract.appTitle : 'App';
  document.body.classList.toggle('has-bar', !!uiConfig.banner?.text);
  $('#app').innerHTML = `${bannerHTML()}
    <div class="corner">${prefButtons()}</div>
    <div class="login-wrap">
      <aside class="login-brand">
        <div>
          <div class="eyebrow">${t('login.eyebrow')}</div>
          <div class="word" id="login-word">${esc(title)}</div>
          <p class="claim">${t('login.claim')}</p>
        </div>
        <div class="foot">${ICON.grid} <span>${t('login.generated')}</span> · <code>${esc(tenantLabel())}</code></div>
      </aside>
      <section class="login-side"><div class="login reveal">
        <h1 id="login-title">${t('login.title')}</h1>
        <div class="sub">${esc(title)} · ${t('login.generated')}</div>
        ${tenantHint()}
        ${msg ? `<div class="banner err">${ICON.alert}<div>${esc(msg)}</div></div>` : ''}
        <label><span>${t('login.email')}</span><input id="l-email" type="email" placeholder="usted@empresa.co" autocomplete="username"></label>
        <label><span>${t('login.password')}</span><input id="l-pass" type="password" placeholder="••••••••" autocomplete="current-password"></label>
        <div class="row">
          <button id="l-go" class="btn btn-primary">${t('login.signin')}</button>
          <button id="l-su" class="btn">${t('login.signup')}</button>
        </div>
        <small>${t('login.help')}</small>
      </div></section>
    </div>`;
  wirePrefButtons(() => renderLogin(msg));
  const busy = (b) => { $('#l-go').disabled = b; $('#l-su').disabled = b; };
  const go = () => { busy(true); login($('#l-email').value, $('#l-pass').value).catch((e) => renderLogin(e.message)); };
  $('#l-go').onclick = go;
  $('#l-pass').onkeydown = (e) => { if (e.key === 'Enter') go(); };
  $('#l-email').onkeydown = (e) => { if (e.key === 'Enter') $('#l-pass').focus(); };
  $('#l-su').onclick = () => { busy(true); signup($('#l-email').value, $('#l-pass').value).catch((e) => renderLogin(e.message)); };
  // The contract is public (engine-global): fetch it lazily so the login card
  // can greet with the app's own name; ignore failures (bare host, offline).
  if (!contract) {
    loadContract(api).then((c) => {
      contract = c;
      const w = $('#login-word'); if (w) w.textContent = c.appTitle;
      const s = document.querySelector('.login .sub'); if (s) s.textContent = `${c.appTitle} · ${t('login.generated')}`;
    }).catch(() => {});
  }
}

// ── boot: read the contract, probe permissions, draw the shell ───────────────
async function boot() {
  contract = await loadContract(api);
  // §5: one cheap request per resource; deny-by-default answers for us.
  await Promise.all(contract.resources.map(async (r) => {
    try {
      const res = await api(`/api/${r.name}?per_page=1&count=true`);
      probe[r.name] = { total: res.meta?.total ?? 0 };
    } catch (e) {
      probe[r.name] = e.status === 403 ? { denied: true } : { error: true };
    }
  }));
  current = null;
  renderShell();
  renderHome();
}

function monogram(title) {
  const w = String(title).trim().split(/[\s_]+/).filter(Boolean);
  const m = w.length >= 2 ? w[0][0] + w[1][0] : String(title).slice(0, 2);
  return m.toUpperCase();
}
function initials(email) {
  const local = String(email ?? '?').split('@')[0];
  const parts = local.split(/[._-]+/).filter(Boolean);
  return ((parts[0]?.[0] ?? '?') + (parts[1]?.[0] ?? '')).toUpperCase();
}

function renderShell() {
  document.title = `${contract.appTitle} — /app`;
  document.documentElement.lang = lang;
  const items = contract.resources.map((r) => {
    const p = probe[r.name] ?? {};
    const cls = p.denied ? 'denied' : '';
    const count = p.denied ? ICON.lock : (p.total ?? '');
    const title = p.denied ? ` title="${t('nav.denied')}"` : '';
    return `<li class="item ${cls}" data-res="${r.name}"${title}><span class="mono">${esc(monogram(r.title))}</span><span class="t">${esc(r.title)}</span><span class="count">${count}</span></li>`;
  }).join('');
  const virtual = Object.entries(contract.virtual).map(([n, v]) =>
    `<li class="item virtual" title="${esc(v.description ?? '')}"><span class="mono">${ICON.file}</span><span class="t">${esc(n)}</span><span class="count">engine</span></li>`).join('');
  document.body.classList.toggle('has-bar', !!uiConfig.banner?.text);
  $('#app').innerHTML = `${bannerHTML()}
    <div class="topbar">
      <button id="menu-btn" class="btn btn-ghost btn-icon" aria-label="${t('nav.menu')}">${ICON.menu}</button>
      <span class="name">${esc(contract.appTitle)}</span>
      ${demo ? `<span class="chip demo">${t('demo.tag')}</span>` : ''}
    </div>
    <div class="shell">
      <nav class="side">
        <div class="brand" title="from /openapi.json — this UI knows nothing else">
          <span class="avatar">${esc(monogram(contract.appTitle))}</span>
          <div><div class="name">${esc(contract.appTitle)}</div><div class="sub">${esc(tenantLabel())} · ${contract.resources.length} ${t('home.resources').toLowerCase()}</div></div>
        </div>
        <ul id="menu-top"><li class="item" data-home>${ICON.home}<span class="t">${t('nav.home')}</span></li></ul>
        <h2>${t('nav.resources')}</h2>
        <ul id="menu">${items}</ul>
        ${virtual ? `<h2>${t('nav.engine')}</h2><ul>${virtual}</ul>` : ''}
        <div class="grow"></div>
        <div class="userblock">
          <span class="avatar round">${esc(initials(user?.email))}</span>
          <div class="who"><b title="${esc(user?.email ?? '')}">${esc(user?.email ?? '')}</b><span>${esc(user?.role ?? '')}${demo ? ` · ${t('demo.tag')}` : ''}</span></div>
        </div>
        <div class="prefs">${prefButtons(`<button id="logout" class="btn btn-ghost btn-sm btn-icon" title="${t('nav.logout')}" aria-label="${t('nav.logout')}">${ICON.logout}</button>`)}</div>
      </nav>
      <main class="main"><div class="page" id="main"></div></main>
    </div>
    <div class="drawer-bg" id="drawer-bg"></div>
    ${demo ? `<div class="demobar">${ICON.info}<span>${t('demo.banner')}</span></div>` : ''}`;
  document.querySelectorAll('#menu li.item:not(.denied)').forEach((li) => {
    li.onclick = () => { closeDrawer(); selectResource(li.dataset.res); };
  });
  $('#menu-top li').onclick = () => { closeDrawer(); current = null; markActive(); renderHome(); };
  $('#logout').onclick = () => { token = null; user = null; demo = false; renderLogin(); };
  $('#menu-btn').onclick = () => document.body.classList.toggle('drawer-open');
  $('#drawer-bg').onclick = closeDrawer;
  wirePrefButtons(() => { renderShell(); if (current) selectResource(current.name); else renderHome(); });
  markActive();
}

function closeDrawer() { document.body.classList.remove('drawer-open'); }
function markActive() {
  document.querySelectorAll('#menu li').forEach((li) => li.classList.toggle('active', !!current && li.dataset.res === current.name));
  $('#menu-top li')?.classList.toggle('active', !current);
}

// ── home: one tile per resource, the role's reach at a glance ───────────────
function renderHome() {
  const rs = contract.resources;
  const total = rs.reduce((n, r) => n + (probe[r.name]?.total ?? 0), 0);
  const tiles = rs.map((r) => {
    const p = probe[r.name] ?? {};
    if (p.denied) return `<div class="card stat denied"><div class="lbl"><span class="avatar sm dim">${esc(monogram(r.title))}</span><span>${esc(r.title)}</span></div><div class="n">${ICON.lock}</div><div class="hint">${t('stat.denied')}</div></div>`;
    return `<a class="card stat" href="#" data-res="${r.name}"><span class="go">${ICON.right}</span><div class="lbl"><span class="avatar sm">${esc(monogram(r.title))}</span><span>${esc(r.title)}</span></div><div class="n" data-count="${p.total ?? 0}">0</div><div class="hint">${t('stat.records')}${p.error ? ' · ⚠' : ''}</div></a>`;
  }).join('');
  $('#main').innerHTML = `
    <div class="card-dark home-hero reveal">
      <div>
        <div class="eyebrow">${t('home.eyebrow')}</div>
        <h1>${esc(contract.appTitle)}</h1>
        <p>${t('home.sub', { n: rs.length })}</p>
      </div>
      <div class="kpis">
        <div class="kpi"><b class="num" data-count="${rs.length}">0</b><span>${t('home.resources')}</span></div>
        <div class="kpi"><b class="num" data-count="${total}">0</b><span>${t('home.records')}</span></div>
        <div class="kpi"><b class="txt">${esc(user?.role ?? '')}</b><span>${t('home.role')}</span></div>
      </div>
    </div>
    <div class="stats reveal-2">${tiles}</div>`;
  document.querySelectorAll('.stat[data-res]').forEach((a) => a.onclick = (e) => { e.preventDefault(); selectResource(a.dataset.res); });
  countUp();
}

function countUp() {
  const reduce = window.matchMedia?.('(prefers-reduced-motion: reduce)').matches;
  const fmt = new Intl.NumberFormat(locale());
  document.querySelectorAll('[data-count]').forEach((el) => {
    const to = Number(el.dataset.count) || 0;
    if (reduce || to === 0) { el.textContent = fmt.format(to); return; }
    const t0 = performance.now(), dur = 520;
    const step = (now) => {
      const p = Math.min(1, (now - t0) / dur);
      const e = 1 - Math.pow(1 - p, 3);
      el.textContent = fmt.format(Math.round(to * e));
      if (p < 1) requestAnimationFrame(step);
    };
    requestAnimationFrame(step);
  });
}

// ── list view (§8) ───────────────────────────────────────────────────────────
const NAMEISH = ['nombre', 'name', 'titulo', 'title', 'razon_social', 'codigo', 'code', 'numero', 'radicado', 'asunto', 'sku', 'email'];
function columnsFor(res) {
  const pref = (k) => { const i = NAMEISH.findIndex((p) => k === p || k.includes(p)); return i === -1 ? 99 : i; };
  const kind = (f) => {
    if (f.transitions) return 1;
    if (f.enum) return 2;
    if (f.relation) return 3;
    if (f.type === 'integer' || f.type === 'number') return 4;
    if (f.format === 'date-time') return 5;
    if (f.type === 'boolean') return 6;
    if (f.type === 'string' && !f.maxLength && !f.format) return 7;   // free text (long) — last
    return 0;
  };
  const cols = res.fields
    .filter((f) => f.key !== 'id' && !f.file && f.type !== 'object' && !f.json && !f.auto && (f.format !== 'uuid' || f.relation))
    .sort((a, b) => {
      const pa = pref(a.key), pb = pref(b.key);
      if (pa !== pb) return pa - pb;
      const ka = kind(a), kb = kind(b);
      if (ka !== kb) return ka - kb;
      return 0;
    })
    .slice(0, 5);
  const auto = res.fields.find((f) => f.auto && f.format === 'date-time');
  if (cols.length < 5 && auto) cols.push(auto);
  return cols;
}
function filterFields(res) {
  return res.fields.filter((f) => f.transitions || f.enum || f.type === 'boolean').slice(0, 4);
}

async function selectResource(name) {
  current = contract.byName[name];
  listState[name] = listState[name] ?? { sort: null, order: 'asc', search: '', filters: {}, view: 'list', page: 1, after: [], total: null };
  markActive();
  const st = listState[name];
  if (st.view === 'board' && current.stateField) await renderBoard();
  else await renderList();
}

function pageHeader(res, st, extraActions = '') {
  const total = st.total;
  const sub = total == null ? '' : total === 1 ? t('list.totalOne') : t('list.total', { n: new Intl.NumberFormat(locale()).format(total) });
  return `<div class="phead">
    <div><div class="eyebrow">${t('list.eyebrow')}</div><h1>${esc(res.title)}</h1><div class="sub num" id="ph-sub">${sub}</div></div>
    <div class="actions">${extraActions}${res.canCreate ? `<button id="new" class="btn btn-primary">${ICON.plus}<span class="txt">${t('list.new')}</span></button>` : ''}</div>
  </div>`;
}
function toolbarHTML(res, st) {
  const ff = filterFields(res);
  const filters = ff.map((f) => {
    const cur = st.filters[f.key] ?? '';
    const opts = f.type === 'boolean'
      ? [['true', t('list.filterYes')], ['false', t('list.filterNo')]]
      : (f.states ?? f.enum ?? []).map((v) => [v, v]);
    return `<select class="compact" data-filter="${f.key}" aria-label="${esc(f.label)}"><option value="">${esc(f.label)}: ${t('list.all')}</option>${opts.map(([v, l]) => `<option value="${esc(v)}"${String(cur) === v ? ' selected' : ''}>${esc(l)}</option>`).join('')}</select>`;
  }).join('');
  const hasFilters = Object.keys(st.filters).length > 0;
  const seg = res.stateField ? `<div class="seg" role="tablist">
      <button class="${st.view !== 'board' ? 'on' : ''}" data-view="list">${ICON.list}${t('view.list')}</button>
      <button class="${st.view === 'board' ? 'on' : ''}" data-view="board">${ICON.board}${t('view.board')}</button></div>` : '';
  return `<div class="toolbar">
    <div class="search">${ICON.search}<input id="search" type="search" placeholder="${t('list.search')}" value="${esc(st.search)}" aria-label="${t('list.search')}"></div>
    <div class="filters">${filters}${hasFilters ? `<button id="clear-filters" class="btn btn-ghost btn-sm">${ICON.x}${t('list.clearFilters')}</button>` : ''}</div>
    <span class="spacer"></span>
    ${seg}
  </div>`;
}
function wireToolbar(st, rerender) {
  $('#search').onchange = (e) => { st.search = e.target.value; st.after = []; st.page = 1; st.total = null; rerender(); };
  document.querySelectorAll('select[data-filter]').forEach((sel) => sel.onchange = () => {
    if (sel.value === '') delete st.filters[sel.dataset.filter]; else st.filters[sel.dataset.filter] = sel.value;
    st.after = []; st.page = 1; st.total = null; rerender();
  });
  if ($('#clear-filters')) $('#clear-filters').onclick = () => { st.filters = {}; st.after = []; st.page = 1; st.total = null; rerender(); };
  document.querySelectorAll('.seg [data-view]').forEach((b) => b.onclick = () => {
    st.view = b.dataset.view; st.after = []; st.page = 1;
    if (st.view === 'board') renderBoard(); else renderList();
  });
  if ($('#new')) $('#new').onclick = () => renderForm(null);
}

function skeletonTable() {
  return `<div class="card list"><div class="skel-rows">${'<div class="skel-row"><div class="skel w80"></div><div class="skel pill"></div><div class="skel w60"></div><div class="skel w40"></div><div class="skel w60"></div></div>'.repeat(6)}</div></div>`;
}

function listQuery(st) {
  const q = new URLSearchParams();
  q.set('per_page', String(PER));
  if (st.search) q.set('search', st.search);
  for (const [k, v] of Object.entries(st.filters)) q.set(`filter[${k}][eq]`, v);
  if (st.sort) {
    // A cursor and a sort are mutually exclusive on the engine (named 400), so a
    // sorted list pages by OFFSET; the unsorted list stays keyset.
    q.set('sort', st.sort); q.set('order', st.order);
    if (st.page > 1) q.set('page', String(st.page));
    if (st.total == null) q.set('count', 'true');
  } else {
    const cursor = st.after[st.after.length - 1];
    if (cursor) q.set('after', cursor);
    else if (st.total == null) q.set('count', 'true');   // count + cursor is a 400
  }
  return q;
}

async function renderList() {
  const res = current, st = listState[res.name];
  st.view = 'list';
  $('#main').innerHTML = `<div class="reveal">${pageHeader(res, st)}${toolbarHTML(res, st)}${skeletonTable()}</div>`;
  wireToolbar(st, renderList);
  let data;
  try {
    data = await api(`/api/${res.name}?` + listQuery(st).toString());
  } catch (e) {
    if (current !== res) return;
    $('#main').querySelector('.card.list').outerHTML = `<div class="banner err">${ICON.alert}<div>${esc(e.message)}</div><button id="retry" class="btn btn-sm">${t('list.retry')}</button></div>`;
    $('#retry').onclick = () => renderList();
    return;
  }
  if (current !== res || st.view !== 'list') return;
  if (data.meta?.total != null) st.total = data.meta.total;
  let rows = data.data;
  if (demo) rows = demoMergeList(res.name, rows, st);
  st.rows = rows;
  st.serverRows = data.data;
  const cols = columnsFor(res);
  await Promise.all(cols.filter((c) => c.relation).map((c) => loadRelLabels(c)));
  if (current !== res || st.view !== 'list') return;
  const ph = $('#ph-sub'); if (ph && st.total != null) ph.textContent = st.total === 1 ? t('list.totalOne') : t('list.total', { n: new Intl.NumberFormat(locale()).format(st.total) });

  const tf = titleField(res);
  const head = cols.map((c) => {
    const sortable = !c.relation;
    const sorted = st.sort === c.key;
    const numeric = c.type === 'integer' || c.type === 'number';
    return `<th class="${sortable ? 'sortable' : ''} ${sorted ? 'sorted' : ''} ${numeric ? 'num' : ''}" ${sortable ? `data-k="${c.key}"` : ''}><span class="sk">${esc(c.label)}${sortable ? (sorted && st.order === 'desc' ? ICON.down : ICON.up) : ''}</span></th>`;
  }).join('');
  const body = rows.map((row, i) => {
    const tds = cols.map((c) => {
      const numeric = c.type === 'integer' || c.type === 'number';
      return `<td data-l="${esc(c.label)}" class="${numeric ? 'num' : ''} ${tf && c.key === tf.key ? 'primary' : ''}">${cellHTML(row[c.key], c)}</td>`;
    }).join('');
    const act = `<button class="btn btn-ghost btn-sm btn-icon act-open" data-i="${i}" aria-label="${res.canEdit ? t('list.edit') : t('list.view')}" title="${res.canEdit ? t('list.edit') : t('list.view')}">${res.canEdit ? ICON.pencil : ICON.eye}</button>` +
      (res.canDelete ? `<button class="btn btn-ghost btn-sm btn-icon act-del" data-i="${i}" aria-label="${t('list.delete')}" title="${t('list.delete')}">${ICON.trash}</button>` : '');
    return `<tr class="clickable" data-i="${i}">${tds}<td class="rowact">${act}</td></tr>`;
  }).join('');
  const filtered = !!st.search || Object.keys(st.filters).length > 0;
  const emptyHTML = `<div class="empty">
      <div class="ico">${ICON.inbox}</div>
      <b>${filtered ? t('list.emptyFiltered') : t('list.empty')}</b>
      <p>${filtered ? t('list.emptyFilteredHint') : t('list.emptyHint')}</p>
      ${filtered ? `<button id="empty-clear" class="btn btn-sm">${t('list.clearFilters')}</button>` : (res.canCreate ? `<button id="empty-new" class="btn btn-primary">${ICON.plus}${t('list.createFirst')}</button>` : '')}
    </div>`;
  const pageNo = st.sort ? st.page : st.after.length + 1;
  const canPrev = st.sort ? st.page > 1 : st.after.length > 0;
  const foot = `<div class="tfoot">
      <span class="num">${t('list.page', { n: pageNo })} · ${t('list.showing', { n: rows.length })}</span>
      <span class="pg">
        ${canPrev ? `<button id="pg-first" class="btn">${t('list.first')}</button><button id="pg-prev" class="btn">${t('list.prev')}</button>` : ''}
        ${data.meta?.has_next ? `<button id="pg-next" class="btn">${t('list.next')} ${ICON.right}</button>` : ''}
      </span>
    </div>`;
  $('#main').querySelector('.card.list').outerHTML = `<div class="card list reveal">
    <div id="list-banner"></div>
    ${rows.length === 0 ? emptyHTML : `<div class="tablewrap"><table><thead><tr>${head}<th></th></tr></thead><tbody>${body}</tbody></table></div>`}
    ${rows.length === 0 && !canPrev ? '' : foot}
  </div>`;

  document.querySelectorAll('th[data-k]').forEach((th) => th.onclick = () => {
    const k = th.dataset.k;
    st.order = st.sort === k && st.order === 'asc' ? 'desc' : 'asc';
    st.sort = k; st.after = []; st.page = 1;
    renderList();
  });
  if ($('#empty-new')) $('#empty-new').onclick = () => renderForm(null);
  if ($('#empty-clear')) $('#empty-clear').onclick = () => { st.filters = {}; st.search = ''; st.total = null; renderList(); };
  if ($('#pg-next')) $('#pg-next').onclick = () => { if (st.sort) st.page++; else st.after.push(data.data[data.data.length - 1].id); renderList(); };
  if ($('#pg-prev')) $('#pg-prev').onclick = () => { if (st.sort) st.page = Math.max(1, st.page - 1); else st.after.pop(); renderList(); };
  if ($('#pg-first')) $('#pg-first').onclick = () => { st.after = []; st.page = 1; renderList(); };
  document.querySelectorAll('tbody tr').forEach((tr) => tr.onclick = (ev) => {
    if (ev.target.closest('.act-del')) return;
    renderForm(st.rows[Number(tr.dataset.i)]);
  });
  document.querySelectorAll('.act-del').forEach((b) => b.onclick = (ev) => {
    ev.stopPropagation();
    renderForm(st.rows[Number(b.dataset.i)], { confirmDelete: true });
  });
}

// ── relation labels: one fetch per target, cached for the session ───────────
async function loadRelLabels(f) {
  const target = f.relation;
  if (relLabels[target] instanceof Map) return relLabels[target];
  if (relLabels[target]) return relLabels[target];           // in flight
  relLabels[target] = (async () => {
    let rows = [];
    try { rows = (await api(`/api/${target}?per_page=100`)).data; } catch { rows = []; }
    if (demo) rows = demoMergeList(target, rows, null);
    const m = new Map();
    for (const r of rows) m.set(String(r[f.references] ?? r.id), rowLabel(r, f.references));
    m.rows = rows;
    return m;
  })();
  relLabels[target] = await relLabels[target];              // the resolved Map, not the promise
  return relLabels[target];
}
function relLabel(f, v) {
  const m = relLabels[f.relation];
  const map = m && typeof m.get === 'function' ? m : null;
  return map?.get(String(v)) ?? String(v ?? '').slice(0, 8) + '…';
}
function invalidateRel(target) { delete relLabels[target]; }

// ── value display ────────────────────────────────────────────────────────────
function locale() { return lang === 'es' ? 'es-CO' : 'en-US'; }
const isMoney = (f) => /_(centavos|cents)$/.test(f.key);
function fmtNumber(v, f) {
  if (isMoney(f)) {
    const n = Number(v) / 100;
    return '$ ' + new Intl.NumberFormat(locale(), { minimumFractionDigits: n % 1 ? 2 : 0, maximumFractionDigits: 2 }).format(n);
  }
  return new Intl.NumberFormat(locale(), { maximumFractionDigits: 4 }).format(Number(v));
}
function fmtDate(v) {
  const d = new Date(v);
  if (isNaN(d)) return String(v);
  return new Intl.DateTimeFormat(locale(), { day: '2-digit', month: '2-digit', year: 'numeric', hour: '2-digit', minute: '2-digit', hour12: false }).format(d).replace(',', '');
}
function stateChip(f, v) {
  if (v == null || v === '') return `<span class="nil">${t('form.none')}</span>`;
  const idx = (f.states ?? []).indexOf(String(v));
  const cls = 's' + ((idx === -1 ? 7 : idx % 8) + 1);
  return `<span class="chip state ${cls} ${isTerminal(f, String(v)) ? 'terminal' : ''}"><span class="dot"></span>${esc(String(v).replace(/_/g, ' '))}</span>`;
}
function cellHTML(v, f) {
  if (v === null || v === undefined || v === '') return `<span class="cell nil">${t('form.none')}</span>`;
  if (f.transitions) return stateChip(f, v);
  if (f.enum) return `<span class="chip">${esc(String(v).replace(/_/g, ' '))}</span>`;
  if (f.type === 'boolean') return v ? `<span class="chip on">${ICON.check}${t('bool.yes')}</span>` : `<span class="chip off">${t('bool.no')}</span>`;
  if (f.relation) return `<span class="cell rel">${esc(relLabel(f, v))}</span>`;
  if (f.format === 'date-time') return `<span class="cell date">${esc(fmtDate(v))}</span>`;
  if (f.type === 'integer' || f.type === 'number') return `<span class="cell num">${esc(fmtNumber(v, f))}</span>`;
  if (f.format === 'uuid') return `<span class="chip mono">${esc(String(v).slice(0, 8))}</span>`;
  if (typeof v === 'object') return `<span class="chip mono">{${Object.keys(v).length}}</span>`;
  if (f.json) return `<span class="chip mono">${esc(String(v).slice(0, 24))}</span>`;
  return `<span class="cell" title="${esc(String(v))}">${esc(String(v))}</span>`;
}

// ── board: the state machine as columns; a drag is a legal transition ───────
async function renderBoard() {
  const res = current, st = listState[res.name];
  const sf = res.stateField;
  if (!sf) return renderList();
  st.view = 'board';
  $('#main').innerHTML = `<div class="reveal">${pageHeader(res, st)}${toolbarHTML(res, st)}<div class="board" id="board">${sf.states.map(() => `<div class="col"><header><span class="skel pill"></span></header><div class="skel card-skel"></div><div class="skel card-skel"></div></div>`).join('')}</div></div>`;
  wireToolbar(st, renderBoard);
  const q = new URLSearchParams({ per_page: String(BOARD_MAX) });
  if (st.search) q.set('search', st.search);
  for (const [k, v] of Object.entries(st.filters)) q.set(`filter[${k}][eq]`, v);
  if (st.total == null) q.set('count', 'true');
  let data;
  try { data = await api(`/api/${res.name}?` + q.toString()); } catch (e) {
    if (current !== res) return;
    $('#board').outerHTML = `<div class="banner err">${ICON.alert}<div>${esc(e.message)}</div><button id="retry" class="btn btn-sm">${t('list.retry')}</button></div>`;
    $('#retry').onclick = () => renderBoard();
    return;
  }
  if (current !== res || st.view !== 'board') return;
  if (data.meta?.total != null) st.total = data.meta.total;
  let rows = data.data;
  if (demo) rows = demoMergeList(res.name, rows, { ...st, after: [], page: 1 });
  st.rows = rows;
  const cols = columnsFor(res).filter((c) => c.key !== sf.key).slice(0, 3);
  await Promise.all(cols.filter((c) => c.relation).map((c) => loadRelLabels(c)));
  if (current !== res || st.view !== 'board') return;
  const ph = $('#ph-sub'); if (ph && st.total != null) ph.textContent = st.total === 1 ? t('list.totalOne') : t('list.total', { n: new Intl.NumberFormat(locale()).format(st.total) });
  drawBoard(res, st, sf, cols, data.meta?.has_next);
}

function drawBoard(res, st, sf, cols, hasMore) {
  const tf = titleField(res);
  const byState = {};
  for (const r of st.rows) (byState[r[sf.key]] = byState[r[sf.key]] ?? []).push(r);
  const colHTML = sf.states.map((s, si) => {
    const list = byState[s] ?? [];
    const cards = list.map((row) => {
      const title = tf ? row[tf.key] : rowLabel(row, 'id');
      const kv = cols.filter((c) => !tf || c.key !== tf.key).map((c) => `<div class="kv"><span>${esc(c.label)}</span><span>${plainValue(row[c.key], c)}</span></div>`).join('');
      const moves = (sf.transitions[s] ?? []).map((to) => `<button class="chip state s${(sf.states.indexOf(to) % 8) + 1} mv" data-to="${esc(to)}" data-id="${row.id}">${ICON.right}${esc(to.replace(/_/g, ' '))}</button>`).join('');
      return `<article class="kcard" draggable="${res.canEdit && !isTerminal(sf, s) ? 'true' : 'false'}" data-id="${row.id}" data-state="${esc(s)}"><b title="${esc(String(title ?? ''))}">${esc(String(title ?? '—'))}</b>${kv}${res.canEdit ? `<div class="moves">${moves}</div>` : ''}</article>`;
    }).join('');
    return `<section class="col" data-state="${esc(s)}"><header>${stateChip(sf, s)}<span class="count">${list.length}</span></header>${cards}${list.length === 0 ? `<div class="more dim">${t('form.none')}</div>` : ''}</section>`;
  }).join('');
  $('#board').outerHTML = `<div class="board reveal" id="board">${colHTML}</div>${hasMore ? `<div class="banner info">${ICON.info}<div>${t('board.limit', { n: BOARD_MAX })}</div></div>` : ''}`;
  const board = $('#board');
  const rowById = (id) => st.rows.find((r) => r.id === id);

  board.querySelectorAll('.kcard').forEach((card) => {
    card.onclick = (ev) => { if (ev.target.closest('.mv')) return; renderForm(rowById(card.dataset.id)); };
    card.ondragstart = (ev) => {
      ev.dataTransfer.setData('text/plain', card.dataset.id);
      ev.dataTransfer.effectAllowed = 'move';
      card.classList.add('dragging');
      const from = card.dataset.state;
      board.querySelectorAll('.col').forEach((col) => col.classList.toggle('illegal', col.dataset.state !== from && !(sf.transitions[from] ?? []).includes(col.dataset.state)));
    };
    card.ondragend = () => { card.classList.remove('dragging'); board.querySelectorAll('.col').forEach((c) => c.classList.remove('illegal', 'over')); };
  });
  board.querySelectorAll('.mv').forEach((b) => b.onclick = (ev) => { ev.stopPropagation(); moveCard(res, st, sf, b.dataset.id, b.dataset.to); });
  board.querySelectorAll('.col').forEach((col) => {
    col.ondragover = (ev) => { if (col.classList.contains('illegal')) return; ev.preventDefault(); ev.dataTransfer.dropEffect = 'move'; col.classList.add('over'); };
    col.ondragleave = () => col.classList.remove('over');
    col.ondrop = (ev) => {
      ev.preventDefault(); col.classList.remove('over');
      const id = ev.dataTransfer.getData('text/plain');
      moveCard(res, st, sf, id, col.dataset.state);
    };
  });
}

async function moveCard(res, st, sf, id, to) {
  const row = st.rows.find((r) => r.id === id);
  if (!row) return;
  const from = row[sf.key];
  if (from === to) return;
  if (!(sf.transitions[from] ?? []).includes(to)) { toast(t('board.illegal', { from }), 'err'); return; }
  try {
    const r = await api(`/api/${res.name}/${id}`, { method: 'PATCH', body: { [sf.key]: to } }); // rule 2
    Object.assign(row, r ?? { [sf.key]: to });
    row[sf.key] = to;
    toast(t('board.moved', { from, to }));
    const cols = columnsFor(res).filter((c) => c.key !== sf.key).slice(0, 3);
    drawBoard(res, st, sf, cols, false);
  } catch (e) {
    toast(e.message, 'err');
    if (e.status === 422) renderBoard();          // the loser of a race reloads the row
  }
}

function plainValue(v, f) {
  if (v === null || v === undefined || v === '') return t('form.none');
  if (f.type === 'boolean') return v ? t('bool.yes') : t('bool.no');
  if (f.relation) return esc(relLabel(f, v));
  if (f.format === 'date-time') return esc(fmtDate(v));
  if (f.type === 'integer' || f.type === 'number') return esc(fmtNumber(v, f));
  if (typeof v === 'object') return `{${Object.keys(v).length}}`;
  return esc(String(v));
}

// ── the generic form (§6: the five rules) — a drawer / bottom sheet ─────────
async function renderForm(row, opts = {}) {
  const res = current;
  const editing = !!row;
  const readonly = editing && !res.canEdit;
  const fields = formFields(res);
  const controls = await Promise.all(fields.map(async (f) => {
    const val = editing ? row[f.key] : null;
    return `<div class="f" data-f="${f.key}">
      <label for="c-${f.key}">${esc(f.label)}${f.required && !readonly ? ` <span class="req" title="${t('form.required')}">*</span>` : ''}</label>
      ${await controlHTML(f, val, editing, readonly)}
      <em class="ferr" data-f="${f.key}"></em>
    </div>`;
  }));
  const old = $('#form-bg'); if (old) old.remove();
  const title = editing ? ((titleField(res) && row[titleField(res).key]) || rowLabel(row) || res.title) : null;
  $('#app').insertAdjacentHTML('beforeend', `
    <div class="drawer-bgd" id="form-bg"><div class="drawer" role="dialog" aria-modal="true">
      <header>
        <div class="ttl"><div class="eyebrow">${readonly ? t('form.view') : editing ? t('form.edit') : t('form.new')} · ${esc(res.title)}</div><h2>${esc(String(title || res.title))}</h2></div>
        <button type="button" id="form-close" class="btn btn-ghost btn-icon" aria-label="${t('form.close')}">${ICON.x}</button>
      </header>
      <div class="body">
        <div id="form-confirm"></div>
        <div id="form-banner">${readonly ? `<div class="banner info">${ICON.lock}<div>${t('form.readonly')}</div></div>` : ''}</div>
        <form id="gform" novalidate>${controls.join('')}</form>
      </div>
      <footer>
        ${editing && res.canDelete ? `<button type="button" id="form-del" class="btn btn-danger">${ICON.trash}${t('form.delete')}</button>` : ''}
        <span class="grow"></span>
        <button type="button" id="form-cancel" class="btn">${readonly ? t('form.close') : t('form.cancel')}</button>
        ${readonly ? '' : `<button type="submit" form="gform" id="form-save" class="btn btn-primary">${editing ? t('form.save') : t('form.create')}</button>`}
      </footer>
    </div></div>`);
  const close = () => { const el = $('#form-bg'); if (el) el.remove(); };
  $('#form-cancel').onclick = close;
  $('#form-close').onclick = close;
  $('#form-bg').onclick = (ev) => { if (ev.target.id === 'form-bg') close(); };
  const esc_ = (ev) => { if (ev.key === 'Escape') { close(); document.removeEventListener('keydown', esc_); } };
  document.addEventListener('keydown', esc_);
  wireFileInputs();
  const showConfirm = () => {
    $('#form-confirm').innerHTML = `<div class="confirm"><b>${t('form.confirmDelete')}</b><span class="muted hint">${t('form.confirmHint')}</span>
      <div class="row"><button type="button" id="del-no" class="btn btn-sm">${t('form.cancel')}</button><button type="button" id="del-yes" class="btn btn-danger solid btn-sm">${ICON.trash}${t('form.delete')}</button></div></div>`;
    $('#del-no').onclick = () => { $('#form-confirm').innerHTML = ''; };
    $('#del-yes').onclick = async () => {
      $('#del-yes').disabled = true;
      try {
        await api(`/api/${res.name}/${row.id}`, { method: 'DELETE' });
        close(); toast(t('form.deleted'));
        probe[res.name] = { total: Math.max(0, (probe[res.name]?.total ?? 1) - 1) };
        listState[res.name].total = null;
        invalidateRel(res.name);
        refreshNavCount(res);
        listState[res.name].view === 'board' ? renderBoard() : renderList();
      } catch (e) {
        // §8: the 409 names the referencing table — show it verbatim.
        $('#form-confirm').innerHTML = '';
        $('#form-banner').innerHTML = `<div class="banner err">${ICON.alert}<div>${esc(e.message)}</div></div>`;
      }
    };
  };
  if ($('#form-del')) $('#form-del').onclick = showConfirm;
  if (opts.confirmDelete && editing && res.canDelete) showConfirm();
  const first = document.querySelector('#gform input:not([type=hidden]):not([type=file]):not(:disabled), #gform select:not(:disabled), #gform textarea:not(:disabled)');
  if (first && !opts.confirmDelete && window.matchMedia('(min-width: 901px)').matches) first.focus();

  $('#gform').onsubmit = async (ev) => {
    ev.preventDefault();
    if (readonly) return;
    document.querySelectorAll('.f.field-err').forEach((el) => el.classList.remove('field-err'));
    const body = {};
    let bad = false;
    for (const f of fields) {
      let v;
      try { v = readControl(f); } catch (err) { paintField(f.key, err.message); bad = true; continue; }
      if (!editing && (v === null || v === '')) continue;      // rule 1: OMIT empty on create
      if (editing && v === '') { body[f.key] = null; continue; } // rule 3: explicit null clears
      if (v !== null) body[f.key] = v;
      else if (editing) body[f.key] = null;
    }
    if (bad) { $('#form-banner').innerHTML = `<div class="banner err">${ICON.alert}<div>${t('form.fixFields')}</div></div>`; return; }
    if (editing) for (const k of Object.keys(body)) if (deepEq(body[k], row[k])) delete body[k];   // PATCH only what changed
    const save = $('#form-save'); save.disabled = true; save.insertAdjacentHTML('afterbegin', '<span class="spin"></span>');
    try {
      let r;
      if (editing) r = Object.keys(body).length ? await api(`/api/${res.name}/${row.id}`, { method: 'PATCH', body }) : row; // rule 2
      else r = await api(`/api/${res.name}`, { method: 'POST', body });
      close();
      toast(editing ? t('form.saved') : t('form.created'));
      if (!editing) { probe[res.name] = { total: (probe[res.name]?.total ?? 0) + 1 }; listState[res.name].total = null; refreshNavCount(res); }
      invalidateRel(res.name);
      if (editing && r) Object.assign(row, r);
      listState[res.name].view === 'board' ? renderBoard() : renderList();
    } catch (e) {
      save.disabled = false; save.querySelector('.spin')?.remove();
      if (e.status === 422 && e.fields.length) {               // rule 4: paint them ALL
        let painted = 0;
        for (const fe of e.fields) if (paintField(fe.field, fe.message ?? fe.rule)) painted++;
        const firstErr = document.querySelector('.f.field-err');
        if (firstErr) firstErr.scrollIntoView({ block: 'center', behavior: 'smooth' });
        $('#form-banner').innerHTML = painted ? `<div class="banner err">${ICON.alert}<div>${t('form.fixFields')}</div></div>` : `<div class="banner err">${ICON.alert}<div>${esc(e.message)}${e.fields.map((fe) => ` · ${esc(fe.field)}: ${esc(fe.message ?? fe.rule)}`).join('')}</div></div>`;
      } else {                                                  // rule 4: 409 keeps the work
        $('#form-banner').innerHTML = `<div class="banner err">${ICON.alert}<div>${esc(e.message)}</div></div>`;
        $('#form-banner').scrollIntoView({ block: 'nearest' });
      }
    }
  };
}

// Form order (the contract lists properties alphabetically): the title field,
// then the other required fields, then by kind — text, relations, enums,
// numbers, dates, switches, the lifecycle, files, JSON. Structural, not named.
function formFields(res) {
  const tf = titleField(res);
  const kind = (f) => {
    if (tf && f.key === tf.key) return 0;
    if (f.transitions) return 8;
    if (f.file) return 9;
    if (f.type === 'object' || f.json) return 10;
    if (f.required) return 1;
    if (f.relation) return 3;
    if (f.enum) return 4;
    if (f.type === 'integer' || f.type === 'number') return 5;
    if (f.format === 'date-time') return 6;
    if (f.type === 'boolean') return 7;
    return 2;
  };
  return res.fields.filter((f) => !f.readOnly && !f.auto).map((f, i) => [f, i]).sort((a, b) => kind(a[0]) - kind(b[0]) || a[1] - b[1]).map(([f]) => f);
}

function paintField(key, msg) {
  const wrap = document.querySelector(`.f[data-f="${key}"]`);
  if (!wrap) return false;
  wrap.classList.add('field-err');
  wrap.querySelector('.ferr').textContent = msg;
  return true;
}
function deepEq(a, b) { return JSON.stringify(a ?? null) === JSON.stringify(b ?? null); }
function refreshNavCount(res) {
  const li = document.querySelector(`#menu li[data-res="${res.name}"] .count`);
  if (li) li.textContent = probe[res.name]?.total ?? '';
}

async function controlHTML(f, val, editing, readonly) {
  const kind = controlFor(f);
  const v = val ?? '';
  const id = `c-${f.key}`;
  const dis = readonly ? ' disabled' : '';
  // Native `required` on CREATE only, and only when the engine cannot fill the
  // field itself (a `required` field WITH a schema default is satisfiable by
  // omission — the contract's `default` keyword says so). The browser blocks
  // the empty submit with a pointed message; a partially-filled submit still
  // exercises the engine's 422 (painted below).
  const req = !editing && f.required && f.default == null ? ' required' : '';
  const lim = `${f.maxLength ? ` maxlength="${f.maxLength}"` : ''}${f.minimum != null ? ` min="${f.minimum}"` : ''}${f.maximum != null ? ` max="${f.maximum}"` : ''}`;
  const help = (txt) => (txt ? `<span class="help">${txt}</span>` : '');
  switch (kind) {
    case 'state': {
      // rule 5: only legal moves. Create → initial states; edit → current +
      // its transitions; terminal → read-only.
      const chip = (s, checked) => `<label><input type="radio" name="${f.key}" value="${esc(s)}"${checked ? ' checked' : ''}${dis}>${stateChip(f, s)}</label>`;
      if (!editing) {
        const init = f.initialStates ?? [];
        return `<div class="chips">${init.map((s, i) => chip(s, i === 0)).join('')}</div>`;
      }
      const nexts = f.transitions?.[v] ?? [];
      if (nexts.length === 0) return `<div class="ro">${stateChip(f, v)}<span>${ICON.lock} ${t('form.terminal')}</span></div><input type="hidden" name="${f.key}" value="${esc(v)}" data-locked="1">`;
      return `<div class="chips">${chip(v, true)}<span class="arrow">${ICON.right}</span>${nexts.map((s) => chip(s, false)).join('')}</div>`;
    }
    case 'select': {
      const opts = ['', ...(f.enum ?? [])].map((s) =>
        `<option value="${esc(s)}"${s === v ? ' selected' : ''}>${esc(s || t('form.none'))}</option>`).join('');
      return `<select id="${id}" name="${f.key}"${req}${dis}>${opts}</select>`;
    }
    case 'relation': {
      // §7: send the REFERENCED column's value, never blindly the row id.
      const m = await loadRelLabels(f);
      const rows = m.rows ?? [];
      const opts = [`<option value="">${t('form.none')}</option>`, ...rows.map((r) =>
        `<option value="${esc(r[f.references])}"${String(r[f.references]) === String(v) ? ' selected' : ''}>${esc(rowLabel(r, f.references))}</option>`)].join('');
      return `<select id="${id}" name="${f.key}" data-refcol="${esc(f.references)}"${req}${dis}>${opts}</select>`;
    }
    case 'file': {
      const policy = [f.accept ? t('file.accepts', { list: [].concat(f.accept).join(', ') }) : '',
        f.maxBytes ? t('file.max', { mb: (f.maxBytes / 1048576).toFixed(1) }) : ''].filter(Boolean).join(' · ');
      const image = [].concat(f.accept ?? []).some((a) => String(a).startsWith('image'));
      return `<div class="filewrap" data-image="${image ? '1' : ''}">
        <div class="pick">
          <label class="btn btn-sm" for="${id}-pick">${ICON.upload}${v ? t('file.change') : t('file.choose')}</label>
          <input id="${id}-pick" type="file" data-for="${f.key}"${f.accept ? ` accept="${esc([].concat(f.accept).map((a) => (a === 'pdf' ? 'application/pdf' : a.includes('/') ? a : a + '/*')).join(','))}"` : ''}${dis}>
          <input type="hidden" name="${f.key}" value="${esc(v)}">
          <span class="fname">${v ? t('file.attached', { id: esc(String(v).slice(0, 8)) }) : ''}</span>
          ${v && !readonly ? `<button type="button" class="btn btn-ghost btn-sm file-clear" data-for="${f.key}">${ICON.x}${t('file.remove')}</button>` : ''}
        </div>
        <div class="preview-slot" data-id="${esc(v)}"></div>
        ${help(esc(policy))}
      </div>`;
    }
    case 'checkbox': return `<label class="switch"><input id="${id}" type="checkbox" name="${f.key}"${val ? ' checked' : ''}${dis}><span class="track"></span><span class="lbl">${val ? t('bool.yes') : t('bool.no')}</span></label>`;
    case 'json': return `<textarea id="${id}" name="${f.key}" class="mono" data-json="1"${dis}>${esc(v === '' ? '' : JSON.stringify(v, null, 2))}</textarea>${help(t('form.jsonHelp'))}`;
    case 'textarea': return `<textarea id="${id}" name="${f.key}"${lim}${req}${dis}>${esc(v)}</textarea>`;
    case 'textarea-short': return `<textarea id="${id}" name="${f.key}" class="short"${req}${dis}>${esc(v)}</textarea>`;
    case 'datetime-local': return `<input id="${id}" type="datetime-local" name="${f.key}" value="${esc(toLocalInput(v))}"${req}${dis}>`;
    case 'number': return `<input id="${id}" type="number" name="${f.key}" value="${esc(v)}"${lim}${req}${dis} inputmode="${f.type === 'integer' ? 'numeric' : 'decimal'}"${f.type === 'integer' ? '' : ' step="any"'}>${isMoney(f) ? help(esc(lang === 'es' ? 'en centavos' : 'in cents')) : ''}`;
    default: return `<input id="${id}" type="${kind}" name="${f.key}" value="${esc(v)}"${lim}${req}${dis}>`;
  }
}

function toLocalInput(v) {
  if (!v) return '';
  const d = new Date(v);
  if (isNaN(d)) return String(v).slice(0, 16);
  const p = (n) => String(n).padStart(2, '0');
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())}T${p(d.getHours())}:${p(d.getMinutes())}`;
}

function wireFileInputs() {
  document.querySelectorAll('.switch input').forEach((inp) => inp.onchange = () => { inp.parentElement.querySelector('.lbl').textContent = inp.checked ? t('bool.yes') : t('bool.no'); });
  document.querySelectorAll('.file-clear').forEach((b) => b.onclick = () => {
    const wrap = b.closest('.filewrap');
    wrap.querySelector('input[type=hidden]').value = '';
    wrap.querySelector('.fname').textContent = '';
    wrap.querySelector('.preview-slot').innerHTML = '';
    b.remove();
  });
  document.querySelectorAll('.filewrap .preview-slot[data-id]').forEach(async (slot) => {
    const id = slot.dataset.id, wrap = slot.closest('.filewrap');
    if (!id || !wrap.dataset.image || demo) return;
    // An <img> cannot send Authorization: display goes through the signed URL.
    try { const u = await api(`/api/files/${id}/url`); slot.innerHTML = `<img class="preview" src="${esc(u.url)}" alt="">`; } catch { /* no preview */ }
  });
  document.querySelectorAll('input[type=file][data-for]').forEach((inp) => {
    inp.onchange = async () => {
      const file = inp.files[0];
      if (!file) return;
      const wrap = inp.closest('.filewrap');
      const name = wrap.querySelector('.fname');
      name.innerHTML = `<span class="muted">${esc(file.name)} · ${t('file.uploading')}</span>`;
      const fd = new FormData();
      fd.append('file', file);
      try {
        const r = await api('/api/files', { method: 'POST', body: fd });
        wrap.querySelector(`input[type=hidden][name="${inp.dataset.for}"]`).value = r.file_id;
        name.innerHTML = `${esc(file.name)} <span class="ok">${t('file.uploaded')}</span>`;
        if (wrap.dataset.image && file.type.startsWith('image/')) wrap.querySelector('.preview-slot').innerHTML = `<img class="preview" src="${URL.createObjectURL(file)}" alt="">`;
      } catch (e) {
        name.innerHTML = `<span class="err">${esc(e.message)}</span>`;
      }
    };
  });
}

function readControl(f) {
  const el = document.querySelector(`#gform [name="${f.key}"]:not([type=radio])`) ?? document.querySelector(`#gform [name="${f.key}"]:checked`);
  if (!el) return null;
  if (el.dataset.locked) return el.value;                   // terminal state: unchanged
  if (el.type === 'checkbox') return el.checked;
  const v = el.value;
  if (v === '') return '';
  if (el.dataset.json) { try { return JSON.parse(v); } catch { throw new Error(t('form.jsonBad')); } }
  if (f.type === 'integer') return parseInt(v, 10);           // rule 2: JSON numbers
  if (f.type === 'number') return parseFloat(v);
  if (f.format === 'date-time') return new Date(v).toISOString();
  return v;
}

// ── toasts ───────────────────────────────────────────────────────────────────
function toast(msg, kind = 'ok') {
  const box = $('#toasts');
  if (!box) return;
  const el = document.createElement('div');
  el.className = `toast ${kind}`;
  el.innerHTML = `${kind === 'err' ? ICON.alert : kind === 'info' ? ICON.info : ICON.check}<span>${esc(msg)}</span>`;
  box.appendChild(el);
  setTimeout(() => { el.classList.add('out'); setTimeout(() => el.remove(), 220); }, kind === 'err' ? 5200 : 3200);
}

function esc(s) {
  return String(s ?? '').replace(/[&<>"']/g, (c) =>
    ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]));
}

function showFatal(e) {
  $('#app').innerHTML = `<div class="fatal"><div class="card"><div class="banner err">${ICON.alert}<div>${t('boot.failed', { msg: esc(e.message) })}</div></div><button id="fatal-back" class="btn">${t('nav.logout')}</button></div></div>`;
  $('#fatal-back').onclick = () => { token = null; renderLogin(); };
}

applyTheme();
fetch('./ui-config.json').then((r) => (r.ok ? r.json() : {})).then((c) => { uiConfig = c ?? {}; }).catch(() => {})
  .finally(() => renderLogin());
