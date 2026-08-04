import { createResource, createSignal, onMount, onCleanup, Show } from "solid-js"
import { useNavigate } from "@solidjs/router"
import { DataTable } from "../components/DataTable"
import { Modal } from "../components/Modal"
import { Button, Field, StatusBadge, toast } from "../components/ui"
import { api, ApiError } from "../lib/api"
import { logout } from "../lib/auth"
import { setSelectedTenant, bumpTenants } from "../lib/tenantContext"
import { registerCommands } from "../lib/commands"

const DEFAULT_SCHEMA = JSON.stringify({
  $schema: "https://appximo.com/schema/v1",
  version: "1",
  name: "todo-api",
  resources: { tasks: { fields: { title: { type: "string", required: true } } } },
  rbac: { roles: { admin: { resources: "*", actions: ["*"] } } },
}, null, 2)

export function Tenants() {
  const navigate = useNavigate()

  const fetchTenants = async () => {
    try { return (await api.listTenants()).tenants || [] }
    catch (ex) {
      if (ex instanceof ApiError && (ex.status === 401 || ex.status === 403)) { logout(); navigate("/login") }
      throw ex
    }
  }
  const [tenants, { refetch }] = createResource(fetchTenants)

  const [showCreate, setShowCreate] = createSignal(false)
  const [delTarget, setDelTarget] = createSignal(null)

  onMount(() => {
    const cleanup = registerCommands([
      { id: "tenant:create", label: "Create tenant", hint: "Tenants", run: () => setShowCreate(true) },
      { id: "tenant:refresh", label: "Refresh tenants", hint: "Tenants", run: () => refetch() },
    ])
    onCleanup(cleanup)
  })

  const toggleSuspend = async (t) => {
    try {
      if (t.suspended) { await api.activateTenant(t.id); toast(`Activated ${t.id}`) }
      else { await api.suspendTenant(t.id); toast(`Suspended ${t.id}`) }
      refetch(); bumpTenants()
    } catch (ex) { toast(ex.message || "Operation failed", "err") }
  }

  // Clicking a tenant's name enters it: sets the tenant context and opens its
  // data — the app → tenant → data navigation (ADMIN-CONSOLE-S1).
  const openTenant = (t) => { setSelectedTenant(t.id); navigate("/data") }

  const columns = [
    {
      accessorKey: "display_name",
      header: "Tenant",
      size: 170,
      cell: (c) => (
        <button class="linklike cell-id" title={`Browse ${c.row.original.id}'s data`} onClick={() => openTenant(c.row.original)}>
          {c.getValue() || c.row.original.id}
        </button>
      ),
    },
    { accessorKey: "id", header: "ID", size: 170, cell: (c) => <span class="secondary" title={c.getValue()}>{c.getValue()}</span> },
    {
      accessorKey: "suspended",
      header: "Status",
      size: 110,
      cell: (c) => <StatusBadge kind={c.getValue() ? "warn" : "ok"} okLabel="Active" warnLabel="Suspended" />,
    },
    { accessorKey: "plan", header: "Plan", size: 80, cell: (c) => <span class="secondary">{c.getValue() || "—"}</span> },
    {
      accessorKey: "resource_count", header: "Resources",
      size: 90,
      meta: { align: "right" },
      cell: (c) => <span class="num">{c.getValue() ?? 0}</span>,
    },
    {
      // pg_stat estimate — the same inventory `appximo tenant list` prints.
      accessorKey: "data_rows", header: "Rows(~)",
      size: 85,
      meta: { align: "right" },
      cell: (c) => <span class="num">{Number(c.getValue() || 0).toLocaleString("en-US")}</span>,
    },
    {
      accessorKey: "user_count", header: "Users",
      size: 70,
      meta: { align: "right" },
      cell: (c) => <span class="num">{Number(c.getValue() || 0).toLocaleString("en-US")}</span>,
    },
    {
      accessorKey: "created_at", header: "Created",
      size: 110,
      cell: (c) => <span class="secondary">{fmtDate(c.getValue())}</span>,
    },
    {
      id: "actions", header: "",
      size: 150,
      cell: (c) => {
        const t = c.row.original
        return (
          <div class="cell-actions row" style={{ "justify-content": "flex-end" }}>
            <Button size="sm" variant="ghost" onClick={() => toggleSuspend(t)}>
              {t.suspended ? "Activate" : "Suspend"}
            </Button>
            <Button size="sm" variant="ghost" class="btn-danger" onClick={() => setDelTarget(t)}>Delete</Button>
          </div>
        )
      },
    },
  ]

  return (
    <>
      <div class="pagehead">
        <h1>Tenants</h1>
        <span class="spacer" />
        <Button variant="ghost" size="sm" onClick={() => refetch()}>Refresh</Button>
        <Button variant="primary" onClick={() => setShowCreate(true)}>New tenant</Button>
      </div>

      <Show when={tenants.error}><div class="errbar">Could not load tenants: {tenants.error.message}</div></Show>

      <DataTable
        data={tenants() || []}
        columns={columns}
        emptyMessage={tenants.loading ? "Loading…" : "No tenants yet. Create the first one."}
      />

      <CreateTenantDialog open={showCreate()} onClose={() => setShowCreate(false)} onCreated={() => { setShowCreate(false); refetch(); bumpTenants() }} navigate={navigate} />
      <DeleteTenantDialog target={delTarget()} onClose={() => setDelTarget(null)} onDeleted={() => { setDelTarget(null); refetch(); bumpTenants() }} />
    </>
  )
}

