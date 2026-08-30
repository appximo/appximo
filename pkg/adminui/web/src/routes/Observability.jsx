import { createResource, createSignal, createMemo, createEffect, onMount, onCleanup, Show, For } from "solid-js"
import { useNavigate } from "@solidjs/router"
import { DataTable } from "../components/DataTable"
import { Chart } from "../components/Chart"
import { Button } from "../components/ui"
import { api, ApiError } from "../lib/api"
import { logout } from "../lib/auth"
import { selectedTenant } from "../lib/tenantContext"
import { registerCommands } from "../lib/commands"
import { chartTheme, themeTick } from "../lib/theme"

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
      Time-series builds from per-minute snapshots — it appears after the first ~60 s
      of traffic. The current values are shown in the cards above.
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
function sloLabel(s) { return s === "critical" ? "Critical" : s === "warning" ? "Warning" : "Healthy" }

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
        <StatCard label="SLO status"><MetricBadge kind={sloKind(slo().status)} label={sloLabel(slo().status)} /></StatCard>
        <StatCard label="p50 latency" sub="uncached">{fmtMs(lat()?.p50_us)} <span class="unit">ms</span></StatCard>
        <StatCard label="p95 latency" sub="uncached">{fmtMs(lat()?.p95_us)} <span class="unit">ms</span></StatCard>
        <StatCard label="p99 latency" sub="uncached">{fmtMs(lat()?.p99_us)} <span class="unit">ms</span></StatCard>
        <StatCard label="Burn rate" sub="5-minute window">{(slo().burn_rate_5m ?? 0).toFixed(2)} <span class="unit">×</span></StatCard>
        <StatCard label="Requests" sub="sampled">{lat()?.count ?? 0}</StatCard>
        <StatCard label="Anomalies">{props.data().anomaly_count ?? 0}</StatCard>
      </div>

      <div class="card chart-card">
        <div class="chart-head"><h3>Latency over time</h3><span class="muted">p50 · p95 · ms</span></div>
        <Show when={history().length > 0} fallback={<ChartEmpty />}>
          <Chart option={latOpt()} height="240px" />
        </Show>
      </div>

      <div class="card chart-card">
        <div class="chart-head"><h3>SLO burn rate</h3><span class="muted">multi-window thresholds overlaid</span></div>
        <Show when={history().length > 0} fallback={<ChartEmpty />}>
          <Chart option={burnOpt()} height="200px" />
        </Show>
      </div>
    </div>
  )
}

