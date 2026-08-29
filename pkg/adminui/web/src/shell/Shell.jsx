import { For, Show, createMemo, createResource } from "solid-js"
import { A, useNavigate, useLocation } from "@solidjs/router"
import { ThemeToggle, Button } from "../components/ui"
import { CommandPalette } from "../components/CommandPalette"
import { TenantSelect } from "../components/TenantSelect"
import { admin, logout } from "../lib/auth"
import { api } from "../lib/api"
import { pageCommands } from "../lib/commands"

// The console's taxonomy (ADMIN-CONSOLE-S1): Overview (the board) → App (the
// structure: Studio, docs, fleet — the OTHER faces, linked, not rebuilt) →
// Tenants (the instances) → the selected tenant's Data/Users/Files/History.
const NAV = [
  {
    label: "Platform",
    items: [
      { href: "/", title: "Overview", icon: "◈", enabled: true, end: true },
      { href: "/tenants", title: "Tenants", icon: "▦", enabled: true },
    ],
  },
  {
    label: "Tenant",
    items: [
      { href: "/data", title: "Data", icon: "≣", enabled: true },
      { href: "/users", title: "Users", icon: "◑", enabled: true },
      { href: "/files", title: "Files", icon: "▤", enabled: true },
      { href: "/history", title: "History", icon: "↺", enabled: true },
    ],
  },
  {
    label: "Insight",
    items: [
      // Observability (ADMIN-UI-V2): metrics charts, trace waterfall, anomalies.
      { href: "/observability", title: "Observability", icon: "◔", enabled: true },
      // Resources (CENTINELA-C-S1): the engine's own footprint + the attribution
      // verdict — "is it me, the database, or the plan's quota?" under load.
      { href: "/resources", title: "Resources", icon: "◉", enabled: true },
    ],
  },
]

// The sibling consoles — same platform, other faces. Studio designs/deploys the
// schema; /docs is the served API contract; /fleet exists only under fleet serve.
const CONSOLES = [
  { href: "/editor", title: "Studio (schema)", icon: "✎" },
  { href: "/docs", title: "API docs", icon: "❐" },
]

export function Shell(props) {
  const navigate = useNavigate()
  const location = useLocation()

  // Fleet marker: /health carries fleet_apps only in the in-process fleet
  // runtime — that's when a /fleet console exists to link to.
  const [health] = createResource(() => api.health().catch(() => null))
  const consoles = () => {
    const list = [...CONSOLES]
    if ((health()?.fleet_apps ?? 0) > 0) list.push({ href: "/fleet", title: "Fleet console", icon: "⚑" })
    return list
  }

  const baseCommands = createMemo(() => {
    const cmds = []
    for (const g of NAV) for (const it of g.items) {
      if (it.enabled) cmds.push({ id: "nav:" + it.href, label: "Go to " + it.title, hint: "Navigate", run: () => navigate(it.href) })
    }
    for (const c of consoles()) {
      cmds.push({ id: "console:" + c.href, label: "Open " + c.title, hint: "Consoles", run: () => window.open(c.href, "_blank") })
    }
    cmds.push({ id: "theme", label: "Toggle theme", hint: "Appearance", run: () => {
      const cur = document.documentElement.getAttribute("data-theme") || "light"
      const next = cur === "dark" ? "light" : "dark"
      document.documentElement.setAttribute("data-theme", next)
      try { localStorage.setItem("appximo_admin_theme", next) } catch { /* ignore */ }
    } })
    cmds.push({ id: "logout", label: "Log out", hint: "Session", run: () => { logout(); navigate("/login") } })
    return cmds
  })

  const allCommands = () => [...pageCommands(), ...baseCommands()]

  return (
    <div class="shell">
      <aside class="sidebar">
        <div class="brand"><span class="logo" aria-hidden="true" /><span>Appximo</span></div>
        <For each={NAV}>{(group) => (
          <nav class="navgroup">
            <div class="label">{group.label}</div>
            <For each={group.items}>{(it) => (
              <Show when={it.enabled} fallback={
                <span class="navitem" aria-disabled="true" title="Coming soon"><span aria-hidden="true">{it.icon}</span>{it.title}<span class="spacer" /><span class="muted" style={{ "font-size": "10px" }}>soon</span></span>
              }>
                <A href={it.href} class="navitem" activeClass="active" end={it.end ?? false}>
                  <span aria-hidden="true">{it.icon}</span>{it.title}
                </A>
              </Show>
            )}</For>
          </nav>
        )}</For>
        <nav class="navgroup">
          <div class="label">Consoles</div>
          <For each={consoles()}>{(c) => (
            <a href={c.href} target="_blank" rel="noopener" class="navitem" title="Opens in a new tab">
              <span aria-hidden="true">{c.icon}</span>{c.title}<span class="spacer" /><span class="muted" aria-hidden="true">↗</span>
            </a>
          )}</For>
        </nav>
        <div class="spacer" />
        <div class="muted" style={{ "font-size": "11px", padding: "8px" }}>Press <kbd>⌘K</kbd> for commands</div>
      </aside>

      <div class="main">
        <header class="topbar">
          <button class="btn btn-ghost btn-icon nav-toggle" aria-label="Menu" onClick={() => document.querySelector(".shell")?.classList.toggle("nav-open")}>☰</button>
          <strong>{titleFor(location.pathname)}</strong>
          <div class="spacer" />
          <TenantSelect />
          <ThemeToggle />
          <Show when={admin()}>
            <span class="secondary topbar-email" style={{ "font-size": "13px" }}>{admin().email}</span>
          </Show>
          <Button variant="ghost" size="sm" onClick={() => { logout(); navigate("/login") }}>Log out</Button>
        </header>
        <main class="content">{props.children}</main>
      </div>

      <CommandPalette commands={allCommands} />
    </div>
  )
}

function titleFor(path) {
  if (path.startsWith("/tenants")) return "Tenants"
  if (path.startsWith("/users")) return "Users"
  if (path.startsWith("/data")) return "Data"
  if (path.startsWith("/files")) return "Files"
  if (path.startsWith("/history")) return "History"
  if (path.startsWith("/observability")) return "Observability"
  if (path.startsWith("/resources")) return "Resources"
  return "Overview"
}
