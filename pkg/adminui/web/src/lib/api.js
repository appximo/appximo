// API client for the ADMIN-API-V1 backend. All routes live under /admin/* on the
// SAME origin the panel is served from, so paths are relative. The platform JWT is
// attached as a Bearer header. A 401/403 surfaces as an ApiError the caller maps.

const TOKEN_KEY = "appitools_admin_token"

export function getToken() {
  try { return localStorage.getItem(TOKEN_KEY) || "" } catch { return "" }
}
export function setToken(t) {
  try { t ? localStorage.setItem(TOKEN_KEY, t) : localStorage.removeItem(TOKEN_KEY) } catch { /* ignore */ }
}

export class ApiError extends Error {
  constructor(status, body) {
    super((body && body.error) || `HTTP ${status}`)
    this.status = status
    this.body = body
  }
}

async function request(method, path, { body, auth = true } = {}) {
  const headers = { "Content-Type": "application/json" }
  if (auth) {
    const t = getToken()
    if (t) headers["Authorization"] = "Bearer " + t
  }
  const res = await fetch(path, {
    method,
    headers,
    body: body === undefined ? undefined : JSON.stringify(body),
  })
  let data = null
  const text = await res.text()
  if (text) { try { data = JSON.parse(text) } catch { data = { raw: text } } }
  if (!res.ok) throw new ApiError(res.status, data)
  return data
}

export const api = {
  // auth
  login: (email, password) => request("POST", "/admin/auth/login", { body: { email, password }, auth: false }),
  mfaVerify: (mfa_token, code) => request("POST", "/admin/auth/mfa/verify", { body: { mfa_token, code }, auth: false }),
  refresh: () => request("POST", "/admin/auth/refresh", { body: {} }),
  // tenants
  listTenants: () => request("GET", "/admin/tenants"),
  getTenant: (id) => request("GET", "/admin/tenants/" + encodeURIComponent(id)),
  createTenant: (payload) => request("POST", "/admin/tenants", { body: payload }),
  suspendTenant: (id) => request("POST", `/admin/tenants/${encodeURIComponent(id)}/suspend`, { body: {} }),
  activateTenant: (id) => request("POST", `/admin/tenants/${encodeURIComponent(id)}/activate`, { body: {} }),
  deleteTenant: (id, confirm) => request("DELETE", "/admin/tenants/" + encodeURIComponent(id), { body: { confirm } }),
  // users (per tenant)
  listUsers: (tid) => request("GET", `/admin/tenants/${encodeURIComponent(tid)}/users`),
  createUser: (tid, payload) => request("POST", `/admin/tenants/${encodeURIComponent(tid)}/users`, { body: payload }),
  updateUser: (tid, uid, patch) => request("PATCH", `/admin/tenants/${encodeURIComponent(tid)}/users/${encodeURIComponent(uid)}`, { body: patch }),
  deleteUser: (tid, uid) => request("DELETE", `/admin/tenants/${encodeURIComponent(tid)}/users/${encodeURIComponent(uid)}`),
  // data navigation (read-only browse)
  listResources: (tid) => request("GET", `/admin/tenants/${encodeURIComponent(tid)}/resources`),
  listData: (tid, resource, qs) => request("GET", `/admin/tenants/${encodeURIComponent(tid)}/data/${encodeURIComponent(resource)}${qs ? "?" + qs : ""}`),
  // observability (read-only): one round-trip returns latency, SLO, anomalies,
  // recent + persisted-slow traces; `params` adds ?history=<hours> & ?traces=slow.
  observability: (tid, params) => {
    const qs = params ? new URLSearchParams(params).toString() : ""
    return request("GET", `/admin/observability/tenants/${encodeURIComponent(tid)}${qs ? "?" + qs : ""}`)
  },
  // files (per tenant; UI-F5-S1 admin routes — same Store as the tenant API)
  listFiles: (tid, page = 1) => request("GET", `/admin/tenants/${encodeURIComponent(tid)}/files?page=${page}&per_page=50`),
  fileURL: (tid, fid) => request("GET", `/admin/tenants/${encodeURIComponent(tid)}/files/${encodeURIComponent(fid)}/url`),
  deleteFile: (tid, fid) => request("DELETE", `/admin/tenants/${encodeURIComponent(tid)}/files/${encodeURIComponent(fid)}`),
  uploadFile: async (tid, file) => {
    const form = new FormData()
    form.append("file", file)
    const headers = {}
    const t = getToken()
    if (t) headers["Authorization"] = "Bearer " + t
    const res = await fetch(`/admin/tenants/${encodeURIComponent(tid)}/files`, { method: "POST", headers, body: form })
    let data = null
    const text = await res.text()
    if (text) { try { data = JSON.parse(text) } catch { data = { raw: text } } }
    if (!res.ok) throw new ApiError(res.status, data)
    return data
  },
  // schema version history (VERSION-S1; read-only here — rollback lives in Studio)
  schemaHistory: (tid, page = 1) => request("GET", `/admin/tenants/${encodeURIComponent(tid)}/schema/history?page=${page}&per_page=50`),
  schemaVersion: (tid, v) => request("GET", `/admin/tenants/${encodeURIComponent(tid)}/schema/history/${v}`),
  // engine surface (for the Overview): what the running engine serves + how a
  // deploy activates; /health is unauthenticated (version + fleet marker).
  servedResources: () => request("GET", "/admin/served-resources"),
  health: () => request("GET", "/health", { auth: false }),
}
