// app.js — the generic screens of backoffice-spec: menu (permission-probed),
// list (sort/search/keyset pagination), form (the five rules), relation
// selects honoring x-appximo-references, constrained state selects, a file
// widget with the declared policy. No resource is named anywhere in this file.
// This is the embedded /app copy (ENG-38); the teaching copy consumers adapt
// into their own SPA lives in examples/backoffice-guide/web/app.js.
//
// DEMO-SHOWCASE-S1 added the chrome around the pattern: Spanish/English UI
// strings (i18n.js — browser-derived, persisted override), mobile-first
// responsive layout, light/dark themes, consumer theme tokens (theme.css),
// and an optional DEMO MODE: for roles listed in /app/ui-config.json the
// SPA simulates writes in a per-session in-memory overlay so a public demo
// stays touchable while the role's server-side RBAC stays read-only — a
// visitor's write never reaches the server, and a reload resets everything.
import { loadContract, controlFor } from './contract.js';
import { t, lang, setLang } from './i18n.js';

const $ = (sel) => document.querySelector(sel);
let token = null;
let contract = null;
let current = null;          // selected resource
let listState = {};          // per resource: {sort, order, search, after:[], rows}
const probe = {};            // resource -> {denied|error|total}
let uiConfig = {};           // served at /app/ui-config.json (demo roles, …)
let demo = false;            // demo mode: writes are simulated, never sent

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
    ov(overlay.deleted, res).add ? overlay.deleted[res].add(id) : (overlay.deleted[res] = new Set([id]));
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
  if (!st || st.after.length === 0) {
    let created = [...(overlay.created[res]?.values() ?? [])];
    if (st?.search) {
      const q = st.search.toLowerCase();
      created = created.filter((r) => Object.values(r).some((v) => String(v ?? '').toLowerCase().includes(q)));
    }
    out.unshift(...created.reverse());
  }
  return out;
}

