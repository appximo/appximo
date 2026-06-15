import { createResource, For, Show, createEffect } from "solid-js"
import { api } from "../lib/api"
import { selectedTenant, setSelectedTenant }
  from "../lib/tenantContext"

// TenantSelect — the global tenant context picker in the topbar. The super-admin
// chooses which tenant Users/Data operate on; the choice is reflected here and
// persisted. Fetches the tenant list once.
export function TenantSelect() {
  const [tenants] = createResource(async () => {
    try { return (await api.listTenants()).tenants || [] } catch { return [] }
  })

  // Default the selection to the first tenant once the list loads (if none chosen
  // or the chosen one no longer exists).
  createEffect(() => {
    const list = tenants()
    if (!list || list.length === 0) return
    const cur = selectedTenant()
    if (!cur || !list.some((t) => t.id === cur)) setSelectedTenant(list[0].id)
  })

  return (
    <div class="row" style={{ gap: "var(--space-2)" }}>
      <span class="muted" style={{ "font-size": "12px" }}>Tenant</span>
      <Show when={(tenants() || []).length > 0} fallback={<span class="muted" style={{ "font-size": "13px" }}>no tenants</span>}>
        <select
          aria-label="Selected tenant"
          value={selectedTenant()}
          onChange={(e) => setSelectedTenant(e.currentTarget.value)}
          style={{ "min-width": "160px" }}
        >
          <For each={tenants()}>{(t) => (
            <option value={t.id}>{t.display_name || t.id}{t.suspended ? " (suspended)" : ""}</option>
          )}</For>
        </select>
      </Show>
    </div>
  )
}
