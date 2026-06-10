import { createSignal, onMount, onCleanup, Show, For } from 'solid-js'
import * as echarts from 'echarts/core'
import { BarChart, BoxplotChart, LineChart } from 'echarts/charts'
import { GridComponent, TooltipComponent, MarkLineComponent, LegendComponent } from 'echarts/components'
import { CanvasRenderer } from 'echarts/renderers'

echarts.use([
  BarChart, BoxplotChart, LineChart,
  GridComponent, TooltipComponent, MarkLineComponent, LegendComponent,
  CanvasRenderer,
])

// Shared dark/slate/mono chart styling, matching LiveMetrics.jsx.
const BASE = {
  backgroundColor: 'transparent',
  textStyle: { color: '#94a3b8', fontFamily: 'monospace' },
}
const AXIS = {
  axisLine: { lineStyle: { color: '#334155' } },
  axisLabel: { color: '#64748b', fontSize: 10 },
}
const SPLIT = { lineStyle: { color: '#1e293b' } }
const TIP = { backgroundColor: '#1e293b', borderColor: '#334155', textStyle: { color: '#e2e8f0' } }

const BLUE = '#60a5fa'        // baseline (Run A)
const GREEN = '#34d399'       // candidate (Run B)
const BLUE_FILL = 'rgba(96,165,250,0.5)'
const GREEN_FILL = 'rgba(52,211,153,0.5)'

const DIR_COLOR = { improvement: 'text-green-400', regression: 'text-red-400', no_change: 'text-slate-400' }
const DIR_LABEL = { improvement: 'IMPROVEMENT', regression: 'REGRESSION', no_change: 'NO CHANGE' }

const fmtMs = (x) => (x ?? 0).toFixed(2)
const fmtPct = (x) => (x ?? 0).toFixed(1)
const fmtP = (p) => (p === 0 || p < 0.001 ? '<0.001' : p.toFixed(3))
const fmtDate = (s) => { const d = new Date(s); return Number.isNaN(+d) ? s : d.toLocaleString() }

