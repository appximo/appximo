import { createResource, createSignal, onMount, onCleanup, Show, For } from "solid-js"
import { useNavigate } from "@solidjs/router"
import { DataTable } from "../components/DataTable"
import { Modal } from "../components/Modal"
import { Button, Field, StatusBadge, toast } from "../components/ui"
import { api, ApiError } from "../lib/api"
import { logout } from "../lib/auth"
import { selectedTenant } from "../lib/tenantContext"
import { registerCommands } from "../lib/commands"

export function Users() {
  const navigate = useNavigate()
  const tid = () => selectedTenant()

  const fetchUsers = async (id) => {
    if (!id) return []
    try { return (await api.listUsers(id)).users || [] }
    catch (ex) {
      if (ex instanceof ApiError && (ex.status === 401 || ex.status === 403)) { logout(); navigate("/login") }
      throw ex
    }
  }
  // Keyed on the selected tenant: switching tenant refetches.
  const [users, { refetch }] = createResource(tid, fetchUsers)
  const [roles] = createResource(tid, async (id) => {
    if (!id) return []
    try { return (await api.listResources(id)).roles || [] } catch { return [] }
  })

  const [showCreate, setShowCreate] = createSignal(false)
  const [roleTarget, setRoleTarget] = createSignal(null)
  const [delTarget, setDelTarget] = createSignal(null)

  onMount(() => {
    onCleanup(registerCommands([
      { id: "user:create", label: "Create user", hint: "Users", run: () => setShowCreate(true) },
      { id: "user:refresh", label: "Refresh users", hint: "Users", run: () => refetch() },
    ]))
  })

  const toggleSuspend = async (u) => {
    try {
      await api.updateUser(tid(), u.id, { suspended: !u.suspended })
      toast(u.suspended ? `Reactivated ${u.email}` : `Suspended ${u.email}`)
      refetch()
    } catch (ex) { toast(ex.message || "Operation failed", "err") }
  }

  const columns = [
    { accessorKey: "email", header: "Email", cell: (c) => <span class="cell-id">{c.getValue()}</span> },
    { accessorKey: "role", header: "Role", cell: (c) => <span class="secondary">{c.getValue()}</span> },
    {
      accessorKey: "suspended", header: "Status",
      cell: (c) => <StatusBadge kind={c.getValue() ? "warn" : "ok"} okLabel="Active" warnLabel="Suspended" />,
    },
    {
      accessorKey: "email_verified", header: "Verified",
      cell: (c) => c.getValue()
        ? <span class="badge badge-ok"><span aria-hidden="true">✓</span><span>Verified</span></span>
        : <span class="muted">— Unverified</span>,
    },
    { accessorKey: "created_at", header: "Created", cell: (c) => <span class="secondary">{fmtDate(c.getValue())}</span> },
    {
      id: "actions", header: "",
      cell: (c) => {
        const u = c.row.original
        return (
          <div class="cell-actions row" style={{ "justify-content": "flex-end" }}>
            <Button size="sm" variant="ghost" onClick={() => setRoleTarget(u)}>Role</Button>
            <Button size="sm" variant="ghost" onClick={() => toggleSuspend(u)}>{u.suspended ? "Activate" : "Suspend"}</Button>
            <Button size="sm" variant="ghost" class="btn-danger" onClick={() => setDelTarget(u)}>Delete</Button>
          </div>
        )
      },
    },
  ]

  return (
    <>
      <div class="pagehead">
        <h1>Users</h1>
        <Show when={tid()}><span class="muted">· {tid()}</span></Show>
        <span class="spacer" />
        <Button variant="ghost" size="sm" onClick={() => refetch()}>Refresh</Button>
        <Button variant="primary" disabled={!tid()} onClick={() => setShowCreate(true)}>New user</Button>
      </div>

      <Show when={tid()} fallback={<div class="empty">Select a tenant to manage its users.</div>}>
        <Show when={users.error}><div class="errbar">Could not load users: {users.error.message}</div></Show>
        <DataTable
          data={users() || []}
          columns={columns}
          emptyMessage={users.loading ? "Loading…" : "No users in this tenant yet."}
        />
      </Show>

      <CreateUserDialog open={showCreate()} tid={tid()} roles={roles() || []}
        onClose={() => setShowCreate(false)} onCreated={() => { setShowCreate(false); refetch() }} />
      <RoleDialog target={roleTarget()} tid={tid()} roles={roles() || []}
        onClose={() => setRoleTarget(null)} onSaved={() => { setRoleTarget(null); refetch() }} />
      <DeleteUserDialog target={delTarget()} tid={tid()}
        onClose={() => setDelTarget(null)} onDeleted={() => { setDelTarget(null); refetch() }} />
    </>
  )
}

