import { createSignal, createMemo, createEffect, onMount, onCleanup, Show, For } from "solid-js"
import { useNavigate } from "@solidjs/router"
import { Chart } from "../components/Chart"
import { Button, toast } from "../components/ui"
import { api, ApiError } from "../lib/api"
import { logout } from "../lib/auth"
import { registerCommands } from "../lib/commands"
import { chartTheme, themeTick } from "../lib/theme"

// Resources (CENTINELA-C-S1, Module C) — the engine's OWN footprint on the box
// and the ATTRIBUTION verdict under load: "is it me (CPU, GC, locks, memory),
// the database (pool, query), or the host (the plan's CPU quota, the box's
// RAM)?" Everything on screen is the /admin/resources payload — four layers
// read by ONE collector goroutine, a deterministic verdict with its evidence —
// nothing is derived here. Three views: LIVE (cards), LOAD TEST (the
// correlation window + the verdict), SNAPSHOT (export the run as JSON, compare
// with a previous one). Generic by construction: nothing names a resource, a
// tenant or an app.

const MONO = { "font-family": "ui-monospace, SFMono-Regular, Menlo, monospace" }

// ── attribution vocabulary → label, owner reading, status kind ──────────────
const ATTR = {
  healthy:         { label: "Healthy",          kind: "ok",   who: "nothing to attribute" },
  cpu_saturated:   { label: "CPU saturated",    kind: "warn", who: "Appximo — or the box's sizing" },
  gc_pressure:     { label: "GC pressure",      kind: "warn", who: "Appximo — allocation in the code path" },
  cpu_throttled:   { label: "CPU throttled",    kind: "crit", who: "the host — the plan's quota, not the code" },
  pool_exhausted:  { label: "Pool exhausted",   kind: "crit", who: "the database / the pool config" },
  db_bound:        { label: "Database-bound",   kind: "crit", who: "the database (or the network to it) — not Appximo" },
  memory_pressure: { label: "Memory pressure",  kind: "crit", who: "memory — this process, or the box" },
  lock_contention: { label: "Lock contention",  kind: "warn", who: "Appximo — locks inside the process" },
}
const attr = (a) => ATTR[a] || { label: a || "—", kind: "ok", who: "" }
const ownerLabel = (o) => ({ appximo: "Appximo", database: "the database", host: "the host", none: "no one" }[o] || o)

// ── formatting ──────────────────────────────────────────────────────────────
const fmtMs = (ms) => (ms == null ? "—" : ms >= 100 ? ms.toFixed(0) : ms >= 10 ? ms.toFixed(1) : ms.toFixed(2))
const fmtMiB = (b) => (b == null || b < 0 ? "—" : (b / 1048576).toFixed(b >= 1073741824 ? 0 : 1))
const fmtPct = (f) => (f == null ? "—" : (f * 100).toFixed(f * 100 >= 10 ? 0 : 1))
const fmtNum = (n) => (n == null ? "—" : Number(n).toLocaleString())
const fmtClock = (ms) => { try { return new Date(ms).toLocaleTimeString() } catch { return "—" } }
const fmtSig = (s) => {
  if (s.unit === "%") return s.value.toFixed(1) + " %"
  if (s.unit === "ms") return fmtMs(s.value) + " ms"
  if (s.unit === "×") return s.value.toFixed(2) + "×"
  if (s.unit === "connections" || s.unit === "count") return fmtNum(s.value)
  if (s.unit === "/s") return s.value.toFixed(1) + "/s"
  return (s.value * 100).toFixed(1) + " %"
}
const fmtThr = (s) => {
  if (s.threshold === 0) return "—"
  if (s.unit === "%") return "≥ " + s.threshold + " %"
  if (s.unit === "ms") return "≥ " + s.threshold + " ms"
  if (s.unit === "×") return "≥ " + s.threshold + "×"
  if (s.unit === "connections") return "= " + s.threshold
  if (s.unit === "count" || s.unit === "/s") return "≥ " + s.threshold
  return "≥ " + (s.threshold * 100).toFixed(0) + " %"
}

