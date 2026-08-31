import { createResource, createSignal, onMount, onCleanup, Show, For } from "solid-js"
import { useNavigate } from "@solidjs/router"
import { DataTable } from "../components/DataTable"
import { Modal } from "../components/Modal"
import { Button, Field, StatusBadge, toast } from "../components/ui"
import { api, ApiError } from "../lib/api"
import { logout } from "../lib/auth"
import { selectedTenant } from "../lib/tenantContext"
import { registerCommands } from "../lib/commands"
import { PageIntro } from "../shell/Shell"
import { t } from "../lib/i18n"

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
      { id: "user:create", label: t("u.create.title"), hint: t("u.title"), run: () => setShowCreate(true) },
      { id: "user:refresh", label: t("c.refresh") + " · " + t("u.title"), hint: t("u.title"), run: () => refetch() },
    ]))
  })

  const toggleSuspend = async (u) => {
    try {
      await api.updateUser(tid(), u.id, { suspended: !u.suspended })
      toast(u.suspended ? t("u.reactivated", { e: u.email }) : t("u.suspendedMsg", { e: u.email }))
      refetch()
    } catch (ex) { toast(ex.message || t("c.opFailed"), "err") }
  }

  const columns = [
    { accessorKey: "email", header: t("u.th.email"), cell: (c) => <span class="cell-id">{c.getValue()}</span> },
    { accessorKey: "role", header: t("u.th.role"), cell: (c) => <span class="secondary">{c.getValue()}</span> },
    {
      accessorKey: "suspended", header: t("u.th.status"),
      cell: (c) => <StatusBadge kind={c.getValue() ? "warn" : "ok"} okLabel={t("c.active")} warnLabel={t("c.suspended")} />,
    },
    {
      accessorKey: "email_verified", header: t("u.th.verified"),
      cell: (c) => c.getValue()
        ? <span class="badge badge-ok"><span aria-hidden="true">✓</span><span>{t("u.verified")}</span></span>
        : <span class="muted">{t("u.unverified")}</span>,
    },
    { accessorKey: "created_at", header: t("u.th.created"), cell: (c) => <span class="secondary">{fmtDate(c.getValue())}</span> },
    {
      id: "actions", header: "",
      cell: (c) => {
        const u = c.row.original
        return (
          <div class="cell-actions row" style={{ "justify-content": "flex-end" }}>
            <Button size="sm" variant="ghost" onClick={() => setRoleTarget(u)}>{t("u.role")}</Button>
            <Button size="sm" variant="ghost" onClick={() => toggleSuspend(u)}>{u.suspended ? t("c.activate") : t("c.suspend")}</Button>
            <Button size="sm" variant="ghost" class="btn-danger" onClick={() => setDelTarget(u)}>{t("c.delete")}</Button>
          </div>
        )
      },
    },
  ]

  return (
    <>
      <div class="pagehead">
        <h1>{t("u.title")}</h1>
        <Show when={tid()}><span class="muted">· {tid()}</span></Show>
        <span class="spacer" />
        <Button variant="ghost" size="sm" onClick={() => refetch()}>{t("c.refresh")}</Button>
        <Button variant="primary" disabled={!tid()} onClick={() => setShowCreate(true)}>{t("u.new")}</Button>
      </div>
      <PageIntro>{t("intro.users")}</PageIntro>

      <Show when={tid()} fallback={<div class="empty">{t("u.select")}</div>}>
        <Show when={users.error}><div class="errbar">{t("u.couldNotLoad", { e: users.error.message })}</div></Show>
        <DataTable
          data={users() || []}
          columns={columns}
          emptyMessage={users.loading ? t("c.loading") : t("u.empty")}
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
      toast(t("u.created", { e: email().trim() }))
      setEmail(""); setPassword(""); setRole("")
      props.onCreated()
    } catch (ex) {
      setErr((ex.body && ex.body.error) || ex.message || t("u.create.failed"))
    } finally { setBusy(false) }
  }

  return (
    <Modal open={props.open} onClose={(o) => !o && props.onClose()} busy={busy()} title={t("u.create.title")}
      description={t("u.create.desc")}>
      <Show when={err()}><div class="errbar">{err()}</div></Show>
      <Field id="u-email" label={t("u.create.email")} type="email" value={email()} onInput={setEmail} placeholder="user@tenant.com" />
      <Field id="u-pass" label={t("u.create.password")} type="password" value={password()} onInput={setPassword} hint={t("u.create.pwHint")} />
      <div class="field">
        <label for="u-role">{t("u.create.role")}</label>
        <select id="u-role" value={role()} onChange={(e) => setRole(e.currentTarget.value)}>
          <For each={props.roles}>{(r) => <option value={r}>{r}</option>}</For>
        </select>
      </div>
      <div class="actions">
        <Button variant="ghost" onClick={props.onClose} disabled={busy()}>{t("c.cancel")}</Button>
        <Button variant="primary" onClick={submit} disabled={busy() || !email().trim() || !password()}>{busy() ? t("c.creating") : t("u.create.btn")}</Button>
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
      toast(t("u.role.updated", { e: props.target.email }))
      props.onSaved()
    } catch (ex) { setErr(ex.message || t("u.role.failed")) }
    finally { setBusy(false) }
  }

  return (
    <Modal open={!!props.target} onClose={(o) => !o && props.onClose()} busy={busy()} title={t("u.role.title")}
      description={props.target ? t("u.role.desc", { e: props.target.email }) : ""}>
      <Show when={err()}><div class="errbar">{err()}</div></Show>
      <div class="field">
        <label for="r-role">{t("u.create.role")}</label>
        <select id="r-role" value={role() || current()} onChange={(e) => setRole(e.currentTarget.value)}>
          <For each={props.roles}>{(r) => <option value={r}>{r}</option>}</For>
        </select>
      </div>
      <div class="actions">
        <Button variant="ghost" onClick={props.onClose} disabled={busy()}>{t("c.cancel")}</Button>
        <Button variant="primary" onClick={submit} disabled={busy()}>{busy() ? t("c.saving") : t("c.save")}</Button>
      </div>
    </Modal>
  )
}

function DeleteUserDialog(props) {
  const [busy, setBusy] = createSignal(false)
  const [err, setErr] = createSignal("")
  const submit = async () => {
    setErr(""); setBusy(true)
    try { await api.deleteUser(props.tid, props.target.id); toast(t("u.deleted", { e: props.target.email })); props.onDeleted() }
    catch (ex) { setErr(ex.message || t("u.del.failed")) }
    finally { setBusy(false) }
  }
  return (
    <Modal open={!!props.target} onClose={(o) => !o && props.onClose()} busy={busy()} title={t("u.del.title")}
      description={props.target ? t("u.del.desc", { e: props.target.email }) : ""}>
      <Show when={err()}><div class="errbar">{err()}</div></Show>
      <div class="actions">
        <Button variant="ghost" onClick={props.onClose} disabled={busy()}>{t("c.cancel")}</Button>
        <Button variant="danger" onClick={submit} disabled={busy()}>{busy() ? t("c.deleting") : t("c.delete")}</Button>
      </div>
    </Modal>
  )
}

function fmtDate(v) {
  if (!v) return "—"
  try { return new Date(v).toLocaleDateString(undefined, { year: "numeric", month: "short", day: "numeric" }) }
  catch { return String(v) }
}
