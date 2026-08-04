import { createSignal, Show } from "solid-js"
import { useNavigate } from "@solidjs/router"
import { Button, Field } from "../components/ui"
import { login, completeMfa } from "../lib/auth"

export function Login() {
  const navigate = useNavigate()
  const [email, setEmail] = createSignal("")
  const [password, setPassword] = createSignal("")
  const [step, setStep] = createSignal("creds") // "creds" | "mfa"
  const [mfaToken, setMfaToken] = createSignal("")
  const [code, setCode] = createSignal("")
  const [err, setErr] = createSignal("")
  const [busy, setBusy] = createSignal(false)

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

  return (
    <div class="login-wrap">
      <div class="card login-card">
        <h1>Appximo Admin</h1>
        <Show when={step() === "creds"} fallback={
          <>
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
          </>
        }>
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
      </div>
    </div>
  )
}