function Badge(props) {
  const k = () => props.kind || "ok"
  const icon = { ok: "✓", warn: "▲", crit: "✕" }
  return <span class={"badge badge-" + k()}><span aria-hidden="true">{icon[k()]}</span><span>{props.label}</span></span>
}

function Stat(props) {
  return (
    <div class="stat card res-stat">
      <div class="stat-label">{props.label}</div>
      <div class="stat-value">{props.children}</div>
      <Show when={props.sub}><div class="stat-sub muted">{props.sub}</div></Show>
    </div>
  )
}

// The verdict — the product. Ink banner (the one strong surface on the page),
// the sentence an operator acts on, whose problem it is, the evidence.
function VerdictBanner(props) {
  const v = () => props.verdict
  const a = () => attr(v()?.attribution)
  const fired = () => (v()?.signals || []).filter((s) => s.fired)
  const rest = () => (v()?.signals || []).filter((s) => !s.fired)
  const [open, setOpen] = createSignal(false)
  return (
    <div class={"verdict verdict-" + a().kind}>
      <div class="verdict-head">
        <span class="verdict-eyebrow">{props.eyebrow}</span>
        <span class="spacer" />
        <Badge kind={a().kind} label={a().label} />
      </div>
      <div class="verdict-title">{props.title || (v()?.attribution === "healthy" ? "No bottleneck" : "Bottleneck: " + a().label.toLowerCase())}</div>
      <Show when={v()?.owner && v()?.owner !== "none"}>
        <div class="verdict-owner">Whose problem: <strong>{ownerLabel(v().owner)}</strong> · {a().who}</div>
      </Show>
      <div class="verdict-reason">{v()?.reason || "—"}</div>
      <Show when={(v()?.also || []).length > 0}>
        <div class="verdict-also">Also firing: <For each={v().also}>{(x) => <span class="chip">{attr(x).label}</span>}</For></div>
      </Show>
      <Show when={(v()?.signals || []).length > 0}>
        <button class="verdict-toggle" onClick={() => setOpen(!open())}>{open() ? "Hide evidence" : `Evidence — ${fired().length} of ${v().signals.length} signals fired`}</button>
        <Show when={open()}>
          <table class="sigtable">
            <thead><tr><th>Signal</th><th class="num-h">Value</th><th class="num-h">Rule</th><th></th></tr></thead>
            <tbody>
              <For each={[...fired(), ...rest()]}>{(s) => (
                <tr class={s.fired ? "fired" : ""}>
                  <td style={MONO}>{s.name}</td>
                  <td class="num">{fmtSig(s)}</td>
                  <td class="num muted">{fmtThr(s)}</td>
                  <td>{s.fired ? <Badge kind="warn" label="fired" /> : <span class="muted">—</span>}</td>
                </tr>
              )}</For>
            </tbody>
          </table>
        </Show>
      </Show>
    </div>
  )
}