function CreateUserDialog(props) {
  const [email, setEmail] = createSignal("")
  const [password, setPassword] = createSignal("")
  const [role, setRole] = createSignal("")
  const [err, setErr] = createSignal("")
  const [busy, setBusy] = createSignal(false)

  const submit = async () => {
    setErr(""); setBusy(true)
    try {
      await api.createUser(props.tid, { email: email().trim(), password: password(), role: role() || (props.roles[0] || "") })
      toast(`Created ${email().trim()}`)
      setEmail(""); setPassword(""); setRole("")
      props.onCreated()
    } catch (ex) {
      setErr((ex.body && ex.body.error) || ex.message || "Create failed")
    } finally { setBusy(false) }
  }

  return (
    <Modal open={props.open} onClose={(o) => !o && props.onClose()} busy={busy()} title="Create user"
      description="Creates a user in this tenant with an existing RBAC role.">
      <Show when={err()}><div class="errbar">{err()}</div></Show>
      <Field id="u-email" label="Email" type="email" value={email()} onInput={setEmail} placeholder="user@tenant.com" />
      <Field id="u-pass" label="Password" type="password" value={password()} onInput={setPassword} hint="min 8 chars" />
      <div class="field">
        <label for="u-role">Role</label>
        <select id="u-role" value={role()} onChange={(e) => setRole(e.currentTarget.value)}>
          <For each={props.roles}>{(r) => <option value={r}>{r}</option>}</For>
        </select>
      </div>
      <div class="actions">
        <Button variant="ghost" onClick={props.onClose} disabled={busy()}>Cancel</Button>
        <Button variant="primary" onClick={submit} disabled={busy() || !email().trim() || !password()}>{busy() ? "Creating…" : "Create"}</Button>
      </div>
    </Modal>
  )
}

function RoleDialog(props) {
  const [role, setRole] = createSignal("")
  const [busy, setBusy] = createSignal(false)
  const [err, setErr] = createSignal("")
  // Seed the select with the user's current role when the dialog opens.
  const current = () => props.target?.role || (props.roles[0] || "")

  const submit = async () => {
    setErr(""); setBusy(true)
    try {
      await api.updateUser(props.tid, props.target.id, { role: role() || current() })
      toast(`Role updated for ${props.target.email}`)
      props.onSaved()
    } catch (ex) { setErr(ex.message || "Update failed") }
    finally { setBusy(false) }
  }

  return (
    <Modal open={!!props.target} onClose={(o) => !o && props.onClose()} busy={busy()} title="Change role"
      description={props.target ? `Assign an existing role to ${props.target.email}.` : ""}>
      <Show when={err()}><div class="errbar">{err()}</div></Show>
      <div class="field">
        <label for="r-role">Role</label>
        <select id="r-role" value={role() || current()} onChange={(e) => setRole(e.currentTarget.value)}>
          <For each={props.roles}>{(r) => <option value={r}>{r}</option>}</For>
        </select>
      </div>
      <div class="actions">
        <Button variant="ghost" onClick={props.onClose} disabled={busy()}>Cancel</Button>
        <Button variant="primary" onClick={submit} disabled={busy()}>{busy() ? "Saving…" : "Save"}</Button>
      </div>
    </Modal>
  )
}

function DeleteUserDialog(props) {
  const [busy, setBusy] = createSignal(false)
  const [err, setErr] = createSignal("")
  const submit = async () => {
    setErr(""); setBusy(true)
    try { await api.deleteUser(props.tid, props.target.id); toast(`Deleted ${props.target.email}`); props.onDeleted() }
    catch (ex) { setErr(ex.message || "Delete failed") }
    finally { setBusy(false) }
  }
  return (
    <Modal open={!!props.target} onClose={(o) => !o && props.onClose()} busy={busy()} title="Delete user"
      description={props.target ? `Permanently remove ${props.target.email} from this tenant.` : ""}>
      <Show when={err()}><div class="errbar">{err()}</div></Show>
      <div class="actions">
        <Button variant="ghost" onClick={props.onClose} disabled={busy()}>Cancel</Button>
        <Button variant="danger" onClick={submit} disabled={busy()}>{busy() ? "Deleting…" : "Delete"}</Button>
      </div>
    </Modal>
  )
}

function fmtDate(v) {
  if (!v) return "—"
  try { return new Date(v).toLocaleDateString(undefined, { year: "numeric", month: "short", day: "numeric" }) }
  catch { return String(v) }
}
