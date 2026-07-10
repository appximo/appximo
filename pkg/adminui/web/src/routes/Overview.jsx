import { createResource, Show, For } from "solid-js"
import { useNavigate } from "@solidjs/router"
import { Button } from "../components/ui"
import { api, ApiError } from "../lib/api"
import { logout } from "../lib/auth"
import { setSelectedTenant } from "../lib/tenantContext"

// Overview — the state board (ADMIN-CONSOLE-S1): what apps/tenants/data exist,
// at a glance, plus the doors into the other consoles (Studio, API docs, Fleet).
// It COMPOSES existing surfaces — /health (version + fleet marker),
// /admin/served-resources (live surface + activation), /admin/tenants (per-tenant
// inventory incl. row/user estimates) — no new engine machinery behind it.
export function Overview() {
  const navigate = useNavigate()

  const [tenants] = createResource(async () => {
    try { return (await api.listTenants()).tenants || [] }
    catch (ex) {
      if (ex instanceof ApiError && (ex.status === 401 || ex.status === 403)) { logout(); navigate("/login") }
      throw ex
    }
  })
  const [health] = createResource(() => api.health().catch(() => null))
  const [served] = createResource(() => api.servedResources().catch(() => null))
  // The boot schema (its name = the app this engine serves). Same read Studio boots from.
  const [bootSchema] = createResource(async () => {
    try {
      const res = await fetch("/editor/current-schema")
      return res.ok ? await res.json() : null
    } catch { return null }
  })

  const totals = () => {
    const list = tenants() || []
    return {
      tenants: list.length,
      suspended: list.filter((t) => t.suspended).length,
      rows: list.reduce((a, t) => a + (t.data_rows || 0), 0),
      users: list.reduce((a, t) => a + (t.user_count || 0), 0),
    }
  }
  const appName = () => bootSchema()?.name || "—"
  const activation = () => served()?.activation === "hot_swap" ? "hot-swap deploys" : "restart to activate new resources"
  const isFleet = () => (health()?.fleet_apps ?? 0) > 0

  const goTenant = (id, path) => { setSelectedTenant(id); navigate(path) }

  return (
    <>
      <div class="pagehead">
        <h1>Overview</h1>
        <span class="spacer" />
        <Button variant="ghost" size="sm" onClick={() => window.open("/editor", "_blank")}>Open Studio</Button>
        <Button variant="ghost" size="sm" onClick={() => window.open("/docs", "_blank")}>API docs</Button>
        <Show when={isFleet()}>
          <Button variant="ghost" size="sm" onClick={() => window.open("/fleet", "_blank")}>Fleet console</Button>
        </Show>
      </div>

      <div class="statgrid" style={{ "margin-bottom": "var(--space-4)" }}>
        <StatCard label="App (boot schema)" sub={health() ? `engine ${health().version}` : ""}>{appName()}</StatCard>
        <StatCard label="Served resources" sub={activation()}>{served()?.resources?.length ?? "—"}</StatCard>
        <StatCard label="Tenants" sub={totals().suspended ? `${totals().suspended} suspended` : "all active"}>{totals().tenants}</StatCard>
        <StatCard label="Data rows" sub="pg_stat estimate">{fmtNum(totals().rows)}</StatCard>
        <StatCard label="Users" sub="across all tenants">{fmtNum(totals().users)}</StatCard>
      </div>

      <Show when={tenants.error}><div class="errbar">Could not load tenants: {tenants.error.message}</div></Show>

      <div class="card" style={{ padding: "var(--space-3)" }}>
        <div class="row" style={{ "margin-bottom": "var(--space-2)" }}>
          <strong style={{ "font-size": "13px" }}>Tenants</strong>
          <span class="spacer" />
          <Button size="sm" variant="ghost" onClick={() => navigate("/tenants")}>Manage tenants</Button>
        </div>
        <Show when={(tenants() || []).length > 0} fallback={<div class="empty">{tenants.loading ? "Loading…" : "No tenants yet — create the first one under Tenants."}</div>}>
          <table class="minitable">
            <thead>
              <tr><th>Tenant</th><th class="num-h">Resources</th><th class="num-h">Rows(~)</th><th class="num-h">Users</th><th>Status</th><th></th></tr>
            </thead>
            <tbody>
              <For each={tenants()}>{(t) => (
                <tr>
                  <td><span class="cell-id" title={t.id}>{t.display_name || t.id}</span> <span class="muted" style={{ "font-size": "11px" }}>{t.id}</span></td>
                  <td class="num">{t.resource_count ?? 0}</td>
                  <td class="num">{fmtNum(t.data_rows)}</td>
                  <td class="num">{fmtNum(t.user_count)}</td>
                  <td>{t.suspended ? <span class="muted">Suspended</span> : <span>Active</span>}</td>
                  <td>
                    <div class="cell-actions row" style={{ "justify-content": "flex-end", gap: "4px" }}>
                      <Button size="sm" variant="ghost" onClick={() => goTenant(t.id, "/data")}>Data</Button>
                      <Button size="sm" variant="ghost" onClick={() => goTenant(t.id, "/users")}>Users</Button>
                      <Button size="sm" variant="ghost" onClick={() => goTenant(t.id, "/files")}>Files</Button>
                      <Button size="sm" variant="ghost" onClick={() => goTenant(t.id, "/history")}>History</Button>
                    </div>
                  </td>
                </tr>
              )}</For>
            </tbody>
          </table>
        </Show>
      </div>
    </>
  )
}

function StatCard(props) {
  return (
    <div class="stat card">
      <div class="stat-label">{props.label}</div>
      <div class="stat-value">{props.children}</div>
      <Show when={props.sub}><div class="stat-sub muted">{props.sub}</div></Show>
    </div>
  )
}

function fmtNum(v) {
  return Number(v || 0).toLocaleString("en-US")
}
