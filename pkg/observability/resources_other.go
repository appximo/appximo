//go:build !linux

package observability

// Layers 2 and 3 exist only on Linux (cgroup v2, /proc, PSI). Elsewhere the
// process section reports source "unavailable" and PSI "unavailable" — an
// honest answer the attribution rules treat as "no signal", never as healthy.

type processReader struct{}

func newProcessReader() *processReader { return &processReader{} }

func (r *processReader) read() {}

func (r *processReader) fill(ps *ProcessStats, pr *PressureStats, _ float64) {
	ps.Source = "unavailable"
	ps.MemMaxBytes, ps.MemPeakBytes, ps.PidsMax, ps.CPUQuotaUsec = -1, -1, -1, -1
	pr.Source = "unavailable"
}