// THE tenant id rule — the UX mirror of the backend authority (controlplane):
// the id becomes the Postgres schema tenant_<id> and the Host subdomain, so
// hyphens/uppercase/spaces are refused up front (the "punto-gafas-v1" bug).
const TENANT_ID_RE = /^[a-z][a-z0-9]{1,29}$/ // ENG-11: schema alphabet ∩ DNS-label alphabet
function tenantIdIssue(raw) {
  if (raw === "") return null
  if (/[A-Z]/.test(raw)) return "Uppercase is not allowed — use lowercase."
  if (/_/.test(raw)) return "Underscores are not allowed — a web address cannot contain one, so the app would never answer."
  if (/[-\s.]/.test(raw)) return "Hyphens, spaces and dots are not allowed — the database name cannot contain them."
  if (!/^[a-z]/.test(raw)) return "Must start with a lowercase letter."
  if (raw.length < 2 || raw.length > 30) return "Must be 2–30 characters."
  if (!TENANT_ID_RE.test(raw)) return "Only lowercase letters and digits."
  return null
}
function suggestTenantId(raw) {
  const s = raw.toLowerCase().replace(/[^a-z0-9]/g, "").replace(/^[^a-z]+/, "").slice(0, 30)
  return TENANT_ID_RE.test(s) ? s : ""
}

