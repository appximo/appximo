import { createSignal } from "solid-js"
import { api, getToken, setToken } from "./api"

// Auth store. The token persists in localStorage (this is a SERVED app, not a
// sandboxed artifact, so localStorage is the right place) so a refresh keeps the
// session. `admin` holds the logged-in super-admin's public profile.
const [token, setTokenSig] = createSignal(getToken())
const [admin, setAdmin] = createSignal(null)

export { token, admin }
export function isAuthed() { return !!token() }

// login returns { mfaRequired, mfaToken } when a second factor is needed; the
// caller then completes it via completeMfa. On full success the token is stored.
export async function login(email, password) {
  const res = await api.login(email, password)
  if (res && res.mfa_required) {
    return { mfaRequired: true, mfaToken: res.mfa_token }
  }
  applySession(res)
  return { mfaRequired: false }
}

export async function completeMfa(mfaToken, code) {
  const res = await api.mfaVerify(mfaToken, code)
  applySession(res)
}

function applySession(res) {
  if (!res || !res.token) throw new Error("no token in response")
  setToken(res.token)
  setTokenSig(res.token)
  setAdmin(res.admin || null)
}

export function logout() {
  setToken("")
  setTokenSig("")
  setAdmin(null)
}
