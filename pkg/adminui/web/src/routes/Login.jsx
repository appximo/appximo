import { createSignal, onMount, Show } from "solid-js"
import { useNavigate } from "@solidjs/router"
import { Button, Field } from "../components/ui"
import { login, completeMfa, bootstrap } from "../lib/auth"
import { api } from "../lib/api"

export function Login() {
  const navigate = useNavigate()
  const [email, setEmail] = createSignal("")
  const [password, setPassword] = createSignal("")
  const [step, setStep] = createSignal("creds") // "creds" | "mfa" | "bootstrap"
  const [mfaToken, setMfaToken] = createSignal("")
  const [code, setCode] = createSignal("")
  const [adminKey, setAdminKey] = createSignal("")
  const [err, setErr] = createSignal("")
  const [busy, setBusy] = createSignal(false)

  // First-run detection (PHASE4-FIRST-MILE-S1): if no platform admin exists yet,
  // the login form is a dead end — guide the operator to create the first one.
  // On any status error we fall back to the normal sign-in form (fail safe).
  onMount(async () => {
    try {
      const s = await api.authStatus()
      if (s && s.bootstrapped === false) setStep("bootstrap")
    } catch { /* status unavailable — keep the sign-in form */ }
  })

  const submitCreds = async (e) => {
    e?.preventDefault()
    setErr(""); setBusy(true)
    try {
      const r = await login(email().trim(), password())
      if (r.mfaRequired) { setMfaToken(r.mfaToken); setStep("mfa") }
      else navigate("/")
    } catch (ex) {
      // Generic message, consistent with the backend's anti-enumeration response.
      setErr(ex.status === 401 ? "Invalid credentials." : "Sign-in failed. Try again.")
    } finally { setBusy(false) }
  }

  const submitMfa = async (e) => {
    e?.preventDefault()
    setErr(""); setBusy(true)
    try {
      await completeMfa(mfaToken(), code().trim())
      navigate("/")
    } catch (ex) {
      setErr(ex.status === 401 ? "Invalid code." : "Verification failed.")
    } finally { setBusy(false) }
  }

  const submitBootstrap = async (e) => {
    e?.preventDefault()
    setErr(""); setBusy(true)
    try {
      await bootstrap(adminKey().trim(), email().trim(), password())
      navigate("/")
    } catch (ex) {
      if (ex.status === 403) setErr("Wrong admin key. Use the exact ADMIN_KEY value the server was started with.")
      else if (ex.status === 409) { setErr("An admin already exists — sign in instead."); setStep("creds") }
      else setErr((ex.body && ex.body.error) || "Could not create the admin. Try again.")
    } finally { setBusy(false) }
  }

  return (
    <div class="login-wrap">
      <div class="card login-card">
        <h1>Appximo Admin</h1>
        <Show when={step() === "creds"}>
          <p class="sub">Sign in to the platform admin panel.</p>
          <form onSubmit={submitCreds}>
            <Show when={err()}><div class="errbar">{err()}</div></Show>
            <Field id="email" label="Email" type="email" value={email()} onInput={setEmail}
              placeholder="you@example.com" autocomplete="username" />
            <Field id="password" label="Password" type="password" value={password()} onInput={setPassword}
              autocomplete="current-password" />
            <Button type="submit" variant="primary" disabled={busy()}>
              {busy() ? "Signing in…" : "Sign in"}
            </Button>
          </form>
        </Show>
        <Show when={step() === "mfa"}>
          <p class="sub">Enter the 6-digit code from your authenticator (or a backup code).</p>
          <form onSubmit={submitMfa}>
            <Show when={err()}><div class="errbar">{err()}</div></Show>
            <Field id="code" label="Authentication code" value={code()} onInput={setCode}
              placeholder="123456" autocomplete="one-time-code" inputmode="numeric" />
            <Button type="submit" variant="primary" disabled={busy()} class="btn" >
              {busy() ? "Verifying…" : "Verify"}
            </Button>
            <Button variant="ghost" class="btn" onClick={() => { setStep("creds"); setErr(""); setCode("") }}>Back</Button>
          </form>
        </Show>
        <Show when={step() === "bootstrap"}>
          <p class="sub">
            <strong>No admin exists yet.</strong> This server has just been set up —
            create the first platform admin now. You'll need the <code>ADMIN_KEY</code> the
            server was started with (it's in the environment / .env of whoever runs it).
          </p>
          <form onSubmit={submitBootstrap}>
            <Show when={err()}><div class="errbar">{err()}</div></Show>
            <Field id="adminkey" label="Admin key (the server's ADMIN_KEY)" type="password"
              value={adminKey()} onInput={setAdminKey} autocomplete="off" />
            <Field id="email" label="Your email" type="email" value={email()} onInput={setEmail}
              placeholder="you@example.com" autocomplete="username" />
            <Field id="password" label="Choose a password (12+ characters)" type="password"
              value={password()} onInput={setPassword} autocomplete="new-password" />
            <Button type="submit" variant="primary" disabled={busy()}>
              {busy() ? "Creating…" : "Create the first admin"}
            </Button>
          </form>
          <p class="sub" style="margin-top:12px">
            Prefer the terminal? The same thing:
            <br /><code>appximo admin create --email you@example.com --password '…'</code>
          </p>
          <Button variant="ghost" class="btn" onClick={() => { setStep("creds"); setErr("") }}>
            I already have an admin — sign in
          </Button>
        </Show>
      </div>
    </div>
  )
}