function TracesTab(props) {
  const [mode, setMode] = createSignal("recent")
  const [sel, setSel] = createSignal(null)
  const traces = () => (mode() === "slow" ? props.data().slow_traces : props.data().recent_traces) || []

  const columns = [
    { accessorKey: "ts", header: "Time", size: 110, cell: (c) => <span class="secondary">{fmtClock(c.getValue())}</span> },
    {
      accessorKey: "route", header: "Endpoint", size: 280,
      cell: (c) => <span title={(c.row.original.method ? c.row.original.method + " " : "") + c.getValue()}>{c.row.original.method ? <span class="muted">{c.row.original.method} </span> : null}{c.getValue() || "—"}</span>,
    },
    { accessorKey: "total_us", header: "Duration", size: 100, meta: { align: "right" }, cell: (c) => <span class="num">{fmtMs(c.getValue())} ms</span> },
    { accessorKey: "status", header: "Status", size: 90, cell: (c) => <MetricBadge kind={statusKind(c.getValue())} label={String(c.getValue())} /> },
    { accessorKey: "trace_id", header: "Trace", size: 120, cell: (c) => <span class="muted" style={MONO} title={c.getValue()}>{shortId(c.getValue())}</span> },
    { id: "_open", header: "", size: 90, cell: (c) => <div class="cell-actions"><Button size="sm" variant="ghost" onClick={() => setSel(c.row.original)}>Waterfall</Button></div> },
  ]

  return (
    <div class="col" style={{ gap: "var(--space-4)" }}>
      <div class="row">
        <div class="seg">
          <button class={"seg-btn" + (mode() === "recent" ? " active" : "")} onClick={() => { setMode("recent"); setSel(null) }}>Recent</button>
          <button class={"seg-btn" + (mode() === "slow" ? " active" : "")} onClick={() => { setMode("slow"); setSel(null) }}>Slow / errors · 24h</button>
        </div>
        <span class="spacer" />
        <span class="muted" style={{ "font-size": "12px" }}>{traces().length} traces</span>
      </div>

      <DataTable
        data={traces()}
        columns={columns}
        emptyMessage={mode() === "slow" ? "No slow or errored requests in the last 24h." : "No recent requests — send traffic to /api/… to populate."}
        maxHeight="42vh"
      />

      <Show when={sel()} fallback={<div class="muted" style={{ "font-size": "13px", padding: "var(--space-3)" }}>Select a trace to see its span waterfall.</div>}>
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
        <span class="muted" style={{ "font-size": "12px" }}>· {fmtMs(props.trace.total_us)} ms total · {fmtClock(props.trace.ts)}</span>
        <span class="spacer" />
        <span class="muted" style={MONO} title={props.trace.trace_id}>{props.trace.trace_id}</span>
        <Button size="sm" variant="ghost" onClick={props.onClose}>Close</Button>
      </div>
      <Show when={props.trace.error_msg}><div class="errbar">{props.trace.error_msg}</div></Show>
      <Show when={props.trace.user_id || props.trace.role || props.trace.country}>
        <div class="muted" style={{ "font-size": "12px", "margin-bottom": "var(--space-3)" }}>
          {props.trace.user_id ? <span>user <span style={MONO}>{props.trace.user_id}</span> </span> : null}
          {props.trace.role ? <span>· role <strong>{props.trace.role}</strong> </span> : null}
          {props.trace.browser ? <span>· {props.trace.browser} / {props.trace.os} </span> : null}
          {props.trace.country ? <span>· {props.trace.country}</span> : null}
        </div>
      </Show>
      <Show when={props.trace.sql}>
        <details class="stack" open>
          <summary class="muted">Failed statement (the query the driver rejected)</summary>
          <pre class="sqlblock" style={MONO}>{props.trace.sql}</pre>
        </details>
      </Show>
      <Waterfall trace={props.trace} />
      <Show when={props.trace.full_url}>
        <div class="row" style={{ "margin-top": "var(--space-3)", gap: "var(--space-2)" }}>
          <Button size="sm" variant="ghost" onClick={() => navigator.clipboard.writeText(curlFor(props.trace))}>Copy as curl</Button>
          <span class="muted" style={{ "font-size": "12px" }}>Authorization is never stored — the curl carries <span style={MONO}>$TOKEN</span>.</span>
        </div>
        <Show when={props.trace.body}>
          <details class="stack"><summary class="muted">Request body (redacted, {props.trace.body.length} bytes)</summary><pre class="sqlblock" style={MONO}>{props.trace.body}</pre></details>
        </Show>
      </Show>
      <Show when={props.trace.stack && props.trace.stack.length}>
        <details class="stack">
          <summary class="muted">Stack ({props.trace.stack.length} frames)</summary>
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
    <Show when={spans().length > 0} fallback={<div class="muted" style={{ "font-size": "13px" }}>No spans recorded for this trace.</div>}>
      <div class="wf">
        <div class="wf-bars">
          <For each={spans()}>{(s, i) => (
            <button class={"wf-row" + (sel() === i() ? " active" : "")} onClick={() => setSel(i())}>
              <span class="wf-name" title={s.name}>{s.name}</span>
              <span class="wf-track">
                <span class={"wf-bar" + (s.err ? " fail" : (errored() ? " err" : ""))}
                  style={{ left: (offsets()[i()] / total()) * 100 + "%", width: Math.max((s.dur_us / total()) * 100, 0.6) + "%" }} />
              </span>
              <span class="wf-dur num">{fmtMs(s.dur_us)} ms{s.err ? <span class="wf-failtag" title="the error was recorded during this stage"> ✗ failed here</span> : null}</span>
            </button>
          )}</For>
        </div>
        <Show when={cur()}>
          <aside class="wf-detail">
            <div class="wf-detail-name">{cur().name}</div>
            <Row k="Duration" v={fmtMs(cur().dur_us) + " ms"} />
            <Row k="Raw" v={cur().dur_us + " µs"} />
            <Row k="% of total" v={((cur().dur_us / total()) * 100).toFixed(1) + "%"} />
            <Row k="Starts at" v={fmtMs(offsets()[sel()]) + " ms"} />
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
    { accessorKey: "route", header: "Endpoint", size: 200, cell: (c) => <span style={MONO} title={c.getValue()}>{c.getValue() || "—"}</span> },
    { accessorKey: "message", header: "Problem (normalized)", size: 380, cell: (c) => <span title={c.getValue()}>{c.getValue() || "(no message)"}</span> },
    { accessorKey: "count", header: "Events", size: 80, meta: { align: "right" }, cell: (c) => <span class="num">{c.getValue()}</span> },
    { id: "users", header: "Users", size: 70, meta: { align: "right" }, cell: (c) => <span class="num">{(c.row.original.users || []).length}</span> },
    { accessorKey: "first_seen", header: "Since", size: 110, cell: (c) => <span class="secondary">{fmtClock(c.getValue())}</span> },
    { accessorKey: "last_seen", header: "Last", size: 110, cell: (c) => <span class="secondary">{fmtClock(c.getValue())}</span> },
    { accessorKey: "sample_trace_id", header: "Trace", size: 110, cell: (c) => <span class="muted" style={MONO} title={c.getValue()}>{shortId(c.getValue())}</span> },
  ]

  const anomalyCols = [
    { accessorKey: "ts", header: "Time", size: 170, cell: (c) => <span class="secondary">{fmtClock(c.getValue())}</span> },
    { accessorKey: "latency_us", header: "Latency", size: 110, meta: { align: "right" }, cell: (c) => <span class="num">{fmtMs(c.getValue())} ms</span> },
    { accessorKey: "z_score", header: "Z-score", size: 100, meta: { align: "right" }, cell: (c) => <span class="num">{Number(c.getValue()).toFixed(2)}</span> },
    {
      id: "sev", header: "Severity", size: 130,
      cell: (c) => { const z = c.row.original.z_score; return <MetricBadge kind={z >= 6 ? "crit" : "warn"} label={z >= 6 ? "High" : "Elevated"} /> },
    },
  ]

  const errorCols = [
    { accessorKey: "message", header: "Error", size: 360, cell: (c) => <span title={c.getValue()}>{c.getValue()}</span> },
    { accessorKey: "count", header: "Count", size: 90, meta: { align: "right" }, cell: (c) => <span class="num">{c.getValue()}</span> },
    { accessorKey: "last_seen", header: "Last seen", size: 180, cell: (c) => <span class="secondary">{fmtMillis(c.getValue())}</span> },
  ]

  return (
    <div class="col" style={{ gap: "var(--space-4)" }}>
      <div class="statgrid">
        <StatCard label="SLO status"><MetricBadge kind={sloKind(slo().status)} label={sloLabel(slo().status)} /></StatCard>
        <StatCard label="Error ratio" sub="5-minute window">{(((slo().error_ratio_5m ?? 0) * 100).toFixed(2))} <span class="unit">%</span></StatCard>
        <StatCard label="Burn rate" sub="1-hour window">{(slo().burn_rate_1h ?? 0).toFixed(2)} <span class="unit">×</span></StatCard>
        <StatCard label="Anomalies" sub="since start">{props.data().anomaly_count ?? 0}</StatCard>
        <StatCard label="Problems · 24h" sub={groups().reduce((a, g) => a + (g.count || 0), 0) + " events"}>{groups().length}</StatCard>
      </div>

      <div class="col" style={{ gap: "var(--space-2)" }}>
        <h3>Problems <span class="muted" style={{ "font-weight": "400", "font-size": "13px" }}>(5xx grouped by route + normalized message + top frame — one row per defect, not per occurrence)</span></h3>
        <DataTable data={groups()} columns={groupCols} emptyMessage="No server errors in the last 24h." maxHeight="32vh" />
      </div>

      <div class="col" style={{ gap: "var(--space-2)" }}>
        <h3>Latency anomalies <span class="muted" style={{ "font-weight": "400", "font-size": "13px" }}>(z-score &gt; 3 over the per-tenant EWMA)</span></h3>
        <DataTable data={anomalies()} columns={anomalyCols} emptyMessage="No anomalies detected." maxHeight="32vh" />
      </div>

      <div class="col" style={{ gap: "var(--space-2)" }}>
        <h3>Error groups <span class="muted" style={{ "font-weight": "400", "font-size": "13px" }}>(deduplicated by fingerprint)</span></h3>
        <DataTable data={errors()} columns={errorCols} emptyMessage="No errors recorded." maxHeight="32vh" />
      </div>
    </div>
  )
}