// ── LIVE: the cards, one per layer ──────────────────────────────────────────
function LiveTab(props) {
  const s = () => props.latest()
  const rt = () => s()?.runtime || {}
  const pc = () => s()?.process_cgroup || {}
  const pr = () => s()?.pressure || {}
  const db = () => s()?.db_client || {}
  const sv = () => s()?.db_server_local_only || {}
  const rq = () => s()?.request || {}
  const memMax = () => (pc().mem_max_bytes > 0 ? fmtMiB(pc().mem_max_bytes) + " MiB max" : "no cgroup limit")
  const quota = () => (pc().cpu_quota_usec > 0 && pc().cpu_period_usec > 0 ? (pc().cpu_quota_usec / pc().cpu_period_usec).toFixed(2) + " CPU quota" : "no CPU quota")
  return (
    <div class="col" style={{ gap: "var(--space-4)" }}>
      <VerdictBanner verdict={s()?.verdict} eyebrow={`Latest tick · ${fmtClock(s()?.ts)} · ${s()?.mode} ${(s()?.interval_ms / 1000).toFixed(0)} s`} />

      <h3 class="res-h">Requests</h3>
      <div class="statgrid">
        <Stat label="Throughput" sub={`${fmtNum(rq().count)} requests in the tick`}>{(rq().rps ?? 0).toFixed(0)} <span class="unit">rps</span></Stat>
        <Stat label="p50 / p95" sub="ms, this tick">{fmtMs(rq().latency_p50_ms)} <span class="unit">/ {fmtMs(rq().latency_p95_ms)}</span></Stat>
        <Stat label="p99" sub={`max ${fmtMs(rq().latency_max_ms)} ms`}>{fmtMs(rq().latency_p99_ms)} <span class="unit">ms</span></Stat>
        <Stat label="Shed / errors" sub="429+503 · 5xx">{fmtNum((rq().status_429 || 0) + (rq().status_503 || 0))} <span class="unit">/ {fmtNum(rq().errors_5xx)}</span></Stat>
      </div>

      <h3 class="res-h">Memory <span class="muted">· cgroup v2 {pc().source === "cgroup" ? "" : "(unavailable — /proc fallback)"}</span></h3>
      <div class="statgrid">
        <Stat label="RSS" sub={`peak ${fmtMiB(pc().mem_peak_bytes)} MiB`}>{fmtMiB(pc().rss_bytes)} <span class="unit">MiB</span></Stat>
        <Stat label="cgroup memory.current" sub={pc().cgroup_shared ? "shared cgroup — includes other processes" : memMax()}>{fmtMiB(pc().mem_current_bytes)} <span class="unit">MiB</span></Stat>
        <Stat label="Live heap" sub={`goal ${fmtMiB(rt().heap_goal_bytes)} MiB · GOGC ${rt().gogc_percent ?? "—"}`}>{fmtMiB(rt().heap_objects_bytes)} <span class="unit">MiB</span></Stat>
        <Stat label="Runtime mapped" sub={rt().gomemlimit_bytes && rt().gomemlimit_bytes < 9e18 ? `GOMEMLIMIT ${fmtMiB(rt().gomemlimit_bytes)} MiB` : "GOMEMLIMIT unset"}>{fmtMiB(rt().memory_total_bytes)} <span class="unit">MiB</span></Stat>
      </div>

      <h3 class="res-h">CPU <span class="muted">· runtime/metrics + cpu.stat</span></h3>
      <div class="statgrid">
        <Stat label="Busy" sub={`of GOMAXPROCS ${rt().gomaxprocs ?? "—"} · ${quota()}`}>{fmtPct(rt().cpu_busy_fraction)} <span class="unit">%</span></Stat>
        <Stat label="Scheduler wait p99" sub="runnable goroutines waiting for a CPU">{fmtMs((rt().sched_latency_p99_s || 0) * 1000)} <span class="unit">ms</span></Stat>
        <Stat label="Throttled" sub={`cgroup: ${fmtNum(pc().cpu_nr_throttled_delta)} periods this tick`}>{fmtPct((pc().cpu_throttled_delta_usec || 0) / Math.max(1, (s()?.interval_ms || 1000) * 1000))} <span class="unit">%</span></Stat>
        <Stat label="Process CPU" sub="user + system, cumulative">{((pc().cpu_usage_usec || 0) / 1e6).toFixed(0)} <span class="unit">s</span></Stat>
      </div>

      <h3 class="res-h">GC · goroutines · locks</h3>
      <div class="statgrid">
        <Stat label="GC share of CPU" sub={`${(rt().gc_cycles_delta ?? 0)} cycles this tick`}>{fmtPct(rt().gc_cpu_fraction)} <span class="unit">%</span></Stat>
        <Stat label="GC pause p99" sub={`max ${fmtMs((rt().gc_pause_total_max_s || 0) * 1000)} ms`}>{fmtMs((rt().gc_pause_total_p99_s || 0) * 1000)} <span class="unit">ms</span></Stat>
        <Stat label="Goroutines" sub={`${pc().threads ?? "—"} OS threads`}>{fmtNum(rt().goroutines)}</Stat>
        <Stat label="Mutex wait" sub={`${(rt().mutex_wait_total_s || 0).toFixed(3)} s total`}>{((rt().mutex_wait_delta_s || 0) * 1000).toFixed(2)} <span class="unit">ms/tick</span></Stat>
      </div>

      <h3 class="res-h">Database <span class="muted">· pool (client side)</span></h3>
      <div class="statgrid">
        <Stat label="Pool" sub={db().saturated ? "SATURATED — no idle connection" : `${db().idle_conns ?? 0} idle · ${db().constructing_conns ?? 0} constructing`}>{db().acquired_conns ?? 0} <span class="unit">/ {db().max_conns ?? 0}</span></Stat>
        <Stat label="Empty acquires" sub={`${fmtNum(db().empty_acquire_count)} total`}>{fmtNum(db().empty_acquire_delta)} <span class="unit">this tick</span></Stat>
        <Stat label="Waited for a connection" sub="this tick">{fmtMs(db().empty_acquire_wait_delta_ms)} <span class="unit">ms</span></Stat>
        <Stat label="Query p99" sub={`${fmtNum(db().query_count)} queries · p50 ${fmtMs(db().query_latency_p50_ms)} ms`}>{fmtMs(db().query_latency_p99_ms)} <span class="unit">ms</span></Stat>
      </div>
      <Show when={sv().observable} fallback={
        <div class="card res-note"><strong>Postgres server: not observable from the app.</strong> {sv().reason || "The database is remote — its RAM, CPU and I/O are not this process's to read. That is by design: the client side above is what Appximo controls."}</div>
      }>
        <div class="statgrid">
          <Stat label="Database size" sub={sv().reason ? sv().reason : `probed ${fmtClock(sv().probed_at)}`}>{fmtMiB(sv().db_size_bytes)} <span class="unit">MiB</span></Stat>
          <Stat label="Cache hit ratio" sub={`${fmtNum(sv().blks_read)} blocks read from disk`}>{fmtPct(sv().cache_hit_ratio)} <span class="unit">%</span></Stat>
          <Stat label="Backends" sub={`${sv().active_conns ?? 0} active · ${sv().waiting ?? 0} waiting · ${sv().idle_in_transaction ?? 0} idle in tx`}>{fmtNum(sv().total_backends)}</Stat>
          <Stat label="Deadlocks · temp" sub={`temp ${fmtMiB(sv().temp_bytes)} MiB · ${sv().pg_stat_statements ? "pg_stat_statements on" : "no pg_stat_statements"}`}>{fmtNum(sv().deadlocks)}</Stat>
        </div>
      </Show>

      <h3 class="res-h">Host pressure <span class="muted">· PSI {pr().source === "cgroup" ? "of this cgroup" : pr().source === "host" ? "of the whole host" : "unavailable"}</span></h3>
      <div class="statgrid">
        <For each={[["CPU", pr().cpu], ["Memory", pr().memory], ["I/O", pr().io]]}>{([name, l]) => (
          <Stat label={name + " stall (some)"} sub={`60 s ${(l?.some_avg60 ?? 0).toFixed(1)} % · 300 s ${(l?.some_avg300 ?? 0).toFixed(1)} % · full ${(l?.full_avg10 ?? 0).toFixed(1)} %`}>{(l?.some_avg10 ?? 0).toFixed(1)} <span class="unit">% / 10 s</span></Stat>
        )}</For>
      </div>
    </div>
  )
}

