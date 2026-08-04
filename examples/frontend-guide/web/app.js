// The spec's patterns, verbatim and minimal (docs/FRONTEND_SPEC_LLM.md):
// §5 ApiError + api()  ·  §4.2 session + 401 bounce  ·  §6 screen states
// §7 files: XHR upload with progress → attach → signed-URL + public display.
'use strict';

// ---- §5: ONE error type, ONE api() — every screen consumes these ----------
class ApiError extends Error {
  constructor(status, body) {
    super(body?.error || `HTTP ${status}`);
    this.status = status;
    this.body = body || {};
    this.fields = Array.isArray(body?.fields) ? body.fields : [];
  }
  get isNetwork() { return this.status === 0; }
  fieldMap() {
    const m = {};
    for (const f of this.fields) if (!m[f.field]) m[f.field] = f.message;
    return m;
  }
}

async function api(path, { method = 'GET', body, token, headers = {} } = {}) {
  const h = { ...headers };
  if (body !== undefined) h['Content-Type'] = 'application/json';
  if (token) h['Authorization'] = `Bearer ${token}`;
  let res;
  try {
    res = await fetch(path, { method, headers: h, body: body !== undefined ? JSON.stringify(body) : undefined });
  } catch {
    throw new ApiError(0, { error: 'No connection — check your network and retry.' });
  }
  let data = null;
  const text = await res.text();
  if (text) { try { data = JSON.parse(text); } catch { data = { error: text.slice(0, 200) }; } }
  if (!res.ok) throw new ApiError(res.status, data);
  return data;
}

// ---- §4.2: session in localStorage, expiry check, 401 bounce --------------
const KEY = 'frontend_guide_auth_v1';
const session = { token: '', email: '' };
try { Object.assign(session, JSON.parse(localStorage.getItem(KEY) || '{}')); } catch { /* corrupted → anonymous */ }

function saveSession(token, email) {
  session.token = token; session.email = email;
  try { localStorage.setItem(KEY, JSON.stringify(session)); } catch { /* private mode */ }
  render();
}
function clearSession() {
  session.token = ''; session.email = '';
  try { localStorage.removeItem(KEY); } catch { /* ignore */ }
  render();
}
function isExpired(token) {
  try {
    const p = JSON.parse(atob(token.split('.')[1].replace(/-/g, '+').replace(/_/g, '/')));
    return (p.exp ?? 0) * 1000 < Date.now();
  } catch { return true; }
}
// Authenticated call with the 401 bounce (dead session → back to login).
async function papi(path, opts = {}) {
  try { return await api(path, { ...opts, token: session.token }); }
  catch (e) { if (e instanceof ApiError && e.status === 401) clearSession(); throw e; }
}

// ---- §7.2: upload with REAL progress needs XHR, not fetch -----------------
function uploadFile(file, token, onProgress) {
  return new Promise((resolve, reject) => {
    const form = new FormData();
    form.append('file', file, file.name); // the field name MUST be "file"
    const xhr = new XMLHttpRequest();
    xhr.open('POST', '/api/files');
    xhr.setRequestHeader('Authorization', `Bearer ${token}`);
    xhr.upload.onprogress = (ev) => { if (ev.lengthComputable) onProgress(ev.loaded / ev.total); };
    xhr.onload = () => {
      let body = null;
      try { body = JSON.parse(xhr.responseText); } catch { body = { error: xhr.responseText.slice(0, 200) }; }
      xhr.status === 201 ? resolve(body) : reject(new ApiError(xhr.status, body));
    };
    xhr.onerror = () => reject(new ApiError(0, { error: 'Upload interrupted — nothing was saved; retry.' }));
    xhr.send(form);
  });
}

// ---- DOM helpers ----------------------------------------------------------
const $ = (sel) => document.querySelector(sel);
function show(el, on = true) { el.hidden = !on; }
function setError(el, msg) { el.textContent = msg || ''; show(el, !!msg); }

// ---- auth screen ----------------------------------------------------------
async function doAuth(mode) {
  setError($('#auth-error'), '');
  const email = $('#email').value.trim(), password = $('#password').value;
  try {
    const res = await api(`/auth/${mode}`, { method: 'POST', body: { email, password } });
    if (res.mfa_required) { setError($('#auth-error'), 'This account has MFA — out of this example\'s scope.'); return; }
    saveSession(res.token, res.user?.email ?? email);
  } catch (e) {
    if (e.status === 401) setError($('#auth-error'), 'Wrong email or password.');
    else if (e.status === 409) setError($('#auth-error'), 'That email already has an account — log in instead.');
    else if (e.status === 403 && mode === 'signup') setError($('#auth-error'), 'Signup is disabled — run the server with APPXIMO_AUTH_SIGNUP_ROLE=editor.');
    else if (e.status === 422 || e.status === 400) setError($('#auth-error'), e.body.error || 'Check the fields.');
    else if (e.status === 429) setError($('#auth-error'), 'Too many attempts — wait a moment.');
    else setError($('#auth-error'), e.isNetwork ? e.message : 'Could not sign in. Try again.');
  }
}
$('#auth-form').addEventListener('submit', (ev) => { ev.preventDefault(); doAuth('login'); });
$('#signup-btn').addEventListener('click', () => doAuth('signup'));

