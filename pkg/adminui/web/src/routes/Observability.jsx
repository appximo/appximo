import { createResource, createSignal, createMemo, createEffect, onMount, onCleanup, Show, For } from "solid-js"
import { useNavigate, useSearchParams } from "@solidjs/router"
import { DataTable } from "../components/DataTable"
import { Chart } from "../components/Chart"
import { Button } from "../components/ui"
import { api, ApiError } from "../lib/api"
import { logout } from "../lib/auth"
import { selectedTenant } from "../lib/tenantContext"
import { registerCommands } from "../lib/commands"
import { chartTheme, themeTick } from "../lib/theme"
import { PageIntro } from "../shell/Shell"
import { t } from "../lib/i18n"

// Observability (ADMIN-UI-V2) — the visual face of the observability the engine
// already computes. It EXPOSES the existing /admin/observability/tenants/{id}
// payload (latency snapshot, SLO burn-rate, recent + persisted-slow traces with
// span breakdowns, error groups, and the z-score anomalies) — nothing is
// re-derived here. Platform admin picks the tenant via the topbar selector; the
// store is already tenant-scoped so there is no cross-tenant leak.

const MONO = { "font-family": "ui-monospace, SFMono-Regular, Menlo, monospace", "font-size": "11px" }
const ICON = { ok: "✓", warn: "▲", crit: "✕" }

// MetricBadge — DOUBLE CHANNEL status (colour + semantic icon + text), never colour
// alone (WCAG 1.4.1). Reuses the .badge tokens from the foundation.
function MetricBadge(props) {
  const k = () => (props.kind === "crit" ? "crit" : props.kind === "warn" ? "warn" : "ok")
  return (
    <span class={"badge badge-" + k()}>
      <span aria-hidden="true">{ICON[k()]}</span>
      <span>{props.label}</span>
    </span>
  )
}

function StatCard(props) {
  return (
    <div class="stat card">
      <div class="stat-label">{props.label}</div>
      <div class="stat-value">{props.children}</div>
      <Show when={props.sub}><div class="stat-sub muted">{props.sub}</div></Show>
    </div>
  )
}

function ChartEmpty() {
  return (
    <div class="chart-empty muted">
      {t("o.chartEmpty")}
    </div>
  )
}

// ── formatting helpers ──────────────────────────────────────────────────────
function fmtMs(us) {
  if (us == null) return "—"
  const m = us / 1000
  if (m >= 100) return m.toFixed(0)
  if (m >= 1) return m.toFixed(2)
  return m.toFixed(3)
}
function fmtClock(tsUs) { try { return new Date(tsUs / 1000).toLocaleTimeString() } catch { return "—" } }
function fmtMillis(ms) { try { return new Date(ms).toLocaleString() } catch { return "—" } }
function shortId(v) { const s = String(v || ""); return s.length > 12 ? s.slice(0, 10) + "…" : s }
function statusKind(code) { return code >= 500 ? "crit" : code >= 400 ? "warn" : "ok" }
function sloKind(s) { return s === "critical" ? "crit" : s === "warning" ? "warn" : "ok" }
function sloLabel(s) { return s === "critical" ? t("o.critical") : s === "warning" ? t("o.warning") : t("o.healthy") }

// ── ECharts option builders (data-ink high: no chartjunk, dashed light grid) ──
function latencyOption(history, t) {
  const p50 = history.map((h) => [h.ts * 1000, +(h.p50_us / 1000).toFixed(3)])
  const p95 = history.map((h) => [h.ts * 1000, +(h.p95_us / 1000).toFixed(3)])
  return {
    animation: false,
    grid: { left: 48, right: 16, top: 30, bottom: 26 },
    color: [t.brand, t.warn],
    legend: { data: ["p50", "p95"], right: 6, top: 2, icon: "roundRect", itemWidth: 10, itemHeight: 10, textStyle: { color: t.text, fontSize: 11 } },
    tooltip: { trigger: "axis", valueFormatter: (v) => (v == null ? "—" : v.toFixed(2) + " ms") },
    xAxis: { type: "time", axisLine: { lineStyle: { color: t.grid } }, axisTick: { show: false }, axisLabel: { color: t.muted, fontSize: 11, hideOverlap: true } },
    yAxis: { type: "value", name: "ms", nameTextStyle: { color: t.muted, fontSize: 10 }, splitLine: { lineStyle: { color: t.grid, type: "dashed" } }, axisLabel: { color: t.muted, fontSize: 11 } },
    series: [
      { name: "p50", type: "line", showSymbol: false, smooth: true, lineStyle: { width: 2 }, data: p50 },
      { name: "p95", type: "line", showSymbol: false, smooth: true, lineStyle: { width: 2 }, data: p95 },
    ],
  }
}

