import { createResource, createSignal, onMount, onCleanup, Show } from "solid-js"
import { useNavigate } from "@solidjs/router"
import { DataTable } from "../components/DataTable"
import { Modal } from "../components/Modal"
import { Button, toast } from "../components/ui"
import { api, ApiError } from "../lib/api"
import { logout } from "../lib/auth"
import { selectedTenant } from "../lib/tenantContext"
import { registerCommands } from "../lib/commands"

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
      { id: "files:upload", label: "Upload file", hint: "Files", run: () => fileInput?.click() },
      { id: "files:refresh", label: "Refresh files", hint: "Files", run: () => refetch() },
    ]))
  })

  const upload = async (e) => {
    const f = e.currentTarget.files?.[0]
    e.currentTarget.value = ""
    if (!f || !tid()) return
    setBusy(true)
    try {
      await api.uploadFile(tid(), f)
      toast(`Uploaded ${f.name}`)
      refetch()
    } catch (ex) {
      toast((ex.body && ex.body.error) || ex.message || "Upload failed", "err")
    } finally { setBusy(false) }
  }

  const download = async (f) => {
    try {
      const { url } = await api.fileURL(tid(), f.id)
      window.open(url, "_blank")
    } catch (ex) { toast(ex.message || "Could not mint download URL", "err") }
  }

  const doDelete = async () => {
    const f = delTarget()
    if (!f) return
    setBusy(true)
    try {
      await api.deleteFile(tid(), f.id)
      toast(`Deleted ${f.original_name || f.id}`)
      setDelTarget(null)
      refetch()
    } catch (ex) {
      toast((ex.body && ex.body.error) || ex.message || "Delete failed", "err")
    } finally { setBusy(false) }
  }

  const columns = [
    { accessorKey: "original_name", header: "Name", size: 260, cell: (c) => <span class="cell-id" title={c.getValue()}>{c.getValue() || "—"}</span> },
    { accessorKey: "content_type", header: "Type", size: 150, cell: (c) => <span class="secondary">{c.getValue() || "—"}</span> },
    { accessorKey: "size", header: "Size", size: 90, meta: { align: "right" }, cell: (c) => <span class="num">{fmtSize(c.getValue())}</span> },
    { accessorKey: "created_at", header: "Uploaded", size: 150, cell: (c) => <span class="secondary">{fmtDateTime(c.getValue())}</span> },
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
            <Button size="sm" variant="ghost" onClick={() => download(f)}>Download</Button>
            <Button size="sm" variant="ghost" class="btn-danger" onClick={() => setDelTarget(f)}>Delete</Button>
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
        <h1>Files</h1>
        <Show when={tid()}><span class="muted">· {tid()}</span></Show>
        <span class="spacer" />
        <Show when={res() && res().backend}><span class="muted" style={{ "font-size": "12px" }}>backend: {res().backend}</span></Show>
        <Button variant="ghost" size="sm" onClick={() => refetch()}>Refresh</Button>
        <Button variant="primary" size="sm" onClick={() => fileInput?.click()} disabled={busy() || !tid()}>
          {busy() ? "Uploading…" : "Upload file"}
        </Button>
        <input type="file" ref={fileInput} style={{ display: "none" }} onChange={upload} />
      </div>

      <Show when={tid()} fallback={<div class="empty">Select a tenant to manage its files.</div>}>
        <Show when={!res()?.disabled} fallback={<div class="empty">The file store is disabled on this deployment.</div>}>
          <DataTable
            data={files()}
            columns={columns}
            emptyMessage={res.loading ? "Loading…" : "No files uploaded yet."}
          />
          <Show when={total() > perPage()}>
            <div class="row" style={{ "justify-content": "center", gap: "var(--space-2)", "margin-top": "var(--space-3)" }}>
              <Button variant="ghost" size="sm" disabled={page() <= 1} onClick={() => setPage(page() - 1)}>Previous</Button>
              <span class="muted" style={{ "font-size": "12px" }}>page {page()} · {total()} file(s)</span>
              <Button variant="ghost" size="sm" disabled={page() * perPage() >= total()} onClick={() => setPage(page() + 1)}>Next</Button>
            </div>
          </Show>
        </Show>
      </Show>

      <Modal open={!!delTarget()} onClose={(o) => !o && setDelTarget(null)} busy={busy()} title="Delete file"
        description={`Deletes "${delTarget()?.original_name || delTarget()?.id}" from the tenant's store. A file still attached to a record is protected (409).`}>
        <div class="actions">
          <Button variant="ghost" onClick={() => setDelTarget(null)} disabled={busy()}>Cancel</Button>
          <Button variant="danger" onClick={doDelete} disabled={busy()}>{busy() ? "Deleting…" : "Delete file"}</Button>
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
