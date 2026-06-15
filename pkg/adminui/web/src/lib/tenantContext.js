import { createSignal } from "solid-js"

// The tenant the super-admin is currently operating on. Users + Data screens read
// it. Persisted in localStorage so a refresh keeps the context. (A future
// tenant-admin would have a fixed tenant and no selector — documented extension
// point: seed this from the JWT's tenant_id and hide the selector.)
const KEY = "appitools_admin_tenant"
let initial = ""
try { initial = localStorage.getItem(KEY) || "" } catch { /* ignore */ }

const [selectedTenant, setSelectedTenantSig] = createSignal(initial)
export { selectedTenant }

export function setSelectedTenant(id) {
  try { id ? localStorage.setItem(KEY, id) : localStorage.removeItem(KEY) } catch { /* ignore */ }
  setSelectedTenantSig(id)
}
