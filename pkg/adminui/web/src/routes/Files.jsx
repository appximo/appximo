import { createResource, createSignal, onMount, onCleanup, Show } from "solid-js"
import { useNavigate } from "@solidjs/router"
import { DataTable } from "../components/DataTable"
import { Modal } from "../components/Modal"
import { Button, toast } from "../components/ui"
import { api, ApiError } from "../lib/api"
import { logout } from "../lib/auth"
import { selectedTenant } from "../lib/tenantContext"
import { registerCommands } from "../lib/commands"
import { PageIntro } from "../shell/Shell"
import { t } from "../lib/i18n"

// Files — the selected tenant's file store, in the console (ADMIN-CONSOLE-S1).
// Thin face over the UI-F5-S1 admin file routes: the SAME files.Store the
// tenant API uses (OWASP upload validation, dedup-aware delete, signed URLs).
export function Files() {
  const navigate = useNavigate()
  const tid = () => selectedTenant()

  const onAuthErr = (ex) => {
    if (ex instanceof ApiError && (ex.status === 401 || ex.status === 403)) { logout(); navigate("/login") }
  }

  const [page, setPage] = createSignal(1)
  const [res, { refetch }] = createResource(() => tid() && `${tid()}:${page()}`, async () => {
    if (!tid()) return null
    try { return await api.listFiles(tid(), page()) }
    catch (ex) {
      onAuthErr(ex)
      if (ex instanceof ApiError && ex.status === 503) return { disabled: true, files: [] }
      throw ex
    }
  })

  const [delTarget, setDelTarget] = createSignal(null)
  const [busy, setBusy] = createSignal(false)
  let fileInput

  onMount(() => {
    onCleanup(registerCommands([
      { id: "files:upload", label: t("f.upload"), hint: t("f.title"), run: () => fileInput?.click() },
      { id: "files:refresh", label: t("c.refresh") + " · " + t("f.title"), hint: t("f.title"), run: () => refetch() },
    ]))
  })

  const upload = async (e) => {
    const f = e.currentTarget.files?.[0]
    e.currentTarget.value = ""
    if (!f || !tid()) return
    setBusy(true)
    try {
      await api.uploadFile(tid(), f)
      toast(t("f.uploaded", { n: f.name }))
      refetch()
    } catch (ex) {
      toast((ex.body && ex.body.error) || ex.message || t("f.uploadFailed"), "err")
    } finally { setBusy(false) }
  }

  const download = async (f) => {
    try {
      const { url } = await api.fileURL(tid(), f.id)
      window.open(url, "_blank")
    } catch (ex) { toast(ex.message || t("f.urlFailed"), "err") }
  }

  const doDelete = async () => {
    const f = delTarget()
    if (!f) return
    setBusy(true)
    try {
      await api.deleteFile(tid(), f.id)
      toast(t("f.deleted", { n: f.original_name || f.id }))
      setDelTarget(null)
      refetch()
    } catch (ex) {
      toast((ex.body && ex.body.error) || ex.message || t("f.deleteFailed"), "err")
    } finally { setBusy(false) }
  }

  const columns = [
    { accessorKey: "original_name", header: t("f.th.name"), size: 260, cell: (c) => <span class="cell-id" title={c.getValue()}>{c.getValue() || "—"}</span> },
    { accessorKey: "content_type", header: t("f.th.type"), size: 150, cell: (c) => <span class="secondary">{c.getValue() || "—"}</span> },
    { accessorKey: "size", header: t("f.th.size"), size: 90, meta: { align: "right" }, cell: (c) => <span class="num">{fmtSize(c.getValue())}</span> },
    { accessorKey: "created_at", header: t("f.th.uploaded"), size: 150, cell: (c) => <span class="secondary">{fmtDateTime(c.getValue())}</span> },
    {
      accessorKey: "id", header: "id", size: 110,
      cell: (c) => <span class="muted" style={{ "font-family": "ui-monospace, monospace", "font-size": "11px" }} title={c.getValue()}>{String(c.getValue()).slice(0, 8)}…</span>,
    },
    {
      id: "actions", header: "", size: 150,
      cell: (c) => {
        const f = c.row.original
        return (
          <div class="cell-actions row" style={{ "justify-content": "flex-end", gap: "4px" }}>
            <Button size="sm" variant="ghost" onClick={() => download(f)}>{t("f.download")}</Button>
            <Button size="sm" variant="ghost" class="btn-danger" onClick={() => setDelTarget(f)}>{t("c.delete")}</Button>
          </div>
        )
      },
    },
  ]

  const files = () => res()?.files || []
  const total = () => res()?.total ?? 0
  const perPage = () => res()?.per_page || 50

  return (
    <>
      <div class="pagehead">
        <h1>{t("f.title")}</h1>
        <Show when={tid()}><span class="muted">· {tid()}</span></Show>
        <span class="spacer" />
        <Show when={res() && res().backend}><span class="muted" style={{ "font-size": "12px" }}>{t("f.backend", { b: res().backend })}</span></Show>
        <Button variant="ghost" size="sm" onClick={() => refetch()}>{t("c.refresh")}</Button>
        <Button variant="primary" size="sm" onClick={() => fileInput?.click()} disabled={busy() || !tid()}>
          {busy() ? t("f.uploading") : t("f.upload")}
        </Button>
        <input type="file" ref={fileInput} style={{ display: "none" }} onChange={upload} />
      </div>
      <PageIntro>{t("intro.files")}</PageIntro>

      <Show when={tid()} fallback={<div class="empty">{t("f.select")}</div>}>
        <Show when={!res()?.disabled} fallback={<div class="empty">{t("f.disabled")}</div>}>
          <DataTable
            data={files()}
            columns={columns}
            emptyMessage={res.loading ? t("c.loading") : t("f.empty")}
          />
          <Show when={total() > perPage()}>
            <div class="row" style={{ "justify-content": "center", gap: "var(--space-2)", "margin-top": "var(--space-3)" }}>
              <Button variant="ghost" size="sm" disabled={page() <= 1} onClick={() => setPage(page() - 1)}>{t("c.prev")}</Button>
              <span class="muted" style={{ "font-size": "12px" }}>{t("f.pageOf", { p: page(), n: total() })}</span>
              <Button variant="ghost" size="sm" disabled={page() * perPage() >= total()} onClick={() => setPage(page() + 1)}>{t("c.next")}</Button>
            </div>
          </Show>
        </Show>
      </Show>

      <Modal open={!!delTarget()} onClose={(o) => !o && setDelTarget(null)} busy={busy()} title={t("f.del.title")}
        description={t("f.del.desc", { n: delTarget()?.original_name || delTarget()?.id })}>
        <div class="actions">
          <Button variant="ghost" onClick={() => setDelTarget(null)} disabled={busy()}>{t("c.cancel")}</Button>
          <Button variant="danger" onClick={doDelete} disabled={busy()}>{busy() ? t("c.deleting") : t("f.del.btn")}</Button>
        </div>
      </Modal>
    </>
  )
}

function fmtSize(n) {
  const v = Number(n || 0)
  if (v < 1024) return v + " B"
  if (v < 1024 * 1024) return (v / 1024).toFixed(1) + " KB"
  if (v < 1024 * 1024 * 1024) return (v / (1024 * 1024)).toFixed(1) + " MB"
  return (v / (1024 * 1024 * 1024)).toFixed(1) + " GB"
}

function fmtDateTime(v) {
  if (!v) return "—"
  try { return new Date(v).toLocaleString() } catch { return String(v) }
}
