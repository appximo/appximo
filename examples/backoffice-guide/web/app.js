// app.js — the generic screens of backoffice-spec: menu (permission-probed),
// list (sort/search/keyset pagination), form (the five rules), relation
// selects honoring x-appximo-references, constrained state selects, a file
// widget with the declared policy. No resource is named anywhere in this file.
import { loadContract, controlFor } from './contract.js';

const $ = (sel) => document.querySelector(sel);
let token = null;
let contract = null;
let current = null;          // selected resource
let listState = {};          // per resource: {sort, order, search, after:[], rows}
const probe = {};            // resource -> {denied|error|total}

class ApiError extends Error {
  constructor(status, body) {
    super(body?.error ?? `HTTP ${status}`);
    this.status = status;
    this.fields = body?.fields ?? [];
  }
}

async function api(path, opts = {}) {
  const headers = { ...(opts.headers ?? {}) };
  if (token) headers['Authorization'] = 'Bearer ' + token;
  let body = opts.body;
  if (body !== undefined && !(body instanceof FormData)) {
    headers['Content-Type'] = 'application/json';
    body = JSON.stringify(body);
  }
  const res = await fetch(path, { method: opts.method ?? 'GET', headers, body });
  if (res.status === 204) return null;
  const isJSON = (res.headers.get('content-type') ?? '').includes('json');
  const data = isJSON ? await res.json() : null;
  if (!res.ok) throw new ApiError(res.status, data);
  return data;
}

// ── auth (frontend-spec §2 — minimal) ────────────────────────────────────────
async function login(email, password) {
  const r = await api('/auth/login', { method: 'POST', body: { email, password } });
  token = r.token;
  boot().catch(showFatal);
}
async function signup(email, password) {
  const r = await api('/auth/signup', { method: 'POST', body: { email, password } });
  token = r.token;
  boot().catch(showFatal);
}

function renderLogin(msg = '') {
  $('#app').innerHTML = `
    <div class="login">
      <h1>Back-office <span class="muted">— generated from /openapi.json</span></h1>
      ${msg ? `<div class="banner err">${esc(msg)}</div>` : ''}
      <input id="l-email" type="email" placeholder="email" autocomplete="username">
      <input id="l-pass" type="password" placeholder="password" autocomplete="current-password">
      <div class="row">
        <button id="l-go" class="primary">Log in</button>
        <button id="l-su">Sign up</button>
      </div>
    </div>`;
  $('#l-go').onclick = () => login($('#l-email').value, $('#l-pass').value).catch((e) => renderLogin(e.message));
  $('#l-su').onclick = () => signup($('#l-email').value, $('#l-pass').value).catch((e) => renderLogin(e.message));
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
  const items = contract.resources.map((r) => {
    const p = probe[r.name] ?? {};
    const cls = p.denied ? 'denied' : '';
    const badge = p.denied ? '⛔' : (p.total ?? '');
    return `<li class="${cls}" data-res="${r.name}">${esc(r.title)} <span class="badge">${badge}</span></li>`;
  }).join('');
  const virtual = Object.entries(contract.virtual).map(([n, v]) =>
    `<li class="virtual" title="${esc(v.description ?? '')}">${esc(n)} <span class="badge">engine</span></li>`).join('');
  $('#app').innerHTML = `
    <div class="shell">
      <nav>
        <h2>Resources</h2>
        <ul id="menu">${items}</ul>
        ${virtual ? `<h2>Engine</h2><ul>${virtual}</ul>` : ''}
        <button id="logout" class="subtle">Log out</button>
      </nav>
      <main id="main"></main>
    </div>`;
  document.querySelectorAll('#menu li:not(.denied)').forEach((li) => {
    li.onclick = () => selectResource(li.dataset.res);
  });
  $('#logout').onclick = () => { token = null; renderLogin(); };
}