export default function BenchmarkLab() {
  const [runs, setRuns] = createSignal([])
  const [aId, setAId] = createSignal('')
  const [bId, setBId] = createSignal('')
  const [verdict, setVerdict] = createSignal(null)
  const [comparing, setComparing] = createSignal(false)
  const [cmpErr, setCmpErr] = createSignal(null)
  const [statsAB, setStatsAB] = createSignal(null) // [statsA, statsB] for the n/outlier captions

  const [mode, setMode] = createSignal('runs')      // 'runs' = run-vs-run · 'groups' = label-vs-label
  const [groupA, setGroupA] = createSignal('')
  const [groupB, setGroupB] = createSignal('')

  const [form, setForm] = createSignal({ label: 'ui-run', runs: 3, rate: 50, duration: '15s' })
  const [protoLines, setProtoLines] = createSignal([])
  const [protoRunning, setProtoRunning] = createSignal(false)

  let histRef, boxRef, trendRef, termRef
  let histChart, boxChart, trendChart

  const runById = (id) => runs().find((r) => String(r.id) === String(id))
  const labelOf = (id) => { const r = runById(id); return r ? `${r.label} (#${r.id})` : `#${id}` }
  // Group base label = the part before "#" (the protocol stores runs as "<label>#<i>"),
  // matching runIDsForLabel on the backend.
  const groupLabels = () => [...new Set(runs().map((r) => String(r.label).split('#')[0]))].sort()

  onMount(() => {
    histChart = echarts.init(histRef)
    boxChart = echarts.init(boxRef)
    trendChart = echarts.init(trendRef)
    loadRuns()
    const onResize = () => { histChart?.resize(); boxChart?.resize(); trendChart?.resize() }
    window.addEventListener('resize', onResize)
    onCleanup(() => window.removeEventListener('resize', onResize))
  })
  onCleanup(() => { histChart?.dispose(); boxChart?.dispose(); trendChart?.dispose() })

  const loadRuns = async () => {
    try {
      const resp = await fetch('/api/bench/runs')
      if (!resp.ok) throw new Error('HTTP ' + resp.status)
      const data = await resp.json()
      setRuns(data)
      // Default: newest run as candidate (B), the one before it as baseline (A).
      // /runs comes back id DESC, so data[0] is newest.
      if (data.length >= 2) {
        if (!bId()) setBId(String(data[0].id))
        if (!aId()) setAId(String(data[1].id))
      }
      const bases = [...new Set(data.map((r) => String(r.label).split('#')[0]))].sort()
      if (bases.length) {
        if (!groupA()) setGroupA(bases[0])
        if (!groupB()) setGroupB(bases[1] ?? bases[0])
      }
      renderTrend(data)
    } catch (e) {
      setCmpErr('No se pudo cargar la lista de runs: ' + e.message)
    }
  }

  const renderTrend = (data) => {
    if (!trendChart || !data?.length) return
    const sorted = [...data].sort((x, y) => new Date(x.created_at) - new Date(y.created_at))
    const point = (key) => sorted.map((r) => ({ value: [new Date(r.created_at).getTime(), r[key]], name: r.label }))
    trendChart.setOption({
      ...BASE,
      grid: { left: 52, right: 20, top: 34, bottom: 28 },
      legend: { data: ['p95', 'p50'], top: 4, right: 8, textStyle: { color: '#94a3b8', fontSize: 11 } },
      tooltip: {
        ...TIP, trigger: 'axis',
        formatter: (ps) => {
          const label = ps[0]?.data?.name ?? ''
          const rows = ps.map((p) => `${p.marker}${p.seriesName}: ${(+p.value[1]).toFixed(2)}ms`).join('<br/>')
          return `<b>${label}</b><br/>${rows}`
        },
      },
      xAxis: { type: 'time', ...AXIS, splitLine: { show: false } },
      yAxis: { type: 'value', name: 'ms', ...AXIS, splitLine: SPLIT },
      series: [
        {
          name: 'p95', type: 'line', symbol: 'circle', symbolSize: 6, smooth: true,
          data: point('p95_ms'), lineStyle: { color: BLUE, width: 2 }, itemStyle: { color: BLUE },
          markLine: {
            silent: true, symbol: 'none',
            lineStyle: { color: '#f87171', type: 'dashed' },
            label: { color: '#f87171', fontSize: 10, formatter: 'SLO 15ms' },
            data: [{ yAxis: 15 }],
          },
        },
        {
          name: 'p50', type: 'line', symbol: 'circle', symbolSize: 6, smooth: true,
          data: point('p50_ms'), lineStyle: { color: GREEN, width: 2 }, itemStyle: { color: GREEN },
        },
      ],
    }, true)
  }

  const compare = async () => {
    setCmpErr(null)
    const a = Number(aId()), b = Number(bId())
    if (!a || !b) { setCmpErr('Seleccioná dos runs.'); return }
    if (a === b) { setCmpErr('Elegí dos runs distintos.'); return }
    setComparing(true)
    try {
      const resp = await fetch('/api/bench/compare', {
        method: 'POST', headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ run_a: a, run_b: b }),
      })
      if (!resp.ok) { const e = await resp.json().catch(() => ({})); throw new Error(e.error || ('HTTP ' + resp.status)) }
      setVerdict(await resp.json())
      await renderComparison(a, b)
    } catch (e) {
      setVerdict(null); setStatsAB(null); setCmpErr('Comparación falló: ' + e.message)
    } finally {
      setComparing(false)
    }
  }

  // Switching mode clears the verdict so a runs-verdict never renders under the
  // group layout (and vice-versa). The per-run hist/box charts stay mounted
  // (hidden via CSS in group mode) so they keep their ECharts instance; resize
  // them when they become visible again.
  const switchMode = (m) => {
    if (m === mode()) return
    setMode(m)
    setVerdict(null); setStatsAB(null); setCmpErr(null)
    if (m === 'runs') queueMicrotask(() => { histChart?.resize(); boxChart?.resize() })
  }

  const compareGroups = async () => {
    setCmpErr(null)
    const la = groupA(), lb = groupB()
    if (!la || !lb) { setCmpErr('Seleccioná dos grupos.'); return }
    setComparing(true)
    try {
      const resp = await fetch('/api/bench/compare-groups', {
        method: 'POST', headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ label_a: la, label_b: lb }),
      })
      if (!resp.ok) { const e = await resp.json().catch(() => ({})); throw new Error(e.error || ('HTTP ' + resp.status)) }
      setVerdict(await resp.json())
      setStatsAB(null) // group mode has no per-run boxplot captions
    } catch (e) {
      setVerdict(null); setCmpErr('Comparación de grupos falló: ' + e.message)
    } finally {
      setComparing(false)
    }
  }

  const renderComparison = async (a, b) => {
    const [ha, hb, sa, sb] = await Promise.all([
      fetch(`/api/bench/runs/${a}/histogram`).then((r) => r.json()),
      fetch(`/api/bench/runs/${b}/histogram`).then((r) => r.json()),
      fetch(`/api/bench/runs/${a}/stats`).then((r) => r.json()),
      fetch(`/api/bench/runs/${b}/stats`).then((r) => r.json()),
    ])
    renderHistogram(ha, hb)
    renderBoxplots(sa, sb)
    setStatsAB([sa, sb])
  }

  const renderHistogram = (ha, hb) => {
    if (!histChart) return
    const nameA = labelOf(aId()), nameB = labelOf(bId())
    const allMs = [...ha.buckets, ...hb.buckets].map((x) => x.ms)
    const maxMs = allMs.length ? Math.max(...allMs) : 1
    const minMs = allMs.length ? Math.min(...allMs) : 0
    // Heavy tail → log X so the sub-ms bulk stays legible next to the tail.
    // Buckets are 1ms wide; the [0,1) bucket has ms=0 and log(0) is undefined,
    // so when going log we plot each bucket at its center (ms+0.5).
    const useLog = (maxMs + 0.5) / (minMs + 0.5) > 100

    const barCommon = { type: 'bar', barGap: '-100%' } // -100% overlaps the two series
    let xAxis, seriesA, seriesB
    if (useLog) {
      xAxis = { type: 'log', name: 'ms', ...AXIS, splitLine: { show: false } }
      const pts = (h) => h.buckets.map((x) => [x.ms + 0.5, x.count])
      seriesA = { ...barCommon, name: nameA, data: pts(ha), itemStyle: { color: BLUE_FILL } }
      seriesB = { ...barCommon, name: nameB, data: pts(hb), itemStyle: { color: GREEN_FILL } }
    } else {
      // Linear: align both series onto the shared sorted set of 1ms buckets so
      // they overlap cleanly on a category axis (0 where a bucket is absent).
      const cats = [...new Set(allMs)].sort((p, q) => p - q)
      const lut = (h) => Object.fromEntries(h.buckets.map((x) => [x.ms, x.count]))
      const la = lut(ha), lb = lut(hb)
      xAxis = { type: 'category', name: 'ms', data: cats.map(String), ...AXIS, splitLine: { show: false } }
      seriesA = { ...barCommon, name: nameA, data: cats.map((m) => la[m] ?? 0), itemStyle: { color: BLUE_FILL }, barCategoryGap: '10%' }
      seriesB = { ...barCommon, name: nameB, data: cats.map((m) => lb[m] ?? 0), itemStyle: { color: GREEN_FILL }, barCategoryGap: '10%' }
    }
    histChart.setOption({
      ...BASE,
      grid: { left: 52, right: 20, top: 34, bottom: 28 },
      legend: { data: [nameA, nameB], top: 4, right: 8, textStyle: { color: '#94a3b8', fontSize: 11 } },
      tooltip: { ...TIP, trigger: 'axis', axisPointer: { type: 'shadow' } },
      xAxis,
      yAxis: { type: 'value', name: 'count', ...AXIS, splitLine: SPLIT },
      series: [seriesA, seriesB],
    }, true)
  }

  const renderBoxplots = (sa, sb) => {
    if (!boxChart) return
    const box = (s, border, fill) => ({
      value: [s.min, s.q1, s.median, s.q3, s.max],
      itemStyle: { color: fill, borderColor: border },
    })
    boxChart.setOption({
      ...BASE,
      grid: { left: 52, right: 20, top: 18, bottom: 30 },
      tooltip: {
        ...TIP, trigger: 'item',
        formatter: (p) => {
          const v = p.value.length >= 6 ? p.value.slice(1) : p.value // boxplot prepends the category index
          const [mn, q1, md, q3, mx] = v
          return `min ${fmtMs(mn)}<br/>Q1 ${fmtMs(q1)}<br/>median ${fmtMs(md)}<br/>Q3 ${fmtMs(q3)}<br/>max ${fmtMs(mx)} ms`
        },
      },
      xAxis: { type: 'category', data: [labelOf(aId()), labelOf(bId())], ...AXIS, splitLine: { show: false } },
      yAxis: { type: 'value', name: 'ms', ...AXIS, splitLine: SPLIT },
      series: [{
        type: 'boxplot',
        data: [box(sa, BLUE, 'rgba(96,165,250,0.25)'), box(sb, GREEN, 'rgba(52,211,153,0.25)')],
      }],
    }, true)
  }

  // ── verdict prose ──────────────────────────────────────────────────────────
  const isGroupResult = (v) => v && v.label_a !== undefined
  const verdictText = () => {
    const v = verdict()
    if (!v) return ''
    let nA, nB, suffix = ''
    if (isGroupResult(v)) {
      nA = v.n_a; nB = v.n_b
      suffix = ` · grupos A=${v.label_a} (${v.n_runs_a} runs) vs B=${v.label_b} (${v.n_runs_b} runs)`
    } else {
      nA = runById(v.run_a)?.n_requests ?? '—'
      nB = runById(v.run_b)?.n_requests ?? '—'
    }
    const ci = `IC95% del delta de medianas [${fmtMs(v.ci_lower_ms)}, ${fmtMs(v.ci_upper_ms)}]ms`
    const stat = `p=${fmtP(v.p_value)}, ${ci}, n_A=${nA}, n_B=${nB}`
    let core
    if (v.direction === 'improvement') core = `B es ${fmtPct(Math.abs(v.delta_pct))}% más rápido que A en p95 (${stat})`
    else if (v.direction === 'regression') core = `B es ${fmtPct(Math.abs(v.delta_pct))}% más lento que A en p95 (${stat})`
    else if (v.p_value >= 0.05) core = `Sin diferencia estadísticamente significativa (${stat})` // no_change: not significant
    else core = `Diferencia estadísticamente detectable pero por debajo del umbral práctico de ${fmtMs(v.min_effect_ms)}ms (${stat})` // no_change: below min_effect
    return core + suffix
  }

  // ── protocol launcher (SSE over POST via fetch ReadableStream) ───────────────
  const appendProto = (line) => {
    setProtoLines((prev) => [...prev, line])
    if (termRef) termRef.scrollTop = termRef.scrollHeight
  }

  const runProtocol = async () => {
    if (protoRunning()) return
    const f = form()
    setProtoLines([`$ bench-protocol runs=${f.runs} label=${f.label} rate=${f.rate} duration=${f.duration}`])
    setProtoRunning(true)
    try {
      const resp = await fetch('/api/bench/protocol', {
        method: 'POST', headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ runs: Number(f.runs), label: f.label, rate: Number(f.rate), duration: f.duration }),
      })
      // Validation rejections come back as JSON, not SSE.
      if (!resp.ok) {
        const e = await resp.json().catch(() => ({}))
        appendProto(`✗ ${e.error || 'HTTP ' + resp.status}`)
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
          if (line === '') { ev = ''; continue }                 // blank line ends the SSE event
          if (line.startsWith('event:')) { ev = line.slice(6).trim(); continue }
          if (line.startsWith('data:')) {
            const payload = line.slice(5).replace(/^ /, '')       // drop the single SSE field-space, keep content indent
            if (ev === 'done') {
              let exit = 0
              try { exit = JSON.parse(payload).exit } catch { /* keep 0 */ }
              appendProto(exit === 0 ? '✓ protocolo completo (exit 0)' : `✗ protocolo terminó con exit ${exit}`)
            } else if (ev !== 'start') {
              appendProto(payload)
            }
          }
        }
      }
    } catch (e) {
      appendProto(`✗ error de red: ${e.message}`)
    } finally {
      setProtoRunning(false)
      await loadRuns() // surface the freshly-imported runs in the selects + trend
    }
  }

  const v = verdict
  return (
    <div class="space-y-6">
      {/* ── 2a. comparison selector + verdict ── */}
      <section class="space-y-3">
        <div class="flex items-center justify-between gap-3 flex-wrap">
          <div class="text-xs text-slate-600 uppercase tracking-widest">Comparador estadístico</div>
          <div class="inline-flex rounded border border-slate-700 overflow-hidden text-xs">
            <button onClick={() => switchMode('runs')}
              class={`px-3 py-1.5 ${mode() === 'runs' ? 'bg-slate-700 text-slate-100' : 'bg-slate-900 text-slate-400 hover:text-slate-200'}`}>
              Comparar runs individuales
            </button>
            <button onClick={() => switchMode('groups')}
              class={`px-3 py-1.5 border-l border-slate-700 ${mode() === 'groups' ? 'bg-slate-700 text-slate-100' : 'bg-slate-900 text-slate-400 hover:text-slate-200'}`}>
              Comparar grupos por label
            </button>
          </div>
        </div>

        <Show
          when={mode() === 'runs'}
          fallback={
            <div class="flex flex-wrap items-end gap-3">
              <GroupSelect label="Grupo A · baseline" value={groupA()} onChange={setGroupA} labels={groupLabels()} accent="text-blue-400" />
              <GroupSelect label="Grupo B · candidato" value={groupB()} onChange={setGroupB} labels={groupLabels()} accent="text-green-400" />
              <button onClick={compareGroups} disabled={comparing()}
                class="px-4 py-2 rounded text-sm border border-slate-700 bg-slate-800 text-slate-200
                       hover:border-slate-500 disabled:opacity-50 disabled:cursor-not-allowed">
                <Show when={comparing()} fallback="Comparar grupos">⏳ Comparando…</Show>
              </button>
            </div>
          }>
          <div class="flex flex-wrap items-end gap-3">
            <RunSelect label="Run A · baseline" value={aId()} onChange={setAId} runs={runs()} accent="text-blue-400" />
            <RunSelect label="Run B · candidato" value={bId()} onChange={setBId} runs={runs()} accent="text-green-400" />
            <button onClick={compare} disabled={comparing()}
              class="px-4 py-2 rounded text-sm border border-slate-700 bg-slate-800 text-slate-200
                     hover:border-slate-500 disabled:opacity-50 disabled:cursor-not-allowed">
              <Show when={comparing()} fallback="Comparar">⏳ Comparando…</Show>
            </button>
          </div>
        </Show>
        <Show when={cmpErr()}>
          <div class="px-3 py-2 rounded text-sm badge-fail">{cmpErr()}</div>
        </Show>
        <Show when={v()}>
          <div class="rounded border border-slate-800 bg-slate-900 p-4 space-y-3">
            <div class={`text-2xl font-bold ${DIR_COLOR[v().direction] ?? 'text-slate-400'}`}>
              {DIR_LABEL[v().direction] ?? v().direction}
            </div>
            <div class="grid grid-cols-2 sm:grid-cols-4 gap-3 text-sm">
              <Metric label="p-value" val={fmtP(v().p_value)} ok={v().p_value < 0.05} />
              <Metric label="IC95% (ms)" val={`[${fmtMs(v().ci_lower_ms)}, ${fmtMs(v().ci_upper_ms)}]`} />
              <Metric label="Δ p95" val={`${v().delta_pct >= 0 ? '+' : ''}${fmtPct(v().delta_pct)}%`} />
              <Metric label="min_effect" val={`${fmtMs(v().min_effect_ms)} ms`} />
            </div>
            <Show when={isGroupResult(v())}>
              <div class="grid grid-cols-1 sm:grid-cols-2 gap-2 text-xs text-slate-500 border-t border-slate-800 pt-3">
                <div>
                  <span class="text-blue-400">A · {v().label_a}</span> · {v().n_runs_a} runs · {v().n_a} pts ·
                  CV entre-runs <span class={v().cv_between_runs_a > 0.05 ? 'text-red-400' : 'text-slate-300'}>{fmtPct(v().cv_between_runs_a * 100)}%</span>
                </div>
                <div>
                  <span class="text-green-400">B · {v().label_b}</span> · {v().n_runs_b} runs · {v().n_b} pts ·
                  CV entre-runs <span class={v().cv_between_runs_b > 0.05 ? 'text-red-400' : 'text-slate-300'}>{fmtPct(v().cv_between_runs_b * 100)}%</span>
                </div>
              </div>
            </Show>
            <div class="text-sm text-slate-300 leading-relaxed">{verdictText()}</div>
          </div>
        </Show>
      </section>

      {/* ── 2b. overlaid distributions (per-run only; kept mounted, hidden in group mode) ── */}
      <section class="space-y-2" classList={{ hidden: mode() === 'groups' }}>
        <div class="text-xs text-slate-600">
          Distribuciones superpuestas — <span class="text-blue-400">A</span> vs <span class="text-green-400">B</span> (latencia ms × count)
        </div>
        <div ref={histRef} class="w-full h-56 bg-slate-900 rounded border border-slate-800" />
      </section>

      {/* ── 2c. boxplots (per-run only; kept mounted, hidden in group mode) ── */}
      <section class="space-y-2" classList={{ hidden: mode() === 'groups' }}>
        <div class="text-xs text-slate-600">Boxplots (min · Q1 · mediana · Q3 · max)</div>
        <div ref={boxRef} class="w-full h-56 bg-slate-900 rounded border border-slate-800" />
        <Show when={statsAB()}>
          <div class="grid grid-cols-2 gap-3 text-xs text-slate-500">
            <For each={statsAB()}>{(s, i) => (
              <div class="text-center">
                <span class={i() === 0 ? 'text-blue-400' : 'text-green-400'}>{i() === 0 ? 'A' : 'B'}</span>
                {' '}· n={s.n} · outliers IQR={s.outliers_iqr}
              </div>
            )}</For>
          </div>
        </Show>
      </section>

      {/* ── 2d. historical trend ── */}
      <section class="space-y-2">
        <div class="text-xs text-slate-600">
          Trend histórico — <span class="text-blue-400">p95</span> / <span class="text-green-400">p50</span> por run · SLO 15ms
        </div>
        <div ref={trendRef} class="w-full h-56 bg-slate-900 rounded border border-slate-800" />
      </section>

      {/* ── 2e. protocol launcher ── */}
      <section class="space-y-3">
        <div class="text-xs text-slate-600 uppercase tracking-widest">Lanzar protocolo</div>
        <div class="flex flex-wrap items-end gap-3">
          <Field label="label">
            <input type="text" value={form().label} onInput={(e) => setForm({ ...form(), label: e.currentTarget.value })}
              class="w-40 bg-slate-900 border border-slate-700 rounded px-2 py-1 text-sm text-slate-200" />
          </Field>
          <Field label="runs">
            <input type="number" min="1" max="20" value={form().runs} onInput={(e) => setForm({ ...form(), runs: e.currentTarget.value })}
              class="w-20 bg-slate-900 border border-slate-700 rounded px-2 py-1 text-sm text-slate-200" />
          </Field>
          <Field label="rate">
            <input type="number" min="10" max="1000" value={form().rate} onInput={(e) => setForm({ ...form(), rate: e.currentTarget.value })}
              class="w-24 bg-slate-900 border border-slate-700 rounded px-2 py-1 text-sm text-slate-200" />
          </Field>
          <Field label="duration">
            <select value={form().duration} onChange={(e) => setForm({ ...form(), duration: e.currentTarget.value })}
              class="bg-slate-900 border border-slate-700 rounded px-2 py-1 text-sm text-slate-200">
              <For each={['10s', '15s', '30s', '60s']}>{(d) => <option value={d}>{d}</option>}</For>
            </select>
          </Field>
          <button
            onClick={runProtocol} disabled={protoRunning()}
            class={`px-4 py-2 rounded text-sm border transition-all
              ${protoRunning()
                ? 'border-yellow-500 bg-yellow-500/10 text-yellow-400 animate-pulse'
                : 'border-slate-700 bg-slate-800 text-slate-200 hover:border-slate-500'}
              disabled:opacity-50 disabled:cursor-not-allowed`}>
            <Show when={protoRunning()} fallback="Ejecutar protocolo">⏳ Ejecutando…</Show>
          </button>
        </div>
        <Show when={protoLines().length > 0}>
          <div ref={termRef} class="terminal h-64">
            <For each={protoLines()}>{(line) => (
              <div class={line.includes('✗') || line.includes('WARNING') ? 'fail' : line.includes('✓') ? 'pass' : ''}>{line}</div>
            )}</For>
            <Show when={protoRunning()}><div class="text-yellow-500 animate-pulse">▋</div></Show>
          </div>
        </Show>
      </section>
    </div>
  )
}