// ── screen ───────────────────────────────────────────────────────────────────
export function Observability() {
  const navigate = useNavigate()
  const tid = () => selectedTenant()
  const [tab, setTab] = createSignal("metrics")
  const [range, setRange] = createSignal("24")
  const [live, setLive] = createSignal(false)

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
    { id: "obs:refresh", label: "Refresh observability", hint: "Observability", run: () => refetch() },
  ])))

  return (
    <>
      <div class="pagehead">
        <h1>Observability</h1>
        <Show when={tid()}><span class="muted">· {tid()}</span></Show>
        <span class="spacer" />
        <Show when={data.loading}><span class="muted" style={{ "font-size": "12px" }}>Loading…</span></Show>
        <Button variant="ghost" size="sm" onClick={() => refetch()}>Refresh</Button>
      </div>

      <Show when={tid()} fallback={<div class="empty">Select a tenant (top-right) to see its observability.</div>}>
        <Show when={data.error}><div class="errbar">Could not load observability: {data.error?.message}</div></Show>

        <div class="row obs-toolbar">
          <div class="seg">
            <For each={[["metrics", "Metrics"], ["traces", "Traces"], ["issues", "Issues"]]}>{(it) => (
              <button class={"seg-btn" + (tab() === it[0] ? " active" : "")} onClick={() => setTab(it[0])}>{it[1]}</button>
            )}</For>
          </div>
          <span class="spacer" />
          <Show when={tab() === "metrics"}>
            <label class="muted" style={{ "font-size": "12px" }} for="obs-range">Window</label>
            <select id="obs-range" value={range()} onChange={(e) => setRange(e.currentTarget.value)}>
              <option value="1">1h</option>
              <option value="6">6h</option>
              <option value="24">24h</option>
              <option value="168">7d</option>
            </select>
            <Button size="sm" variant={live() ? "primary" : "ghost"} onClick={() => setLive(!live())}
              ariaLabel={live() ? "Pause live refresh" : "Start live refresh"}>
              {live() ? "● Live" : "Live"}
            </Button>
          </Show>
        </div>

        <Show when={data()} fallback={<div class="empty">{data.loading ? "Loading…" : "No data yet."}</div>}>
          <Show when={tab() === "metrics"}><MetricsTab data={data} /></Show>
          <Show when={tab() === "traces"}><TracesTab data={data} /></Show>
          <Show when={tab() === "issues"}><IssuesTab data={data} /></Show>
        </Show>
      </Show>
    </>
  )
}