function burnOption(history, t) {
  const burn = history.map((h) => [h.ts * 1000, +(h.burn_rate || 0).toFixed(3)])
  return {
    animation: false,
    grid: { left: 48, right: 16, top: 18, bottom: 26 },
    color: [t.brand],
    tooltip: { trigger: "axis", valueFormatter: (v) => (v == null ? "—" : v.toFixed(2) + "×") },
    xAxis: { type: "time", axisLine: { lineStyle: { color: t.grid } }, axisTick: { show: false }, axisLabel: { color: t.muted, fontSize: 11, hideOverlap: true } },
    yAxis: { type: "value", name: "burn ×", nameTextStyle: { color: t.muted, fontSize: 10 }, splitLine: { lineStyle: { color: t.grid, type: "dashed" } }, axisLabel: { color: t.muted, fontSize: 11 } },
    series: [{
      name: "burn", type: "line", showSymbol: false, smooth: true, lineStyle: { width: 2 }, areaStyle: { opacity: 0.07 },
      data: burn,
      markLine: {
        silent: true, symbol: "none",
        data: [
          { yAxis: 6, lineStyle: { color: t.warn, type: "dashed", width: 1 }, label: { formatter: "warning 6×", color: t.warn, fontSize: 10, position: "insideEndTop" } },
          { yAxis: 14.4, lineStyle: { color: t.crit, type: "dashed", width: 1 }, label: { formatter: "critical 14.4×", color: t.crit, fontSize: 10, position: "insideEndTop" } },
        ],
      },
    }],
  }
}

// ── tabs ─────────────────────────────────────────────────────────────────────
function MetricsTab(props) {
  const lat = () => { const l = props.data().latency || {}; return l.uncached || l.cached || null }
  const slo = () => props.data().slo || {}
  const history = () => props.data().history || []
  const latOpt = createMemo(() => { themeTick(); return latencyOption(history(), chartTheme()) })
  const burnOpt = createMemo(() => { themeTick(); return burnOption(history(), chartTheme()) })


  return (
    <div class="col" style={{ gap: "var(--space-4)" }}>
      <div class="statgrid">
        <StatCard label={t("o.slo")}><MetricBadge kind={sloKind(slo().status)} label={sloLabel(slo().status)} /></StatCard>
        <StatCard label={t("o.p50")} sub={t("o.uncached")}>{fmtMs(lat()?.p50_us)} <span class="unit">ms</span></StatCard>
        <StatCard label={t("o.p95")} sub={t("o.uncached")}>{fmtMs(lat()?.p95_us)} <span class="unit">ms</span></StatCard>
        <StatCard label={t("o.p99")} sub={t("o.uncached")}>{fmtMs(lat()?.p99_us)} <span class="unit">ms</span></StatCard>
        <StatCard label={t("o.burn")} sub={t("o.burn5")}>{(slo().burn_rate_5m ?? 0).toFixed(2)} <span class="unit">×</span></StatCard>
        <StatCard label={t("o.requests")} sub={t("o.sampled")}>{lat()?.count ?? 0}</StatCard>
        <StatCard label={t("o.anomalies")}>{props.data().anomaly_count ?? 0}</StatCard>
      </div>

      <div class="card chart-card">
        <div class="chart-head"><h3>{t("o.latencyOverTime")}</h3><span class="muted">p50 · p95 · ms</span></div>
        <Show when={history().length > 0} fallback={<ChartEmpty />}>
          <Chart option={latOpt()} height="240px" />
        </Show>
      </div>

      <div class="card chart-card">
        <div class="chart-head"><h3>{t("o.sloBurn")}</h3><span class="muted">{t("o.thresholds")}</span></div>
        <Show when={history().length > 0} fallback={<ChartEmpty />}>
          <Chart option={burnOpt()} height="200px" />
        </Show>
      </div>
    </div>
  )
}

