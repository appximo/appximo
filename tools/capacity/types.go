package main

import "runtime"

func runtimeNumCPU() int { return runtime.NumCPU() }

// WindowVerdict is the engine's OWN reading of a load level — the Module C
// self-monitor summarising the window it just lived through. The sweep records
// it beside every point, so the ladder produces a saturation SEQUENCE (what
// gives first, what gives next) and not only a number.
type WindowVerdict struct {
	Dominant     string         `json:"dominant"`
	Owner        string         `json:"owner"`
	Reason       string         `json:"reason"`
	Distribution map[string]int `json:"distribution"`
	TrafficTicks int            `json:"traffic_ticks"`
	PeakRPS      float64        `json:"peak_rps"`
	PeakP99Ms    float64        `json:"peak_p99_ms"`
	Requests     int64          `json:"requests"`
	Shed         int64          `json:"shed_429_503"`
	Errors5xx    int64          `json:"errors_5xx"`
}

// EngineSample is what the endurance run watches for: the heap floor after GC,
// the RSS, the goroutine count and the pool — the four things whose SLOPE over
// hours says "leak" where any single value says nothing.
type EngineSample struct {
	HeapObjectsBytes  uint64 `json:"heap_objects_bytes"`
	RuntimeTotalBytes uint64 `json:"runtime_memory_total_bytes"`
	RSSBytes          uint64 `json:"rss_bytes"`
	Goroutines        int    `json:"goroutines"`
	GCCyclesTotal     uint64 `json:"gc_cycles_total"`
	PoolTotal         int32  `json:"pool_total_conns"`
	PoolMax           int32  `json:"pool_max_conns"`
	Attribution       string `json:"attribution"`
}
