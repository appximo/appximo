import { createSignal } from "solid-js"

// The tenant the super-admin is currently operating on. Users + Data screens read
// it. Persisted in localStorage so a refresh keeps the context. (A future
// tenant-admin would have a fixed tenant and no selector — documented extension
// point: seed this from the JWT's tenant_id and hide the selector.)
const KEY = "appximo_admin_tenant"
let initial = ""
try { initial = localStorage.getItem(KEY) || "" } catch { /* ignore */ }

const [selectedTenant, setSelectedTenantSig] = createSignal(initial)
export { selectedTenant }

export function setSelectedTenant(id) {
  try { id ? localStorage.setItem(KEY, id) : localStorage.removeItem(KEY) } catch { /* ignore */ }
  setSelectedTenantSig(id)
}

// tenantsVersion — bumped by any tenant mutation (create/delete/suspend) so the
// topbar TenantSelect refetches its list. Without it, selecting a JUST-created
// tenant got reverted: the picker's stale list didn't contain it yet, and its
// "chosen tenant no longer exists" guard reset the selection.
// Starts at 1: a Solid resource skips falsy sources, and 0 would suppress the
// initial fetch.
const [tenantsVersion, setTenantsVersion] = createSignal(1)
export { tenantsVersion }
export function bumpTenants() { setTenantsVersion((v) => v + 1) }
