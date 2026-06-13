import { createSignal, onMount, Show, For } from 'solid-js'

// Acceptance tab: runs scripts/acceptance-test.sh against a registered server
// over SSH tunnels (data-plane + control-plane) and streams output line by line.
// PASS/FAIL/INFO lines are color-coded; the summary card turns red if any
// checks fail or the script exits non-zero.

const lineClass = (line) => {
  if (line.startsWith('✓ PASS')) return 'pass'
  if (line.startsWith('✗ FAIL')) return 'fail'
  if (line.startsWith('ℹ INFO')) return 'text-slate-400'
  if (line.startsWith('──')) return 'text-slate-600'
  if (line.startsWith('⎇')) return 'text-blue-400'
  return ''
}

export default function Acceptance() {
  const [servers, setServers] = createSignal([])
  const [serverId, setServerId] = createSignal('')
  const [lines, setLines] = createSignal([])
  const [running, setRunning] = createSignal(false)
  const [summary, setSummary] = createSignal(null)
  let termRef

  onMount(async () => {
    try {
      const ss = await fetch('/api/servers').then(r => r.json())
      setServers(ss)
      if (ss.length && !serverId()) setServerId(String(ss[0].id))
    } catch { /* empty registry */ }
  })

  const append = (line) => {
    setLines(prev => [...prev, line])
    if (termRef) termRef.scrollTop = termRef.scrollHeight
  }

  const run = async () => {
    if (running() || !serverId()) return
    setLines([])
    setSummary(null)
    setRunning(true)
    try {
      const resp = await fetch('/api/acceptance-run?server=' + serverId())
      if (!resp.ok) {
        const e = await resp.json().catch(() => ({}))
        append('✗ ' + (e.error || 'HTTP ' + resp.status))
        return
      }
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
          if (ev === 'line') {
            try { append(JSON.parse(payload).line) } catch { append(payload) }
          } else if (ev === 'done') {
            try { setSummary(JSON.parse(payload)) } catch { /* ignore */ }
          }
        }
      }
    } catch (e) {
      append('✗ error de red: ' + e.message)
    } finally {
      setRunning(false)
    }
  }

  const summaryOk = (s) => s.fail === 0 && s.exit === 0

  return (
    <div class="space-y-4">
      <section class="space-y-3">
        <div class="text-xs text-slate-600 uppercase tracking-widest">Acceptance Test</div>

        <div class="flex flex-wrap items-end gap-3">
          <label class="flex flex-col gap-1">
            <span class="text-xs text-slate-600">server</span>
            <select value={serverId()} onChange={e => setServerId(e.currentTarget.value)}
              class="bg-slate-900 border border-slate-700 rounded px-2 py-1.5 text-sm text-slate-200 min-w-56">
              <For each={servers()}>{s => (
                <option value={String(s.id)}>
                  {s.name} · {s.host}{s.is_production ? ' · PROD' : ''}
                </option>
              )}</For>
            </select>
          </label>

          <button onClick={run} disabled={running() || !serverId()}
            class={`px-4 py-2 rounded text-sm border transition-all
              ${running()
                ? 'border-yellow-500 bg-yellow-500/10 text-yellow-400 animate-pulse'
                : 'border-slate-700 bg-slate-800 text-slate-200 hover:border-slate-500'}
              disabled:opacity-50 disabled:cursor-not-allowed`}>
            <Show when={running()} fallback="▶ Run Acceptance">⏳ Corriendo…</Show>
          </button>

          <Show when={running()}>
            <span class="text-xs text-slate-500 pb-2 animate-pulse">corriendo acceptance-test.sh…</span>
          </Show>
        </div>

        <Show when={lines().length > 0}>
          <div ref={termRef} class="terminal h-96">
            <For each={lines()}>{line => (
              <div class={lineClass(line)}>{line || ' '}</div>
            )}</For>
            <Show when={running()}><div class="text-yellow-500 animate-pulse">▋</div></Show>
          </div>
        </Show>

        <Show when={summary()}>
          {s => (
            <div class={`p-3 rounded border text-sm font-mono flex items-center gap-4
              ${summaryOk(s())
                ? 'border-green-800 bg-green-500/10 text-green-400'
                : 'border-red-800 bg-red-500/10 text-red-400'}`}>
              <span class="font-bold">
                {summaryOk(s()) ? '✓ ACEPTACIÓN COMPLETA' : '✗ FALLO'}
              </span>
              <span class="text-green-400">PASS: {s().pass}</span>
              <span class={s().fail > 0 ? 'text-red-400' : 'text-slate-500'}>FAIL: {s().fail}</span>
              <span class="text-slate-400">INFO: {s().info}</span>
              <Show when={s().exit !== 0 && s().fail === 0}>
                <span class="text-red-400 text-xs">(exit {s().exit})</span>
              </Show>
            </div>
          )}
        </Show>
      </section>
    </div>
  )
}