// ── LOAD TEST: the correlation window ───────────────────────────────────────
function seriesOption(series, t, lines, yName, fmt) {
  const xs = series.map((s) => s.ts)
  return {
    animation: false,
    grid: { left: 52, right: 16, top: 30, bottom: 26 },
    color: lines.map((l) => l.color),
    legend: { data: lines.map((l) => l.name), right: 6, top: 2, icon: "roundRect", itemWidth: 10, itemHeight: 10, textStyle: { color: t.text, fontSize: 11 } },
    tooltip: { trigger: "axis", valueFormatter: (v) => (v == null ? "—" : fmt(v)) },
    xAxis: { type: "time", axisLine: { lineStyle: { color: t.grid } }, axisTick: { show: false }, axisLabel: { color: t.muted, fontSize: 11, hideOverlap: true } },
    yAxis: { type: "value", name: yName, nameTextStyle: { color: t.muted, fontSize: 10 }, splitLine: { lineStyle: { color: t.grid, type: "dashed" } }, axisLabel: { color: t.muted, fontSize: 11 } },
    series: lines.map((l) => ({ name: l.name, type: "line", showSymbol: false, smooth: false, lineStyle: { width: l.width || 2 }, areaStyle: l.area ? { opacity: 0.08 } : undefined, data: series.map((s, i) => [xs[i], l.get(s)]) })),
  }
}

