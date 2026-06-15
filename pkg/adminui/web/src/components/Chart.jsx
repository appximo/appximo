import { onMount, onCleanup, createEffect } from "solid-js"
// Tree-shaken ECharts: only the pieces the panel uses, so the bundle stays lean
// (same minimal-dependency ethos as the engine). Canvas renderer — no inline SVG,
// works under the panel's strict CSP (script-src 'self').
import * as echarts from "echarts/core"
import { LineChart, BarChart } from "echarts/charts"
import {
  GridComponent, TooltipComponent, LegendComponent, MarkLineComponent,
} from "echarts/components"
import { CanvasRenderer } from "echarts/renderers"

echarts.use([
  LineChart, BarChart,
  GridComponent, TooltipComponent, LegendComponent, MarkLineComponent,
  CanvasRenderer,
])

// Chart — a thin ECharts host. Data-ink high: the caller's option carries the
// (theme-resolved) colors and we add no chartjunk here. Reactive to props.option:
// it updates IN PLACE via setOption (notMerge) so a live refresh redraws only the
// canvas — no page reflow, no row jumps. Resizes with its container; disposes on
// cleanup.
export function Chart(props) {
  let el
  let chart

  onMount(() => {
    chart = echarts.init(el, null, { renderer: "canvas" })
    if (props.option) chart.setOption(props.option)
    const ro = new ResizeObserver(() => chart && chart.resize())
    ro.observe(el)
    onCleanup(() => { ro.disconnect(); chart && chart.dispose() })
  })

  // Rebuild whenever the option changes (data refresh or theme flip).
  createEffect(() => {
    const opt = props.option
    if (chart && opt) chart.setOption(opt, { notMerge: true, lazyUpdate: true })
  })

  return <div ref={el} class="chart" style={{ height: props.height || "260px", width: "100%" }} />
}
