import { createSignal, For, Show, onMount } from "solid-js"

export function Button(props) {
  const variant = () => props.variant || "default"
  const cls = () => [
    "btn",
    variant() === "primary" ? "btn-primary" : "",
    variant() === "danger" ? "btn-danger" : "",
    variant() === "ghost" ? "btn-ghost" : "",
    props.size === "sm" ? "btn-sm" : "",
    props.class || "",
  ].join(" ")
  return (
    <button class={cls()} type={props.type || "button"} disabled={props.disabled}
      onClick={props.onClick} aria-label={props.ariaLabel}>
      {props.children}
    </button>
  )
}

export function Field(props) {
  return (
    <div class="field">
      <label for={props.id}>{props.label}</label>
      <input
        id={props.id}
        type={props.type || "text"}
        value={props.value || ""}
        placeholder={props.placeholder}
        autocomplete={props.autocomplete}
        inputmode={props.inputmode}
        onInput={(e) => props.onInput?.(e.currentTarget.value)}
        onKeyDown={props.onKeyDown}
      />
      <Show when={props.hint}><span class="hint">{props.hint}</span></Show>
      <Show when={props.error}><span class="err">{props.error}</span></Show>
    </div>
  )
}

// StatusBadge — ALWAYS dual channel: a coloured dot + an icon glyph + a text
// label, so status is never conveyed by colour alone (WCAG 1.4.1).
export function StatusBadge(props) {
  const map = {
    ok:   { cls: "badge-ok",   icon: "●", label: props.okLabel || "Active" },
    warn: { cls: "badge-warn", icon: "⏸", label: props.warnLabel || "Suspended" },
    crit: { cls: "badge-crit", icon: "✕", label: props.critLabel || "Error" },
  }
  const s = () => map[props.kind] || map.ok
  return (
    <span class={"badge " + s().cls}>
      <span aria-hidden="true">{s().icon}</span>
      <span>{s().label}</span>
    </span>
  )
}

const STORAGE_THEME = "appximo_admin_theme"
export function initTheme() {
  let t = "light"
  try { t = localStorage.getItem(STORAGE_THEME) || "light" } catch { /* ignore */ }
  document.documentElement.setAttribute("data-theme", t)
  return t
}
export function ThemeToggle() {
  const [theme, setTheme] = createSignal(document.documentElement.getAttribute("data-theme") || "light")
  const toggle = () => {
    const next = theme() === "dark" ? "light" : "dark"
    document.documentElement.setAttribute("data-theme", next)
    try { localStorage.setItem(STORAGE_THEME, next) } catch { /* ignore */ }
    setTheme(next)
  }
  return (
    <Button variant="ghost" class="btn-icon" ariaLabel="Toggle theme" onClick={toggle}>
      {theme() === "dark" ? "☀" : "☾"}
    </Button>
  )
}

// Toasts — minimal store + viewport. No skeletons/spinners for fast ops; a toast
// confirms the result of an action instead.
const [toasts, setToasts] = createSignal([])
let toastId = 0
export function toast(message, kind = "ok") {
  const id = ++toastId
  setToasts((t) => [...t, { id, message, kind }])
  setTimeout(() => setToasts((t) => t.filter((x) => x.id !== id)), 3500)
}
export function Toaster() {
  return (
    <div class="toasts" aria-live="polite">
      <For each={toasts()}>{(t) => <div class={"toast " + t.kind}>{t.message}</div>}</For>
    </div>
  )
}