function LoadTab(props) {
  const series = () => props.series() || []
  const win = () => props.window() || {}
  const [pick, setPick] = createSignal(null)
  const picked = () => (pick() != null ? series()[pick()] : null)
  const th = () => { themeTick(); return chartTheme() }
  const latOpt = createMemo(() => seriesOption(series(), th(), [
    { name: "p99 ms", color: th().crit, get: (s) => +(s.request?.latency_p99_ms || 0).toFixed(2) },
    { name: "query p99 ms", color: th().warn, get: (s) => +(s.db_client?.query_latency_p99_ms || 0).toFixed(2) },
    { name: "p50 ms", color: th().brand, get: (s) => +(s.request?.latency_p50_ms || 0).toFixed(2) },
  ], "ms", (v) => v.toFixed(2) + " ms"))
  const rpsOpt = createMemo(() => seriesOption(series(), th(), [
    { name: "rps", color: th().brand, area: true, get: (s) => +(s.request?.rps || 0).toFixed(1) },
    { name: "shed (429+503)/s", color: th().crit, get: (s) => +(((s.request?.status_429 || 0) + (s.request?.status_503 || 0)) / Math.max(0.001, (s.interval_ms || 1000) / 1000)).toFixed(1) },
  ], "req/s", (v) => v.toFixed(1)))
  const cpuOpt = createMemo(() => seriesOption(series(), th(), [
    { name: "sched wait p99 ms", color: th().crit, get: (s) => +((s.runtime?.sched_latency_p99_s || 0) * 1000).toFixed(3) },
    { name: "GC share %", color: th().warn, get: (s) => +((s.runtime?.gc_cpu_fraction || 0) * 100).toFixed(1) },
    { name: "throttled %", color: th().brand, get: (s) => +(((s.process_cgroup?.cpu_throttled_delta_usec || 0) / Math.max(1, (s.interval_ms || 1000) * 1000)) * 100).toFixed(1) },
    { name: "mutex wait ms", color: th().muted, get: (s) => +((s.runtime?.mutex_wait_delta_s || 0) * 1000).toFixed(2) },
  ], "ms · %", (v) => v.toFixed(2)))
  const poolOpt = createMemo(() => seriesOption(series(), th(), [
    { name: "pool acquired", color: th().brand, area: true, get: (s) => s.db_client?.acquired_conns || 0 },
    { name: "pool max", color: th().muted, width: 1, get: (s) => s.db_client?.max_conns || 0 },
    { name: "empty acquires", color: th().crit, get: (s) => s.db_client?.empty_acquire_delta || 0 },
  ], "connections", (v) => v.toFixed(0)))
  const psiOpt = createMemo(() => seriesOption(series(), th(), [
    { name: "PSI cpu some %", color: th().crit, get: (s) => s.pressure?.cpu_some_avg10 || 0 },
    { name: "PSI memory some %", color: th().warn, get: (s) => s.pressure?.mem_some_avg10 || 0 },
    { name: "PSI io some %", color: th().brand, get: (s) => s.pressure?.io_some_avg10 || 0 },
    { name: "RSS MiB", color: th().muted, get: (s) => +((s.process_cgroup?.rss_bytes || 0) / 1048576).toFixed(1) },
  ], "% · MiB", (v) => v.toFixed(1)))
  const dist = () => {
    const d = win().distribution || {}
    const total = Object.values(d).reduce((a, b) => a + b, 0) || 1
    return Object.keys(ATTR).filter((k) => d[k]).map((k) => ({ k, n: d[k], pct: (d[k] / total) * 100 }))
  }
  return (
    <div class="col" style={{ gap: "var(--space-4)" }}>
      <VerdictBanner
        verdict={{ attribution: win().dominant, owner: win().owner, reason: win().reason, signals: win().peak_tick?.verdict?.signals || [], also: win().peak_tick?.verdict?.also || [] }}
        eyebrow={`Window · ${series().length} ticks · ${series().length ? fmtClock(series()[0].ts) + " → " + fmtClock(series()[series().length - 1].ts) : "—"} · peak ${(win().peak_rps || 0).toFixed(0)} rps · peak p99 ${fmtMs(win().peak_p99_ms)} ms`}
        title={win().dominant === "healthy" ? "No bottleneck in the window" : "Bottleneck: " + attr(win().dominant).label.toLowerCase()}
      />

      <div class="card res-timeline">
        <div class="chart-head"><h3>Attribution per tick</h3><span class="muted">click a tick for its verdict · {fmtNum(win().requests)} requests · {fmtNum(win().shed_429_503)} shed · {fmtNum(win().errors_5xx)} 5xx</span></div>
        <div class="ticks" role="list">
          <For each={series()}>{(s, i) => (
            <button role="listitem" class={"tick tick-" + attr(s.attribution).kind + (s.attribution === "healthy" ? " tick-healthy" : "") + (pick() === i() ? " active" : "")}
              title={`${fmtClock(s.ts)} · ${attr(s.attribution).label} · ${(s.request?.rps || 0).toFixed(0)} rps · p99 ${fmtMs(s.request?.latency_p99_ms)} ms`}
              onClick={() => setPick(pick() === i() ? null : i())} />
          )}</For>
        </div>
        <div class="dist">
          <For each={dist()}>{(d) => <span class="chip"><Badge kind={attr(d.k).kind} label={attr(d.k).label} /> {d.n} · {d.pct.toFixed(0)} %</span>}</For>
        </div>
        <Show when={picked()}>
          <VerdictBanner verdict={picked().verdict} eyebrow={`Tick ${fmtClock(picked().ts)} · ${(picked().request?.rps || 0).toFixed(0)} rps · p99 ${fmtMs(picked().request?.latency_p99_ms)} ms · pool ${picked().db_client?.acquired_conns}/${picked().db_client?.max_conns} · sched ${fmtMs((picked().runtime?.sched_latency_p99_s || 0) * 1000)} ms`} />
        </Show>
      </div>

      <Show when={series().length > 1} fallback={<div class="card chart-empty muted">The correlation series builds at 1 s while this view is open. Start a k6 (or wait for traffic) and the ticks appear here.</div>}>
        <div class="res-charts">
          <div class="card chart-card"><div class="chart-head"><h3>Latency</h3><span class="muted">p99 · query p99 · p50</span></div><Chart option={latOpt()} height="200px" /></div>
          <div class="card chart-card"><div class="chart-head"><h3>Throughput</h3><span class="muted">rps · shed</span></div><Chart option={rpsOpt()} height="200px" /></div>
          <div class="card chart-card"><div class="chart-head"><h3>CPU · GC · locks · quota</h3><span class="muted">sched wait · GC share · throttled · mutex</span></div><Chart option={cpuOpt()} height="200px" /></div>
          <div class="card chart-card"><div class="chart-head"><h3>Connection pool</h3><span class="muted">acquired of max · empty acquires</span></div><Chart option={poolOpt()} height="200px" /></div>
          <div class="card chart-card"><div class="chart-head"><h3>Host pressure · memory</h3><span class="muted">PSI some avg10 · RSS</span></div><Chart option={psiOpt()} height="200px" /></div>
        </div>
      </Show>
    </div>
  )
}