// ── list view (§8) ───────────────────────────────────────────────────────────
function columnsFor(res) {
  const preferred = ['nombre', 'name', 'titulo', 'title', 'codigo', 'code', 'estado', 'status', 'email', 'socio'];
  const cols = res.fields
    .filter((f) => !f.file && f.key !== 'id')
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

async function renderList() {
  const st = listState[current.name];
  const q = new URLSearchParams();
  q.set('per_page', '10');
  if (st.sort) { q.set('sort', st.sort); q.set('order', st.order); }
  if (st.search) q.set('search', st.search);
  const cursor = st.after[st.after.length - 1];
  if (cursor) q.set('after', cursor);
  $('#main').innerHTML = `<div class="loading">Loading ${esc(current.title)}…</div>`;
  let res;
  try {
    res = await api(`/api/${current.name}?` + q.toString());
  } catch (e) {
    $('#main').innerHTML = `<div class="banner err">${esc(e.message)} <button onclick="location.reload()">Retry</button></div>`;
    return;
  }
  st.rows = res.data;
  const cols = columnsFor(current);
  const head = cols.map((c) =>
    `<th data-k="${c.key}" class="${st.sort === c.key ? 'sorted-' + st.order : ''}">${esc(c.key)}</th>`).join('');
  const body = res.data.map((row, i) => {
    const tds = cols.map((c) => `<td>${esc(displayValue(row[c.key], c))}</td>`).join('');
    const del = current.canDelete ? `<button class="subtle del" data-i="${i}">✕</button>` : '';
    return `<tr data-i="${i}">${tds}<td>${del}</td></tr>`;
  }).join('');
  $('#main').innerHTML = `
    <div class="toolbar">
      <h1>${esc(current.title)}</h1>
      <input id="search" placeholder="search…" value="${esc(st.search)}">
      ${current.canCreate ? '<button id="new" class="primary">+ New</button>' : ''}
    </div>
    <div id="list-banner"></div>
    ${res.data.length === 0 ? '<div class="empty">No records' + (st.search ? ' for that search' : '') + '.</div>' : `
    <table><thead><tr>${head}<th></th></tr></thead><tbody>${body}</tbody></table>`}
    <div class="pager">
      ${st.after.length ? '<button id="pg-first">⇤ first page</button>' : ''}
      ${res.meta?.has_next ? '<button id="pg-next">next ›</button>' : ''}
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
    if (!confirm(`Delete this ${current.name} record?`)) return;
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
      <h2>${editing ? 'Edit' : 'New'} ${esc(current.name)}</h2>
      <div id="form-banner"></div>
      <form id="gform">${controls.join('')}
        <div class="row">
          <button type="submit" class="primary">${editing ? 'Save' : 'Create'}</button>
          <button type="button" id="form-cancel">Cancel</button>
        </div>
      </form>
    </div></div>`);
  $('#form-cancel').onclick = () => $('#form-bg').remove();
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
      if (nexts.length === 0) return `<input name="${f.key}" value="${esc(v)}" disabled title="terminal state — no legal moves">`;
      const opts = [v, ...nexts].map((s) => `<option${s === v ? ' selected' : ''}>${esc(s)}</option>`).join('');
      return `<select name="${f.key}">${opts}</select>`;
    }
    case 'select': {
      const opts = ['', ...(f.enum ?? [])].map((s) =>
        `<option value="${esc(s)}"${s === v ? ' selected' : ''}>${esc(s || '—')}</option>`).join('');
      return `<select name="${f.key}">${opts}</select>`;
    }
    case 'relation': {
      // §7: send the REFERENCED column's value, never blindly the row id.
      let rows = [];
      try { rows = (await api(`/api/${f.relation}?per_page=100`)).data; } catch { rows = []; }
      const label = (r) => r.nombre ?? r.name ?? r.titulo ?? r.title ?? r.email ?? r[f.references];
      const opts = ['<option value="">—</option>', ...rows.map((r) =>
        `<option value="${esc(r[f.references])}"${r[f.references] === v ? ' selected' : ''}>${esc(label(r))}</option>`)].join('');
      return `<select name="${f.key}" data-refcol="${esc(f.references)}">${opts}</select>`;
    }
    case 'file': {
      const policy = [f.accept ? `accepts: ${[].concat(f.accept).join(', ')}` : '',
        f.maxBytes ? `max ${(f.maxBytes / 1048576).toFixed(1)} MiB` : ''].filter(Boolean).join(' · ');
      return `<span class="filewrap"><input type="file" data-for="${f.key}">
        <input type="hidden" name="${f.key}" value="${esc(v)}">
        <small class="muted">${esc(policy)} ${v ? '· attached: ' + esc(String(v).slice(0, 8)) + '…' : ''}</small></span>`;
    }
    case 'checkbox': return `<input type="checkbox" name="${f.key}"${val ? ' checked' : ''}>`;
    case 'textarea': return `<textarea name="${f.key}"${lim}>${esc(v)}</textarea>`;
    case 'datetime-local': return `<input type="datetime-local" name="${f.key}" value="${esc(String(v).slice(0, 16))}">`;
    default: return `<input type="${kind}" name="${f.key}" value="${esc(v)}"${lim}>`;
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
        inp.insertAdjacentHTML('afterend', '<small class="ok">uploaded ✓</small>');
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
  $('#app').innerHTML = `<div class="banner err">Failed to start: ${esc(e.message)}</div>`;
}

renderLogin();
