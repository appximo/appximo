import { createSignal, onMount, onCleanup, Show, For } from 'solid-js'
import * as echarts from 'echarts/core'
import { LineChart } from 'echarts/charts'
import { GridComponent, TooltipComponent, MarkLineComponent } from 'echarts/components'
import { CanvasRenderer } from 'echarts/renderers'

echarts.use([LineChart, GridComponent, TooltipComponent, MarkLineComponent, CanvasRenderer])

const CHART_THEME = {
  backgroundColor: 'transparent',
  textStyle: { color: '#94a3b8', fontFamily: 'monospace' },
  grid: { left: 55, right: 20, top: 30, bottom: 30 },
  tooltip: { trigger: 'axis', backgroundColor: '#1e293b', borderColor: '#334155' },
  xAxis: {
    type: 'time',
    axisLine: { lineStyle: { color: '#334155' } },
    axisLabel: { color: '#64748b', fontSize: 10 },
    splitLine: { show: false },
  },
  yAxis: {
    axisLine: { lineStyle: { color: '#334155' } },
    axisLabel: { color: '#64748b', fontSize: 10 },
    splitLine: { lineStyle: { color: '#1e293b' } },
  },
}

export default function LiveMetrics() {
  const [samples, setSamples] = createSignal([])
  const [connected, setConnected] = createSignal(false)
  const [servers, setServers] = createSignal([])
  const [serverId, setServerId] = createSignal('')
  let p95Ref, reqRef, es, p95Chart, reqChart

  // (Re)connect the SSE stream for the selected server. Remote servers are
  // scraped on demand server-side: the scrape starts with this subscription
  // and stops when the last watcher closes.
  const connect = (id) => {
    es?.close()
    setSamples([])
    setConnected(false)
    es = new EventSource(id ? `/api/metrics/live?server=${id}` : '/api/metrics/live')
    es.onopen = () => setConnected(true)
    es.onmessage = (e) => {
      const data = JSON.parse(e.data)
      if (!data?.length) return
      setSamples(data)

      p95Chart.setOption({ ...CHART_THEME,
        yAxis: { ...CHART_THEME.yAxis, name: 'ms' },
        series: [{
          type: 'line', smooth: true, symbol: 'none',
          lineStyle: { color: '#60a5fa', width: 2 },
          areaStyle: { color: 'rgba(96,165,250,0.05)' },
          data: data.map(s => [s.ts * 1000, s.p95_ms?.toFixed(2) ?? 0]),
          markLine: {
            silent: true,
            lineStyle: { color: '#f87171', type: 'dashed' },
            data: [{ yAxis: 15, name: 'SLO' }],
            label: { color: '#f87171', fontSize: 10 },
          }
        }]
      })

      reqChart.setOption({ ...CHART_THEME,
        yAxis: { ...CHART_THEME.yAxis, name: 'total' },
        series: [{
          type: 'line', smooth: true, symbol: 'none',
          lineStyle: { color: '#34d399', width: 2 },
          areaStyle: { color: 'rgba(52,211,153,0.05)' },
          data: data.map(s => [s.ts * 1000, s.requests_total ?? 0]),
        }]
      })
    }
    es.onerror = () => setConnected(false)
  }

  onMount(async () => {
    p95Chart = echarts.init(p95Ref)
    reqChart  = echarts.init(reqRef)

    try {
      const ss = await fetch('/api/servers').then((r) => r.json())
      setServers(ss)
      // Default: the local box (105-dev) when registered, else the local ring.
      const local = ss.find((s) => s.is_local)
      if (local) setServerId(String(local.id))
    } catch { /* empty registry → local ring */ }
    connect(serverId())

    const onResize = () => { p95Chart?.resize(); reqChart?.resize() }
    window.addEventListener('resize', onResize)
    onCleanup(() => window.removeEventListener('resize', onResize))
  })

  const pickServer = (id) => { setServerId(id); connect(id) }

  onCleanup(() => { es?.close(); p95Chart?.dispose(); reqChart?.dispose() })

  const last = () => samples()[samples().length - 1]

  return (
    <div class="space-y-4">
      <div class="flex items-center gap-3 text-xs text-slate-500 flex-wrap">
        <Show when={servers().length > 0}>
          <select value={serverId()} onChange={(e) => pickServer(e.currentTarget.value)}
            class="bg-slate-900 border border-slate-700 rounded px-2 py-1 text-xs text-slate-200">
            <For each={servers()}>{(s) => (
              <option value={String(s.id)}>{s.name} · {s.host}{s.is_production ? ' · PROD' : ''}</option>
            )}</For>
          </select>
        </Show>
        <span class="flex items-center gap-2">
          <span class={`w-2 h-2 rounded-full ${connected() ? 'bg-green-500' : 'bg-red-500'}`} />
          {connected() ? 'SSE activo — scrape cada 5s' : 'Sin conexión'}
        </span>
      </div>
      {last() && (
        <div class="grid grid-cols-4 gap-3">
          {[
            { label: 'p95',            val: `${last().p95_ms?.toFixed(1) ?? '—'} ms`, ok: (last().p95_ms ?? 0) < 15 },
            { label: 'requests_total', val: last().requests_total?.toFixed(0) ?? '—',  ok: true },
            { label: 'active_tenants', val: last().active_tenants?.toFixed(0) ?? '—',  ok: true },
            { label: 'motor',          val: last().motor_up ? 'UP' : 'DOWN',            ok: last().motor_up },
          ].map(kpi => (
            <div class="bg-slate-900 rounded p-3 border border-slate-800">
              <div class="text-xs text-slate-600 mb-1">{kpi.label}</div>
              <div class={`text-lg font-bold ${kpi.ok ? 'text-green-400' : 'text-red-400'}`}>{kpi.val}</div>
            </div>
          ))}
        </div>
      )}
      <div>
        <div class="text-xs text-slate-600 mb-2">p95 latency (ms) — SLO &lt;15ms</div>
        <div ref={p95Ref} class="w-full h-48 bg-slate-900 rounded border border-slate-800" />
      </div>
      <div>
        <div class="text-xs text-slate-600 mb-2">requests_total (acumulado)</div>
        <div ref={reqRef} class="w-full h-48 bg-slate-900 rounded border border-slate-800" />
      </div>
    </div>
  )
}
