import { createSignal, onCleanup, For, Show } from 'solid-js'

const TARGETS = [
  { id: 'test',             label: 'Unit',        desc: '-race -short' },
  { id: 'test-integration', label: 'Integration', desc: 'testcontainers' },
  { id: 'test-e2e',         label: 'E2E',         desc: 'httpexpect' },
  { id: 'test-resilience',  label: 'Resilience',  desc: 'toxiproxy' },
  { id: 'test-all',         label: 'Full Suite',  desc: 'todo' },
  { id: 'bench',            label: 'Benchmark',   desc: 'benchstat' },
]

export default function TestRunner() {
  const [lines, setLines] = createSignal([])
  const [running, setRunning] = createSignal(null)
  const [lastExit, setLastExit] = createSignal(null)
  const [history, setHistory] = createSignal([])
  let es = null
  let terminalRef

  const run = (target) => {
    if (running()) return
    if (es) { es.close(); es = null }
    setLines([`$ make ${target}`])
    setRunning(target)
    setLastExit(null)
    const startTs = Date.now()
    es = new EventSource(`/api/run?target=${target}`)
    es.onmessage = (e) => {
      setLines(prev => [...prev, e.data])
      if (terminalRef) terminalRef.scrollTop = terminalRef.scrollHeight
    }
    es.addEventListener('done', (e) => {
      const { exit } = JSON.parse(e.data)
      const duration = ((Date.now() - startTs) / 1000).toFixed(1)
      const result = { target, code: exit, duration, ts: new Date().toLocaleTimeString() }
      setLastExit(result)
      setHistory(prev => [result, ...prev].slice(0, 20))
      setRunning(null)
      es.close(); es = null
    })
    es.onerror = () => { setRunning(null); es?.close(); es = null }
  }

  onCleanup(() => es?.close())

  const colorLine = (line) => {
    if (line.includes('FAIL') || line.includes('Error')) return 'fail'
    if (line.includes('PASS') || line.includes('ok  \t')) return 'pass'
    return ''
  }

  return (
    <div class="space-y-4">
      <div class="flex flex-wrap gap-2">
        <For each={TARGETS}>{(t) => (
          <button onClick={() => run(t.id)} disabled={!!running()}
            title={`make ${t.id}`}
            class={`px-3 py-2 rounded text-sm border transition-all
              ${running() === t.id
                ? 'border-yellow-500 bg-yellow-500/10 text-yellow-400 animate-pulse'
                : 'border-slate-700 bg-slate-800 text-slate-300 hover:border-slate-500'}
              disabled:opacity-50 disabled:cursor-not-allowed`}>
            <Show when={running() === t.id} fallback={t.label}>⏳ {t.label}</Show>
          </button>
        )}</For>
      </div>
      <Show when={lastExit()}>
        {(r) => (
          <div class={`flex items-center gap-3 px-3 py-2 rounded text-sm ${r().code === 0 ? 'badge-pass' : 'badge-fail'}`}>
            <span class="font-bold">{r().code === 0 ? '✓ PASS' : `✗ FAIL (exit ${r().code})`}</span>
            <span class="opacity-70">make {r().target}</span>
            <span class="opacity-70">{r().duration}s</span>
            <span class="opacity-50 ml-auto">{r().ts}</span>
          </div>
        )}
      </Show>
      <div ref={terminalRef} class="terminal h-96">
        <For each={lines()}>{(line) => <div class={colorLine(line)}>{line}</div>}</For>
        <Show when={running()}><div class="text-yellow-500 animate-pulse">▋</div></Show>
      </div>
      <Show when={history().length > 0}>
        <div class="space-y-1">
          <For each={history()}>{(r) => (
            <div class="flex items-center gap-3 text-xs px-2 py-1 rounded bg-slate-900">
              <span class={r.code === 0 ? 'text-green-400' : 'text-red-400'}>{r.code === 0 ? '✓' : '✗'}</span>
              <span class="text-slate-400 font-mono">make {r.target}</span>
              <span class="text-slate-600">{r.duration}s</span>
              <span class="text-slate-700 ml-auto">{r.ts}</span>
            </div>
          )}</For>
        </div>
      </Show>
    </div>
  )
}