// ── SNAPSHOT: export the run, compare with a previous one ───────────────────
function SnapshotTab(props) {
  const [busy, setBusy] = createSignal(false)
  const [other, setOther] = createSignal(null)
  const [otherName, setOtherName] = createSignal("")
  const download = async () => {
    setBusy(true)
    try {
      const text = await api.resourcesSnapshot()
      const blob = new Blob([text], { type: "application/json" })
      const url = URL.createObjectURL(blob)
      const a = document.createElement("a")
      a.href = url; a.download = `appximo-resources-${new Date().toISOString().replace(/[:.]/g, "-")}.json`
      document.body.appendChild(a); a.click(); a.remove()
      setTimeout(() => URL.revokeObjectURL(url), 2000)
      toast("Snapshot downloaded")
    } catch (e) { toast("Could not export: " + (e.message || e), "err") }
    setBusy(false)
  }
  const copy = async () => {
    try { await navigator.clipboard.writeText(await api.resourcesSnapshot()); toast("Snapshot copied to the clipboard") }
    catch (e) { toast("Could not copy: " + (e.message || e), "err") }
  }
  const load = (file) => {
    if (!file) return
    const r = new FileReader()
    r.onload = () => { try { setOther(JSON.parse(r.result)); setOtherName(file.name) } catch { toast("Not a snapshot JSON", "err") } }
    r.readAsText(file)
  }
  const win = () => props.window() || {}
  const ow = () => other()?.window || {}
  const Row = (p) => <tr><td>{p.label}</td><td class="num">{p.a}</td><Show when={other()}><td class="num">{p.b}</td></Show></tr>
  return (
    <div class="col" style={{ gap: "var(--space-4)" }}>
      <div class="card res-note">
        <strong>Export this run.</strong> The snapshot is the collector's ring — every tick with its four layers and its verdict, plus the engine build and the host — as one JSON document (schema <span style={MONO}>appximo.selfmon.snapshot/v1</span>). Attach it to a report, or load a previous one below to compare before and after a change. Nothing in it was produced by a language model: the verdicts are the deterministic rules and their evidence.
        <div class="row" style={{ "margin-top": "var(--space-3)", "flex-wrap": "wrap" }}>
          <Button variant="primary" onClick={download} disabled={busy()}>Download JSON</Button>
          <Button variant="ghost" onClick={copy}>Copy to clipboard</Button>
          <label class="btn btn-ghost">Load a previous snapshot… <input type="file" accept="application/json" style={{ display: "none" }} onChange={(e) => load(e.currentTarget.files?.[0])} /></label>
        </div>
      </div>
      <div class="card" style={{ padding: "var(--space-3)" }}>
        <div class="row" style={{ "margin-bottom": "var(--space-2)" }}><strong style={{ "font-size": "13px" }}>Window summary</strong><span class="spacer" /><span class="muted" style={{ "font-size": "12px" }}>{other() ? `this run vs ${otherName()}` : "this run"}</span></div>
        <table class="minitable">
          <thead><tr><th>Metric</th><th class="num-h">Now</th><Show when={other()}><th class="num-h">Loaded</th></Show></tr></thead>
          <tbody>
            <Row label="Dominant verdict" a={attr(win().dominant).label} b={attr(ow().dominant).label} />
            <Row label="Ticks (with traffic)" a={`${win().ticks ?? 0} (${win().traffic_ticks ?? 0})`} b={`${ow().ticks ?? 0} (${ow().traffic_ticks ?? 0})`} />
            <Row label="Requests" a={fmtNum(win().requests)} b={fmtNum(ow().requests)} />
            <Row label="Peak rps" a={(win().peak_rps || 0).toFixed(0)} b={(ow().peak_rps || 0).toFixed(0)} />
            <Row label="Peak p99 (ms)" a={fmtMs(win().peak_p99_ms)} b={fmtMs(ow().peak_p99_ms)} />
            <Row label="Shed (429+503)" a={fmtNum(win().shed_429_503)} b={fmtNum(ow().shed_429_503)} />
            <Row label="5xx" a={fmtNum(win().errors_5xx)} b={fmtNum(ow().errors_5xx)} />
            <Row label="Engine" a={props.version() || "—"} b={other()?.engine?.version || "—"} />
          </tbody>
        </table>
        <Show when={other()}><div class="muted" style={{ "font-size": "12px", "margin-top": "var(--space-2)" }}>Loaded verdict: {ow().reason}</div></Show>
      </div>
    </div>
  )
}

