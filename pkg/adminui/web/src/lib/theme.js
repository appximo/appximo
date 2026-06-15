import { createSignal } from "solid-js"

// Canvas charts can't read live CSS variables (the canvas takes concrete colors at
// draw time), so when the light/dark theme flips we must rebuild a chart's option
// with freshly-resolved colors. themeTick() is a signal that bumps on every
// data-theme change; read it inside a chart-option memo to make the option reactive
// to the theme toggle. The MutationObserver is installed once, lazily.
const [themeVersion, setThemeVersion] = createSignal(0)
let started = false

function ensureObserver() {
  if (started || typeof MutationObserver === "undefined") return
  started = true
  const mo = new MutationObserver(() => setThemeVersion((v) => v + 1))
  mo.observe(document.documentElement, { attributes: true, attributeFilter: ["data-theme"] })
}

export function themeTick() {
  ensureObserver()
  return themeVersion()
}

// resolveColor returns a canvas-safe color string for a CSS custom property by
// letting the browser compute it (getComputedStyle on a hidden probe returns an
// rgb()/rgba() value, which every canvas accepts — unlike a raw oklch() token).
export function resolveColor(varName, fallback = "#888888") {
  try {
    const probe = document.createElement("span")
    probe.style.color = `var(${varName})`
    probe.style.display = "none"
    document.body.appendChild(probe)
    const c = getComputedStyle(probe).color
    probe.remove()
    return c || fallback
  } catch {
    return fallback
  }
}

// chartTheme resolves the palette a chart needs, fresh for the current theme.
export function chartTheme() {
  return {
    text: resolveColor("--color-text-secondary", "#666"),
    muted: resolveColor("--color-text-muted", "#999"),
    grid: resolveColor("--color-border", "#eee"),
    surface: resolveColor("--color-surface", "#fff"),
    brand: resolveColor("--color-action", "#3b5bdb"),
    ok: resolveColor("--color-status-ok-fg", "#2f9e44"),
    warn: resolveColor("--color-status-warn-fg", "#e8590c"),
    crit: resolveColor("--color-status-crit-fg", "#e03131"),
  }
}