function TracesTab(props) {
  const [mode, setMode] = createSignal(props.initialMode || "recent")
  const [sel, setSel] = createSignal(props.initialTrace || null)
  const traces = () => (mode() === "slow" ? props.data().slow_traces : props.data().recent_traces) || []

  const columns = [
    { accessorKey: "ts", header: t("o.th.time"), size: 110, cell: (c) => <span class="secondary">{fmtClock(c.getValue())}</span> },
    {
      accessorKey: "route", header: t("o.th.endpoint"), size: 280,
      cell: (c) => <span title={(c.row.original.method ? c.row.original.method + " " : "") + c.getValue()}>{c.row.original.method ? <span class="muted">{c.row.original.method} </span> : null}{c.getValue() || "—"}</span>,
    },
    { accessorKey: "total_us", header: t("o.th.duration"), size: 100, meta: { align: "right" }, cell: (c) => <span class="num">{fmtMs(c.getValue())} ms</span> },
    { accessorKey: "status", header: t("o.th.status"), size: 90, cell: (c) => <MetricBadge kind={statusKind(c.getValue())} label={String(c.getValue())} /> },
    { accessorKey: "trace_id", header: t("o.th.trace"), size: 120, cell: (c) => <span class="muted" style={MONO} title={c.getValue()}>{shortId(c.getValue())}</span> },
    { id: "_open", header: "", size: 90, cell: (c) => <div class="cell-actions"><Button size="sm" variant="ghost" onClick={() => setSel(c.row.original)}>{t("o.waterfall")}</Button></div> },
  ]

  return (
    <div class="col" style={{ gap: "var(--space-4)" }}>
      <div class="row">
        <div class="seg">
          <button class={"seg-btn" + (mode() === "recent" ? " active" : "")} onClick={() => { setMode("recent"); setSel(null) }}>{t("o.recent")}</button>
          <button class={"seg-btn" + (mode() === "slow" ? " active" : "")} onClick={() => { setMode("slow"); setSel(null) }}>{t("o.slow")}</button>
        </div>
        <span class="spacer" />
        <span class="muted" style={{ "font-size": "12px" }}>{t("o.traces", { n: traces().length })}</span>
      </div>

      <DataTable
        data={traces()}
        columns={columns}
        emptyMessage={mode() === "slow" ? t("o.noSlow") : t("o.noRecent")}
        maxHeight="42vh"
      />

      <Show when={sel()} fallback={<div class="muted" style={{ "font-size": "13px", padding: "var(--space-3)" }}>{props.notice || t("o.selectTrace")}</div>}>
        <TraceDetail trace={sel()} onClose={() => setSel(null)} />
      </Show>
    </div>
  )
}

// curlFor rebuilds the request from what the trace persisted: method, full URL,
// the filtered headers (credentials were replaced by "[Filtered]" at capture —
// they become $TOKEN here) and the redacted body when APPXIMO_TRACE_BODY was on.
function curlFor(t) {
  const parts = ["curl", "-i", "-X", t.method || "GET"]
  const h = t.headers || {}
  for (const k of Object.keys(h)) {
    const lk = k.toLowerCase()
    if (lk === "content-length" || lk === "host" || lk === "accept-encoding") continue
    let v = h[k]
    if (lk === "authorization") v = "Bearer $TOKEN"
    else if (v === "[Filtered]") continue
    parts.push("-H", JSON.stringify(k + ": " + v))
  }
  if (t.body) parts.push("--data-binary", JSON.stringify(t.body))
  parts.push(JSON.stringify(t.full_url))
  return parts.join(" ")
}

