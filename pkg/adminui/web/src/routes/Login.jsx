import { createSignal, onMount, Show } from "solid-js"
import { useNavigate } from "@solidjs/router"
import { Button, Field, LangToggle } from "../components/ui"
import { t } from "../lib/i18n"
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
      setErr(ex.status === 401 ? t("l.invalid") : t("l.failed"))
    } finally { setBusy(false) }
  }

  const submitMfa = async (e) => {
    e?.preventDefault()
    setErr(""); setBusy(true)
    try {
      await completeMfa(mfaToken(), code().trim())
      navigate("/")
    } catch (ex) {
      setErr(ex.status === 401 ? t("l.badCode") : t("l.verifyFailed"))
    } finally { setBusy(false) }
  }

  const submitBootstrap = async (e) => {
    e?.preventDefault()
    setErr(""); setBusy(true)
    try {
      await bootstrap(adminKey().trim(), email().trim(), password())
      navigate("/")
    } catch (ex) {
      if (ex.status === 403) setErr(t("l.wrongKey"))
      else if (ex.status === 409) { setErr(t("l.exists")); setStep("creds") }
      else setErr((ex.body && ex.body.error) || t("l.createFailed"))
    } finally { setBusy(false) }
  }

  return (
    <div class="login-wrap">
      <div class="card login-card">
        <div class="row"><h1 style={{ margin: 0 }}>{t("l.title")}</h1><span class="spacer" /><LangToggle /></div>
        <Show when={step() === "creds"}>
          <p class="sub">{t("l.sub")}</p>
          <form onSubmit={submitCreds}>
            <Show when={err()}><div class="errbar">{err()}</div></Show>
            <Field id="email" label={t("l.email")} type="email" value={email()} onInput={setEmail}
              placeholder="you@example.com" autocomplete="username" />
            <Field id="password" label={t("l.password")} type="password" value={password()} onInput={setPassword}
              autocomplete="current-password" />
            <Button type="submit" variant="primary" disabled={busy()}>
              {busy() ? t("l.signingIn") : t("l.signin")}
            </Button>
          </form>
        </Show>
        <Show when={step() === "mfa"}>
          <p class="sub">{t("l.mfa")}</p>
          <form onSubmit={submitMfa}>
            <Show when={err()}><div class="errbar">{err()}</div></Show>
            <Field id="code" label={t("l.code")} value={code()} onInput={setCode}
              placeholder="123456" autocomplete="one-time-code" inputmode="numeric" />
            <Button type="submit" variant="primary" disabled={busy()} class="btn" >
              {busy() ? t("l.verifying") : t("l.verify")}
            </Button>
            <Button variant="ghost" class="btn" onClick={() => { setStep("creds"); setErr(""); setCode("") }}>{t("l.back")}</Button>
          </form>
        </Show>
        <Show when={step() === "bootstrap"}>
          <p class="sub" innerHTML={t("l.boot")} />
          <form onSubmit={submitBootstrap}>
            <Show when={err()}><div class="errbar">{err()}</div></Show>
            <Field id="adminkey" label={t("l.adminKey")} type="password"
              value={adminKey()} onInput={setAdminKey} autocomplete="off" />
            <Field id="email" label={t("l.yourEmail")} type="email" value={email()} onInput={setEmail}
              placeholder="you@example.com" autocomplete="username" />
            <Field id="password" label={t("l.choosePw")} type="password"
              value={password()} onInput={setPassword} autocomplete="new-password" />
            <Button type="submit" variant="primary" disabled={busy()}>
              {busy() ? t("c.creating") : t("l.createFirst")}
            </Button>
          </form>
          <p class="sub" style="margin-top:12px">
            {t("l.terminal")}
            <br /><code>appximo admin create --email you@example.com --password '…'</code>
          </p>
          <Button variant="ghost" class="btn" onClick={() => { setStep("creds"); setErr("") }}>
            {t("l.haveAdmin")}
          </Button>
        </Show>
      </div>
    </div>
  )
}
