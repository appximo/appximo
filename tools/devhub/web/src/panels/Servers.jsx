import { createSignal, onMount, Show, For } from 'solid-js'
import InfoTip from '../components/InfoTip'

// Servers panel (S47b): registry view + per-server admin-key management.
// The API only ever exposes EXISTENCE of a secret (secret-status) and its
// usage audit — never the value, not even masked. Fetch = one click = the
// human authorization for the DevHub to read the key over SSH and store it
// age-encrypted on its own host.

const fmtDate = (s) => { const d = new Date(s + 'Z'); return Number.isNaN(+d) ? s : d.toLocaleString() }

export default function Servers() {
  const [servers, setServers] = createSignal([])
  const [status, setStatus] = createSignal({})   // id -> {admin_key_present, source}
  const [audit, setAudit] = createSignal({})     // id -> entries
  const [busy, setBusy] = createSignal({})       // id -> bool
  const [msg, setMsg] = createSignal({})         // id -> {ok, text}
  const [manual, setManual] = createSignal({})   // id -> input value

  const load = async () => {
    try {
      const ss = await fetch('/api/servers').then((r) => r.json())
      setServers(ss)
      for (const s of ss) {
        refreshStatus(s.id)
        refreshAudit(s.id)
      }
    } catch { /* registry vacío */ }
  }
  onMount(load)

  const refreshStatus = async (id) => {
    try {
      const st = await fetch(`/api/servers/${id}/secret-status`).then((r) => r.json())
      setStatus((prev) => ({ ...prev, [id]: st }))
    } catch { /* ignore */ }
  }
  const refreshAudit = async (id) => {
    try {
      const entries = await fetch(`/api/audit/secrets?server_id=${id}`).then((r) => r.json())
      setAudit((prev) => ({ ...prev, [id]: entries.slice(0, 10) }))
    } catch { /* ignore */ }
  }

  const act = async (id, fn) => {
    setBusy((p) => ({ ...p, [id]: true }))
    setMsg((p) => ({ ...p, [id]: null }))
    try {
      const resp = await fn()
      const body = await resp.json().catch(() => ({}))
      if (!resp.ok) throw new Error(body.error || 'HTTP ' + resp.status)
      setMsg((p) => ({ ...p, [id]: { ok: true, text: '✓ ok' } }))
    } catch (e) {
      setMsg((p) => ({ ...p, [id]: { ok: false, text: '✗ ' + e.message } }))
    } finally {
      setBusy((p) => ({ ...p, [id]: false }))
      refreshStatus(id)
      refreshAudit(id)
    }
  }

  const fetchKey = (id) => act(id, () =>
    fetch(`/api/servers/${id}/fetch-admin-key`, { method: 'POST' }))

  const saveManual = (id) => {
    const value = manual()[id]
    if (!value) return
    return act(id, () =>
      fetch(`/api/servers/${id}/admin-key`, {
        method: 'PUT', headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ value }),
      })).then(() => setManual((p) => ({ ...p, [id]: '' })))
  }

  const rotate = (id) => act(id, () =>
    fetch(`/api/servers/${id}/admin-key`, { method: 'DELETE' }))

  return (
    <div class="space-y-6">
      <div class="text-xs text-slate-500 border border-slate-800 bg-slate-900 rounded p-3">
        🔒 Los secrets se almacenan <span class="text-slate-300">encriptados (age)</span> en el host
        del DevHub y <span class="text-slate-300">nunca se muestran ni viajan por la API</span> —
        solo existencia y auditoría de uso.
      </div>

      <For each={servers()} fallback={<div class="text-sm text-slate-600">Sin servers registrados.</div>}>
        {(s) => {
          const st = () => status()[s.id]
          return (
            <section class="rounded border border-slate-800 bg-slate-900 p-4 space-y-3">
              <div class="flex items-center gap-3 flex-wrap">
                <span class="text-slate-200 font-bold">{s.name}</span>
                <span class="text-xs text-slate-500">{s.host}:{s.port} · motor :{s.engine_port}</span>
                <Show when={s.is_production}>
                  <span class="text-xs px-2 py-0.5 rounded border border-red-800 text-red-400 bg-red-500/10">PROD</span>
                </Show>
                <Show when={st()} fallback={<span class="text-xs text-slate-600">admin key: …</span>}>
                  <span class={`text-xs px-2 py-0.5 rounded border ${st().admin_key_present
                    ? 'border-green-800 text-green-400 bg-green-500/10'
                    : 'border-yellow-800 text-yellow-400 bg-yellow-500/10'}`}>
                    admin key: {st().admin_key_present ? `✓ configurada (${st().source})` : '✗ falta'}
                  </span>
                  <InfoTip label="Para qué se usa la admin key">
                    La key se usa solo para leer /metrics del server. Si las métricas
                    aparecen en 0 con motor UP, probablemente fue rotada: mirá la auditoría.
                  </InfoTip>
                </Show>
              </div>

              <div class="flex flex-wrap items-center gap-2">
                <button onClick={() => fetchKey(s.id)} disabled={busy()[s.id]}
                  class="px-3 py-1.5 rounded text-xs border border-slate-700 bg-slate-800 text-slate-200
                         hover:border-slate-500 disabled:opacity-50">
                  {busy()[s.id] ? '⏳…' : '⬇ Obtener del servidor'}
                </button>
                <input type="password" placeholder="o pegá la key manualmente…" autocomplete="off"
                  value={manual()[s.id] ?? ''}
                  onInput={(e) => setManual((p) => ({ ...p, [s.id]: e.currentTarget.value }))}
                  class="w-56 bg-slate-950 border border-slate-700 rounded px-2 py-1.5 text-xs text-slate-200" />
                <button onClick={() => saveManual(s.id)} disabled={busy()[s.id] || !(manual()[s.id])}
                  class="px-3 py-1.5 rounded text-xs border border-slate-700 bg-slate-800 text-slate-200
                         hover:border-slate-500 disabled:opacity-50">
                  Guardar
                </button>
                <Show when={st()?.admin_key_present && st()?.source === 'store'}>
                  <button onClick={() => rotate(s.id)} disabled={busy()[s.id]}
                    class="px-3 py-1.5 rounded text-xs border border-red-900 text-red-400 hover:border-red-700 disabled:opacity-50">
                    Rotar / limpiar
                  </button>
                </Show>
                <Show when={msg()[s.id]}>
                  <span class={`text-xs ${msg()[s.id].ok ? 'text-green-400' : 'text-red-400'}`}>{msg()[s.id].text}</span>
                </Show>
              </div>

              <Show when={(audit()[s.id] ?? []).length > 0}>
                <div class="text-xs text-slate-600 pt-1">Últimos accesos al secret:</div>
                <table class="text-xs w-full max-w-md">
                  <tbody>
                    <For each={audit()[s.id]}>{(a) => (
                      <tr class="text-slate-500 border-b border-slate-800/60">
                        <td class="py-0.5 pr-4 text-slate-400">{a.operation}</td>
                        <td>{fmtDate(a.ts)}</td>
                      </tr>
                    )}</For>
                  </tbody>
                </table>
              </Show>
            </section>
          )
        }}
      </For>
    </div>
  )
}