function TraceDetail(props) {
  return (
    <div class="card trace-detail">
      <div class="row" style={{ "margin-bottom": "var(--space-3)" }}>
        <MetricBadge kind={statusKind(props.trace.status)} label={"HTTP " + props.trace.status} />
        <strong style={{ "font-size": "13px" }}>{props.trace.method ? props.trace.method + " " : ""}{props.trace.route}</strong>
        <span class="muted" style={{ "font-size": "12px" }}>· {fmtMs(props.trace.total_us)} ms {t("o.total")} · {fmtClock(props.trace.ts)}</span>
        <span class="spacer" />
        <span class="muted" style={MONO} title={props.trace.trace_id}>{props.trace.trace_id}</span>
        <Button size="sm" variant="ghost" onClick={props.onClose}>{t("c.close")}</Button>
      </div>
      <Show when={props.trace.error_msg}><div class="errbar">{props.trace.error_msg}</div></Show>
      <Show when={props.trace.user_id || props.trace.role || props.trace.country}>
        <div class="muted" style={{ "font-size": "12px", "margin-bottom": "var(--space-3)" }}>
          {props.trace.user_id ? <span>{t("o.user")} <span style={MONO}>{props.trace.user_id}</span> </span> : null}
          {props.trace.role ? <span>· {t("o.role")} <strong>{props.trace.role}</strong> </span> : null}
          {props.trace.browser ? <span>· {props.trace.browser} / {props.trace.os} </span> : null}
          {props.trace.country ? <span>· {props.trace.country}</span> : null}
        </div>
      </Show>
      <Show when={props.trace.sql}>
        <details class="stack" open>
          <summary class="muted">{t("o.failedStmt")}</summary>
          <pre class="sqlblock" style={MONO}>{props.trace.sql}</pre>
        </details>
      </Show>
      <Waterfall trace={props.trace} />
      <Show when={props.trace.full_url}>
        <div class="row" style={{ "margin-top": "var(--space-3)", gap: "var(--space-2)" }}>
          <Button size="sm" variant="ghost" onClick={() => navigator.clipboard.writeText(curlFor(props.trace))}>{t("o.copyCurl")}</Button>
          <span class="muted" style={{ "font-size": "12px" }}>{t("o.curlNote")}</span>
        </div>
        <Show when={props.trace.body}>
          <details class="stack"><summary class="muted">{t("o.body", { n: props.trace.body.length })}</summary><pre class="sqlblock" style={MONO}>{props.trace.body}</pre></details>
        </Show>
      </Show>
      <Show when={props.trace.stack && props.trace.stack.length}>
        <details class="stack">
          <summary class="muted">{t("o.stack", { n: props.trace.stack.length })}</summary>
          <For each={props.trace.stack}>{(f) => (
            <div class="stack-frame"><span>{f.function}</span> <span class="muted" style={MONO}>{f.file}:{f.line}</span></div>
          )}</For>
        </details>
      </Show>
    </div>
  )
}