function RunSelect(props) {
  return (
    <label class="flex flex-col gap-1">
      <span class={`text-xs ${props.accent}`}>{props.label}</span>
      <select
        value={props.value} onChange={(e) => props.onChange(e.currentTarget.value)}
        class="bg-slate-900 border border-slate-700 rounded px-2 py-1.5 text-sm text-slate-200 min-w-64">
        <option value="">— elegir run —</option>
        <For each={props.runs}>{(r) => (
          <option value={String(r.id)}>{`#${r.id} ${r.label} · ${fmtDate(r.created_at)} · p95 ${fmtMs(r.p95_ms)}ms`}</option>
        )}</For>
      </select>
    </label>
  )
}

function GroupSelect(props) {
  return (
    <label class="flex flex-col gap-1">
      <span class={`text-xs ${props.accent}`}>{props.label}</span>
      <select
        value={props.value} onChange={(e) => props.onChange(e.currentTarget.value)}
        class="bg-slate-900 border border-slate-700 rounded px-2 py-1.5 text-sm text-slate-200 min-w-64">
        <option value="">— elegir grupo —</option>
        <For each={props.labels}>{(l) => <option value={l}>{l}</option>}</For>
      </select>
    </label>
  )
}

function Metric(props) {
  return (
    <div class="bg-slate-950/40 rounded p-2 border border-slate-800">
      <div class="text-xs text-slate-600 mb-1">{props.label}</div>
      <div class={`font-bold ${props.ok === undefined ? 'text-slate-200' : props.ok ? 'text-green-400' : 'text-slate-400'}`}>{props.val}</div>
    </div>
  )
}

function Field(props) {
  return (
    <label class="flex flex-col gap-1">
      <span class="text-xs text-slate-600">{props.label}</span>
      {props.children}
    </label>
  )
}
