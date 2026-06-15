import { For, Show, createMemo } from "solid-js"
import { A, useNavigate, useLocation } from "@solidjs/router"
import { ThemeToggle, Button } from "../components/ui"
import { CommandPalette } from "../components/CommandPalette"
import { TenantSelect } from "../components/TenantSelect"
import { admin, logout } from "../lib/auth"
import { pageCommands } from "../lib/commands"

const NAV = [
  {
    label: "Management",
    items: [
      { href: "/tenants", title: "Tenants", icon: "▦", enabled: true },
      { href: "/users", title: "Users", icon: "◑", enabled: true },
      { href: "/data", title: "Data", icon: "≣", enabled: true },
    ],
  },
  {
    label: "Insight",
    items: [
      // Observability (ADMIN-UI-V2): metrics charts, trace waterfall, anomalies.
      { href: "/observability", title: "Observability", icon: "◔", enabled: true },
    ],
  },
]

export function Shell(props) {
  const navigate = useNavigate()
  const location = useLocation()

  const baseCommands = createMemo(() => {
    const cmds = []
    for (const g of NAV) for (const it of g.items) {
      if (it.enabled) cmds.push({ id: "nav:" + it.href, label: "Go to " + it.title, hint: "Navigate", run: () => navigate(it.href) })
    }
    cmds.push({ id: "theme", label: "Toggle theme", hint: "Appearance", run: () => {
      const cur = document.documentElement.getAttribute("data-theme") || "light"
      const next = cur === "dark" ? "light" : "dark"
      document.documentElement.setAttribute("data-theme", next)
      try { localStorage.setItem("appitools_admin_theme", next) } catch { /* ignore */ }
    } })
    cmds.push({ id: "logout", label: "Log out", hint: "Session", run: () => { logout(); navigate("/login") } })
    return cmds
  })

  const allCommands = () => [...pageCommands(), ...baseCommands()]

  return (
    <div class="shell">
      <aside class="sidebar">
        <div class="brand"><span class="logo" aria-hidden="true" /><span>Appitools</span></div>
        <For each={NAV}>{(group) => (
          <nav class="navgroup">
            <div class="label">{group.label}</div>
            <For each={group.items}>{(it) => (
              <Show when={it.enabled} fallback={
                <span class="navitem" aria-disabled="true" title="Coming soon"><span aria-hidden="true">{it.icon}</span>{it.title}<span class="spacer" /><span class="muted" style={{ "font-size": "10px" }}>soon</span></span>
              }>
                <A href={it.href} class="navitem" activeClass="active" end={false}>
                  <span aria-hidden="true">{it.icon}</span>{it.title}
                </A>
              </Show>
            )}</For>
          </nav>
        )}</For>
        <div class="spacer" />
        <div class="muted" style={{ "font-size": "11px", padding: "8px" }}>Press <kbd>⌘K</kbd> for commands</div>
      </aside>

      <div class="main">
        <header class="topbar">
          <strong>{titleFor(location.pathname)}</strong>
          <div class="spacer" />
          <TenantSelect />
          <ThemeToggle />
          <Show when={admin()}>
            <span class="secondary" style={{ "font-size": "13px" }}>{admin().email}</span>
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
  if (path.startsWith("/observability")) return "Observability"
  return "Admin"
}