// Waterfall — spans are SEQUENTIAL stages (each Mark records the time since the
// previous), so each bar's left offset is the cumulative duration before it and its
// width is its own duration, relative to the trace total. Honest to the data: the
// engine records flat sequential stages, not a nested tree.
function Waterfall(props) {
  const [sel, setSel] = createSignal(0)
  const spans = () => props.trace.spans || []
  const total = () => {
    const t = props.trace.total_us || spans().reduce((a, s) => a + (s.dur_us || 0), 0)
    return t > 0 ? t : 1
  }
  const offsets = createMemo(() => {
    let acc = 0
    return spans().map((s) => { const o = acc; acc += (s.dur_us || 0); return o })
  })
  const errored = () => (props.trace.status || 0) >= 500
  const cur = () => spans()[sel()]

  return (
    <Show when={spans().length > 0} fallback={<div class="muted" style={{ "font-size": "13px" }}>{t("o.noSpans")}</div>}>
      <div class="wf">
        <div class="wf-bars">
          <For each={spans()}>{(s, i) => (
            <button class={"wf-row" + (sel() === i() ? " active" : "")} onClick={() => setSel(i())}>
              <span class="wf-name" title={s.name}>{s.name}</span>
              <span class="wf-track">
                <span class={"wf-bar" + (s.err ? " fail" : (errored() ? " err" : ""))}
                  style={{ left: (offsets()[i()] / total()) * 100 + "%", width: Math.max((s.dur_us / total()) * 100, 0.6) + "%" }} />
              </span>
              <span class="wf-dur num">{fmtMs(s.dur_us)} ms{s.err ? <span class="wf-failtag" title={t("o.failedHereTitle")}> {t("o.failedHere")}</span> : null}</span>
            </button>
          )}</For>
        </div>
        <Show when={cur()}>
          <aside class="wf-detail">
            <div class="wf-detail-name">{cur().name}</div>
            <Row k={t("o.duration")} v={fmtMs(cur().dur_us) + " ms"} />
            <Row k={t("o.raw")} v={cur().dur_us + " µs"} />
            <Row k={t("o.pctTotal")} v={((cur().dur_us / total()) * 100).toFixed(1) + "%"} />
            <Row k={t("o.startsAt")} v={fmtMs(offsets()[sel()]) + " ms"} />
          </aside>
        </Show>
      </div>
    </Show>
  )
}

function Row(props) {
  return (
    <div class="row" style={{ "justify-content": "space-between", gap: "var(--space-3)" }}>
      <span class="muted" style={{ "font-size": "12px" }}>{props.k}</span>
      <span class="num" style={{ "font-size": "12px" }}>{props.v}</span>
    </div>
  )
}

