import { For, Show, createMemo, createResource } from "solid-js"
import { A, useNavigate, useLocation } from "@solidjs/router"
import { ThemeToggle, LangToggle, Button } from "../components/ui"
import { CommandPalette } from "../components/CommandPalette"
import { TenantSelect } from "../components/TenantSelect"
import { admin, logout } from "../lib/auth"
import { api } from "../lib/api"
import { pageCommands } from "../lib/commands"
import { t } from "../lib/i18n"

// The console's taxonomy (ADMIN-CONSOLE-S1): Overview (the board) → App (the
// structure: Studio, docs, fleet — the OTHER faces, linked, not rebuilt) →
// Tenants (the instances) → the selected tenant's Data/Users/Files/History →
// Health (MANUAL-OPERACION-S1: the group is named for the question it answers —
// "how is it?" — not for the discipline that answers it).
const NAV = [
  {
    label: t("nav.platform"),
    items: [
      { href: "/", title: t("nav.overview"), icon: "◈", enabled: true, end: true },
      { href: "/tenants", title: t("nav.tenants"), icon: "▦", enabled: true },
    ],
  },
  {
    label: t("nav.tenant"),
    items: [
      { href: "/data", title: t("nav.data"), icon: "≣", enabled: true },
      { href: "/users", title: t("nav.users"), icon: "◑", enabled: true },
      { href: "/files", title: t("nav.files"), icon: "▤", enabled: true },
      { href: "/history", title: t("nav.history"), icon: "↺", enabled: true },
    ],
  },
  {
    label: t("nav.insight"),
    items: [
      // Observability (ADMIN-UI-V2): metrics charts, trace waterfall, anomalies, problems.
      { href: "/observability", title: t("nav.observability"), icon: "◔", enabled: true },
      // Resources (CENTINELA-C-S1): the engine's own footprint + the attribution
      // verdict — "is it me, the database, or the plan's quota?" under load.
      { href: "/resources", title: t("nav.resources"), icon: "◉", enabled: true },
    ],
  },
]

// The sibling consoles — same platform, other faces. Studio designs/deploys the
// schema; /docs is the served API contract; /fleet exists only under fleet serve.
const CONSOLES = [
  { href: "/editor", title: t("nav.studio"), icon: "✎" },
  { href: "/docs", title: t("nav.docs"), icon: "❐" },
]

export function Shell(props) {
  const navigate = useNavigate()
  const location = useLocation()

  // Fleet marker: /health carries fleet_apps only in the in-process fleet
  // runtime — that's when a /fleet console exists to link to.
  const [health] = createResource(() => api.health().catch(() => null))
  const consoles = () => {
    const list = [...CONSOLES]
    if ((health()?.fleet_apps ?? 0) > 0) list.push({ href: "/fleet", title: t("nav.fleet"), icon: "⚑" })
    return list
  }

  const baseCommands = createMemo(() => {
    const cmds = []
    for (const g of NAV) for (const it of g.items) {
      if (it.enabled) cmds.push({ id: "nav:" + it.href, label: t("cmd.goto", { name: it.title }), hint: t("cmd.navigate"), run: () => navigate(it.href) })
    }
    for (const c of consoles()) {
      cmds.push({ id: "console:" + c.href, label: t("cmd.open", { name: c.title }), hint: t("cmd.consoles"), run: () => window.open(c.href, "_blank") })
    }
    cmds.push({ id: "theme", label: t("cmd.theme"), hint: t("cmd.appearance"), run: () => {
      const cur = document.documentElement.getAttribute("data-theme") || "light"
      const next = cur === "dark" ? "light" : "dark"
      document.documentElement.setAttribute("data-theme", next)
      try { localStorage.setItem("appximo_admin_theme", next) } catch { /* ignore */ }
    } })
    cmds.push({ id: "logout", label: t("cmd.logout"), hint: t("cmd.session"), run: () => { logout(); navigate("/login") } })
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
                <span class="navitem" aria-disabled="true"><span aria-hidden="true">{it.icon}</span>{it.title}</span>
              }>
                <A href={it.href} class="navitem" activeClass="active" end={it.end ?? false}>
                  <span aria-hidden="true">{it.icon}</span>{it.title}
                </A>
              </Show>
            )}</For>
          </nav>
        )}</For>
        <nav class="navgroup">
          <div class="label">{t("nav.consoles")}</div>
          <For each={consoles()}>{(c) => (
            <a href={c.href} target="_blank" rel="noopener" class="navitem" title={t("nav.newtab")}>
              <span aria-hidden="true">{c.icon}</span>{c.title}<span class="spacer" /><span class="muted" aria-hidden="true">↗</span>
            </a>
          )}</For>
        </nav>
        <div class="spacer" />
        <div class="muted" style={{ "font-size": "11px", padding: "8px" }}>{t("nav.commands")}</div>
      </aside>

      <div class="main">
        <header class="topbar">
          <button class="btn btn-ghost btn-icon nav-toggle" aria-label={t("top.menu")} onClick={() => document.querySelector(".shell")?.classList.toggle("nav-open")}>☰</button>
          <strong>{titleFor(location.pathname)}</strong>
          <div class="spacer" />
          <TenantSelect />
          <LangToggle />
          <ThemeToggle />
          <Show when={admin()}>
            <span class="secondary topbar-email" style={{ "font-size": "13px" }}>{admin().email}</span>
          </Show>
          <Button variant="ghost" size="sm" onClick={() => { logout(); navigate("/login") }}>{t("top.logout")}</Button>
        </header>
        <main class="content">{props.children}</main>
      </div>

      <CommandPalette commands={allCommands} />
    </div>
  )
}

function titleFor(path) {
  if (path.startsWith("/tenants")) return t("nav.tenants")
  if (path.startsWith("/users")) return t("nav.users")
  if (path.startsWith("/data")) return t("nav.data")
  if (path.startsWith("/files")) return t("nav.files")
  if (path.startsWith("/history")) return t("nav.history")
  if (path.startsWith("/observability")) return t("nav.observability")
  if (path.startsWith("/resources")) return t("nav.resources")
  return t("nav.overview")
}

// PageIntro — the one line under every h1 saying what the section answers
// (MANUAL-OPERACION-S1: an owner entering for the first time should not have to
// guess what "Resources" or "Observability" are for).
export function PageIntro(props) {
  return <p class="page-intro muted">{props.children}</p>
}