// ── the route ───────────────────────────────────────────────────────────────
export function Resources() {
  const navigate = useNavigate()
  const [tab, setTab] = createSignal("live")
  const [data, setData] = createSignal(null)
  const [err, setErr] = createSignal("")
  const [health, setHealth] = createSignal(null)
  let timer
  const poll = async () => {
    try {
      const d = await api.resources({ live: 1, series: 300 })
      setData(d); setErr("")
    } catch (ex) {
      if (ex instanceof ApiError && (ex.status === 401 || ex.status === 403)) { logout(); navigate("/login"); return }
      setErr(ex?.body?.error || ex.message || String(ex))
    }
  }
  onMount(async () => {
    setHealth(await api.health().catch(() => null))
    await poll()
    timer = setInterval(poll, 1000)
    onCleanup(() => clearInterval(timer))
    onCleanup(registerCommands([
      { id: "res:live", label: "Resources: live board", hint: "Resources", run: () => setTab("live") },
      { id: "res:load", label: "Resources: load test window", hint: "Resources", run: () => setTab("load") },
      { id: "res:snap", label: "Resources: export snapshot", hint: "Resources", run: () => setTab("snapshot") },
    ]))
  })
  const latest = () => data()?.latest
  const series = () => data()?.series
  const window_ = () => data()?.window
  const col = () => data()?.collector || {}
  return (
    <>
      <div class="pagehead res-head">
        <h1>Resources</h1>
        <span class="muted res-sub">the engine's own footprint · is it me, the database or the host?</span>
        <span class="spacer" />
        <Show when={data()}><span class={"badge " + (col().live ? "badge-ok" : "badge-warn")}><span aria-hidden="true">{col().live ? "●" : "○"}</span><span>{col().mode} · {(col().interval_ms / 1000).toFixed(0)} s</span></span></Show>
        <div class="seg">
          <button class={"seg-btn" + (tab() === "live" ? " active" : "")} onClick={() => setTab("live")}>Live</button>
          <button class={"seg-btn" + (tab() === "load" ? " active" : "")} onClick={() => setTab("load")}>Load test</button>
          <button class={"seg-btn" + (tab() === "snapshot" ? " active" : "")} onClick={() => setTab("snapshot")}>Snapshot</button>
        </div>
      </div>
      <Show when={err()}><div class="errbar">{err()}</div></Show>
      <Show when={data()} fallback={<div class="empty">{err() ? "" : "Reading the collector…"}</div>}>
        <Show when={tab() === "live"}><LiveTab latest={latest} /></Show>
        <Show when={tab() === "load"}><LoadTab series={series} window={window_} /></Show>
        <Show when={tab() === "snapshot"}><SnapshotTab window={window_} version={() => health()?.version} /></Show>
      </Show>
    </>
  )
}