function IssuesTab(props) {
  const slo = () => props.data().slo || {}
  const anomalies = () => props.data().anomalies || []
  const errors = () => props.data().errors || []
  const groups = () => props.data().error_groups || []
  const groupCols = [
    { accessorKey: "route", header: t("o.th.endpoint"), size: 200, cell: (c) => <span style={MONO} title={c.getValue()}>{c.getValue() || "—"}</span> },
    { accessorKey: "message", header: t("o.th.problem"), size: 380, cell: (c) => <span title={c.getValue()}>{c.getValue() || "(no message)"}</span> },
    { accessorKey: "count", header: t("o.th.events"), size: 80, meta: { align: "right" }, cell: (c) => <span class="num">{c.getValue()}</span> },
    { id: "users", header: t("o.th.users"), size: 70, meta: { align: "right" }, cell: (c) => <span class="num">{(c.row.original.users || []).length}</span> },
    { accessorKey: "first_seen", header: t("o.th.since"), size: 110, cell: (c) => <span class="secondary">{fmtClock(c.getValue())}</span> },
    { accessorKey: "last_seen", header: t("o.th.last"), size: 110, cell: (c) => <span class="secondary">{fmtClock(c.getValue())}</span> },
    // The sample trace is a LINK into Traces (MANUAL-OPERACION-S1): a problem
    // row used to end in an inert id the reader had to look up by hand.
    { accessorKey: "sample_trace_id", header: t("o.th.trace"), size: 110, cell: (c) => <button class="linklike" style={MONO} title={t("o.openTrace")} onClick={() => props.openTrace(c.getValue())}>{shortId(c.getValue())} ↗</button> },
  ]

  const anomalyCols = [
    { accessorKey: "ts", header: t("o.th.time"), size: 170, cell: (c) => <span class="secondary">{fmtClock(c.getValue())}</span> },
    { accessorKey: "latency_us", header: t("o.th.latency"), size: 110, meta: { align: "right" }, cell: (c) => <span class="num">{fmtMs(c.getValue())} ms</span> },
    { accessorKey: "z_score", header: t("o.th.z"), size: 100, meta: { align: "right" }, cell: (c) => <span class="num">{Number(c.getValue()).toFixed(2)}</span> },
    {
      id: "sev", header: t("o.th.severity"), size: 130,
      cell: (c) => { const z = c.row.original.z_score; return <MetricBadge kind={z >= 6 ? "crit" : "warn"} label={z >= 6 ? t("o.high") : t("o.elevated")} /> },
    },
  ]

  const errorCols = [
    { accessorKey: "message", header: t("o.th.error"), size: 360, cell: (c) => <span title={c.getValue()}>{c.getValue()}</span> },
    { accessorKey: "count", header: t("o.th.count"), size: 90, meta: { align: "right" }, cell: (c) => <span class="num">{c.getValue()}</span> },
    { accessorKey: "last_seen", header: t("o.th.lastSeen"), size: 180, cell: (c) => <span class="secondary">{fmtMillis(c.getValue())}</span> },
  ]

  return (
    <div class="col" style={{ gap: "var(--space-4)" }}>
      <div class="statgrid">
        <StatCard label={t("o.slo")}><MetricBadge kind={sloKind(slo().status)} label={sloLabel(slo().status)} /></StatCard>
        <StatCard label={t("o.errRatio")} sub={t("o.burn5")}>{(((slo().error_ratio_5m ?? 0) * 100).toFixed(2))} <span class="unit">%</span></StatCard>
        <StatCard label={t("o.burn")} sub={t("o.burn1h")}>{(slo().burn_rate_1h ?? 0).toFixed(2)} <span class="unit">×</span></StatCard>
        <StatCard label={t("o.anomalies")} sub={t("o.sinceStart")}>{props.data().anomaly_count ?? 0}</StatCard>
        <StatCard label={t("o.problems24")} sub={t("o.events", { n: groups().reduce((a, g) => a + (g.count || 0), 0) })}>{groups().length}</StatCard>
      </div>

      <div class="col" style={{ gap: "var(--space-2)" }}>
        <h3>{t("o.problems")} <span class="muted" style={{ "font-weight": "400", "font-size": "13px" }}>{t("o.problemsSub")}</span></h3>
        <DataTable data={groups()} columns={groupCols} emptyMessage={t("o.noProblems")} maxHeight="32vh" />
      </div>

      <div class="col" style={{ gap: "var(--space-2)" }}>
        <h3>{t("o.anomaliesH")} <span class="muted" style={{ "font-weight": "400", "font-size": "13px" }}>{t("o.anomaliesSub")}</span></h3>
        <DataTable data={anomalies()} columns={anomalyCols} emptyMessage={t("o.noAnomalies")} maxHeight="32vh" />
      </div>

      <div class="col" style={{ gap: "var(--space-2)" }}>
        <h3>{t("o.errorsH")} <span class="muted" style={{ "font-weight": "400", "font-size": "13px" }}>{t("o.errorsSub")}</span></h3>
        <DataTable data={errors()} columns={errorCols} emptyMessage={t("o.noErrors")} maxHeight="32vh" />
      </div>
    </div>
  )
}