class ApiError extends Error {
  constructor(status, body) {
    super(body?.error ?? `HTTP ${status}`);
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
  const res = await fetch(path, { method, headers, body });
  if (res.status === 204) return null;
  const isJSON = (res.headers.get('content-type') ?? '').includes('json');
  const data = isJSON ? await res.json() : null;
  if (!res.ok) throw new ApiError(res.status, data);
  return data;
}

// ── theme (light / dark / auto) ──────────────────────────────────────────────
const THEME_KEY = 'appximo.app.theme';
function applyTheme() {
  const v = localStorage.getItem(THEME_KEY);
  if (v === 'light' || v === 'dark') document.documentElement.dataset.theme = v;
  else delete document.documentElement.dataset.theme;
}
function themeLabel() {
  const v = localStorage.getItem(THEME_KEY);
  return v === 'light' ? '☀' : v === 'dark' ? '☾' : '◐';
}
function themeTitle() {
  const v = localStorage.getItem(THEME_KEY);
  return t(v === 'light' ? 'theme.light' : v === 'dark' ? 'theme.dark' : 'theme.auto');
}
function cycleTheme() {
  const v = localStorage.getItem(THEME_KEY);
  const next = v === 'light' ? 'dark' : v === 'dark' ? null : 'light';
  if (next) localStorage.setItem(THEME_KEY, next);
  else localStorage.removeItem(THEME_KEY);
  applyTheme();
}

function prefButtons() {
  return `<button id="pref-lang" class="subtle" title="idioma / language">${lang === 'es' ? 'EN' : 'ES'}</button>
    <button id="pref-theme" class="subtle" title="${esc(themeTitle())}">${themeLabel()}</button>`;
}
function wirePrefButtons(rerender) {
  const l = $('#pref-lang'), th = $('#pref-theme');
  if (l) l.onclick = () => { setLang(lang === 'es' ? 'en' : 'es'); rerender(); };
  if (th) th.onclick = () => { cycleTheme(); rerender(); };
}

// ── auth (frontend-spec §2 — minimal) ────────────────────────────────────────
function claimsOf(tok) {
  try {
    return JSON.parse(atob(tok.split('.')[1].replace(/-/g, '+').replace(/_/g, '/')));
  } catch { return {}; }
}

async function login(email, password) {
  const r = await api('/auth/login', { method: 'POST', body: { email, password } });
  token = r.token;
  enterApp();
}
async function signup(email, password) {
  const r = await api('/auth/signup', { method: 'POST', body: { email, password } });
  token = r.token;
  enterApp();
}
function enterApp() {
  const role = claimsOf(token).role;
  demo = Array.isArray(uiConfig.demo_roles) && uiConfig.demo_roles.includes(role);
  boot().catch(showFatal);
}

// The engine resolves the tenant from the Host subdomain. Opening /app on a
// bare host (localhost, an IP) means every API call will get the named 400 —
// say so BEFORE the first failed login, with the exact URL shape that works.
function tenantHint() {
  const h = window.location.hostname;
  if (h.includes('.')) return '';
  const host = esc(h) + (window.location.port ? ':' + window.location.port : '');
  return `<div class="banner info">${t('login.tenantHint', { host })}</div>`;
}

function renderLogin(msg = '') {
  document.title = 'App — Appximo';
  document.documentElement.lang = lang;
  $('#app').innerHTML = `
    <div class="corner">${prefButtons()}</div>
    <div class="login-wrap"><div class="login">
      <h1 id="login-title">${contract ? esc(contract.appTitle) : 'App'}</h1>
      <div class="sub">${t('login.generated')}</div>
      ${tenantHint()}
      ${msg ? `<div class="banner err">${esc(msg)}</div>` : ''}
      <input id="l-email" type="email" placeholder="${t('login.email')}" autocomplete="username">
      <input id="l-pass" type="password" placeholder="${t('login.password')}" autocomplete="current-password">
      <div class="row">
        <button id="l-go" class="primary">${t('login.signin')}</button>
        <button id="l-su">${t('login.signup')}</button>
      </div>
      <small class="muted">${t('login.help')}</small>
    </div></div>`;
  wirePrefButtons(() => renderLogin(msg));
  const go = () => login($('#l-email').value, $('#l-pass').value).catch((e) => renderLogin(e.message));
  $('#l-go').onclick = go;
  $('#l-pass').onkeydown = (e) => { if (e.key === 'Enter') go(); };
  $('#l-su').onclick = () => signup($('#l-email').value, $('#l-pass').value).catch((e) => renderLogin(e.message));
  // The contract is public (engine-global): fetch it lazily so the login card
  // can greet with the app's own name; ignore failures (bare host, offline).
  if (!contract) {
    loadContract(api).then((c) => {
      contract = c;
      const el = $('#login-title');
      if (el) el.textContent = c.appTitle;
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
  renderShell();
  const first = contract.resources.find((r) => !probe[r.name]?.denied);
  if (first) selectResource(first.name);
}

function renderShell() {
  document.title = `${contract.appTitle} — /app`;
  document.documentElement.lang = lang;
  const items = contract.resources.map((r) => {
    const p = probe[r.name] ?? {};
    const cls = p.denied ? 'denied' : '';
    const badge = p.denied ? '⛔' : (p.total ?? '');
    const title = p.denied ? ` title="${t('nav.denied')}"` : '';
    return `<li class="${cls}" data-res="${r.name}"${title}>${esc(r.title)} <span class="badge">${badge}</span></li>`;
  }).join('');
  const virtual = Object.entries(contract.virtual).map(([n, v]) =>
    `<li class="virtual" title="${esc(v.description ?? '')}">${esc(n)} <span class="badge">engine</span></li>`).join('');
  $('#app').innerHTML = `
    <div class="topbar">
      <button id="menu-btn" aria-label="menu">☰</button>
      <span class="appname">${esc(contract.appTitle)}</span>
      ${demo ? `<span class="badge">${t('demo.tag')}</span>` : ''}
    </div>
    <div class="shell">
      <nav class="side">
        <h2 class="appname" title="from /openapi.json — this UI knows nothing else">${esc(contract.appTitle)}</h2>
        <h2>${t('nav.resources')}</h2>
        <ul id="menu">${items}</ul>
        ${virtual ? `<h2>${t('nav.engine')}</h2><ul>${virtual}</ul>` : ''}
        <div class="grow"></div>
        <div class="navfoot">
          <div class="row">${prefButtons()}</div>
          <button id="logout" class="subtle">${t('nav.logout')}</button>
        </div>
      </nav>
      <main id="main"></main>
    </div>
    <div class="drawer-bg" id="drawer-bg"></div>
    ${demo ? `<div class="demobar">${t('demo.banner')}</div>` : ''}`;
  document.querySelectorAll('#menu li:not(.denied)').forEach((li) => {
    li.onclick = () => { closeDrawer(); selectResource(li.dataset.res); };
  });
  $('#logout').onclick = () => { token = null; demo = false; renderLogin(); };
  $('#menu-btn').onclick = () => document.body.classList.toggle('drawer-open');
  $('#drawer-bg').onclick = closeDrawer;
  wirePrefButtons(() => { renderShell(); if (current) selectResource(current.name); });
}

function closeDrawer() { document.body.classList.remove('drawer-open'); }

// ── list view (§8) ───────────────────────────────────────────────────────────
function columnsFor(res) {
  const preferred = ['nombre', 'name', 'titulo', 'title', 'codigo', 'code', 'estado', 'status', 'email', 'socio'];
  const cols = res.fields
    // No raw UUIDs in a list a human scans: FK relation columns and plain uuid
    // fields stay out of the auto-columns (the form still edits them).
    .filter((f) => !f.file && f.key !== 'id' && !f.relation && f.format !== 'uuid')
    .sort((a, b) => {
      const pa = preferred.findIndex((p) => a.key.includes(p));
      const pb = preferred.findIndex((p) => b.key.includes(p));
      return (pa === -1 ? 99 : pa) - (pb === -1 ? 99 : pb);
    })
    .slice(0, 5);
  return cols;
}

async function selectResource(name) {
  current = contract.byName[name];
  listState[name] = listState[name] ?? { sort: null, order: 'asc', search: '', after: [] };
  document.querySelectorAll('#menu li').forEach((li) =>
    li.classList.toggle('active', li.dataset.res === name));
  await renderList();
}

function skeleton() {
  return `<div class="tablewrap"><div class="skel">${'<div></div>'.repeat(6)}</div></div>`;
}

async function renderList() {
  const st = listState[current.name];
  const q = new URLSearchParams();
  q.set('per_page', '10');
  if (st.sort) { q.set('sort', st.sort); q.set('order', st.order); }
  if (st.search) q.set('search', st.search);
  const cursor = st.after[st.after.length - 1];
  if (cursor) q.set('after', cursor);
  $('#main').innerHTML = `
    <div class="toolbar"><h1>${esc(current.title)}</h1></div>
    ${skeleton()}`;
  let res;
  try {
    res = await api(`/api/${current.name}?` + q.toString());
  } catch (e) {
    $('#main').innerHTML = `<div class="banner err">${esc(e.message)} <button id="retry">${t('list.retry')}</button></div>`;
    $('#retry').onclick = () => renderList();
    return;
  }
  let rows = res.data;
  if (demo) rows = demoMergeList(current.name, rows, st);
  st.rows = rows;
  const cols = columnsFor(current);
  const head = cols.map((c) =>
    `<th data-k="${c.key}" class="${st.sort === c.key ? 'sorted-' + st.order : ''}">${esc(c.key)}</th>`).join('');
  const body = rows.map((row, i) => {
    const tds = cols.map((c) => `<td data-l="${esc(c.key)}">${esc(displayValue(row[c.key], c))}</td>`).join('');
    const del = current.canDelete ? `<button class="subtle del" data-i="${i}" aria-label="${t('list.actions')}">✕</button>` : '';
    return `<tr data-i="${i}">${tds}<td class="rowact">${del}</td></tr>`;
  }).join('');
  $('#main').innerHTML = `
    <div class="toolbar">
      <h1>${esc(current.title)}</h1>
      <input id="search" type="search" placeholder="${t('list.search')}" value="${esc(st.search)}">
      <span class="spacer"></span>
      ${current.canCreate ? `<button id="new" class="primary">+ ${t('list.new')}</button>` : ''}
    </div>
    <div id="list-banner"></div>
    ${rows.length === 0 ? `<div class="empty"><b>${esc(current.title)}</b>${st.search ? t('list.emptySearch') : t('list.empty')}</div>` : `
    <div class="tablewrap"><table><thead><tr>${head}<th></th></tr></thead><tbody>${body}</tbody></table></div>`}
    <div class="pager">
      ${st.after.length ? `<button id="pg-first">⇤ ${t('list.first')}</button>` : ''}
      ${res.meta?.has_next ? `<button id="pg-next">${t('list.next')} ›</button>` : ''}
    </div>`;
  $('#search').onchange = (e) => { st.search = e.target.value; st.after = []; renderList(); };
  document.querySelectorAll('th[data-k]').forEach((th) => th.onclick = () => {
    const k = th.dataset.k;
    st.order = st.sort === k && st.order === 'asc' ? 'desc' : 'asc';
    st.sort = k; st.after = [];
    renderList();
  });
  if ($('#new')) $('#new').onclick = () => renderForm(null);
  if ($('#pg-next')) $('#pg-next').onclick = () => { st.after.push(res.data[res.data.length - 1].id); renderList(); };
  if ($('#pg-first')) $('#pg-first').onclick = () => { st.after = []; renderList(); };
  document.querySelectorAll('tbody tr').forEach((tr) => tr.onclick = (ev) => {
    if (ev.target.classList.contains('del')) return;
    if (current.canEdit) renderForm(st.rows[Number(tr.dataset.i)]);
  });
  document.querySelectorAll('.del').forEach((b) => b.onclick = async () => {
    const row = st.rows[Number(b.dataset.i)];
    if (!confirm(t('list.confirmDelete', { res: current.name }))) return;
    try {
      await api(`/api/${current.name}/${row.id}`, { method: 'DELETE' });
      renderList();
    } catch (e) {
      // §8: the 409 names the referencing table — show it verbatim.
      $('#list-banner').innerHTML = `<div class="banner err">${esc(e.message)}</div>`;
    }
  });
}

function displayValue(v, f) {
  if (v === null || v === undefined) return '';
  if (f.format === 'date-time') return String(v).slice(0, 16).replace('T', ' ');
  if (f.type === 'boolean') return v ? '✓' : '—';
  return String(v);
}

// ── the generic form (§6: the five rules) ────────────────────────────────────
async function renderForm(row) {
  const editing = !!row;
  const fields = current.fields.filter((f) => !f.readOnly && !f.auto);
  const controls = await Promise.all(fields.map(async (f) => {
    const val = editing ? row[f.key] : null;
    return `<label>
      <span>${esc(f.key)}${f.required ? ' *' : ''}</span>
      ${await controlHTML(f, val, editing)}
      <em class="ferr" data-f="${f.key}"></em>
    </label>`;
  }));
  $('#main').insertAdjacentHTML('beforeend', `
    <div class="modal-bg" id="form-bg"><div class="modal">
      <h2>${editing ? t('form.edit') : t('form.new')} · ${esc(current.title)}</h2>
      <div id="form-banner"></div>
      <form id="gform">${controls.join('')}
        <div class="row">
          <button type="submit" class="primary">${editing ? t('form.save') : t('form.create')}</button>
          <button type="button" id="form-cancel">${t('form.cancel')}</button>
        </div>
      </form>
    </div></div>`);
  $('#form-cancel').onclick = () => $('#form-bg').remove();
  $('#form-bg').onclick = (ev) => { if (ev.target.id === 'form-bg') $('#form-bg').remove(); };
  wireFileInputs();
  $('#gform').onsubmit = async (ev) => {
    ev.preventDefault();
    const body = {};
    for (const f of fields) {
      const v = readControl(f);
      if (!editing && (v === null || v === '')) continue;      // rule 1: OMIT empty on create
      if (editing && v === '') { body[f.key] = null; continue; } // rule 3: explicit null clears
      if (v !== null) body[f.key] = v;
      else if (editing) body[f.key] = null;
    }
    try {
      if (editing) await api(`/api/${current.name}/${row.id}`, { method: 'PATCH', body }); // rule 2
      else await api(`/api/${current.name}`, { method: 'POST', body });
      $('#form-bg').remove();
      renderList();
    } catch (e) {
      if (e.status === 422 && e.fields.length) {               // rule 4: paint them ALL
        document.querySelectorAll('.ferr').forEach((el) => el.textContent = '');
        for (const fe of e.fields) {
          const el = document.querySelector(`.ferr[data-f="${fe.field}"]`);
          if (el) el.textContent = fe.message ?? fe.rule;
        }
        const first = document.querySelector('.ferr:not(:empty)');
        if (first) first.scrollIntoView({ block: 'center' });
        if (!e.fields.some((fe) => document.querySelector(`.ferr[data-f="${fe.field}"]`))) {
          $('#form-banner').innerHTML = `<div class="banner err">${esc(e.message)}</div>`;
        }
      } else {                                                  // rule 4: 409 keeps the work
        $('#form-banner').innerHTML = `<div class="banner err">${esc(e.message)}</div>`;
      }
    }
  };
}

async function controlHTML(f, val, editing) {
  const kind = controlFor(f);
  const v = val ?? '';
  // Native `required` on CREATE only, and only when the engine cannot fill the
  // field itself (a `required` field WITH a schema default is satisfiable by
  // omission — the contract's `default` keyword says so). The browser blocks
  // the empty submit with a pointed message; a partially-filled submit still
  // exercises the engine's 422 (painted below).
  const req = !editing && f.required && f.default == null ? ' required' : '';
  const lim = `${f.maxLength ? ` maxlength="${f.maxLength}"` : ''}${f.minimum != null ? ` min="${f.minimum}"` : ''}${f.maximum != null ? ` max="${f.maximum}"` : ''}`;
  switch (kind) {
    case 'state': {
      // rule 5: only legal moves. Create → initial states; edit → current +
      // its transitions; terminal → read-only.
      if (!editing) {
        const opts = (f.initialStates ?? []).map((s) => `<option>${esc(s)}</option>`).join('');
        return `<select name="${f.key}">${opts}</select>`;
      }
      const nexts = f.transitions?.[v] ?? [];
      if (nexts.length === 0) return `<input name="${f.key}" value="${esc(v)}" disabled title="${t('form.terminal')}">`;
      const opts = [v, ...nexts].map((s) => `<option${s === v ? ' selected' : ''}>${esc(s)}</option>`).join('');
      return `<select name="${f.key}">${opts}</select>`;
    }
    case 'select': {
      const opts = ['', ...(f.enum ?? [])].map((s) =>
        `<option value="${esc(s)}"${s === v ? ' selected' : ''}>${esc(s || t('form.none'))}</option>`).join('');
      return `<select name="${f.key}"${req}>${opts}</select>`;
    }
    case 'relation': {
      // §7: send the REFERENCED column's value, never blindly the row id.
      let rows = [];
      try { rows = (await api(`/api/${f.relation}?per_page=100`)).data; } catch { rows = []; }
      if (demo) rows = demoMergeList(f.relation, rows, null);
      const label = (r) => r.nombre ?? r.name ?? r.titulo ?? r.title ?? r.email ?? r[f.references];
      const opts = [`<option value="">${t('form.none')}</option>`, ...rows.map((r) =>
        `<option value="${esc(r[f.references])}"${r[f.references] === v ? ' selected' : ''}>${esc(label(r))}</option>`)].join('');
      return `<select name="${f.key}" data-refcol="${esc(f.references)}"${req}>${opts}</select>`;
    }
    case 'file': {
      const policy = [f.accept ? t('file.accepts', { list: [].concat(f.accept).join(', ') }) : '',
        f.maxBytes ? t('file.max', { mb: (f.maxBytes / 1048576).toFixed(1) }) : ''].filter(Boolean).join(' · ');
      return `<span class="filewrap"><input type="file" data-for="${f.key}">
        <input type="hidden" name="${f.key}" value="${esc(v)}">
        <small class="muted">${esc(policy)} ${v ? '· ' + t('file.attached', { id: esc(String(v).slice(0, 8)) }) : ''}</small></span>`;
    }
    case 'checkbox': return `<input type="checkbox" name="${f.key}"${val ? ' checked' : ''}>`;
    case 'textarea': return `<textarea name="${f.key}"${lim}${req}>${esc(v)}</textarea>`;
    case 'datetime-local': return `<input type="datetime-local" name="${f.key}" value="${esc(String(v).slice(0, 16))}"${req}>`;
    default: return `<input type="${kind}" name="${f.key}" value="${esc(v)}"${lim}${req}>`;
  }
}

function wireFileInputs() {
  document.querySelectorAll('input[type=file][data-for]').forEach((inp) => {
    inp.onchange = async () => {
      const file = inp.files[0];
      if (!file) return;
      const fd = new FormData();
      fd.append('file', file);
      try {
        const r = await api('/api/files', { method: 'POST', body: fd });
        document.querySelector(`input[type=hidden][name="${inp.dataset.for}"]`).value = r.file_id;
        inp.insertAdjacentHTML('afterend', `<small class="ok">${t('file.uploaded')}</small>`);
      } catch (e) {
        inp.insertAdjacentHTML('afterend', `<small class="err">${esc(e.message)}</small>`);
      }
    };
  });
}

function readControl(f) {
  const el = document.querySelector(`#gform [name="${f.key}"]`);
  if (!el) return null;
  if (el.type === 'checkbox') return el.checked;
  const v = el.value;
  if (v === '') return '';
  if (f.type === 'integer') return parseInt(v, 10);           // rule 2: JSON numbers
  if (f.type === 'number') return parseFloat(v);
  if (f.format === 'date-time') return new Date(v).toISOString();
  return v;
}

function esc(s) {
  return String(s ?? '').replace(/[&<>"']/g, (c) =>
    ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]));
}

function showFatal(e) {
  $('#app').innerHTML = `<div class="banner err">${t('boot.failed', { msg: esc(e.message) })}</div>`;
}

applyTheme();
fetch('./ui-config.json').then((r) => (r.ok ? r.json() : {})).then((c) => { uiConfig = c ?? {}; }).catch(() => {})
  .finally(() => renderLogin());