function CreateTenantDialog(props) {
  const [id, setId] = createSignal("")
  const [name, setName] = createSignal("")
  const [email, setEmail] = createSignal("")
  const [plan, setPlan] = createSignal("free")
  const [schema, setSchema] = createSignal(DEFAULT_SCHEMA)
  const [err, setErr] = createSignal("")
  const [busy, setBusy] = createSignal(false)

  const reset = () => { setId(""); setName(""); setEmail(""); setPlan("free"); setSchema(DEFAULT_SCHEMA); setErr("") }

  const submit = async () => {
    setErr(""); setBusy(true)
    let parsed
    try { parsed = JSON.parse(schema()) } catch { setErr("Schema is not valid JSON."); setBusy(false); return }
    try {
      await api.createTenant({ tenant_id: id().trim(), display_name: name().trim() || id().trim(), email: email().trim(), plan: plan().trim(), schema: parsed })
      toast(`Created tenant ${id().trim()}`)
      reset()
      props.onCreated()
    } catch (ex) {
      const e = ex
      setErr((e.body && (e.body.error || (e.body.errors && e.body.errors.join("; ")))) || e.message || "Create failed")
    } finally { setBusy(false) }
  }

  return (
    <Modal open={props.open} onClose={(o) => !o && props.onClose()} busy={busy()} title="Create tenant"
      description="Provisions an isolated Postgres schema and tables for a new tenant.">
      <Show when={err()}><div class="errbar">{err()}</div></Show>
      <Field id="t-id" label="Tenant ID" value={id()} onInput={setId} placeholder="acme"
        hint={tenantIdIssue(id().trim())
          ? undefined
          : id().trim()
            ? `→ schema tenant_${id().trim()} · subdomain ${id().trim()}.<host>`
            : "lowercase letter first, then lowercase/digits/'_' (2–30); no hyphens, uppercase or spaces"} />
      <Show when={tenantIdIssue(id().trim())}>
        <div class="errbar" style={{ "margin-top": "-8px" }}>
          {tenantIdIssue(id().trim())}
          <Show when={suggestTenantId(id().trim())}>
            {" "}
            <button class="linklike" onClick={() => setId(suggestTenantId(id().trim()))}>
              use "{suggestTenantId(id().trim())}"
            </button>
          </Show>
        </div>
      </Show>
      <Field id="t-name" label="Display name" value={name()} onInput={setName} placeholder="Acme Inc." />
      <Field id="t-email" label="Contact email" type="email" value={email()} onInput={setEmail} placeholder="ops@acme.com" />
      <Field id="t-plan" label="Plan" value={plan()} onInput={setPlan} placeholder="free" />
      <div class="field">
        <label for="t-schema">Schema (JSON)</label>
        <textarea id="t-schema" rows="10" style={{ width: "100%", "font-family": "ui-monospace, monospace", "font-size": "12px" }}
          value={schema()} onInput={(e) => setSchema(e.currentTarget.value)} />
      </div>
      <div class="actions">
        <Button variant="ghost" onClick={props.onClose} disabled={busy()}>Cancel</Button>
        <Button variant="primary" onClick={submit} disabled={busy() || !id().trim() || tenantIdIssue(id().trim()) !== null}>{busy() ? "Creating…" : "Create tenant"}</Button>
      </div>
    </Modal>
  )
}

function DeleteTenantDialog(props) {
  const [confirm, setConfirm] = createSignal("")
  const [busy, setBusy] = createSignal(false)
  const [err, setErr] = createSignal("")
  const id = () => props.target?.id || ""

  const submit = async () => {
    setErr(""); setBusy(true)
    try {
      await api.deleteTenant(id(), confirm().trim())
      toast(`Deleted tenant ${id()}`)
      setConfirm("")
      props.onDeleted()
    } catch (ex) { setErr(ex.message || "Delete failed") }
    finally { setBusy(false) }
  }

  return (
    <Modal open={!!props.target} onClose={(o) => !o && (setConfirm(""), props.onClose())} busy={busy()}
      title="Delete tenant"
      description={`This permanently drops the "${id()}" schema and ALL its data. This cannot be undone.`}>
      <Show when={err()}><div class="errbar">{err()}</div></Show>
      <Field id="del-confirm" label={`Type "${id()}" to confirm`} value={confirm()} onInput={setConfirm} placeholder={id()} />
      <div class="actions">
        <Button variant="ghost" onClick={() => { setConfirm(""); props.onClose() }} disabled={busy()}>Cancel</Button>
        <Button variant="danger" onClick={submit} disabled={busy() || confirm().trim() !== id()}>{busy() ? "Deleting…" : "Delete forever"}</Button>
      </div>
    </Modal>
  )
}

function fmtDate(v) {
  if (!v) return "—"
  try { return new Date(v).toLocaleDateString(undefined, { year: "numeric", month: "short", day: "numeric" }) }
  catch { return String(v) }
}
