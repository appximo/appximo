import { createSignal, Show } from 'solid-js'
import TestRunner from './panels/TestRunner'
import LiveMetrics from './panels/LiveMetrics'
import BenchmarkLab from './panels/BenchmarkLab'

const TABS = [
  { id: 'runner',  label: '▶  Test Runner' },
  { id: 'metrics', label: '📊 Live Metrics' },
  { id: 'bench',   label: '⚡ Benchmark Lab' },
  { id: 'coverage',label: '🗺  Coverage',      wip: true },
]

export default function App() {
  const [tab, setTab] = createSignal('runner')
  return (
    <div class="min-h-screen flex flex-col">
      <header class="border-b border-slate-800 px-4 py-3 flex items-center gap-3">
        <span class="text-slate-400 text-xs font-bold tracking-widest">APPITOOLS</span>
        <span class="text-slate-600">|</span>
        <span class="text-slate-300 text-sm">DevHub</span>
      </header>
      <nav class="border-b border-slate-800 px-4 flex gap-1">
        {TABS.map(t => (
          <button
            onClick={() => !t.wip && setTab(t.id)}
            class={`px-4 py-2 text-sm border-b-2 transition-colors
              ${tab() === t.id ? 'border-blue-500 text-blue-400' : 'border-transparent text-slate-500 hover:text-slate-300'}
              ${t.wip ? 'opacity-40 cursor-not-allowed' : 'cursor-pointer'}`}
          >
            {t.label}{t.wip && <span class="ml-1 text-xs text-slate-600">S42</span>}
          </button>
        ))}
      </nav>
      <main class="flex-1 overflow-auto p-4">
        <Show when={tab() === 'runner'}><TestRunner /></Show>
        <Show when={tab() === 'metrics'}><LiveMetrics /></Show>
        <Show when={tab() === 'bench'}><BenchmarkLab /></Show>
      </main>
    </div>
  )
}