// ---- posts list: loading / empty / error+retry / thumbs -------------------
async function loadPosts() {
  const box = $('#posts');
  box.innerHTML = '<div class="skel"></div><div class="skel"></div>'; // §6.1 loading
  let rows;
  try {
    rows = (await papi('/api/posts?sort=created_at&order=desc&per_page=50')).data ?? [];
  } catch (e) {
    box.innerHTML = '';
    const err = document.createElement('div');
    err.className = 'error';
    err.setAttribute('role', 'alert');
    err.textContent = e.isNetwork ? e.message : 'Could not load posts. ';
    const retry = document.createElement('button');
    retry.textContent = 'Retry';
    retry.onclick = loadPosts; // §6.3 network error keeps a retry
    err.appendChild(retry);
    box.appendChild(err);
    return;
  }
  box.innerHTML = '';
  if (rows.length === 0) { // §6.2 empty is a designed state
    box.innerHTML = '<p class="empty">No posts yet — create the first one above.</p>';
    return;
  }
  for (const p of rows) box.appendChild(renderPost(p));
}

function renderPost(p) {
  const el = document.createElement('article');
  el.className = 'post card';
  const img = document.createElement('div');
  img.className = 'thumb';
  if (p.photo) {
    if (p.published) {
      // §7.5 public route: stable URL, browser-cacheable, no token.
      img.innerHTML = `<img src="/api/public-photo?id=${p.photo}" alt="" loading="lazy" />`;
    } else {
      // §7.3 authenticated display: <img> can't send headers → signed URL per render.
      papi(`/api/files/${p.photo}/url`).then(({ url }) => { img.innerHTML = `<img src="${url}" alt="" loading="lazy" />`; })
        .catch(() => { img.textContent = '⚠'; });
      img.textContent = '…';
    }
  } else {
    img.textContent = '·'; // §7.6 records without a photo are normal
  }
  const body = document.createElement('div');
  const badge = p.published ? '<span class="badge ok">published</span>' : '<span class="badge">draft</span>';
  body.innerHTML = `<h3></h3>${badge}<p class="muted"></p>`;
  body.querySelector('h3').textContent = p.title;
  body.querySelector('.muted').textContent = p.body || '';
  const toggle = document.createElement('button');
  toggle.className = 'ghost';
  toggle.textContent = p.published ? 'Unpublish' : 'Publish';
  toggle.onclick = async () => {
    toggle.disabled = true;
    try { await papi(`/api/posts/${p.id}`, { method: 'PATCH', body: { published: !p.published } }); await loadPosts(); }
    catch (e) { toggle.disabled = false; alertRow(el, e.isNetwork ? e.message : (e.body.error || 'Could not save.')); }
  };
  body.appendChild(toggle);
  el.append(img, body);
  return el;
}
function alertRow(el, msg) {
  let n = el.querySelector('.error');
  if (!n) { n = document.createElement('div'); n.className = 'error'; n.setAttribute('role', 'alert'); el.appendChild(n); }
  n.textContent = msg;
}

// ---- create form: §6.4 the 422 field-by-field contract --------------------
$('#create-form').addEventListener('submit', async (ev) => {
  ev.preventDefault();
  const btn = $('#create-btn');
  setError($('#create-error'), '');
  document.querySelectorAll('.field-error').forEach((s) => (s.textContent = ''));
  document.querySelectorAll('#create-form .invalid').forEach((i) => i.classList.remove('invalid'));

  btn.disabled = true;
  try {
    // 1) optional upload FIRST (the §7.1 flow: bytes → file_id → attach)
    let photoID;
    const file = $('#photo-file').files[0];
    if (file) {
      show($('#upload-state'));
      $('#upload-msg').textContent = 'uploading…';
      try {
        const up = await uploadFile(file, session.token, (f) => ($('#upload-bar').value = f));
        photoID = up.file_id;
        $('#upload-msg').textContent = `done (${(up.size / 1024).toFixed(0)} KiB)`;
      } catch (e) {
        if (e.status === 413) throw new ApiError(413, { error: `That file is too big (${(file.size / 1048576).toFixed(1)} MiB) — the server refused it.` });
        if (e.status === 422) throw new ApiError(422, { error: `The server refused the file: ${e.body.error}` });
        throw e;
      }
    }
    // 2) create with the id as a plain field value
    const body = { title: $('#title').value, body: $('#body').value };
    if (photoID) body.photo = photoID;
    await papi('/api/posts', { method: 'POST', body });
    $('#create-form').reset();
    show($('#upload-state'), false);
    await loadPosts();
  } catch (e) {
    if (e instanceof ApiError && e.status === 422 && e.fields.length) {
      const map = e.fieldMap(); // EVERY failing field at once
      for (const [field, msg] of Object.entries(map)) {
        const slot = document.querySelector(`.field-error[data-for="${field}"]`);
        if (slot) slot.textContent = msg;
        const input = $(`#${field}`);
        if (input) input.classList.add('invalid');
      }
      setError($('#create-error'), 'Check the marked fields.');
      document.querySelector('#create-form .invalid')?.scrollIntoView({ block: 'center', behavior: 'smooth' });
    } else if (e instanceof ApiError && e.status === 409) {
      setError($('#create-error'), `Conflict: ${e.body.error}`); // work preserved
    } else {
      setError($('#create-error'), e.isNetwork ? e.message : (e.body?.error || 'Something failed. Nothing was saved — retry.'));
    }
  } finally {
    btn.disabled = false;
  }
});

// ---- shell ----------------------------------------------------------------
function render() {
  const authed = !!session.token && !isExpired(session.token);
  if (session.token && !authed) clearSession(); // expired on arrival
  show($('#auth-card'), !authed);
  show($('#create-card'), authed);
  show($('#posts-section'), authed);
  const s = $('#session');
  s.innerHTML = '';
  if (authed) {
    const who = document.createElement('span');
    who.textContent = session.email + ' ';
    const out = document.createElement('button');
    out.className = 'ghost';
    out.textContent = 'Log out';
    out.onclick = clearSession; // stateless JWT: logout = discard
    s.append(who, out);
    loadPosts();
  }
}
render();
