import { createSignal, createMemo, createEffect, onMount, onCleanup, Show, For } from "solid-js"
import { useNavigate, useSearchParams } from "@solidjs/router"
import { Chart } from "../components/Chart"
import { Button, toast } from "../components/ui"
import { api, ApiError } from "../lib/api"
import { logout } from "../lib/auth"
import { registerCommands } from "../lib/commands"
import { chartTheme, themeTick } from "../lib/theme"
import { PageIntro } from "../shell/Shell"
import { t } from "../lib/i18n"

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
const KIND = { healthy: "ok", cpu_saturated: "warn", gc_pressure: "warn", cpu_throttled: "crit", pool_exhausted: "crit", db_bound: "crit", memory_pressure: "crit", lock_contention: "warn" }
const ATTR = Object.fromEntries(Object.keys(KIND).map((k) => [k, { label: t("r.a." + k), kind: KIND[k], who: t("r.w." + k) }]))
const attr = (a) => ATTR[a] || { label: a || "—", kind: "ok", who: "" }
const ownerLabel = (o) => (["appximo", "database", "host", "none"].includes(o) ? t("r.o." + o) : o)

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
      <div class="verdict-title">{props.title || (v()?.attribution === "healthy" ? t("r.noBottleneck") : t("r.bottleneck", { a: a().label.toLowerCase() }))}</div>
      <Show when={v()?.owner && v()?.owner !== "none"}>
        <div class="verdict-owner">{t("r.whose")} <strong>{ownerLabel(v().owner)}</strong> · {a().who}</div>
      </Show>
      <div class="verdict-reason">{v()?.reason || "—"}</div>
      <Show when={(v()?.also || []).length > 0}>
        <div class="verdict-also">{t("r.alsoFiring")} <For each={v().also}>{(x) => <span class="chip">{attr(x).label}</span>}</For></div>
      </Show>
      <Show when={(v()?.signals || []).length > 0}>
        <button class="verdict-toggle" onClick={() => setOpen(!open())}>{open() ? t("r.hide") : t("r.evidence", { f: fired().length, n: v().signals.length })}</button>
        <Show when={open()}>
          <table class="sigtable">
            <thead><tr><th>{t("r.th.signal")}</th><th class="num-h">{t("r.th.value")}</th><th class="num-h">{t("r.th.rule")}</th><th></th></tr></thead>
            <tbody>
              <For each={[...fired(), ...rest()]}>{(s) => (
                <tr class={s.fired ? "fired" : ""}>
                  <td style={MONO}>{s.name}</td>
                  <td class="num">{fmtSig(s)}</td>
                  <td class="num muted">{fmtThr(s)}</td>
                  <td>{s.fired ? <Badge kind="warn" label={t("r.fired")} /> : <span class="muted">—</span>}</td>
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
  const ho = () => s()?.host || {}
  const bk = () => ho().backup || {}
  const fmtAge = (sec) => (sec == null || sec <= 0 ? "—" : sec >= 172800 ? (sec / 86400).toFixed(1) + " d" : sec >= 7200 ? (sec / 3600).toFixed(1) + " h" : (sec / 60).toFixed(0) + " min")
  const bkKind = () => (!ho().enabled || !bk().dir ? "ok" : bk().alarm ? "crit" : bk().status === "ok" ? "ok" : "warn")
  const bkText = () => {
    if (!ho().enabled || !bk().dir) return t("r.notWatched")
    if (bk().status === "ok" && !bk().stale) return t("r.ok")
    if (bk().status === "ok" && bk().stale) return t("r.stale")
    if (bk().status === "failed") return t("r.failed")
    if (bk().status === "none") return bk().stale ? t("r.neverRan") : t("r.noRunYet")
    return t("r.emptyStatus")
  }
  const memMax = () => (pc().mem_max_bytes > 0 ? t("r.maxOf", { v: fmtMiB(pc().mem_max_bytes) }) : t("r.noLimit"))
  const quota = () => (pc().cpu_quota_usec > 0 && pc().cpu_period_usec > 0 ? t("r.quota", { v: (pc().cpu_quota_usec / pc().cpu_period_usec).toFixed(2) }) : t("r.noQuota"))
  return (
    <div class="col" style={{ gap: "var(--space-4)" }}>
      <VerdictBanner verdict={s()?.verdict} eyebrow={`${t("r.latestTick")} · ${fmtClock(s()?.ts)} · ${s()?.mode} ${(s()?.interval_ms / 1000).toFixed(0)} s`} />

      <h3 class="res-h">{t("r.requests")}</h3>
      <div class="statgrid">
        <Stat label={t("r.throughput")} sub={t("r.inTick", { n: fmtNum(rq().count) })}>{(rq().rps ?? 0).toFixed(0)} <span class="unit">rps</span></Stat>
        <Stat label={t("r.p50p95")} sub={t("r.msTick")}>{fmtMs(rq().latency_p50_ms)} <span class="unit">/ {fmtMs(rq().latency_p95_ms)}</span></Stat>
        <Stat label="p99" sub={t("r.max", { v: fmtMs(rq().latency_max_ms) })}>{fmtMs(rq().latency_p99_ms)} <span class="unit">ms</span></Stat>
        <Stat label={t("r.shed")} sub={t("r.shedSub")}>{fmtNum((rq().status_429 || 0) + (rq().status_503 || 0))} <span class="unit">/ {fmtNum(rq().errors_5xx)}</span></Stat>
      </div>

      <h3 class="res-h">{t("r.memory")} <span class="muted">· cgroup v2 {pc().source === "cgroup" ? "" : t("r.cgroupUnavail")}</span></h3>
      <div class="statgrid">
        <Stat label={t("r.rss")} sub={t("r.peak", { v: fmtMiB(pc().mem_peak_bytes) })}>{fmtMiB(pc().rss_bytes)} <span class="unit">MiB</span></Stat>
        <Stat label={t("r.cgroupMem")} sub={pc().cgroup_shared ? t("r.sharedCgroup") : memMax()}>{fmtMiB(pc().mem_current_bytes)} <span class="unit">MiB</span></Stat>
        <Stat label={t("r.liveHeap")} sub={t("r.goal", { v: fmtMiB(rt().heap_goal_bytes), g: rt().gogc_percent ?? "—" })}>{fmtMiB(rt().heap_objects_bytes)} <span class="unit">MiB</span></Stat>
        <Stat label={t("r.mapped")} sub={rt().gomemlimit_bytes && rt().gomemlimit_bytes < 9e18 ? t("r.gomemlimit", { v: fmtMiB(rt().gomemlimit_bytes) }) : t("r.gomemlimitUnset")}>{fmtMiB(rt().memory_total_bytes)} <span class="unit">MiB</span></Stat>
      </div>

      <h3 class="res-h">{t("r.cpu")} <span class="muted">· runtime/metrics + cpu.stat</span></h3>
      <div class="statgrid">
        <Stat label={t("r.busy")} sub={t("r.ofGomaxprocs", { g: rt().gomaxprocs ?? "—", q: quota() })}>{fmtPct(rt().cpu_busy_fraction)} <span class="unit">%</span></Stat>
        <Stat label={t("r.schedWait")} sub={t("r.schedWaitSub")}>{fmtMs((rt().sched_latency_p99_s || 0) * 1000)} <span class="unit">ms</span></Stat>
        <Stat label={t("r.throttled")} sub={t("r.throttledSub", { n: fmtNum(pc().cpu_nr_throttled_delta) })}>{fmtPct((pc().cpu_throttled_delta_usec || 0) / Math.max(1, (s()?.interval_ms || 1000) * 1000))} <span class="unit">%</span></Stat>
        <Stat label={t("r.processCpu")} sub={t("r.processCpuSub")}>{((pc().cpu_usage_usec || 0) / 1e6).toFixed(0)} <span class="unit">s</span></Stat>
      </div>

      <h3 class="res-h">{t("r.gc")}</h3>
      <div class="statgrid">
        <Stat label={t("r.gcShare")} sub={t("r.cycles", { n: rt().gc_cycles_delta ?? 0 })}>{fmtPct(rt().gc_cpu_fraction)} <span class="unit">%</span></Stat>
        <Stat label={t("r.gcPause")} sub={t("r.max", { v: fmtMs((rt().gc_pause_total_max_s || 0) * 1000) })}>{fmtMs((rt().gc_pause_total_p99_s || 0) * 1000)} <span class="unit">ms</span></Stat>
        <Stat label={t("r.goroutines")} sub={t("r.threads", { n: pc().threads ?? "—" })}>{fmtNum(rt().goroutines)}</Stat>
        <Stat label={t("r.mutex")} sub={t("r.mutexTotal", { v: (rt().mutex_wait_total_s || 0).toFixed(3) })}>{((rt().mutex_wait_delta_s || 0) * 1000).toFixed(2)} <span class="unit">ms/tick</span></Stat>
      </div>

      <h3 class="res-h">{t("r.database")} <span class="muted">· {t("r.poolClient")}</span></h3>
      <div class="statgrid">
        <Stat label={t("r.pool")} sub={db().saturated ? t("r.saturated") : t("r.idle", { i: db().idle_conns ?? 0, c: db().constructing_conns ?? 0 })}>{db().acquired_conns ?? 0} <span class="unit">/ {db().max_conns ?? 0}</span></Stat>
        <Stat label={t("r.emptyAcq")} sub={t("r.total", { n: fmtNum(db().empty_acquire_count) })}>{fmtNum(db().empty_acquire_delta)} <span class="unit">{t("r.thisTick")}</span></Stat>
        <Stat label={t("r.waited")} sub={t("r.thisTick")}>{fmtMs(db().empty_acquire_wait_delta_ms)} <span class="unit">ms</span></Stat>
        <Stat label={t("r.queryP99")} sub={t("r.queries", { n: fmtNum(db().query_count), v: fmtMs(db().query_latency_p50_ms) })}>{fmtMs(db().query_latency_p99_ms)} <span class="unit">ms</span></Stat>
      </div>
      <Show when={sv().observable} fallback={
        <div class="card res-note"><strong>{t("r.pgNotObs")}</strong> {sv().reason || t("r.pgRemote")}</div>
      }>
        <div class="statgrid">
          <Stat label={t("r.dbSize")} sub={sv().reason ? sv().reason : t("r.probed", { v: fmtClock(sv().probed_at) })}>{fmtMiB(sv().db_size_bytes)} <span class="unit">MiB</span></Stat>
          <Stat label={t("r.cacheHit")} sub={t("r.blocksRead", { n: fmtNum(sv().blks_read) })}>{fmtPct(sv().cache_hit_ratio)} <span class="unit">%</span></Stat>
          <Stat label={t("r.backends")} sub={t("r.backendsSub", { a: sv().active_conns ?? 0, w: sv().waiting ?? 0, i: sv().idle_in_transaction ?? 0 })}>{fmtNum(sv().total_backends)}</Stat>
          <Stat label={t("r.deadlocks")} sub={t("r.deadlocksSub", { v: fmtMiB(sv().temp_bytes), s: sv().pg_stat_statements ? t("r.pss") : t("r.noPss") })}>{fmtNum(sv().deadlocks)}</Stat>
        </div>
      </Show>

      <h3 class="res-h">{t("r.diskBackup")} <span class="muted">· {t("r.diskBackupSub")} · {t("r.diskBackupDoc")}</span></h3>
      <div class="statgrid">
        <For each={(ho().disks || []).filter((d) => !d.err)}>{(d) => (
          <Stat label={t("r.disk", { p: d.path })} sub={<><Badge kind={d.low ? "crit" : "ok"} label={d.low ? t("r.low") : t("r.ok")} /> {t("r.totalMiB", { v: fmtMiB(d.total_bytes) })}</>}>{(d.free_pct ?? 0).toFixed(1)} <span class="unit">{t("r.free", { v: fmtMiB(d.free_bytes) })}</span></Stat>
        )}</For>
        <Stat label={t("r.lastBackup")} sub={<><Badge kind={bkKind()} label={bkText()} /> {bk().dir ? t("r.floor", { d: bk().dir, v: fmtAge(bk().max_age_s) }) : t("r.installerSets")}<Show when={bk().line}><br /><span style={MONO}>{t("r.statusLine")} {bk().line}</span></Show></>}>{fmtAge(bk().age_s)} <span class="unit">{t("r.ago")}</span></Stat>
        <Show when={ho().enabled && (ho().disks || []).length === 0}>
          <div class="card res-note">{t("r.noStatfs")}</div>
        </Show>
      </div>

      <h3 class="res-h">{t("r.hostPressure")} <span class="muted">· {pr().source === "cgroup" ? t("r.psiCgroup") : pr().source === "host" ? t("r.psiHost") : t("r.psiUnavail")}</span></h3>
      <div class="statgrid">
        <For each={[["CPU", pr().cpu], [t("r.memory"), pr().memory], ["I/O", pr().io]]}>{([name, l]) => (
          <Stat label={t("r.stall", { n: name })} sub={t("r.stallSub", { a: (l?.some_avg60 ?? 0).toFixed(1), b: (l?.some_avg300 ?? 0).toFixed(1), c: (l?.full_avg10 ?? 0).toFixed(1) })}>{(l?.some_avg10 ?? 0).toFixed(1)} <span class="unit">% / 10 s</span></Stat>
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
        eyebrow={`${t("r.window")} · ${t("r.ticks", { n: series().length })} · ${series().length ? fmtClock(series()[0].ts) + " → " + fmtClock(series()[series().length - 1].ts) : "—"} · ${t("r.peakRps", { v: (win().peak_rps || 0).toFixed(0) })} · ${t("r.peakP99", { v: fmtMs(win().peak_p99_ms) })}`}
        title={win().dominant === "healthy" ? t("r.noBottleneckWin") : t("r.bottleneck", { a: attr(win().dominant).label.toLowerCase() })}
      />

      <div class="card res-timeline">
        <div class="chart-head"><h3>{t("r.attrPerTick")}</h3><span class="muted">{t("r.attrSub", { r: fmtNum(win().requests), s: fmtNum(win().shed_429_503), e: fmtNum(win().errors_5xx) })}</span></div>
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
          <VerdictBanner verdict={picked().verdict} eyebrow={`${t("r.tick")} ${fmtClock(picked().ts)} · ${(picked().request?.rps || 0).toFixed(0)} rps · p99 ${fmtMs(picked().request?.latency_p99_ms)} ms · pool ${picked().db_client?.acquired_conns}/${picked().db_client?.max_conns} · sched ${fmtMs((picked().runtime?.sched_latency_p99_s || 0) * 1000)} ms`} />
        </Show>
      </div>

      <Show when={series().length > 1} fallback={<div class="card chart-empty muted">{t("r.seriesEmpty")}</div>}>
        <div class="res-charts">
          <div class="card chart-card"><div class="chart-head"><h3>{t("r.chart.latency")}</h3><span class="muted">p99 · query p99 · p50</span></div><Chart option={latOpt()} height="200px" /></div>
          <div class="card chart-card"><div class="chart-head"><h3>{t("r.chart.throughput")}</h3><span class="muted">rps · shed</span></div><Chart option={rpsOpt()} height="200px" /></div>
          <div class="card chart-card"><div class="chart-head"><h3>{t("r.chart.cpu")}</h3><span class="muted">sched wait · GC share · throttled · mutex</span></div><Chart option={cpuOpt()} height="200px" /></div>
          <div class="card chart-card"><div class="chart-head"><h3>{t("r.chart.pool")}</h3><span class="muted">acquired of max · empty acquires</span></div><Chart option={poolOpt()} height="200px" /></div>
          <div class="card chart-card"><div class="chart-head"><h3>{t("r.chart.host")}</h3><span class="muted">PSI some avg10 · RSS</span></div><Chart option={psiOpt()} height="200px" /></div>
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
      toast(t("r.snap.downloaded"))
    } catch (e) { toast(t("r.snap.exportFailed", { e: e.message || e }), "err") }
    setBusy(false)
  }
  const copy = async () => {
    try { await navigator.clipboard.writeText(await api.resourcesSnapshot()); toast(t("r.snap.copied")) }
    catch (e) { toast(t("r.snap.copyFailed", { e: e.message || e }), "err") }
  }
  const load = (file) => {
    if (!file) return
    const r = new FileReader()
    r.onload = () => { try { setOther(JSON.parse(r.result)); setOtherName(file.name) } catch { toast(t("r.snap.notJson"), "err") } }
    r.readAsText(file)
  }
  const win = () => props.window() || {}
  const ow = () => other()?.window || {}
  const Row = (p) => <tr><td>{p.label}</td><td class="num">{p.a}</td><Show when={other()}><td class="num">{p.b}</td></Show></tr>
  return (
    <div class="col" style={{ gap: "var(--space-4)" }}>
      <div class="card res-note">
        <strong>{t("r.snap.export")}</strong> {t("r.snap.desc")}
        <div class="row" style={{ "margin-top": "var(--space-3)", "flex-wrap": "wrap" }}>
          <Button variant="primary" onClick={download} disabled={busy()}>{t("r.snap.download")}</Button>
          <Button variant="ghost" onClick={copy}>{t("r.snap.copy")}</Button>
          <label class="btn btn-ghost">{t("r.snap.load")} <input type="file" accept="application/json" style={{ display: "none" }} onChange={(e) => load(e.currentTarget.files?.[0])} /></label>
        </div>
      </div>
      <div class="card" style={{ padding: "var(--space-3)" }}>
        <div class="row" style={{ "margin-bottom": "var(--space-2)" }}><strong style={{ "font-size": "13px" }}>{t("r.snap.summary")}</strong><span class="spacer" /><span class="muted" style={{ "font-size": "12px" }}>{other() ? t("r.snap.vs", { n: otherName() }) : t("r.snap.thisRun")}</span></div>
        <table class="minitable">
          <thead><tr><th>{t("r.snap.metric")}</th><th class="num-h">{t("r.snap.now")}</th><Show when={other()}><th class="num-h">{t("r.snap.loaded")}</th></Show></tr></thead>
          <tbody>
            <Row label={t("r.snap.dominant")} a={attr(win().dominant).label} b={attr(ow().dominant).label} />
            <Row label={t("r.snap.ticks")} a={`${win().ticks ?? 0} (${win().traffic_ticks ?? 0})`} b={`${ow().ticks ?? 0} (${ow().traffic_ticks ?? 0})`} />
            <Row label={t("r.snap.requests")} a={fmtNum(win().requests)} b={fmtNum(ow().requests)} />
            <Row label={t("r.snap.peakRps")} a={(win().peak_rps || 0).toFixed(0)} b={(ow().peak_rps || 0).toFixed(0)} />
            <Row label={t("r.snap.peakP99")} a={fmtMs(win().peak_p99_ms)} b={fmtMs(ow().peak_p99_ms)} />
            <Row label={t("r.snap.shed")} a={fmtNum(win().shed_429_503)} b={fmtNum(ow().shed_429_503)} />
            <Row label="5xx" a={fmtNum(win().errors_5xx)} b={fmtNum(ow().errors_5xx)} />
            <Row label={t("r.snap.engine")} a={props.version() || "—"} b={other()?.engine?.version || "—"} />
          </tbody>
        </table>
        <Show when={other()}><div class="muted" style={{ "font-size": "12px", "margin-top": "var(--space-2)" }}>{t("r.snap.loadedVerdict", { r: ow().reason })}</div></Show>
      </div>
    </div>
  )
}

// ── the route ───────────────────────────────────────────────────────────────
export function Resources() {
  const navigate = useNavigate()
  // The tab lives in the URL (#/resources?tab=load) so a runbook can point at it.
  const [params, setParams] = useSearchParams()
  const tab = () => (["live", "load", "snapshot"].includes(params.tab) ? params.tab : "live")
  const setTab = (v) => setParams({ tab: v })
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
      { id: "res:live", label: t("r.cmd.live"), hint: t("r.title"), run: () => setTab("live") },
      { id: "res:load", label: t("r.cmd.load"), hint: t("r.title"), run: () => setTab("load") },
      { id: "res:snap", label: t("r.cmd.snap"), hint: t("r.title"), run: () => setTab("snapshot") },
    ]))
  })
  const latest = () => data()?.latest
  const series = () => data()?.series
  const window_ = () => data()?.window
  const col = () => data()?.collector || {}
  return (
    <>
      <div class="pagehead res-head">
        <h1>{t("r.title")}</h1>
        <span class="muted res-sub">{t("r.sub")}</span>
        <span class="spacer" />
        <Button variant="ghost" size="sm" onClick={() => navigate("/observability?tab=issues")}>{t("r.toObs")}</Button>
        <Show when={data()}><span class={"badge " + (col().live ? "badge-ok" : "badge-warn")}><span aria-hidden="true">{col().live ? "●" : "○"}</span><span>{col().mode} · {(col().interval_ms / 1000).toFixed(0)} s</span></span></Show>
        <div class="seg">
          <button class={"seg-btn" + (tab() === "live" ? " active" : "")} onClick={() => setTab("live")}>{t("r.tab.live")}</button>
          <button class={"seg-btn" + (tab() === "load" ? " active" : "")} onClick={() => setTab("load")}>{t("r.tab.load")}</button>
          <button class={"seg-btn" + (tab() === "snapshot" ? " active" : "")} onClick={() => setTab("snapshot")}>{t("r.tab.snapshot")}</button>
        </div>
      </div>
      <PageIntro>{t("intro.resources")}</PageIntro>
      <Show when={err()}><div class="errbar">{err()}</div></Show>
      <Show when={data()} fallback={<div class="empty">{err() ? "" : t("r.reading")}</div>}>
        <Show when={tab() === "live"}><LiveTab latest={latest} /></Show>
        <Show when={tab() === "load"}><LoadTab series={series} window={window_} /></Show>
        <Show when={tab() === "snapshot"}><SnapshotTab window={window_} version={() => health()?.version} /></Show>
      </Show>
    </>
  )
}
