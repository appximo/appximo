import { createResource, Show, For } from "solid-js"
import { useNavigate } from "@solidjs/router"
import { Button } from "../components/ui"
import { PageIntro } from "../shell/Shell"
import { api, ApiError } from "../lib/api"
import { logout } from "../lib/auth"
import { selectedTenant, setSelectedTenant } from "../lib/tenantContext"
import { t } from "../lib/i18n"

// Overview — the state board (ADMIN-CONSOLE-S1): what apps/tenants/data exist,
// at a glance, plus the doors into the other consoles (Studio, API docs, Fleet).
// It COMPOSES existing surfaces — /health (version + fleet marker),
// /admin/served-resources (live surface + activation), /admin/tenants (per-tenant
// inventory incl. row/user estimates) — no new engine machinery behind it.
//
// MANUAL-OPERACION-S1 added the "Health now" strip: the self-monitor's latest
// verdict, the last backup and the disk floor (layer 5 — OPS-40 had left them
// at the bottom of Resources only) and the selected tenant's 24 h problem
// count, each card linking to the screen that explains it. Same reads the
// Resources and Observability screens do; nothing new behind it.
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
  // One background tick of the self-monitor (no live=1: the board must not
  // switch the collector to its 1 s cadence — that is the Resources screen's job).
  const [res] = createResource(() => api.resources({ series: 1 }).catch(() => null))
  const [obs] = createResource(() => selectedTenant() || null, (id) => api.observability(id, { history: 1 }).catch(() => null))

  const totals = () => {
    const list = tenants() || []
    return {
      tenants: list.length,
      suspended: list.filter((x) => x.suspended).length,
      rows: list.reduce((a, x) => a + (x.data_rows || 0), 0),
      users: list.reduce((a, x) => a + (x.user_count || 0), 0),
    }
  }
  const appName = () => bootSchema()?.name || "—"
  const activation = () => served()?.activation === "hot_swap" ? t("ov.hotswap") : t("ov.restart")
  const isFleet = () => (health()?.fleet_apps ?? 0) > 0

  const goTenant = (id, path) => { setSelectedTenant(id); navigate(path) }

  // ── health strip ──────────────────────────────────────────────────────────
  const latest = () => res()?.latest
  const verdict = () => latest()?.verdict
  const attrKind = (a) => ({ healthy: "ok", cpu_saturated: "warn", gc_pressure: "warn", lock_contention: "warn" }[a] || (a ? "crit" : "ok"))
  const host = () => latest()?.host || {}
  const bk = () => host().backup || {}
  const disks = () => (host().disks || []).filter((d) => !d.err)
  const lowDisk = () => disks().find((d) => d.low)
  const fmtAge = (sec) => (sec == null || sec <= 0 ? "—" : sec >= 172800 ? (sec / 86400).toFixed(1) + " d" : sec >= 7200 ? (sec / 3600).toFixed(1) + " h" : (sec / 60).toFixed(0) + " min")
  const bkKind = () => (!host().enabled || !bk().dir ? "ok" : bk().alarm ? "crit" : bk().status === "ok" ? "ok" : "warn")
  const bkText = () => {
    if (!host().enabled || !bk().dir) return t("ov.backup.na")
    if (bk().status === "ok" && !bk().stale) return t("r.ok")
    if (bk().status === "ok" && bk().stale) return t("r.stale")
    if (bk().status === "failed") return t("r.failed")
    if (bk().status === "none") return bk().stale ? t("r.neverRan") : t("r.noRunYet")
    return t("r.emptyStatus")
  }
  const groups = () => obs()?.error_groups || []

  return (
    <>
      <div class="pagehead">
        <h1>{t("ov.title")}</h1>
        <span class="spacer" />
        <Button variant="ghost" size="sm" onClick={() => window.open("/editor", "_blank")}>{t("ov.openStudio")}</Button>
        <Button variant="ghost" size="sm" onClick={() => window.open("/docs", "_blank")}>{t("ov.apiDocs")}</Button>
        <Show when={isFleet()}>
          <Button variant="ghost" size="sm" onClick={() => window.open("/fleet", "_blank")}>{t("ov.fleet")}</Button>
        </Show>
      </div>
      <PageIntro>{t("intro.overview")}</PageIntro>

      <div class="statgrid" style={{ "margin-bottom": "var(--space-4)" }}>
        <StatCard label={t("ov.app")} sub={health() ? t("ov.engine", { v: health().version }) : ""}>{appName()}</StatCard>
        <StatCard label={t("ov.served")} sub={activation()}>{served()?.resources?.length ?? "—"}</StatCard>
        <StatCard label={t("ov.tenants")} sub={totals().suspended ? t("ov.suspendedN", { n: totals().suspended }) : t("ov.allActive")}>{totals().tenants}</StatCard>
        <StatCard label={t("ov.rows")} sub={t("ov.pgstat")}>{fmtNum(totals().rows)}</StatCard>
        <StatCard label={t("ov.users")} sub={t("ov.acrossTenants")}>{fmtNum(totals().users)}</StatCard>
      </div>

      <div class="card health-strip" style={{ padding: "var(--space-3)", "margin-bottom": "var(--space-4)" }}>
        <div class="row" style={{ "margin-bottom": "var(--space-2)" }}>
          <strong style={{ "font-size": "13px" }}>{t("ov.health")}</strong>
          <span class="muted" style={{ "font-size": "12px" }}>· {t("ov.health.sub")}</span>
        </div>
        <div class="statgrid">
          <HealthCard label={t("ov.verdict")} kind={latest() ? attrKind(latest().attribution) : "ok"}
            badge={latest() ? t("r.a." + latest().attribution) : "—"}
            sub={latest() ? (verdict()?.owner && verdict().owner !== "none" ? t("ov.verdict.owner", { o: t("r.o." + verdict().owner) }) : verdict()?.reason || "") : t("ov.verdict.na")}
            link={t("ov.goResources")} onClick={() => navigate("/resources")} />
          <HealthCard label={t("ov.backup")} kind={bkKind()} badge={bkText()}
            sub={bk().dir ? `${fmtAge(bk().age_s)} ${t("r.ago")} · ${bk().dir}` : ""}
            link={t("ov.goResources")} onClick={() => navigate("/resources")} />
          <HealthCard label={t("ov.disk")} kind={lowDisk() ? "crit" : "ok"}
            badge={disks().length ? (lowDisk() ? t("r.low") : t("r.ok")) : "—"}
            sub={disks().length ? t("ov.disk.free", { p: (lowDisk() || disks()[0]).free_pct?.toFixed(1), path: (lowDisk() || disks()[0]).path }) : t("ov.disk.na")}
            link={t("ov.goResources")} onClick={() => navigate("/resources")} />
          <HealthCard label={t("ov.problems")} kind={groups().length ? "crit" : "ok"}
            badge={selectedTenant() ? (groups().length ? String(groups().length) : t("ov.problems.none")) : "—"}
            sub={selectedTenant() ? t("ov.problems.sub", { t: selectedTenant() }) : t("c.selectTenant")}
            link={t("ov.goIssues")} onClick={() => navigate("/observability?tab=issues")} />
        </div>
      </div>

      <Show when={tenants.error}><div class="errbar">{t("ov.couldNotLoad", { e: tenants.error.message })}</div></Show>

      <div class="card" style={{ padding: "var(--space-3)" }}>
        <div class="row" style={{ "margin-bottom": "var(--space-2)" }}>
          <strong style={{ "font-size": "13px" }}>{t("ov.tenants")}</strong>
          <span class="spacer" />
          <Button size="sm" variant="ghost" onClick={() => navigate("/tenants")}>{t("ov.manage")}</Button>
        </div>
        <Show when={(tenants() || []).length > 0} fallback={<div class="empty">{tenants.loading ? t("c.loading") : t("ov.noTenants")}</div>}>
          <table class="minitable">
            <thead>
              <tr><th>{t("ov.th.tenant")}</th><th class="num-h">{t("ov.th.resources")}</th><th class="num-h">{t("ov.th.rows")}</th><th class="num-h">{t("ov.th.users")}</th><th>{t("ov.th.status")}</th><th></th></tr>
            </thead>
            <tbody>
              <For each={tenants()}>{(x) => (
                <tr>
                  <td><span class="cell-id" title={x.id}>{x.display_name || x.id}</span> <span class="muted" style={{ "font-size": "11px" }}>{x.id}</span></td>
                  <td class="num">{x.resource_count ?? 0}</td>
                  <td class="num">{fmtNum(x.data_rows)}</td>
                  <td class="num">{fmtNum(x.user_count)}</td>
                  <td>{x.suspended ? <span class="muted">{t("c.suspended")}</span> : <span>{t("c.active")}</span>}</td>
                  <td>
                    <div class="cell-actions row" style={{ "justify-content": "flex-end", gap: "4px" }}>
                      <Button size="sm" variant="ghost" onClick={() => goTenant(x.id, "/data")}>{t("nav.data")}</Button>
                      <Button size="sm" variant="ghost" onClick={() => goTenant(x.id, "/users")}>{t("nav.users")}</Button>
                      <Button size="sm" variant="ghost" onClick={() => goTenant(x.id, "/files")}>{t("nav.files")}</Button>
                      <Button size="sm" variant="ghost" onClick={() => goTenant(x.id, "/history")}>{t("nav.history")}</Button>
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

// HealthCard — a status card with the double channel (icon + text + colour) and
// a link into the screen that explains the number.
function HealthCard(props) {
  const icon = { ok: "✓", warn: "▲", crit: "✕" }
  return (
    <button class="stat card health-card" onClick={props.onClick} type="button">
      <div class="stat-label">{props.label}</div>
      <div class="stat-value"><span class={"badge badge-" + (props.kind || "ok")}><span aria-hidden="true">{icon[props.kind || "ok"]}</span><span>{props.badge}</span></span></div>
      <Show when={props.sub}><div class="stat-sub muted">{props.sub}</div></Show>
      <div class="stat-sub health-link">{props.link}</div>
    </button>
  )
}

function fmtNum(v) {
  return Number(v || 0).toLocaleString()
}
