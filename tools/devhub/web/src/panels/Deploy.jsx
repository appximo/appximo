import { createSignal, onMount, Show, For } from 'solid-js'
import InfoTip from '../components/InfoTip'

// Deploy panel (S47): runs the PRIMER deploy protocol against a registered
// server as an SSE pipeline (build → push → backup → swap/SIGTERM → start →
// smoke) and shows the deploy history. Production servers demand re-typing the
// server name (GitHub delete-repo style) before the button arms.

const STEP_ICON = { git: '⎇', build: '🔨', push: '⬆', backup: '💾', swap: '🔁', start: '▶', smoke: '🔥', record: '📝' }
const STATUS_BADGE = {
  success: 'text-green-400 border-green-800 bg-green-500/10',
  failed: 'text-red-400 border-red-800 bg-red-500/10',
  running: 'text-yellow-400 border-yellow-800 bg-yellow-500/10',
}

const fmtDate = (s) => { const d = new Date(s); return Number.isNaN(+d) ? s : d.toLocaleString() }

export default function Deploy() {
  const [servers, setServers] = createSignal([])
  const [serverId, setServerId] = createSignal('')
  const [confirm, setConfirm] = createSignal('')
  const [head, setHead] = createSignal(null)
  const [lines, setLines] = createSignal([])
  const [running, setRunning] = createSignal(false)
  const [deploys, setDeploys] = createSignal([])
  let termRef

  const selected = () => servers().find((s) => String(s.id) === String(serverId()))
  const needsConfirm = () => selected()?.is_production
  const armed = () => selected() && (!needsConfirm() || confirm() === selected().name)

  onMount(async () => {
    try {
      const ss = await fetch('/api/servers').then((r) => r.json())
      setServers(ss)
      if (ss.length && !serverId()) setServerId(String(ss[0].id))
    } catch { /* empty registry is fine */ }
    loadDeploys()
  })

  const loadDeploys = async () => {
    try { setDeploys(await fetch('/api/deploys').then((r) => r.json())) } catch { /* ignore */ }
  }

  const append = (line) => {
    setLines((prev) => [...prev, line])
    if (termRef) termRef.scrollTop = termRef.scrollHeight
  }

  const launch = async () => {
    if (running() || !armed()) return
    const srv = selected()
    setLines([`$ deploy → ${srv.name} (${srv.host}:${srv.engine_port})`])
    setRunning(true)
    try {
      const resp = await fetch('/api/deploy', {
        method: 'POST', headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ server_id: Number(srv.id), ...(needsConfirm() ? { confirm: confirm() } : {}) }),
      })
      if (!resp.ok) {
        const e = await resp.json().catch(() => ({}))
        append(`✗ ${e.error || 'HTTP ' + resp.status}`)
        return
      }
      // SSE over fetch ReadableStream (same pattern as the Benchmark Lab launcher).
      const reader = resp.body.getReader()
      const dec = new TextDecoder()
      let buf = '', ev = ''
      for (;;) {
        const { done, value } = await reader.read()
        if (done) break
        buf += dec.decode(value, { stream: true })
        let nl
        while ((nl = buf.indexOf('\n')) >= 0) {
          const line = buf.slice(0, nl)
          buf = buf.slice(nl + 1)
          if (line === '') { ev = ''; continue }
          if (line.startsWith('event:')) { ev = line.slice(6).trim(); continue }
          if (!line.startsWith('data:')) continue
          const payload = line.slice(5).replace(/^ /, '')
          if (ev === 'step') {
            try {
              const { step, line: l } = JSON.parse(payload)
              if (step === 'git' && l.startsWith('HEAD ')) setHead(l.slice(5, 17))
              append(`${STEP_ICON[step] ?? '·'} [${step}] ${l}`)
            } catch { append(payload) }
          } else if (ev === 'done') {
            try {
              const d = JSON.parse(payload)
              append(d.status === 'success' ? `✓ DEPLOY OK — sha ${d.sha?.slice(0, 12)}` : `✗ DEPLOY FAILED en paso "${d.step}"`)
            } catch { append(payload) }
          }
        }
      }
    } catch (e) {
      append(`✗ error de red: ${e.message}`)
    } finally {
      setRunning(false)
      setConfirm('')
      loadDeploys()
    }
  }

  return (
    <div class="space-y-6">
      <section class="space-y-3">
        <div class="text-xs text-slate-600 uppercase tracking-widest">
          Deploy — protocolo PRIMER{' '}
          <InfoTip label="Qué hace el pipeline">
            Pipeline: build local (105) → SFTP → backup pre-swap (rollback) →
            SIGTERM → start → smoke. El motor destino se reinicia: requests_total
            vuelve a 0 y hay ~5-10s de DOWN.
          </InfoTip>
        </div>
        <div class="flex flex-wrap items-end gap-3">
          <label class="flex flex-col gap-1">
            <span class="text-xs text-slate-600">server</span>
            <select value={serverId()} onChange={(e) => { setServerId(e.currentTarget.value); setConfirm('') }}
              class="bg-slate-900 border border-slate-700 rounded px-2 py-1.5 text-sm text-slate-200 min-w-56">
              <For each={servers()}>{(s) => (
                <option value={String(s.id)}>{s.name} · {s.host}{s.is_production ? ' · PROD' : ''}</option>
              )}</For>
            </select>
          </label>
          <Show when={needsConfirm()}>
            <label class="flex flex-col gap-1">
              <span class="text-xs text-red-400">⚠ producción — escribí “{selected()?.name}” para confirmar</span>
              <input type="text" value={confirm()} onInput={(e) => setConfirm(e.currentTarget.value)}
                placeholder={selected()?.name}
                class="w-56 bg-slate-900 border border-red-900 rounded px-2 py-1.5 text-sm text-slate-200" />
            </label>
          </Show>
          <button onClick={launch} disabled={running() || !armed()}
            class={`px-4 py-2 rounded text-sm border transition-all
              ${running() ? 'border-yellow-500 bg-yellow-500/10 text-yellow-400 animate-pulse'
                : 'border-slate-700 bg-slate-800 text-slate-200 hover:border-slate-500'}
              disabled:opacity-50 disabled:cursor-not-allowed`}>
            <Show when={running()} fallback="🚀 Deploy HEAD">⏳ Deployando…</Show>
          </button>
          <Show when={head()}>
            <span class="text-xs text-slate-500 pb-2">HEAD {head()}</span>
          </Show>
        </div>
        <Show when={lines().length > 0}>
          <div ref={termRef} class="terminal h-72">
            <For each={lines()}>{(line) => (
              <div class={line.includes('✗') ? 'fail' : line.includes('✓') ? 'pass' : ''}>{line}</div>
            )}</For>
            <Show when={running()}><div class="text-yellow-500 animate-pulse">▋</div></Show>
          </div>
        </Show>
      </section>

      <section class="space-y-2">
        <div class="text-xs text-slate-600 uppercase tracking-widest">Historial</div>
        <Show when={deploys().length} fallback={<div class="text-sm text-slate-600">Sin deploys registrados.</div>}>
          <table class="w-full text-sm">
            <thead>
              <tr class="text-xs text-slate-600 text-left border-b border-slate-800">
                <th class="py-1.5 pr-3">#</th><th class="pr-3">server</th><th class="pr-3">sha</th>
                <th class="pr-3">status</th><th class="pr-3">inicio</th><th>fin</th>
              </tr>
            </thead>
            <tbody>
              <For each={deploys()}>{(d) => (
                <tr class="border-b border-slate-900 text-slate-400">
                  <td class="py-1.5 pr-3">{d.id}</td>
                  <td class="pr-3 text-slate-300">{d.server_name}</td>
                  <td class="pr-3 font-mono text-xs">{d.sha?.slice(0, 12)}</td>
                  <td class="pr-3">
                    <span class={`px-2 py-0.5 rounded border text-xs ${STATUS_BADGE[d.status] ?? 'text-slate-400 border-slate-700'}`}>{d.status}</span>
                  </td>
                  <td class="pr-3 text-xs">{fmtDate(d.started_at)}</td>
                  <td class="text-xs">{d.finished_at ? fmtDate(d.finished_at) : '—'}</td>
                </tr>
              )}</For>
            </tbody>
          </table>
        </Show>
      </section>
    </div>
  )
}
