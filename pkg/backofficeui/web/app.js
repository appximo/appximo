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
import { loadContract, controlFor, isTerminal, rowLabel, titleField, titleFields, namePref } from './contract.js';
import { t, lang, setLang } from './i18n.js';

const $ = (sel) => document.querySelector(sel);
const PER_DEFAULT = 15;            // rows per list page (the default stays 15 — APP-PODER-S1)
const PER_CHOICES = [15, 25, 50, 100, 250]; // the selector; 250 is the ceiling — half a million rows
                                   // in a browser measures the browser, not the engine
const BOARD_MAX = 100;             // rows the board loads (one request, the API cap)
const PER_KEY = (res) => `appximo.app.per.${res}`;
let token = null;
let user = null;                   // {email, role} from the login response / claims
let contract = null;
let current = null;                // selected resource (null = home)
let listState = {};                // per resource: {sort, order, search, filters, view, page, per, total, pages, rows, timing}
let lastCall = null;               // {ms, server:{query, app}} of the most recent api() call (Server-Timing + round trip)
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
  if (!st || (st.page ?? 1) === 1) {
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
  const t0 = performance.now();
  try {
    res = await fetch(path, { method, headers, body });
  } catch {
    throw new ApiError(0, null);
  }
  // The engine publishes its own stage durations (Server-Timing: query;dur=…,
  // app;dur=…) — the honest "how long the query took", independent of how many
  // rows get painted; the round trip is the client's view of the same request.
  lastCall = { ms: performance.now() - t0, server: parseServerTiming(res.headers.get('server-timing')) };
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

function parseServerTiming(h) {
  const out = {};
  for (const part of String(h ?? '').split(',')) {
    const m = part.trim().match(/^([\w-]+)(?:;.*?dur=([\d.]+))?/);
    if (m) out[m[1]] = m[2] !== undefined ? Number(m[2]) : true;   // `cache;desc="hit"` carries no duration: no query ran
  }
  return out;
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
  left: svg('<path d="m15 18-6-6 6-6"/>'),
  first: svg('<path d="m11 18-6-6 6-6M18 18l-6-6 6-6"/>'),
  last: svg('<path d="m13 18 6-6-6-6M6 18l6-6-6-6"/>'),
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
  columns: svg('<rect x="3" y="4" width="18" height="16" rx="2"/><path d="M9 4v16M15 4v16"/>'),
  bookmark: svg('<path d="M6 3h12a1 1 0 0 1 1 1v17l-7-4-7 4V4a1 1 0 0 1 1-1Z"/>'),
  link: svg('<path d="M10 13a5 5 0 0 0 7 0l3-3a5 5 0 0 0-7-7l-1 1"/><path d="M14 11a5 5 0 0 0-7 0l-3 3a5 5 0 0 0 7 7l1-1"/>'),
  download: svg('<path d="M12 4v12m0 0 4-4m-4 4-4-4M4 20h16"/>'),
  checkbox: svg('<rect x="4" y="4" width="16" height="16" rx="3"/><path d="m8 12 3 3 5-6"/>'),
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
  // In batches of 4, not all at once: a 14-resource schema probed in one burst
  // over a public link tripped the engine's per-tenant circuit breaker (503,
  // Retry-After 8) on a small box — seen from outside on the tiendita.
  const queue = [...contract.resources];
  await Promise.all(Array.from({ length: 4 }, async () => {
    for (let r = queue.shift(); r; r = queue.shift()) {
      try {
        const res = await api(`/api/${r.name}?per_page=1&count=true`);
        probe[r.name] = { total: res.meta?.total ?? 0 };
      } catch (e) {
        probe[r.name] = e.status === 403 ? { denied: true } : { error: true };
      }
    }
  }));
  current = null;
  renderShell();
  if (!openFromHash()) renderHome();
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
  $('#menu-top li').onclick = () => { closeDrawer(); goHome(); };
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
// Every column a list may show, in the panel's structural order (the default
// picks the first five of these). The column picker offers exactly this list.
function allColumnsFor(res) {
  const pref = namePref;
  const kind = (f) => {
    if (f.transitions) return 1;
    if (f.enum) return 2;
    if (f.relation) return 3;
    if (f.type === 'integer' || f.type === 'number') return 4;
    if (f.format === 'date-time') return 5;
    if (f.type === 'boolean') return 6;
    if (f.type === 'string' && !f.maxLength && !f.format) return 7;
    return 0;
  };
  const tf = titleField(res);
  return res.fields
    .filter((f) => f.key !== 'id' && !f.file && f.type !== 'object' && !f.json && (f.format !== 'uuid' || f.relation))
    .sort((a, b) => {
      if (tf && (a.key === tf.key || b.key === tf.key)) return a.key === tf.key ? -1 : 1;
      const pa = pref(a.key), pb = pref(b.key);
      if (pa !== pb) return pa - pb;
      const ka = kind(a), kb = kind(b);
      if (ka !== kb) return ka - kb;
      if (a.auto !== b.auto) return a.auto ? 1 : -1;
      return 0;
    });
}
function columnsFor(res, st = null) {
  const chosen = st?.cols;
  if (Array.isArray(chosen) && chosen.length) {
    const all = allColumnsFor(res);
    const picked = all.filter((f) => chosen.includes(f.key));
    if (picked.length) return picked;
  }
  return defaultColumnsFor(res);
}
function defaultColumnsFor(res) {
  const pref = namePref;
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
  const tf = titleField(res);
  const cols = allColumnsFor(res).filter((f) => !f.auto).slice(0, 5);
  const auto = res.fields.find((f) => f.auto && f.format === 'date-time');
  if (cols.length < 5 && auto) cols.push(auto);
  void tf; void pref; void kind;
  return cols;
}
function filterFields(res) {
  return res.fields.filter((f) => f.transitions || f.enum || f.type === 'boolean').slice(0, 4);
}

async function selectResource(name) {
  current = contract.byName[name];
  listState[name] = listState[name] ?? { sort: null, order: 'asc', search: '', filters: {}, view: 'list', page: 1, per: perFor(name), total: null, pages: null };
  markActive();
  const st = listState[name];
  st.sel = st.sel ?? new Set();
  if (st.view === 'board' && current.stateField) await renderBoard();
  else await renderList();
}
function goHome() { current = null; markActive(); if (window.location.hash) { syncingHash = true; history.replaceState(null, '', window.location.pathname); syncingHash = false; } renderHome(); }

// ── saved views (APP-PODER-S1): columns, filters, sort, search, page size —
// browser state only (localStorage), zero engine state. The CURRENT view also
// lives in the URL hash, so a list is shareable by link.
const VIEWS_KEY = (res) => `appximo.app.views.${res}`;
function viewsFor(name) { try { return JSON.parse(store.get(VIEWS_KEY(name)) ?? '[]'); } catch { return []; } }
function saveViews(name, views) { store.set(VIEWS_KEY(name), JSON.stringify(views)); }
function viewOf(st) { return { cols: st.cols ?? null, filters: { ...st.filters }, sort: st.sort, order: st.order, search: st.search, per: st.per }; }
function applyView(st, v) { st.cols = v.cols ?? null; st.filters = { ...(v.filters ?? {}) }; st.sort = v.sort ?? null; st.order = v.order ?? 'asc'; st.search = v.search ?? ''; st.per = PER_CHOICES.includes(v.per) ? v.per : st.per; st.page = 1; st.total = null; st.pages = null; }
let syncingHash = false;
function stateToHash(res, st) {
  const q = new URLSearchParams();
  if (st.search) q.set('q', st.search);
  for (const [k, v] of Object.entries(st.filters)) q.set(`f.${k}`, v);
  if (st.sort) { q.set('sort', st.sort); q.set('order', st.order); }
  if (st.per && st.per !== PER_DEFAULT) q.set('per', String(st.per));
  if (st.page > 1) q.set('page', String(st.page));
  if (Array.isArray(st.cols) && st.cols.length) q.set('cols', st.cols.join(','));
  if (st.view === 'board') q.set('view', 'board');
  const h = `#/${res.name}${q.toString() ? '?' + q.toString() : ''}`;
  if (window.location.hash !== h) { syncingHash = true; history.replaceState(null, '', h); syncingHash = false; }
}
function hashToState(st, qs) {
  const q = new URLSearchParams(qs);
  st.search = q.get('q') ?? '';
  st.filters = {};
  for (const [k, v] of q.entries()) if (k.startsWith('f.')) st.filters[k.slice(2)] = v;
  st.sort = q.get('sort'); st.order = q.get('order') === 'desc' ? 'desc' : 'asc';
  const per = Number(q.get('per')); if (PER_CHOICES.includes(per)) st.per = per;
  st.page = Math.max(1, Number(q.get('page')) || 1);
  st.cols = q.get('cols') ? q.get('cols').split(',').filter(Boolean) : null;
  st.view = q.get('view') === 'board' ? 'board' : 'list';
  st.total = null; st.pages = null;
}
function openFromHash() {
  const m = window.location.hash.match(/^#\/([a-z_]+)(?:\/([0-9a-f-]{36}))?(?:\?(.*))?$/);
  if (!m || !contract?.byName[m[1]] || probe[m[1]]?.denied) return false;
  const [, name, id, qs] = m;
  listState[name] = listState[name] ?? { sort: null, order: 'asc', search: '', filters: {}, view: 'list', page: 1, per: perFor(name), total: null, pages: null };
  if (qs != null) hashToState(listState[name], qs);
  if (id) { api(`/api/${name}/${id}`).then((row) => renderDetail(contract.byName[name], row)).catch(() => selectResource(name)); return true; }
  selectResource(name);
  return true;
}
window.addEventListener('hashchange', () => { if (!syncingHash && contract) { if (!openFromHash() && !window.location.hash) { current = null; markActive(); renderHome(); } } });

function perFor(name) {
  const v = Number(store.get(PER_KEY(name)));
  return PER_CHOICES.includes(v) ? v : PER_DEFAULT;
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
  const pinned = Object.entries(st.filters).filter(([k]) => !ff.some((f) => f.key === k)).map(([k, v]) => {
    const f = res.fields.find((x) => x.key === k);
    const shown = f?.relation ? relLabel(f, v) : String(v);
    return `<span class="chip pin" title="${esc(k)} = ${esc(String(v))}">${esc(f?.label ?? k)}: ${esc(shown)} <button class="unpin" data-k="${esc(k)}" aria-label="${t('list.clearFilters')}">${ICON.x}</button></span>`;
  }).join('');
  const seg = res.stateField ? `<div class="seg" role="tablist">
      <button class="${st.view !== 'board' ? 'on' : ''}" data-view="list">${ICON.list}${t('view.list')}</button>
      <button class="${st.view === 'board' ? 'on' : ''}" data-view="board">${ICON.board}${t('view.board')}</button></div>` : '';
  const views = viewsFor(res.name);
  const viewsMenu = `<div class="menu-wrap"><button id="views-btn" class="btn btn-ghost btn-sm" aria-haspopup="true">${ICON.bookmark}<span class="txt">${t('views.title')}</span>${views.length ? `<span class="count">${views.length}</span>` : ''}</button>
    <div class="popover" id="views-pop" hidden>
      ${views.length ? `<ul class="plist">${views.map((v, i) => `<li><button class="apply-view" data-i="${i}">${esc(v.name)}</button><button class="btn btn-ghost btn-icon btn-sm del-view" data-i="${i}" aria-label="${t('form.delete')}">${ICON.x}</button></li>`).join('')}</ul>` : `<p class="dim">${t('views.none')}</p>`}
      <div class="row"><input id="view-name" type="text" placeholder="${t('views.namePh')}" maxlength="40"><button id="view-save" class="btn btn-sm">${t('views.save')}</button></div>
      <div class="row"><button id="view-share" class="btn btn-ghost btn-sm">${ICON.link}${t('views.share')}</button><button id="view-reset" class="btn btn-ghost btn-sm">${t('views.reset')}</button></div>
    </div></div>`;
  const all = allColumnsFor(res); const cur = new Set(columnsFor(res, st).map((c) => c.key));
  const colsMenu = st.view === 'board' ? '' : `<div class="menu-wrap"><button id="cols-btn" class="btn btn-ghost btn-sm" aria-haspopup="true">${ICON.columns}<span class="txt">${t('cols.title')}</span></button>
    <div class="popover" id="cols-pop" hidden>
      <ul class="plist cols">${all.map((f) => `<li><label><input type="checkbox" data-col="${f.key}"${cur.has(f.key) ? ' checked' : ''}><span>${esc(f.label)}</span></label></li>`).join('')}</ul>
      <div class="row"><button id="cols-reset" class="btn btn-ghost btn-sm">${t('cols.reset')}</button></div>
    </div></div>`;
  return `<div class="toolbar">
    <div class="search">${ICON.search}<input id="search" type="search" placeholder="${t('list.search')}" value="${esc(st.search)}" aria-label="${t('list.search')}"></div>
    <div class="filters">${filters}${pinned}${hasFilters ? `<button id="clear-filters" class="btn btn-ghost btn-sm">${ICON.x}${t('list.clearFilters')}</button>` : ''}</div>
    <span class="spacer"></span>
    <div class="tools">${st.view === 'board' || !(res.canEdit || res.canDelete) ? '' : `<button id="sel-page-btn" class="btn btn-ghost btn-sm">${ICON.checkbox}<span class="txt">${t('bulk.selectPage')}</span></button>`}${colsMenu}${viewsMenu}${st.view === 'board' ? '' : `<div class="menu-wrap"><button id="csv-btn" class="btn btn-ghost btn-sm" aria-haspopup="true">${ICON.download}<span class="txt">CSV</span></button>
      <div class="popover" id="csv-pop" hidden>
        <button id="csv-page" class="btn btn-sm">${t('csv.page', { n: '' }).trim()}</button>
        <button id="csv-all" class="btn btn-sm">${t('csv.all')}</button>
        <p class="dim" id="csv-hint"></p>
      </div></div>`}</div>
    ${seg}
  </div>`;
}
function togglePop(btnId, popId) {
  const b = $(btnId), p = $(popId); if (!b || !p) return;
  b.onclick = (ev) => { ev.stopPropagation(); const open = !p.hidden; document.querySelectorAll('.popover').forEach((x) => x.hidden = true); p.hidden = open; };
  p.onclick = (ev) => ev.stopPropagation();
}
document.addEventListener('click', () => document.querySelectorAll('.popover').forEach((x) => x.hidden = true));
function wireToolbar(st, rerender) {
  $('#search').onchange = (e) => { st.search = e.target.value; st.page = 1; st.total = null; rerender(); };
  document.querySelectorAll('select[data-filter]').forEach((sel) => sel.onchange = () => {
    if (sel.value === '') delete st.filters[sel.dataset.filter]; else st.filters[sel.dataset.filter] = sel.value;
    st.page = 1; st.total = null; rerender();
  });
  if ($('#clear-filters')) $('#clear-filters').onclick = () => { st.filters = {}; st.page = 1; st.total = null; rerender(); };
  document.querySelectorAll('.chip.pin .unpin').forEach((b) => b.onclick = () => { delete st.filters[b.dataset.k]; st.page = 1; st.total = null; rerender(); });
  document.querySelectorAll('.seg [data-view]').forEach((b) => b.onclick = () => {
    st.view = b.dataset.view; st.page = 1;
    if (st.view === 'board') renderBoard(); else renderList();
  });
  if ($('#new')) $('#new').onclick = () => renderForm(null);
  // columns
  togglePop('#cols-btn', '#cols-pop');
  document.querySelectorAll('#cols-pop input[data-col]').forEach((cb) => cb.onchange = () => {
    const chosen = [...document.querySelectorAll('#cols-pop input[data-col]:checked')].map((x) => x.dataset.col);
    if (chosen.length === 0) { cb.checked = true; return; }
    st.cols = chosen; stateToHash(current, st);
    if (st.view === 'list' && st.lastData) paintList(current, st, st.lastData); else rerender();
  });
  if ($('#cols-reset')) $('#cols-reset').onclick = () => { st.cols = null; rerender(); };
  // views
  togglePop('#views-btn', '#views-pop');
  const res_ = current;
  document.querySelectorAll('#views-pop .apply-view').forEach((b) => b.onclick = () => { const v = viewsFor(res_.name)[Number(b.dataset.i)]; if (v) { applyView(st, v); rerender(); } });
  document.querySelectorAll('#views-pop .del-view').forEach((b) => b.onclick = () => { const vs = viewsFor(res_.name); vs.splice(Number(b.dataset.i), 1); saveViews(res_.name, vs); rerender(); });
  if ($('#view-save')) $('#view-save').onclick = () => {
    const name = $('#view-name').value.trim(); if (!name) { $('#view-name').focus(); return; }
    const vs = viewsFor(res_.name).filter((v) => v.name !== name); vs.push({ name, ...viewOf(st) }); saveViews(res_.name, vs);
    toast(t('views.saved', { name })); rerender();
  };
  if ($('#view-share')) $('#view-share').onclick = async () => {
    const url = window.location.href;
    try { await navigator.clipboard.writeText(url); toast(t('views.copied')); } catch { window.prompt(t('views.copyManual'), url); }
  };
  if ($('#view-reset')) $('#view-reset').onclick = () => { applyView(st, { per: st.per }); st.cols = null; rerender(); };
  // CSV: exactly what is loaded, or — said before it happens — every filtered row, page by page, up to the cap
  togglePop('#csv-btn', '#csv-pop');
  if ($('#csv-btn')) $('#csv-btn').addEventListener('click', () => {
    const n = st.rows?.length ?? 0, total = st.total ?? n, pages = Math.ceil(Math.min(total, CSV_MAX_ROWS) / CSV_PAGE);
    $('#csv-page').textContent = t('csv.page', { n });
    $('#csv-all').textContent = t('csv.all');
    $('#csv-all').disabled = total <= n;
    $('#csv-hint').textContent = total > n ? t('csv.allHint', { total: new Intl.NumberFormat(locale()).format(Math.min(total, CSV_MAX_ROWS)), pages, cap: total > CSV_MAX_ROWS ? t('csv.cap', { cap: new Intl.NumberFormat(locale()).format(CSV_MAX_ROWS) }) : '' }) : t('csv.pageHint');
  });
  if ($('#csv-page')) $('#csv-page').onclick = () => { downloadCSV(res_, st, st.rows ?? []); document.querySelectorAll('.popover').forEach((x) => x.hidden = true); };
  if ($('#csv-all')) $('#csv-all').onclick = () => { document.querySelectorAll('.popover').forEach((x) => x.hidden = true); exportAllCSV(res_, st); };
}

// ── CSV (APP-PODER-S1): client-side, the visible columns + id, RFC 4180 quoting, UTF-8 BOM for spreadsheets
const CSV_PAGE = 250;            // the engine's per_page cap
const CSV_MAX_ROWS = 10000;      // the honest ceiling of a browser export; beyond it, a load test against the API is the tool
function csvOf(res, st, rows) {
  const cols = [{ key: 'id', label: 'id' }, ...columnsFor(res, st)];
  const cell = (v, f) => {
    if (v === null || v === undefined) return '';
    if (f.relation) return relLabel(f, v);
    if (typeof v === 'object') return JSON.stringify(v);
    return String(v);
  };
  const q = (x) => /[",\n\r]/.test(x) ? '"' + x.replace(/"/g, '""') + '"' : x;
  const lines = [cols.map((c) => q(c.label ?? c.key)).join(',')];
  for (const r of rows) lines.push(cols.map((c) => q(cell(r[c.key], c))).join(','));
  return '\ufeff' + lines.join('\r\n');
}
function downloadCSV(res, st, rows) {
  const blob = new Blob([csvOf(res, st, rows)], { type: 'text/csv;charset=utf-8' });
  const a = document.createElement('a');
  a.href = URL.createObjectURL(blob); a.download = `${res.name}-${new Date().toISOString().slice(0, 10)}.csv`;
  document.body.appendChild(a); a.click(); a.remove(); setTimeout(() => URL.revokeObjectURL(a.href), 2000);
  toast(t('csv.done', { n: new Intl.NumberFormat(locale()).format(rows.length) }));
}
async function exportAllCSV(res, st) {
  const total = Math.min(st.total ?? 0, CSV_MAX_ROWS), pages = Math.ceil(total / CSV_PAGE);
  const bar = progressPanel(t('csv.exporting', { total: new Intl.NumberFormat(locale()).format(total), pages }));
  const all = [];
  try {
    for (let p = 1; p <= pages; p++) {
      const q = new URLSearchParams({ per_page: String(CSV_PAGE), page: String(p) });
      if (st.search) q.set('search', st.search);
      for (const [k, v] of Object.entries(st.filters)) q.set(`filter[${k}][eq]`, v);
      if (st.sort) { q.set('sort', st.sort); q.set('order', st.order); }
      q.set('fields', listFields(res, columnsFor(res, st)));   // the exported columns only
      const d = await api(`/api/${res.name}?` + q.toString());
      all.push(...(demo ? demoMergeList(res.name, d.data, p === 1 ? { ...st, page: 1 } : { page: p }) : d.data));
      bar.set(p, pages, t('csv.progress', { p, pages, n: new Intl.NumberFormat(locale()).format(all.length) }));
      if (!d.meta?.has_next) break;
    }
    bar.done();
    downloadCSV(res, st, all.slice(0, CSV_MAX_ROWS));
  } catch (e) { bar.fail(e.message); }
}
function progressPanel(title) {
  const old = $('#progress'); if (old) old.remove();
  $('#list-banner')?.insertAdjacentHTML('beforeend', `<div class="progress" id="progress"><div class="ptitle"><span>${esc(title)}</span><button class="btn btn-ghost btn-icon btn-sm" id="progress-x" aria-label="${t('form.close')}">${ICON.x}</button></div><div class="pbar"><div class="pfill"></div></div><div class="pmsg num"></div><div class="plog"></div></div>`);
  const el = $('#progress'); if (el) { el.querySelector('.pfill').style.width = '0%'; $('#progress-x').onclick = () => el.remove(); }   // CSSOM, never a style attribute (CSP style-src 'self')
  return {
    set(i, n, msg) { const e = $('#progress'); if (!e) return; e.querySelector('.pfill').style.width = `${Math.round(100 * i / n)}%`; e.querySelector('.pmsg').textContent = msg ?? ''; },
    log(html) { $('#progress')?.querySelector('.plog').insertAdjacentHTML('beforeend', html); },
    done(msg) { const e = $('#progress'); if (!e) return; e.classList.add('ok'); e.querySelector('.pfill').style.width = '100%'; if (msg) e.querySelector('.pmsg').textContent = msg; },
    fail(msg) { const e = $('#progress'); if (!e) return; e.classList.add('err'); e.querySelector('.pmsg').textContent = msg; },
  };
}

function skeletonTable() {
  return `<div class="card list"><div class="skel-rows">${'<div class="skel-row"><div class="skel w80"></div><div class="skel pill"></div><div class="skel w60"></div><div class="skel w40"></div><div class="skel w60"></div></div>'.repeat(6)}</div></div>`;
}

// ── field selection (MOTOR-FIELDS-S1): the list asks the engine ONLY for the
// columns it will paint. `?fields=` is pushed down to the SQL SELECT, so a
// large json/text column (never a list column — see allColumnsFor) is not
// even read from disk for the page: on the migrated system this was ~940 KB
// and a p99 of 3.8 s per page of 20 that showed a title and a status. The
// footer's «consulta N ms» (Server-Timing) is where the difference shows.
// What a list row must carry besides the visible columns: the state field
// (chips, bulk moves, the board), the title candidates (row labels in the
// bulk bar, the drawer title) and the FKs of visible relation columns (they
// ARE the columns). `id` always comes back. A row that has to be WHOLE (the
// form, the detail) is re-fetched by id — see wholeRow.
function listFields(res, cols) {
  const keys = new Set(['id']);
  for (const c of cols) keys.add(c.key);
  if (res.stateField) keys.add(res.stateField.key);
  for (const f of titleFields(res).slice(0, 2)) keys.add(f.key);
  return [...keys].join(',');
}
// The fields a row needs to be LABELLED (relation targets, peek lists): the
// referenced column + the title candidates. A resource with no title
// candidate is not projected — rowLabel then falls back to scanning the row.
function labelFields(tres, refCol = 'id') {
  if (!tres) return null;
  const tf = titleFields(tres);
  if (!tf.length) return null;
  const keys = new Set(['id', refCol, ...tf.map((f) => f.key)]);
  if (tres.stateField) keys.add(tres.stateField.key);
  return [...keys].join(',');
}
// A projected list row re-fetched whole before a screen that needs every
// field (the edit form, the detail). Falls back to the row it was given (a
// demo-overlay row exists only in the browser; an offline engine).
async function wholeRow(res, row) {
  if (!row || !row.id || row.__whole) return row;
  try { const full = await api(`/api/${res.name}/${row.id}`); full.__whole = true; return full; } catch { return row; }
}

function listQuery(st) {
  // Page-numbered (OFFSET) paging on every list (APP-PODER-S1): a cursor gives
  // no page number, no "of N" and no "go to page" — the orientation a person
  // needs. `count=true` is sent only while the total is unknown (first load,
  // a changed search/filter); the engine's COUNT(*) runs over the SAME
  // filtered, RBAC-scoped set, and the total is kept across pages.
  const q = new URLSearchParams();
  q.set('per_page', String(st.per ?? PER_DEFAULT));
  if (st.page > 1) q.set('page', String(st.page));
  if (st.search) q.set('search', st.search);
  for (const [k, v] of Object.entries(st.filters)) q.set(`filter[${k}][eq]`, v);
  if (st.sort) { q.set('sort', st.sort); q.set('order', st.order); }
  if (st.total == null) q.set('count', 'true');
  if (st.res) q.set('fields', listFields(st.res, columnsFor(st.res, st)));
  return q;
}
function timingHTML(st) {
  const tm = st.timing;
  if (!tm) return '';
  const sv = tm.server ?? {};
  const q = sv.query != null ? sv.query + (typeof sv.count === 'number' ? sv.count : 0) : null;   // the page query + its COUNT(*), when one ran
  const a = sv.app;
  const fmt = (x) => new Intl.NumberFormat(locale(), { maximumFractionDigits: x < 10 ? 1 : 0 }).format(x);
  const engine = 'cache' in sv ? t('list.timingCache') : q != null ? t('list.timingQuery', { q: fmt(q) }) : (a != null ? t('list.timingApp', { a: fmt(a) }) : '');
  return `<span class="timing num" title="${t('list.timingHelp')}">${engine ? engine + ' · ' : ''}${t('list.timingTrip', { t: fmt(tm.ms) })}</span>`;
}

async function renderList() {
  const res = current, st = listState[res.name];
  st.view = 'list';
  st.res = res;
  stateToHash(res, st);
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
  st.timing = lastCall;
  if (data.meta?.total != null) { st.total = data.meta.total; st.pages = data.meta.total_pages ?? Math.max(1, Math.ceil(st.total / st.per)); }
  let rows = data.data;
  if (demo) rows = demoMergeList(res.name, rows, st);
  st.rows = rows;
  st.serverRows = data.data;
  st.lastData = data;
  await paintList(res, st, data);
}

async function paintList(res, st, data) {
  const rows = st.rows;
  const cols = columnsFor(res, st);
  const pinnedRel = Object.keys(st.filters).map((k) => res.fields.find((f) => f.key === k)).filter((f) => f?.relation);
  await Promise.all([...cols.filter((c) => c.relation), ...pinnedRel].map((c) => loadRelLabels(c)));
  await resolveMissingRelLabels(cols.filter((c) => c.relation), rows);
  if (current !== res || st.view !== 'list') return;
  document.querySelectorAll('.chip.pin .unpin').forEach((b) => {
    const f = res.fields.find((x) => x.key === b.dataset.k);
    if (f?.relation) b.previousSibling.textContent = `${f.label}: ${relLabel(f, st.filters[f.key])} `;
  });
  const ph = $('#ph-sub'); if (ph && st.total != null) ph.textContent = st.total === 1 ? t('list.totalOne') : t('list.total', { n: new Intl.NumberFormat(locale()).format(st.total) });

  const tf = titleField(res);
  const sel = st.sel ?? (st.sel = new Set());
  const bulkable = res.canEdit || res.canDelete;
  const selHead = bulkable ? `<th class="selc"><input type="checkbox" id="sel-all" aria-label="${t('bulk.selectPage')}"${rows.length && rows.every((r) => sel.has(r.id)) ? ' checked' : ''}></th>` : '';
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
    const selTd = bulkable ? `<td class="selc"><input type="checkbox" class="sel-row" data-id="${esc(row.id)}" aria-label="${t('bulk.select')}"${sel.has(row.id) ? ' checked' : ''}></td>` : '';
    return `<tr class="clickable ${sel.has(row.id) ? 'selected' : ''}" data-i="${i}">${selTd}${tds}<td class="rowact">${act}</td></tr>`;
  }).join('');
  const filtered = !!st.search || Object.keys(st.filters).length > 0;
  const emptyHTML = `<div class="empty">
      <div class="ico">${ICON.inbox}</div>
      <b>${filtered ? t('list.emptyFiltered') : t('list.empty')}</b>
      <p>${filtered ? t('list.emptyFilteredHint') : t('list.emptyHint')}</p>
      ${filtered ? `<button id="empty-clear" class="btn btn-sm">${t('list.clearFilters')}</button>` : (res.canCreate ? `<button id="empty-new" class="btn btn-primary">${ICON.plus}${t('list.createFirst')}</button>` : '')}
    </div>`;
  const pageNo = st.page;
  const canPrev = st.page > 1;
  const hasNext = st.pages != null ? st.page < st.pages : !!data.meta?.has_next;
  const nf = new Intl.NumberFormat(locale());
  const where = st.pages != null
    ? `${t('list.pageOf', { p: nf.format(pageNo), n: nf.format(st.pages) })} · ${t('list.ofTotal', { n: nf.format(rows.length), total: nf.format(st.total) })}`
    : `${t('list.page', { n: nf.format(pageNo) })} · ${t('list.showing', { n: nf.format(rows.length) })} · <em class="dim">${t('list.noTotal')}</em>`;
  const perSel = `<label class="per"><span>${t('list.perPage')}</span><select id="pg-per" class="compact" aria-label="${t('list.perPage')}">${PER_CHOICES.map((n) => `<option value="${n}"${n === st.per ? ' selected' : ''}>${n}</option>`).join('')}</select></label>`;
  const foot = `<div class="tfoot">
      <span class="num where">${where}</span>
      ${timingHTML(st)}
      <span class="pg">
        ${perSel}
        <button id="pg-first" class="btn btn-icon" ${canPrev ? '' : 'disabled'} title="${t('list.first')}" aria-label="${t('list.first')}">${ICON.first}</button>
        <button id="pg-prev" class="btn btn-icon" ${canPrev ? '' : 'disabled'} title="${t('list.prev')}" aria-label="${t('list.prev')}">${ICON.left}</button>
        <label class="goto num"><input id="pg-goto" type="number" min="1"${st.pages != null ? ` max="${st.pages}"` : ''} value="${pageNo}" aria-label="${t('list.goto')}" inputmode="numeric">${st.pages != null ? `<span>/ ${nf.format(st.pages)}</span>` : ''}</label>
        <button id="pg-next" class="btn btn-icon" ${hasNext ? '' : 'disabled'} title="${t('list.next')}" aria-label="${t('list.next')}">${ICON.right}</button>
        <button id="pg-last" class="btn btn-icon" ${st.pages != null && st.page < st.pages ? '' : 'disabled'} title="${t('list.last')}" aria-label="${t('list.last')}">${ICON.last}</button>
      </span>
    </div>`;
  $('#main').querySelector('.card.list').outerHTML = `<div class="card list reveal">
    <div id="list-banner"></div>
    ${bulkBarHTML(res, st)}
    ${rows.length === 0 ? emptyHTML : `<div class="tablewrap"><table><thead><tr>${selHead}${head}<th></th></tr></thead><tbody>${body}</tbody></table></div>`}
    ${rows.length === 0 && !canPrev ? '' : foot}
  </div>`;
  wireBulk(res, st);

  document.querySelectorAll('th[data-k]').forEach((th) => th.onclick = () => {
    const k = th.dataset.k;
    st.order = st.sort === k && st.order === 'asc' ? 'desc' : 'asc';
    st.sort = k; st.page = 1;
    renderList();
  });
  if ($('#empty-new')) $('#empty-new').onclick = () => renderForm(null);
  if ($('#empty-clear')) $('#empty-clear').onclick = () => { st.filters = {}; st.search = ''; st.total = null; renderList(); };
  const goPage = (n) => { const max = st.pages ?? Infinity; st.page = Math.min(Math.max(1, n | 0), max); renderList(); };
  if ($('#pg-next')) $('#pg-next').onclick = () => goPage(st.page + 1);
  if ($('#pg-prev')) $('#pg-prev').onclick = () => goPage(st.page - 1);
  if ($('#pg-first')) $('#pg-first').onclick = () => goPage(1);
  if ($('#pg-last')) $('#pg-last').onclick = () => goPage(st.pages ?? st.page);
  if ($('#pg-goto')) $('#pg-goto').onchange = (e) => goPage(Number(e.target.value));
  if ($('#pg-per')) $('#pg-per').onchange = (e) => { st.per = Number(e.target.value); store.set(PER_KEY(res.name), String(st.per)); st.page = 1; st.pages = st.total != null ? Math.max(1, Math.ceil(st.total / st.per)) : null; renderList(); };
  document.querySelectorAll('tbody tr').forEach((tr) => tr.onclick = (ev) => {
    if (ev.target.closest('.act-del') || ev.target.closest('.selc')) return;
    if (ev.target.closest('.act-open')) return wholeRow(res, st.rows[Number(tr.dataset.i)]).then((r) => renderForm(r));
    wholeRow(res, st.rows[Number(tr.dataset.i)]).then((r) => renderDetail(res, r));
  });
  document.querySelectorAll('.act-del').forEach((b) => b.onclick = (ev) => {
    ev.stopPropagation();
    wholeRow(res, st.rows[Number(b.dataset.i)]).then((r) => renderForm(r, { confirmDelete: true }));
  });
}

// ── bulk actions (APP-PODER-S1): a selection, then a transition or a delete
// through /api/transaction — batches of at most TX_MAX ops (the engine's cap),
// progress visible, and partial failure NAMED: a batch is atomic, so a failed
// batch is retried row by row to isolate exactly which rows failed and why,
// while the others go through. In demo mode nothing leaves the browser: the
// overlay is applied row by row (the role's RBAC would answer 403 anyway).
const TX_MAX = 100;
function bulkBarHTML(res, st) {
  const n = st.sel?.size ?? 0;
  if (!n) return '';
  const sf = res.stateField;
  const rowsSel = (st.rows ?? []).filter((r) => st.sel.has(r.id));
  const targets = sf ? [...new Set(rowsSel.flatMap((r) => sf.transitions?.[r[sf.key]] ?? []))] : [];
  return `<div class="bulkbar">
    <span class="num"><b>${n}</b> ${t('bulk.selected')}</span>
    ${sf && res.canEdit ? `<select id="bulk-state" class="compact" aria-label="${t('bulk.move')}"><option value="">${t('bulk.move')}</option>${targets.map((to) => `<option value="${esc(to)}">${esc(to.replace(/_/g, ' '))}</option>`).join('')}${targets.length ? '' : `<option disabled>${t('bulk.noMoves')}</option>`}</select>` : ''}
    ${res.canDelete ? `<button id="bulk-del" class="btn btn-danger btn-sm">${ICON.trash}${t('form.delete')}</button>` : ''}
    <button id="bulk-csv" class="btn btn-sm">${ICON.download}CSV</button>
    <span class="spacer"></span>
    <button id="bulk-clear" class="btn btn-ghost btn-sm">${ICON.x}${t('bulk.clear')}</button>
  </div>`;
}
function wireBulk(res, st) {
  const repaint = () => paintList(res, st, st.lastData);
  const all = $('#sel-all'); if (all) all.onchange = () => { for (const r of st.rows) all.checked ? st.sel.add(r.id) : st.sel.delete(r.id); repaint(); };
  const sp = $('#sel-page-btn'); if (sp) sp.onclick = () => { const every = st.rows.every((r) => st.sel.has(r.id)); for (const r of st.rows) every ? st.sel.delete(r.id) : st.sel.add(r.id); repaint(); };
  document.querySelectorAll('.sel-row').forEach((cb) => cb.onchange = () => { cb.checked ? st.sel.add(cb.dataset.id) : st.sel.delete(cb.dataset.id); repaint(); });
  if ($('#bulk-clear')) $('#bulk-clear').onclick = () => { st.sel.clear(); repaint(); };
  if ($('#bulk-csv')) $('#bulk-csv').onclick = () => downloadCSV(res, st, (st.rows ?? []).filter((r) => st.sel.has(r.id)));
  if ($('#bulk-state')) $('#bulk-state').onchange = (e) => { const to = e.target.value; if (to) bulkConfirm(res, st, 'move', to); };
  if ($('#bulk-del')) $('#bulk-del').onclick = () => bulkConfirm(res, st, 'delete');
}
function bulkConfirm(res, st, kind, to = null) {
  const ids = [...st.sel];
  const rowsSel = (st.rows ?? []).filter((r) => st.sel.has(r.id));
  const labels = rowsSel.map((r) => rowLabel(r, 'id', res) || r.id);
  const offPage = ids.length - rowsSel.length;
  const sf = res.stateField;
  const movable = kind === 'move' ? rowsSel.filter((r) => (sf.transitions?.[r[sf.key]] ?? []).includes(to)) : rowsSel;
  const skipped = kind === 'move' ? rowsSel.length - movable.length : 0;
  const nf = new Intl.NumberFormat(locale());
  const title = kind === 'delete' ? t('bulk.confirmDelete', { n: nf.format(ids.length) }) : t('bulk.confirmMove', { n: nf.format(movable.length), to: to.replace(/_/g, ' ') });
  $('#list-banner').innerHTML = `<div class="confirm bulk-confirm ${kind === 'delete' ? 'danger' : ''}">
    <b>${esc(title)}</b>
    <ul class="names">${labels.slice(0, 10).map((l) => `<li>${esc(String(l))}</li>`).join('')}${labels.length > 10 ? `<li class="dim">${t('bulk.andMore', { n: nf.format(labels.length - 10) })}</li>` : ''}</ul>
    ${offPage > 0 ? `<span class="muted hint">${t('bulk.offPage', { n: nf.format(offPage) })}</span>` : ''}
    ${skipped > 0 ? `<span class="muted hint">${t('bulk.skipped', { n: nf.format(skipped), to: to.replace(/_/g, ' ') })}</span>` : ''}
    <span class="muted hint">${t('bulk.batches', { n: nf.format(Math.ceil((kind === 'move' ? movable.length : ids.length) / TX_MAX)), max: TX_MAX })}${kind === 'delete' ? ' · ' + t('form.confirmHint') : ''}</span>
    <div class="row"><button type="button" id="bulk-no" class="btn btn-sm">${t('form.cancel')}</button><button type="button" id="bulk-yes" class="btn ${kind === 'delete' ? 'btn-danger solid' : 'btn-primary'} btn-sm">${kind === 'delete' ? ICON.trash : ICON.check}${kind === 'delete' ? t('bulk.yesDelete', { n: nf.format(ids.length) }) : t('bulk.yesMove', { n: nf.format(movable.length) })}</button></div>
  </div>`;
  $('#list-banner').scrollIntoView({ block: 'nearest' });
  $('#bulk-no').onclick = () => { $('#list-banner').innerHTML = ''; if ($('#bulk-state')) $('#bulk-state').value = ''; };
  $('#bulk-yes').onclick = () => {
    $('#list-banner').innerHTML = '';
    const targetsIds = kind === 'move' ? movable.map((r) => r.id) : ids;
    if (kind === 'move' && offPage > 0) toast(t('bulk.offPageSkipped', { n: nf.format(offPage) }), 'info');
    bulkRun(res, st, kind, targetsIds, to);
  };
}
async function bulkRun(res, st, kind, ids, to) {
  const sf = res.stateField;
  const opOf = (id) => kind === 'delete' ? { op: 'delete', resource: res.name, id } : { op: 'update', resource: res.name, id, data: { [sf.key]: to } };
  const nf = new Intl.NumberFormat(locale());
  const batches = []; for (let i = 0; i < ids.length; i += TX_MAX) batches.push(ids.slice(i, i + TX_MAX));
  const bar = progressPanel(kind === 'delete' ? t('bulk.deleting', { n: nf.format(ids.length) }) : t('bulk.moving', { n: nf.format(ids.length), to: to.replace(/_/g, ' ') }));
  const failed = []; let okCount = 0;
  const labelOf = (id) => { const r = (st.rows ?? []).find((x) => x.id === id); return r ? (rowLabel(r, 'id', res) || id) : id.slice(0, 8); };
  const runOne = async (id) => {
    if (demo) { demoWrite(`/api/${res.name}/${id}`, { method: kind === 'delete' ? 'DELETE' : 'PATCH', body: kind === 'delete' ? undefined : { [sf.key]: to } }); return; }
    await api('/api/transaction', { method: 'POST', body: { operations: [opOf(id)] } });
  };
  for (let b = 0; b < batches.length; b++) {
    const batch = batches[b];
    let batchOK = false;
    if (!demo) {
      try { await api('/api/transaction', { method: 'POST', body: { operations: batch.map(opOf) } }); batchOK = true; }
      catch (e) {
        if (e.status === 403) { failed.push(...batch.map((id) => ({ id, msg: e.message }))); bar.set(b + 1, batches.length); continue; }
        // the batch is atomic: nothing of it applied. Retry row by row so the failure is NAMED and the rest goes through.
        bar.log(`<div class="pline warn">${ICON.alert}${esc(t('bulk.batchFailed', { b: b + 1, msg: e.message }))}</div>`);
      }
    }
    if (batchOK) okCount += batch.length;
    else for (const id of batch) {
      try { await runOne(id); okCount++; }
      catch (e) { failed.push({ id, msg: e.fields?.length ? e.fields.map((f) => `${f.field}: ${f.message ?? f.rule}`).join('; ') : e.message }); }
    }
    bar.set(b + 1, batches.length, t('bulk.progress', { b: b + 1, n: batches.length, ok: nf.format(okCount), fail: nf.format(failed.length) }));
  }
  for (const f of failed) bar.log(`<div class="pline err">${ICON.alert}<b>${esc(String(labelOf(f.id)))}</b> — ${esc(f.msg)}</div>`);
  if (failed.length) bar.fail(t('bulk.partial', { ok: nf.format(okCount), fail: nf.format(failed.length), total: nf.format(ids.length) }));
  else bar.done(t('bulk.allOk', { ok: nf.format(okCount) }));
  toast(failed.length ? t('bulk.partial', { ok: nf.format(okCount), fail: nf.format(failed.length), total: nf.format(ids.length) }) : t('bulk.allOk', { ok: nf.format(okCount) }), failed.length ? 'err' : 'ok');
  // keep the failed rows selected (so they can be retried), refresh the data
  st.sel = new Set(failed.map((f) => f.id));
  if (kind === 'delete') { probe[res.name] = { total: Math.max(0, (probe[res.name]?.total ?? okCount) - okCount) }; refreshNavCount(res); }
  invalidateRel(res.name);
  st.total = null;
  const keep = $('#progress');   // move the node (never re-serialize it: a serialized CSSOM width becomes a style attribute the CSP blocks)
  await renderList();
  if (keep) { $('#list-banner')?.appendChild(keep); keep.querySelector('#progress-x').onclick = () => keep.remove(); }
}

// ── relation labels: one fetch per target, cached for the session ───────────
async function loadRelLabels(f) {
  const target = f.relation;
  if (relLabels[target] instanceof Map) return relLabels[target];
  if (relLabels[target]) return relLabels[target];           // in flight
  relLabels[target] = (async () => {
    let rows = [];
    const tres = contract.byName[target] ?? null;
    const lf = labelFields(tres, f.references);
    try { rows = (await api(`/api/${target}?per_page=100${lf ? '&fields=' + lf : ''}`)).data; } catch { rows = []; }
    if (demo) rows = demoMergeList(target, rows, null);
    const m = new Map();
    for (const r of rows) m.set(String(r[f.references] ?? r.id), rowLabel(r, f.references, tres));
    m.rows = rows;
    return m;
  })();
  relLabels[target] = await relLabels[target];              // the resolved Map, not the promise
  return relLabels[target];
}
// A target past the 100-row page (the Part-F case) has a PARTIAL label map:
// the values this page shows that the map lacks are looked up individually,
// bounded, so a big target still names its rows in the list.
const REL_LOOKUP_MAX = 40;
async function resolveMissingRelLabels(relCols, rows) {
  const jobs = [];
  for (const f of relCols) {
    const m = relLabels[f.relation];
    if (!(m instanceof Map) || (m.rows?.length ?? 0) < REL_SELECT_MAX) continue;
    const missing = [...new Set(rows.map((r) => r[f.key]).filter((v) => v != null && v !== '' && !m.has(String(v))))].slice(0, REL_LOOKUP_MAX);
    const tres = contract.byName[f.relation] ?? null;
    const lf = labelFields(tres, f.references);
    for (const v of missing) jobs.push(api(`/api/${f.relation}?filter[${f.references}][eq]=${encodeURIComponent(v)}&per_page=1${lf ? '&fields=' + lf : ''}`)
      .then((r) => { const row = r.data?.[0]; if (row) m.set(String(v), rowLabel(row, f.references, tres)); }).catch(() => {}));
  }
  if (jobs.length) await Promise.all(jobs);
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
  stateToHash(res, st);
  $('#main').innerHTML = `<div class="reveal">${pageHeader(res, st)}${toolbarHTML(res, st)}<div class="board" id="board">${sf.states.map(() => `<div class="col"><header><span class="skel pill"></span></header><div class="skel card-skel"></div><div class="skel card-skel"></div></div>`).join('')}</div></div>`;
  wireToolbar(st, renderBoard);
  const q = new URLSearchParams({ per_page: String(BOARD_MAX) });
  if (st.search) q.set('search', st.search);
  for (const [k, v] of Object.entries(st.filters)) q.set(`filter[${k}][eq]`, v);
  if (st.total == null) q.set('count', 'true');
  q.set('fields', listFields(res, columnsFor(res).filter((c) => c.key !== sf.key).slice(0, 3)));
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
  if (demo) rows = demoMergeList(res.name, rows, { ...st, page: 1 });
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
      const title = rowLabel(row, 'id', res);
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
    card.onclick = (ev) => { if (ev.target.closest('.mv')) return; wholeRow(res, rowById(card.dataset.id)).then((r) => renderForm(r)); };
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

// ── detail (APP-PODER-S1): one row, its relations both ways, its lifecycle,
// its files — everything derived from the contract. Parents resolve through
// the published subroute (`/api/{res}/{id}/{seg}`) or a filtered lookup on the
// referenced column; children are every resource whose FK points here, as a
// filtered list. Each block fetches on its own and degrades to an inline
// notice: a row that cannot be resolved never breaks the screen. No
// `?include=` is used, so a legacy non-JSON text in a json column (pre-ADR-028)
// cannot break the read either — it shows as text with a badge.
const detailStack = [];   // parent → child navigation, for the back link

async function renderDetail(res, row, opts = {}) {
  if (!row) return;
  current = res;
  listState[res.name] = listState[res.name] ?? { sort: null, order: 'asc', search: '', filters: {}, view: 'list', page: 1, per: perFor(res.name), total: null, pages: null };
  markActive();
  if (opts.push) detailStack.push(opts.push);
  { const h = `#/${res.name}/${row.id}`; if (window.location.hash !== h) { syncingHash = true; history.replaceState(null, '', h); syncingHash = false; } }
  const sf = res.stateField;
  const title = rowLabel(row, 'id', res) || res.title;
  const fieldsOf = (pred) => res.fields.filter(pred);
  const plain = fieldsOf((f) => f.key !== 'id' && !f.file && !f.relation && !(f.json || f.type === 'object') && !(sf && f.key === sf.key))
    .sort((a, b) => (a.auto ? 1 : 0) - (b.auto ? 1 : 0));   // engine timestamps last
  const rels = fieldsOf((f) => f.relation);
  const files = fieldsOf((f) => f.file);
  const jsons = fieldsOf((f) => f.json || f.type === 'object');
  const back = detailStack.length ? `<button id="d-back" class="btn btn-ghost btn-sm">${ICON.left}${t('detail.back')}</button>` : `<button id="d-back" class="btn btn-ghost btn-sm">${ICON.left}${esc(res.title)}</button>`;
  const dl = (list) => list.map((f) => `<div class="dt"><span>${esc(f.label)}</span><div class="dd">${detailValue(row[f.key], f)}</div></div>`).join('');
  const jsonBlocks = jsons.map((f) => {
    const v = row[f.key];
    if (v == null) return `<div class="dt"><span>${esc(f.label)}</span><div class="dd nil">${t('form.none')}</div></div>`;
    if (typeof v === 'string') return `<div class="dt"><span>${esc(f.label)}</span><div class="dd"><span class="chip warn">${ICON.alert}${t('detail.legacyJSON')}</span><pre class="json">${esc(v)}</pre></div></div>`;
    return `<div class="dt full"><span>${esc(f.label)}</span><div class="dd"><pre class="json">${esc(JSON.stringify(v, null, 2))}</pre></div></div>`;
  }).join('');
  const lifecycle = sf ? `<section class="card block" id="d-life"><h3>${esc(sf.label)}</h3>
      <div class="lifecycle">${(sf.states ?? []).map((st_, i) => `<span class="step ${st_ === row[sf.key] ? 'now' : ''} ${isTerminal(sf, st_) ? 'terminal' : ''}">${stateChip(sf, st_)}</span>`).join('<span class="arr">' + ICON.right + '</span>')}</div>
      ${res.canEdit && !isTerminal(sf, row[sf.key]) && (sf.transitions?.[row[sf.key]] ?? []).length ? `<div class="moves">${(sf.transitions[row[sf.key]] ?? []).map((to) => `<button class="chip state s${((sf.states ?? []).indexOf(to) % 8) + 1} mv" data-to="${esc(to)}">${ICON.right}${esc(to.replace(/_/g, ' '))}</button>`).join('')}</div>` : (sf && isTerminal(sf, row[sf.key]) ? `<div class="ro">${ICON.lock} ${t('form.terminal')}</div>` : '')}
    </section>` : '';
  const relBlocks = rels.map((f) => `<section class="card block rel" id="d-rel-${f.key}"><h3>${esc(f.label)}</h3><div class="skel-rows"><div class="skel w60"></div></div></section>`).join('');
  const childBlocks = (res.children ?? []).map((c, i) => `<section class="card block child" id="d-child-${i}"><h3>${esc(c.res.title)} <small class="dim">· ${esc(c.field.label)}</small></h3><div class="skel-rows"><div class="skel w80"></div><div class="skel w60"></div></div></section>`).join('');
  const fileBlocks = files.length ? `<section class="card block" id="d-files"><h3>${t('detail.files')}</h3>${files.map((f) => `<div class="dt"><span>${esc(f.label)}</span><div class="dd" data-file="${esc(row[f.key] ?? '')}" data-image="${[].concat(f.accept ?? []).some((a) => String(a).startsWith('image')) ? 1 : ''}">${row[f.key] ? `<span class="skel w40"></span>` : `<span class="nil">${t('form.none')}</span>`}</div></div>`).join('')}</section>` : '';
  $('#main').innerHTML = `<div class="reveal detail">
    <div class="phead">
      <div>${back}<div class="eyebrow">${esc(res.title)} · ${t('detail.eyebrow')}</div><h1>${esc(String(title))}</h1><div class="sub mono dim">${esc(String(row.id ?? ''))}</div></div>
      <div class="actions">
        ${res.canDelete ? `<button id="d-del" class="btn btn-danger">${ICON.trash}<span class="txt">${t('form.delete')}</span></button>` : ''}
        ${res.canEdit ? `<button id="d-edit" class="btn btn-primary">${ICON.pencil}<span class="txt">${t('list.edit')}</span></button>` : ''}
      </div>
    </div>
    <div class="dgrid">
      <div class="dcol">
        <section class="card block"><h3>${t('detail.fields')}</h3><div class="dl">${dl(plain)}${jsonBlocks}</div></section>
        ${lifecycle}
        ${fileBlocks}
      </div>
      <div class="dcol">
        ${relBlocks}
        ${childBlocks}
        ${!rels.length && !(res.children ?? []).length ? `<section class="card block dim"><p>${t('detail.noRelations')}</p></section>` : ''}
      </div>
    </div>
  </div>`;
  $('#d-back').onclick = () => { const prev = detailStack.pop(); if (prev) renderDetail(prev.res, prev.row); else renderList(); };
  if ($('#d-edit')) $('#d-edit').onclick = () => renderForm(row, { after: (r) => renderDetail(res, r ?? row) });
  if ($('#d-del')) $('#d-del').onclick = () => renderForm(row, { confirmDelete: true, afterDelete: () => { detailStack.length = 0; renderList(); } });
  document.querySelectorAll('#d-life .mv').forEach((b) => b.onclick = async () => {
    const to = b.dataset.to;
    try {
      const r = await api(`/api/${res.name}/${row.id}`, { method: 'PATCH', body: { [sf.key]: to } });
      Object.assign(row, r ?? { [sf.key]: to }); row[sf.key] = to;
      toast(t('board.moved', { from: '', to }).replace(/^Estado:\s*→\s*|^State:\s*→\s*/, ''));
      invalidateRel(res.name);
      renderDetail(res, row);
    } catch (e) { toast(e.message, 'err'); }
  });

  // parents: the subroute when published (target RBAC enforced there), else a lookup on the referenced column
  await Promise.all(rels.map(async (f) => {
    const box = document.getElementById(`d-rel-${f.key}`); if (!box) return;
    const v = row[f.key];
    const tres = contract.byName[f.relation] ?? null;
    if (v == null || v === '') { box.innerHTML = `<h3>${esc(f.label)}</h3><div class="nil">${t('form.none')}</div>`; return; }
    let target = null, err = null;
    try {
      if (f.subroute) target = await api(`/api/${res.name}/${row.id}/${f.subroute}`);
      else { const r = await api(`/api/${f.relation}?filter[${f.references}][eq]=${encodeURIComponent(v)}&per_page=1`); target = r.data?.[0] ?? null; }
      if (target && demo) target = demoMergeList(f.relation, [target], null)[0] ?? target;
    } catch (e) { err = e; }
    if (current !== res) return;
    if (err) { box.innerHTML = `<h3>${esc(f.label)}</h3><div class="banner info">${ICON.info}<div>${t('detail.loadFail', { msg: esc(err.status === 403 ? t('nav.denied') : err.status === 404 ? t('detail.notFound') : err.message) })}</div></div><div class="mono dim">${esc(String(v))}</div>`; return; }
    if (!target) { box.innerHTML = `<h3>${esc(f.label)}</h3><div class="mono dim">${esc(String(v))}</div><div class="dim">${t('detail.notFound')}</div>`; return; }
    const label = tres ? rowLabel(target, f.references, tres) : rowLabel(target, f.references);
    const peek = tres ? tres.fields.filter((x) => x.key !== 'id' && !x.file && !x.relation && !x.json && x.type !== 'object' && !x.auto && target[x.key] != null && target[x.key] !== '').slice(0, 4) : [];
    box.innerHTML = `<h3>${esc(f.label)} <small class="dim">· ${esc(tres?.title ?? f.relation)}</small></h3>
      <div class="relrow"><span class="avatar sm">${esc(monogram(label))}</span><div><b>${esc(String(label))}</b><div class="peek">${peek.map((x) => `<span><em>${esc(x.label)}</em> ${plainValue(target[x.key], x)}</span>`).join('')}</div></div>
      ${tres ? `<button class="btn btn-sm open-rel">${t('detail.open')}${ICON.right}</button>` : ''}</div>`;
    const ob = box.querySelector('.open-rel'); if (ob) ob.onclick = () => renderDetail(tres, target, { push: { res, row } });
  }));

  // children: every resource whose FK points at this row (count + a peek + "see all" = the list pre-filtered)
  await Promise.all((res.children ?? []).map(async (c, i) => {
    const box = document.getElementById(`d-child-${i}`); if (!box) return;
    const key = row[c.field.references] ?? row.id;
    let data = null, err = null;
    try { data = await api(`/api/${c.res.name}?filter[${c.field.key}][eq]=${encodeURIComponent(key)}&per_page=5&count=true`); } catch (e) { err = e; }
    if (current !== res) return;
    const head = `<h3>${esc(c.res.title)} <small class="dim">· ${esc(c.field.label)}</small></h3>`;
    if (err) { box.innerHTML = `${head}<div class="banner info">${ICON.info}<div>${t('detail.loadFail', { msg: esc(err.status === 403 ? t('nav.denied') : err.message) })}</div></div>`; return; }
    let rows = data.data ?? [];
    if (demo) rows = demoMergeList(c.res.name, rows, { page: 1, filters: { [c.field.key]: key } });
    const total = data.meta?.total ?? rows.length;
    // a child with no title field (a junction row: FKs only) is named by its OTHER relations
    const otherRels = titleField(c.res) ? [] : c.res.fields.filter((x) => x.relation && x.key !== c.field.key);
    await Promise.all(otherRels.map((x) => loadRelLabels(x)));
    if (current !== res) return;
    const childLabel = (r) => rowLabel(r, 'id', c.res) !== String(r.id ?? '').slice(0, 8) && titleField(c.res)
      ? rowLabel(r, 'id', c.res)
      : (otherRels.map((x) => r[x.key] != null ? relLabel(x, r[x.key]) : '').filter(Boolean).join(' · ') || rowLabel(r, 'id', c.res) || r.id);
    const nf = new Intl.NumberFormat(locale());
    const csf = c.res.stateField;
    box.innerHTML = `${head}<div class="count-line"><b class="num">${nf.format(total)}</b> <span class="dim">${total === 1 ? t('list.totalOne').replace(/^1 /, '') : t('list.total', { n: '' }).trim()}</span></div>
      ${rows.length ? `<ul class="peeklist">${rows.map((r) => `<li data-id="${esc(r.id)}"><span>${esc(childLabel(r))}</span>${csf && r[csf.key] != null ? stateChip(csf, r[csf.key]) : ''}${ICON.right}</li>`).join('')}</ul>` : `<div class="nil">${t('form.none')}</div>`}
      ${total > rows.length || rows.length ? `<button class="btn btn-sm see-all">${t('detail.viewAll')}${ICON.right}</button>` : ''}`;
    box.querySelectorAll('.peeklist li').forEach((li) => li.onclick = () => renderDetail(c.res, rows.find((r) => r.id === li.dataset.id), { push: { res, row } }));
    const sa = box.querySelector('.see-all'); if (sa) sa.onclick = () => { detailStack.length = 0; listState[c.res.name] = { ...(listState[c.res.name] ?? {}), sort: null, order: 'asc', search: '', filters: { [c.field.key]: String(key) }, view: 'list', page: 1, per: perFor(c.res.name), total: null, pages: null }; selectResource(c.res.name); };
  }));

  // files: a signed URL per attached file (an <a>/<img> cannot send Authorization)
  document.querySelectorAll('#d-files .dd[data-file]').forEach(async (dd) => {
    const id = dd.dataset.file; if (!id) return;
    try {
      const u = demo ? null : await api(`/api/files/${id}/url`);
      dd.innerHTML = `${dd.dataset.image && u ? `<img class="preview" src="${esc(u.url)}" alt="">` : ''}<div>${u ? `<a class="btn btn-sm" href="${esc(u.url)}" target="_blank" rel="noopener">${ICON.file}${t('detail.download')}</a>` : `<span class="chip mono">${esc(id.slice(0, 8))}</span>`}</div>`;
    } catch (e) { dd.innerHTML = `<span class="chip mono">${esc(id.slice(0, 8))}</span> <span class="dim">${esc(e.message)}</span>`; }
  });
}

function detailValue(v, f) {
  if (v === null || v === undefined || v === '') return `<span class="nil">${t('form.none')}</span>`;
  if (f.transitions) return stateChip(f, v);
  if (f.enum) return `<span class="chip">${esc(String(v).replace(/_/g, ' '))}</span>`;
  if (f.type === 'boolean') return v ? `<span class="chip on">${ICON.check}${t('bool.yes')}</span>` : `<span class="chip off">${t('bool.no')}</span>`;
  if (f.format === 'date-time') return `<span class="date">${esc(fmtDate(v))}</span>`;
  if (f.type === 'integer' || f.type === 'number') return `<span class="num">${esc(fmtNumber(v, f))}</span>`;
  if (f.format === 'uuid') return `<span class="chip mono">${esc(String(v))}</span>`;
  return `<span class="txt">${esc(String(v))}</span>`;
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
  const title = editing ? (rowLabel(row, 'id', res) || res.title) : null;
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
        if (opts.afterDelete) opts.afterDelete(); else listState[res.name].view === 'board' ? renderBoard() : renderList();
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
      if (opts.after) opts.after(r ?? row); else listState[res.name].view === 'board' ? renderBoard() : renderList();
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
      if (rows.length >= REL_SELECT_MAX) {
        // APP-PODER-S1 (Part F): past the cap the select is INCOMPLETE — a
        // search against the API (`?search=`, debounced) replaces it, showing
        // the target's title field. Generic: the target and the referenced
        // column come from x-appximo-relation / x-appximo-references.
        const tres = contract.byName[f.relation] ?? null;
        const known = v !== '' ? m.get(String(v)) : '';
        return `<div class="relsearch" data-for="${f.key}" data-target="${esc(f.relation)}" data-ref="${esc(f.references)}">
          <input type="hidden" name="${f.key}" value="${esc(v)}" data-refcol="${esc(f.references)}">
          <input id="${id}" type="search" class="relq" placeholder="${t('rel.searchPh', { res: tres?.title ?? f.relation })}" value="${esc(known ?? '')}" data-resolve="${known ? '' : esc(v)}" autocomplete="off"${dis}>
          <ul class="relopts" hidden></ul>
          ${help(t('rel.searchHelp', { n: REL_SELECT_MAX }))}
        </div>`;
      }
      const opts = [`<option value="">${t('form.none')}</option>`, ...rows.map((r) =>
        `<option value="${esc(r[f.references])}"${String(r[f.references]) === String(v) ? ' selected' : ''}>${esc(m.get(String(r[f.references] ?? r.id)) ?? rowLabel(r, f.references))}</option>`)].join('');
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
    case 'json': {
      // APP-PODER-S1: a real JSON editor for x-appximo-json (text | jsonb) —
      // highlighted, validated as you type, formattable, foldable as a tree —
      // with the two engine limits said in the interface: 1 MiB per request
      // (a bigger document is a 413) and ENG-50 (numbers past 2^53 or with
      // trailing zeros pass through float64 in both directions).
      const text = v === '' ? '' : (typeof v === 'string' ? v : JSON.stringify(v, null, 2));
      const legacy = typeof v === 'string' && v !== '' && !isJSONText(v);
      return `<div class="jsoned" data-for="${f.key}">
        <div class="jtools">
          <span class="jstate num" data-jstate></span>
          <span class="spacer"></span>
          <button type="button" class="btn btn-ghost btn-sm" data-jact="format"${dis}>${t('json.format')}</button>
          <button type="button" class="btn btn-ghost btn-sm" data-jact="compact"${dis}>${t('json.compact')}</button>
          <button type="button" class="btn btn-ghost btn-sm" data-jact="tree">${t('json.tree')}</button>
        </div>
        ${legacy ? `<div class="banner info">${ICON.info}<div>${t('detail.legacyJSON')} — ${t('json.legacyHint')}</div></div>` : ''}
        <div class="jwrap">
          <pre class="jhl" aria-hidden="true"></pre>
          <textarea id="${id}" name="${f.key}" class="mono" data-json="1" spellcheck="false" autocapitalize="off" autocomplete="off"${dis}>${esc(text)}</textarea>
        </div>
        <div class="jtree" hidden></div>
        <div class="jwarn" data-jwarn></div>
        ${help(t('form.jsonHelp'))}
      </div>`;
    }
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

// ── relation search (Part F): past REL_SELECT_MAX rows the target is searched, not listed
const REL_SELECT_MAX = 100;   // the API's per_page cap = the most a select can hold completely
function wireRelSearch() {
  document.querySelectorAll('.relsearch').forEach((box) => {
    const hidden = box.querySelector('input[type=hidden]'), q = box.querySelector('.relq'), list = box.querySelector('.relopts');
    const target = box.dataset.target, ref = box.dataset.ref, tres = contract.byName[target] ?? null;
    const labelOf = (r) => (tres ? rowLabel(r, ref, tres) : rowLabel(r, ref));
    // a value whose label the cached page did not carry: resolve it once
    if (q.dataset.resolve) {
      api(`/api/${target}?filter[${ref}][eq]=${encodeURIComponent(q.dataset.resolve)}&per_page=1`).then((r) => { const row = r.data?.[0]; if (row) q.value = labelOf(row); }).catch(() => {});
    }
    let tm, seq = 0;
    const show = (rows) => {
      if (!rows.length) { list.innerHTML = `<li class="dim">${t('list.emptyFiltered')}</li>`; list.hidden = false; return; }
      list.innerHTML = rows.map((r) => `<li data-v="${esc(String(r[ref] ?? r.id))}" data-l="${esc(labelOf(r))}">${esc(labelOf(r))}</li>`).join('');
      list.hidden = false;
      list.querySelectorAll('li[data-v]').forEach((li) => li.onmousedown = (ev) => { ev.preventDefault(); hidden.value = li.dataset.v; q.value = li.dataset.l; list.hidden = true; });
    };
    q.oninput = () => {
      hidden.value = '';                                   // typing invalidates the previous pick until a result is chosen
      clearTimeout(tm);
      const term = q.value.trim();
      if (!term) { list.hidden = true; return; }
      tm = setTimeout(async () => {
        const my = ++seq;
        try {
          const lf = labelFields(tres, ref);
          const r = await api(`/api/${target}?search=${encodeURIComponent(term)}&per_page=20${lf ? '&fields=' + lf : ''}`);
          if (my !== seq) return;
          let rows = r.data ?? [];
          if (demo) rows = demoMergeList(target, rows, { page: 1, search: term, filters: {} });
          show(rows);
        } catch (e) { if (my === seq) { list.innerHTML = `<li class="dim">${esc(e.message)}</li>`; list.hidden = false; } }
      }, 250);
    };
    q.onblur = () => setTimeout(() => { list.hidden = true; if (!hidden.value) q.value = ''; }, 150);
    q.onkeydown = (e) => { if (e.key === 'Escape') { list.hidden = true; } };
  });
}

// ── the JSON editor (no dependency, CSP-clean) ──────────────────────────────
const JSON_MAX_BYTES = 1048576;                 // the engine's per-request body cap (413 above it)
const JSON_WARN_BYTES = 900 * 1024;
const enc = new TextEncoder();
function jsonBytes(str) { return enc.encode(str).length; }
function isJSONText(str) { try { JSON.parse(str); return true; } catch { return false; } }
// Numbers the HTTP path cannot carry faithfully (ENG-50): integers of 16+ digits
// (2^53 = 9007199254740992 has 16) and decimals with trailing zeros, which
// float64 re-renders (1.50 → 1.5). Found on the RAW text, before the parse
// loses them — that is the whole point of warning here.
function jsonPrecisionRisks(str) {
  const risks = [];
  const re = /(?<![\w.])-?\d+(?:\.\d+)?(?:[eE][+-]?\d+)?(?![\w.])/g;
  let m, n = 0;
  // ignore numbers inside strings: strip string literals first
  const bare = str.replace(/"(?:[^"\\]|\\.)*"/g, '""');
  while ((m = re.exec(bare)) && n < 2000) {
    n++;
    const tok = m[0];
    const int = tok.replace(/^-/, '').split(/[.eE]/)[0];
    if (int.length >= 16) risks.push(tok);
    else if (/\.\d*0$/.test(tok)) risks.push(tok);
  }
  return risks;
}
function hlJSON(str) {
  // one pass over the RAW text (tokens first, escaping per piece — escaping
  // first would turn every quote into &quot; and hide the strings): keys,
  // string values, numbers, literals; everything else verbatim.
  let out = '', last = 0;
  const re = /("(?:[^"\\]|\\.)*")(\s*:)?|(-?\b\d+(?:\.\d+)?(?:[eE][+-]?\d+)?\b)|\b(true|false)\b|\b(null)\b/g;
  let m;
  while ((m = re.exec(str))) {
    out += esc(str.slice(last, m.index));
    const [, str_, colon, num, bool, nul] = m;
    if (str_) out += colon ? `<i class="k">${esc(str_)}</i>${colon}` : `<i class="s">${esc(str_)}</i>`;
    else if (num) out += `<i class="n">${num}</i>`;
    else if (bool) out += `<i class="b">${bool}</i>`;
    else out += `<i class="z">${nul}</i>`;
    last = m.index + m[0].length;
  }
  return out + esc(str.slice(last));
}
function jsonTreeHTML(v, depth = 0) {
  if (v === null) return `<i class="z">null</i>`;
  if (typeof v !== 'object') return typeof v === 'string' ? `<i class="s">${esc(JSON.stringify(v))}</i>` : typeof v === 'number' ? `<i class="n">${v}</i>` : `<i class="b">${v}</i>`;
  const entries = Array.isArray(v) ? v.map((x, i) => [i, x]) : Object.entries(v);
  const open = depth < 2 ? ' open' : '';
  const tag = Array.isArray(v) ? `[${entries.length}]` : `{${entries.length}}`;
  if (!entries.length) return `<i class="p">${Array.isArray(v) ? '[]' : '{}'}</i>`;
  return `<details${open}><summary><i class="p">${tag}</i></summary><ul>${entries.map(([k, x]) => `<li><i class="k">${esc(String(k))}</i>: ${jsonTreeHTML(x, depth + 1)}</li>`).join('')}</ul></details>`;
}
function wireJSONEditors() {
  document.querySelectorAll('.jsoned').forEach((box) => {
    const ta = box.querySelector('textarea'), hl = box.querySelector('.jhl'), state = box.querySelector('[data-jstate]'), warn = box.querySelector('[data-jwarn]'), tree = box.querySelector('.jtree');
    const nf = new Intl.NumberFormat(locale());
    const refresh = () => {
      const v = ta.value;
      hl.innerHTML = hlJSON(v) + '\n';
      const bytes = jsonBytes(v);
      const kb = bytes >= 1024 ? `${nf.format(Math.round(bytes / 1024))} KB` : `${bytes} B`;
      let ok = true, err = '';
      if (v.trim() !== '') { try { JSON.parse(v); } catch (e) { ok = false; err = String(e.message); } }
      state.className = 'jstate num ' + (ok ? 'ok' : 'bad');
      state.textContent = v.trim() === '' ? t('json.empty') : ok ? t('json.valid', { lines: nf.format(v.split('\n').length), size: kb }) : t('json.invalid', { msg: err.replace(/^JSON\.parse: |^Unexpected /, '').slice(0, 80) });
      const notes = [];
      if (bytes >= JSON_MAX_BYTES) notes.push(`<div class="banner err">${ICON.alert}<div>${t('json.tooBig', { kb: nf.format(Math.round(bytes / 1024)) })}</div></div>`);
      else if (bytes >= JSON_WARN_BYTES) notes.push(`<div class="banner info">${ICON.info}<div>${t('json.nearLimit', { kb: nf.format(Math.round(bytes / 1024)) })}</div></div>`);
      if (ok && v.trim() !== '') {
        const risks = jsonPrecisionRisks(v);
        if (risks.length) notes.push(`<div class="banner info">${ICON.alert}<div>${t('json.precision', { n: nf.format(risks.length), sample: esc(risks.slice(0, 3).join(', ')) })}</div></div>`);
      }
      warn.innerHTML = notes.join('');
      box.classList.toggle('invalid', !ok);
    };
    let tm; ta.oninput = () => { clearTimeout(tm); tm = setTimeout(refresh, 120); };
    ta.onscroll = () => { hl.scrollTop = ta.scrollTop; hl.scrollLeft = ta.scrollLeft; };
    ta.onkeydown = (e) => { if (e.key === 'Tab') { e.preventDefault(); const st_ = ta.selectionStart; ta.setRangeText('  ', st_, ta.selectionEnd, 'end'); refresh(); } };
    box.querySelectorAll('[data-jact]').forEach((b) => b.onclick = () => {
      const act = b.dataset.jact;
      if (act === 'tree') {
        const showing = !tree.hidden;
        if (showing) { tree.hidden = true; box.querySelector('.jwrap').hidden = false; b.textContent = t('json.tree'); return; }
        try { tree.innerHTML = ta.value.trim() === '' ? `<span class="nil">${t('json.empty')}</span>` : jsonTreeHTML(JSON.parse(ta.value)); } catch { toast(t('form.jsonBad'), 'err'); return; }
        tree.hidden = false; box.querySelector('.jwrap').hidden = true; b.textContent = t('json.text');
        return;
      }
      try { const parsed = JSON.parse(ta.value); ta.value = act === 'format' ? JSON.stringify(parsed, null, 2) : JSON.stringify(parsed); refresh(); }
      catch { toast(t('form.jsonBad'), 'err'); }
    });
    refresh();
  });
}

function wireFileInputs() {
  wireJSONEditors();
  wireRelSearch();
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
  if (el.dataset.json) {
    let parsed;
    try { parsed = JSON.parse(v); } catch { throw new Error(t('form.jsonBad')); }
    if (jsonBytes(v) >= JSON_MAX_BYTES) throw new Error(t('json.tooBig', { kb: Math.round(jsonBytes(v) / 1024) }));
    return parsed;
  }
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
