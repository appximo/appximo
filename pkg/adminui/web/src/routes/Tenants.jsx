import { createResource, createSignal, onMount, onCleanup, Show } from "solid-js"
import { useNavigate } from "@solidjs/router"
import { DataTable } from "../components/DataTable"
import { Modal } from "../components/Modal"
import { Button, Field, StatusBadge, toast } from "../components/ui"
import { api, ApiError } from "../lib/api"
import { logout } from "../lib/auth"
import { setSelectedTenant, bumpTenants } from "../lib/tenantContext"
import { registerCommands } from "../lib/commands"
import { PageIntro } from "../shell/Shell"
import { t as tr } from "../lib/i18n"

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
      { id: "tenant:create", label: tr("t.create.title"), hint: tr("t.title"), run: () => setShowCreate(true) },
      { id: "tenant:refresh", label: tr("c.refresh") + " · " + tr("t.title"), hint: tr("t.title"), run: () => refetch() },
    ])
    onCleanup(cleanup)
  })

  const toggleSuspend = async (t) => {
    try {
      if (t.suspended) { await api.activateTenant(t.id); toast(tr("t.activated", { id: t.id })) }
      else { await api.suspendTenant(t.id); toast(tr("t.suspendedMsg", { id: t.id })) }
      refetch(); bumpTenants()
    } catch (ex) { toast(ex.message || tr("c.opFailed"), "err") }
  }

  // Clicking a tenant's name enters it: sets the tenant context and opens its
  // data — the app → tenant → data navigation (ADMIN-CONSOLE-S1).
  const openTenant = (t) => { setSelectedTenant(t.id); navigate("/data") }

  const columns = [
    {
      accessorKey: "display_name",
      header: tr("t.th.tenant"),
      size: 170,
      cell: (c) => (
        <button class="linklike cell-id" title={tr("t.browse", { id: c.row.original.id })} onClick={() => openTenant(c.row.original)}>
          {c.getValue() || c.row.original.id}
        </button>
      ),
    },
    { accessorKey: "id", header: tr("t.th.id"), size: 170, cell: (c) => <span class="secondary" title={c.getValue()}>{c.getValue()}</span> },
    {
      accessorKey: "suspended",
      header: tr("t.th.status"),
      size: 110,
      cell: (c) => <StatusBadge kind={c.getValue() ? "warn" : "ok"} okLabel={tr("c.active")} warnLabel={tr("c.suspended")} />,
    },
    { accessorKey: "plan", header: tr("t.th.plan"), size: 80, cell: (c) => <span class="secondary">{c.getValue() || "—"}</span> },
    {
      accessorKey: "resource_count", header: tr("t.th.resources"),
      size: 90,
      meta: { align: "right" },
      cell: (c) => <span class="num">{c.getValue() ?? 0}</span>,
    },
    {
      // pg_stat estimate — the same inventory `appximo tenant list` prints.
      accessorKey: "data_rows", header: tr("t.th.rows"),
      size: 85,
      meta: { align: "right" },
      cell: (c) => <span class="num">{Number(c.getValue() || 0).toLocaleString()}</span>,
    },
    {
      accessorKey: "user_count", header: tr("t.th.users"),
      size: 70,
      meta: { align: "right" },
      cell: (c) => <span class="num">{Number(c.getValue() || 0).toLocaleString()}</span>,
    },
    {
      accessorKey: "created_at", header: tr("t.th.created"),
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
              {t.suspended ? tr("c.activate") : tr("c.suspend")}
            </Button>
            <Button size="sm" variant="ghost" class="btn-danger" onClick={() => setDelTarget(t)}>{tr("c.delete")}</Button>
          </div>
        )
      },
    },
  ]

  return (
    <>
      <div class="pagehead">
        <h1>{tr("t.title")}</h1>
        <span class="spacer" />
        <Button variant="ghost" size="sm" onClick={() => refetch()}>{tr("c.refresh")}</Button>
        <Button variant="primary" onClick={() => setShowCreate(true)}>{tr("t.new")}</Button>
      </div>
      <PageIntro>{tr("intro.tenants")}</PageIntro>

      <Show when={tenants.error}><div class="errbar">{tr("ov.couldNotLoad", { e: tenants.error.message })}</div></Show>

      <DataTable
        data={tenants() || []}
        columns={columns}
        emptyMessage={tenants.loading ? tr("c.loading") : tr("t.empty")}
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
  if (/[A-Z]/.test(raw)) return tr("t.id.upper")
  if (/_/.test(raw)) return tr("t.id.under")
  if (/[-\s.]/.test(raw)) return tr("t.id.dash")
  if (!/^[a-z]/.test(raw)) return tr("t.id.start")
  if (raw.length < 2 || raw.length > 30) return tr("t.id.len")
  if (!TENANT_ID_RE.test(raw)) return tr("t.id.chars")
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
    try { parsed = JSON.parse(schema()) } catch { setErr(tr("t.create.badJson")); setBusy(false); return }
    try {
      await api.createTenant({ tenant_id: id().trim(), display_name: name().trim() || id().trim(), email: email().trim(), plan: plan().trim(), schema: parsed })
      toast(tr("t.created", { id: id().trim() }))
      reset()
      props.onCreated()
    } catch (ex) {
      const e = ex
      setErr((e.body && (e.body.error || (e.body.errors && e.body.errors.join("; ")))) || e.message || tr("t.create.failed"))
    } finally { setBusy(false) }
  }

  return (
    <Modal open={props.open} onClose={(o) => !o && props.onClose()} busy={busy()} title={tr("t.create.title")}
      description={tr("t.create.desc")}>
      <Show when={err()}><div class="errbar">{err()}</div></Show>
      <Field id="t-id" label={tr("t.create.id")} value={id()} onInput={setId} placeholder="acme"
        hint={tenantIdIssue(id().trim())
          ? undefined
          : id().trim()
            ? tr("t.create.idOk", { id: id().trim() })
            : tr("t.create.idHint")} />
      <Show when={tenantIdIssue(id().trim())}>
        <div class="errbar" style={{ "margin-top": "-8px" }}>
          {tenantIdIssue(id().trim())}
          <Show when={suggestTenantId(id().trim())}>
            {" "}
            <button class="linklike" onClick={() => setId(suggestTenantId(id().trim()))}>
              {tr("t.create.use", { s: suggestTenantId(id().trim()) })}
            </button>
          </Show>
        </div>
      </Show>
      <Field id="t-name" label={tr("t.create.name")} value={name()} onInput={setName} placeholder="Acme Inc." />
      <Field id="t-email" label={tr("t.create.email")} type="email" value={email()} onInput={setEmail} placeholder="ops@acme.com" />
      <Field id="t-plan" label={tr("t.create.plan")} value={plan()} onInput={setPlan} placeholder="free" />
      <div class="field">
        <label for="t-schema">{tr("t.create.schema")}</label>
        <textarea id="t-schema" rows="10" style={{ width: "100%", "font-family": "ui-monospace, monospace", "font-size": "12px" }}
          value={schema()} onInput={(e) => setSchema(e.currentTarget.value)} />
      </div>
      <div class="actions">
        <Button variant="ghost" onClick={props.onClose} disabled={busy()}>{tr("c.cancel")}</Button>
        <Button variant="primary" onClick={submit} disabled={busy() || !id().trim() || tenantIdIssue(id().trim()) !== null}>{busy() ? tr("c.creating") : tr("t.create.btn")}</Button>
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
      toast(tr("t.deleted", { id: id() }))
      setConfirm("")
      props.onDeleted()
    } catch (ex) { setErr(ex.message || tr("t.del.failed")) }
    finally { setBusy(false) }
  }

  return (
    <Modal open={!!props.target} onClose={(o) => !o && (setConfirm(""), props.onClose())} busy={busy()}
      title={tr("t.del.title")}
      description={tr("t.del.desc", { id: id() })}>
      <Show when={err()}><div class="errbar">{err()}</div></Show>
      <Field id="del-confirm" label={tr("t.del.confirm", { id: id() })} value={confirm()} onInput={setConfirm} placeholder={id()} />
      <div class="actions">
        <Button variant="ghost" onClick={() => { setConfirm(""); props.onClose() }} disabled={busy()}>{tr("c.cancel")}</Button>
        <Button variant="danger" onClick={submit} disabled={busy() || confirm().trim() !== id()}>{busy() ? tr("c.deleting") : tr("t.del.btn")}</Button>
      </div>
    </Modal>
  )
}

function fmtDate(v) {
  if (!v) return "—"
  try { return new Date(v).toLocaleDateString(undefined, { year: "numeric", month: "short", day: "numeric" }) }
  catch { return String(v) }
}
