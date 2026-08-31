import { createResource, createSignal, Show } from "solid-js"
import { useNavigate } from "@solidjs/router"
import { DataTable } from "../components/DataTable"
import { Modal } from "../components/Modal"
import { Button, toast } from "../components/ui"
import { api, ApiError } from "../lib/api"
import { logout } from "../lib/auth"
import { selectedTenant } from "../lib/tenantContext"
import { PageIntro } from "../shell/Shell"
import { t } from "../lib/i18n"

// History — the selected tenant's schema version timeline (VERSION-S1), read-only
// in the console: browse versions, view any version's full schema. ROLLBACK stays
// in Studio deliberately (it needs the dry-run preview + destructive-approval gate
// UI the editor already has) — the "Roll back in Studio" button is the handoff.
export function History() {
  const navigate = useNavigate()
  const tid = () => selectedTenant()

  const [page, setPage] = createSignal(1)
  const [res, { refetch }] = createResource(() => tid() && `${tid()}:${page()}`, async () => {
    if (!tid()) return null
    try { return await api.schemaHistory(tid(), page()) }
    catch (ex) {
      if (ex instanceof ApiError && (ex.status === 401 || ex.status === 403)) { logout(); navigate("/login") }
      throw ex
    }
  })

  const [viewing, setViewing] = createSignal(null) // {version, schema, …} for the modal
  const [loadingV, setLoadingV] = createSignal(0)

  const openVersion = async (v) => {
    setLoadingV(v.version)
    try { setViewing(await api.schemaVersion(tid(), v.version)) }
    catch (ex) { toast(ex.message || t("h.loadFailed"), "err") }
    finally { setLoadingV(0) }
  }

  const columns = [
    { accessorKey: "version", header: t("h.th.version"), size: 80, meta: { align: "right" }, cell: (c) => <span class="num">v{c.getValue()}</span> },
    { accessorKey: "source", header: t("h.th.source"), size: 110, cell: (c) => <span class="secondary">{c.getValue() || "—"}</span> },
    {
      accessorKey: "resources", header: t("h.th.resources"), size: 320,
      cell: (c) => { const r = c.getValue() || []; const s = r.join(", "); return <span class="secondary" title={s}>{r.length}: {s.length > 60 ? s.slice(0, 60) + "…" : s}</span> },
    },
    {
      accessorKey: "hash", header: t("h.th.hash"), size: 110,
      cell: (c) => <span class="muted" style={{ "font-family": "ui-monospace, monospace", "font-size": "11px" }} title={c.getValue()}>{String(c.getValue() || "").slice(0, 8)}</span>,
    },
    { accessorKey: "created_at", header: t("h.th.deployed"), size: 160, cell: (c) => <span class="secondary">{fmtDateTime(c.getValue())}</span> },
    {
      id: "actions", header: "", size: 90,
      cell: (c) => {
        const v = c.row.original
        return (
          <div class="cell-actions">
            <Button size="sm" variant="ghost" disabled={loadingV() === v.version} onClick={() => openVersion(v)}>
              {loadingV() === v.version ? t("c.loading") : t("c.view")}
            </Button>
          </div>
        )
      },
    },
  ]

  const versions = () => res()?.versions || []
  const total = () => res()?.total ?? 0
  const perPage = () => res()?.per_page || 50

  return (
    <>
      <div class="pagehead">
        <h1>{t("h.title")}</h1>
        <Show when={tid()}><span class="muted">· {tid()}</span></Show>
        <span class="spacer" />
        <Button variant="ghost" size="sm" onClick={() => refetch()}>{t("c.refresh")}</Button>
        <Button variant="ghost" size="sm" onClick={() => window.open("/editor", "_blank")} title={t("h.rollbackTitle")}>
          {t("h.rollback")}
        </Button>
      </div>
      <PageIntro>{t("intro.history")}</PageIntro>

      <Show when={tid()} fallback={<div class="empty">{t("h.select")}</div>}>
        <DataTable
          data={versions()}
          columns={columns}
          emptyMessage={res.loading ? t("c.loading") : t("h.empty")}
        />
        <Show when={total() > perPage()}>
          <div class="row" style={{ "justify-content": "center", gap: "var(--space-2)", "margin-top": "var(--space-3)" }}>
            <Button variant="ghost" size="sm" disabled={page() <= 1} onClick={() => setPage(page() - 1)}>{t("c.prev")}</Button>
            <span class="muted" style={{ "font-size": "12px" }}>{t("h.pageOf", { p: page(), n: total() })}</span>
            <Button variant="ghost" size="sm" disabled={page() * perPage() >= total()} onClick={() => setPage(page() + 1)}>{t("c.next")}</Button>
          </div>
        </Show>
      </Show>

      <Modal open={!!viewing()} onClose={(o) => !o && setViewing(null)} title={viewing() ? t("h.schema", { v: viewing().version }) : t("h.schemaT")}>
        <Show when={viewing()}>
          <div class="muted" style={{ "font-size": "12px", "margin-bottom": "var(--space-2)" }}>
            {viewing().source} · {fmtDateTime(viewing().created_at)} · hash {String(viewing().hash || "").slice(0, 12)}
          </div>
          <pre style={{
            "max-height": "55vh", overflow: "auto", "font-size": "11.5px",
            "font-family": "ui-monospace, monospace", background: "var(--color-surface-2)",
            border: "1px solid var(--color-border)", "border-radius": "var(--radius-md)",
            padding: "var(--space-3)", "white-space": "pre",
          }}>{JSON.stringify(viewing().schema, null, 2)}</pre>
        </Show>
        <div class="actions">
          <Button variant="ghost" onClick={() => setViewing(null)}>{t("c.close")}</Button>
        </div>
      </Modal>
    </>
  )
}

function fmtDateTime(v) {
  if (!v) return "—"
  try { return new Date(v).toLocaleString() } catch { return String(v) }
}
