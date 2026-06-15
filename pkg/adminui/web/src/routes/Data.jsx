import { createResource, createSignal, createEffect, onMount, onCleanup, Show, For } from "solid-js"
import { useNavigate } from "@solidjs/router"
import { DataTable } from "../components/DataTable"
import { Modal } from "../components/Modal"
import { Button, toast } from "../components/ui"
import { api, ApiError } from "../lib/api"
import { logout } from "../lib/auth"
import { selectedTenant } from "../lib/tenantContext"
import { registerCommands } from "../lib/commands"

const NUMERIC = new Set(["int", "int64", "float64"])

export function Data() {
  const navigate = useNavigate()
  const tid = () => selectedTenant()

  const onAuthErr = (ex) => {
    if (ex instanceof ApiError && (ex.status === 401 || ex.status === 403)) { logout(); navigate("/login") }
  }

  const [resources] = createResource(tid, async (id) => {
    if (!id) return []
    try { return (await api.listResources(id)).resources || [] }
    catch (ex) { onAuthErr(ex); throw ex }
  })

  const [selected, setSelected] = createSignal("")
  const [rows, setRows] = createSignal([])
  const [loading, setLoading] = createSignal(false)
  const [hasNext, setHasNext] = createSignal(false)
  const [err, setErr] = createSignal("")
  const [detail, setDetail] = createSignal(null)

  // Pick the first resource once the list loads (or when tenant changes).
  createEffect(() => {
    const list = resources()
    if (!list || list.length === 0) { setSelected(""); setRows([]); return }
    if (!selected() || !list.some((r) => r.name === selected())) pick(list[0].name)
  })

  const currentDef = () => (resources() || []).find((r) => r.name === selected())

  async function pick(name) {
    setSelected(name); setRows([]); setHasNext(false); setErr("")
    await load(name, "")
  }

  async function load(name, after) {
    if (!name || !tid()) return
    setLoading(true)
    try {
      const qs = new URLSearchParams({ per_page: "100" })
      if (after) qs.set("after", after)
      const res = await api.listData(tid(), name, qs.toString())
      const data = res.data || []
      setRows((prev) => after ? [...prev, ...data] : data)
      setHasNext(!!(res.meta && res.meta.has_next))
    } catch (ex) { onAuthErr(ex); setErr(ex.message || "Load failed") }
    finally { setLoading(false) }
  }

  const loadMore = () => {
    const r = rows()
    if (r.length) load(selected(), r[r.length - 1].id)
  }

  onMount(() => {
    onCleanup(registerCommands([
      { id: "data:refresh", label: "Refresh data", hint: "Data", run: () => selected() && pick(selected()) },
    ]))
  })

  // Columns derived from the resource's declared fields. Non-id fields first
  // (never lead with a raw UUID); the id is shown last, monospace + truncated.
  const columns = () => {
    const def = currentDef()
    if (!def) return []
    const fields = def.fields.filter((f) => f.name !== "id")
    const cols = fields.map((f) => ({
      accessorKey: f.name,
      header: f.name,
      meta: NUMERIC.has(f.type) ? { align: "right" } : undefined,
      cell: (c) => renderCell(c.getValue(), f.type),
    }))
    cols.push({
      accessorKey: "id", header: "id",
      cell: (c) => <span class="muted" style={{ "font-family": "ui-monospace, monospace", "font-size": "11px" }} title={c.getValue()}>{shortId(c.getValue())}</span>,
    })
    cols.push({
      id: "_open", header: "",
      cell: (c) => <div class="cell-actions"><Button size="sm" variant="ghost" onClick={() => setDetail(c.row.original)}>View</Button></div>,
    })
    return cols
  }

  return (
    <>
      <div class="pagehead">
        <h1>Data</h1>
        <Show when={tid()}><span class="muted">· {tid()}</span></Show>
        <span class="spacer" />
        <Show when={selected()}><Button variant="ghost" size="sm" onClick={() => pick(selected())}>Refresh</Button></Show>
      </div>

      <Show when={tid()} fallback={<div class="empty">Select a tenant to browse its data.</div>}>
        <div class="data-layout">
          <aside class="reslist card">
            <div class="navgroup">
              <div class="label">Resources</div>
              <Show when={(resources() || []).length > 0} fallback={<span class="muted" style={{ padding: "8px", "font-size": "13px" }}>{resources.loading ? "Loading…" : "No resources"}</span>}>
                <For each={resources()}>{(r) => (
                  <button class={"navitem" + (selected() === r.name ? " active" : "")}
                    style={{ border: "none", background: "transparent", "text-align": "left", width: "100%" }}
                    onClick={() => pick(r.name)}>
                    {r.name}
                  </button>
                )}</For>
              </Show>
            </div>
          </aside>

          <div class="col" style={{ "min-width": 0, gap: "var(--space-3)" }}>
            <Show when={err()}><div class="errbar">{err()}</div></Show>
            <Show when={selected()} fallback={<div class="empty">Pick a resource on the left.</div>}>
              <DataTable
                data={rows()}
                columns={columns()}
                emptyMessage={loading() ? "Loading…" : "No records."}
                maxHeight="calc(100vh - 260px)"
              />
              <Show when={hasNext()}>
                <div class="row" style={{ "justify-content": "center" }}>
                  <Button variant="ghost" onClick={loadMore} disabled={loading()}>{loading() ? "Loading…" : "Load more"}</Button>
                </div>
              </Show>
            </Show>
          </div>
        </div>
      </Show>

      <RecordDetail record={detail()} def={currentDef()} onClose={() => setDetail(null)} />
    </>
  )
}

function RecordDetail(props) {
  return (
    <Modal open={!!props.record} onClose={(o) => !o && props.onClose()} title="Record">
      <Show when={props.record}>
        <div class="col" style={{ gap: "var(--space-2)", "max-height": "60vh", overflow: "auto" }}>
          <For each={Object.keys(props.record)}>{(k) => (
            <div class="row" style={{ "align-items": "flex-start", gap: "var(--space-3)" }}>
              <span class="muted" style={{ "min-width": "120px", "font-size": "12px" }}>{k}</span>
              <span style={{ "word-break": "break-word", "font-size": "13px" }}>{fmtVal(props.record[k])}</span>
            </div>
          )}</For>
        </div>
      </Show>
      <div class="actions">
        <Button variant="ghost" onClick={props.onClose}>Close</Button>
      </div>
    </Modal>
  )
}

function renderCell(v, type) {
  if (v === null || v === undefined) return <span class="muted">—</span>
  if (NUMERIC.has(type)) return <span class="num">{v}</span>
  if (type === "bool") return <span>{v ? "Yes" : "No"}</span>
  if (type === "time") return <span class="secondary">{fmtDateTime(v)}</span>
  const s = String(v)
  return <span title={s}>{s.length > 80 ? s.slice(0, 80) + "…" : s}</span>
}

function fmtVal(v) {
  if (v === null || v === undefined) return "—"
  if (typeof v === "object") return JSON.stringify(v)
  return String(v)
}

function shortId(v) {
  const s = String(v || "")
  return s.length > 12 ? s.slice(0, 8) + "…" : s
}

function fmtDateTime(v) {
  try { return new Date(v).toLocaleString() } catch { return String(v) }
}