// ── screen ───────────────────────────────────────────────────────────────────
export function Observability() {
  const navigate = useNavigate()
  const tid = () => selectedTenant()
  // The tab lives in the URL (#/observability?tab=issues) so a runbook or a
  // drill can point at the exact screen (MANUAL-OPERACION-S1).
  const [params, setParams] = useSearchParams()
  const tab = () => (["metrics", "traces", "issues"].includes(params.tab) ? params.tab : "metrics")
  const setTab = (v) => setParams({ tab: v })
  const [range, setRange] = createSignal("24")
  const [live, setLive] = createSignal(false)
  // Issues → Traces: the sample trace of a problem group opens its waterfall.
  const [traceOpen, setTraceOpen] = createSignal(null) // { trace, mode } | { notice }
  const openTrace = (id) => {
    const d = data() || {}
    const inRecent = (d.recent_traces || []).find((x) => x.trace_id === id)
    const inSlow = (d.slow_traces || []).find((x) => x.trace_id === id)
    if (inRecent) setTraceOpen({ trace: inRecent, mode: "recent" })
    else if (inSlow) setTraceOpen({ trace: inSlow, mode: "slow" })
    else setTraceOpen({ notice: t("o.traceGone") })
    setTab("traces")
  }

  const onAuthErr = (ex) => {
    if (ex instanceof ApiError && (ex.status === 401 || ex.status === 403)) { logout(); navigate("/login") }
  }

  const source = () => (tid() ? { id: tid(), hours: range() } : null)
  const [data, { refetch }] = createResource(source, async (s) => {
    try { return await api.observability(s.id, { history: s.hours, traces: "slow" }) }
    catch (ex) { onAuthErr(ex); throw ex }
  })

  // Live auto-refresh — polling (true streaming SSE for metrics is a V2.1 increment;
  // the obs API is a JSON snapshot, not a stream). Limited to the Metrics tab so the
  // trace/anomaly tables never reorder under the user, and so charts update IN PLACE
  // (Chart.setOption redraws only the canvas). Off by default.
  createEffect(() => {
    if (!live() || tab() !== "metrics" || !tid()) return
    const iv = setInterval(() => refetch(), 5000)
    onCleanup(() => clearInterval(iv))
  })

  onMount(() => onCleanup(registerCommands([
    { id: "obs:refresh", label: t("o.cmd.refresh"), hint: t("o.title"), run: () => refetch() },
  ])))

  return (
    <>
      <div class="pagehead">
        <h1>{t("o.title")}</h1>
        <Show when={tid()}><span class="muted">· {tid()}</span></Show>
        <span class="spacer" />
        <Show when={data.loading}><span class="muted" style={{ "font-size": "12px" }}>{t("c.loading")}</span></Show>
        <Button variant="ghost" size="sm" onClick={() => navigate("/resources")}>{t("o.toResources")}</Button>
        <Button variant="ghost" size="sm" onClick={() => refetch()}>{t("c.refresh")}</Button>
      </div>
      <PageIntro>{t("intro.observability")}</PageIntro>

      <Show when={tid()} fallback={<div class="empty">{t("o.select")}</div>}>
        <Show when={data.error}><div class="errbar">{t("o.couldNotLoad", { e: data.error?.message })}</div></Show>

        <div class="row obs-toolbar">
          <div class="seg">
            <For each={[["metrics", t("o.tab.metrics")], ["traces", t("o.tab.traces")], ["issues", t("o.tab.issues")]]}>{(it) => (
              <button class={"seg-btn" + (tab() === it[0] ? " active" : "")} onClick={() => setTab(it[0])}>{it[1]}</button>
            )}</For>
          </div>
          <span class="spacer" />
          <Show when={tab() === "metrics"}>
            <label class="muted" style={{ "font-size": "12px" }} for="obs-range">{t("o.window")}</label>
            <select id="obs-range" value={range()} onChange={(e) => setRange(e.currentTarget.value)}>
              <option value="1">1h</option>
              <option value="6">6h</option>
              <option value="24">24h</option>
              <option value="168">7d</option>
            </select>
            <Button size="sm" variant={live() ? "primary" : "ghost"} onClick={() => setLive(!live())}
              ariaLabel={live() ? t("o.livePause") : t("o.liveStart")}>
              {live() ? t("o.liveOn") : t("o.live")}
            </Button>
          </Show>
        </div>

        <Show when={data()} fallback={<div class="empty">{data.loading ? t("c.loading") : t("o.noData")}</div>}>
          <Show when={tab() === "metrics"}><MetricsTab data={data} /></Show>
          <Show when={tab() === "traces"}><TracesTab data={data} initialTrace={traceOpen()?.trace} initialMode={traceOpen()?.mode} notice={traceOpen()?.notice} /></Show>
          <Show when={tab() === "issues"}><IssuesTab data={data} openTrace={openTrace} /></Show>
        </Show>
      </Show>
    </>
  )
}
