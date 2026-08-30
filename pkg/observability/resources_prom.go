package observability

import (
	"github.com/prometheus/client_golang/prometheus"
)

// The collector feeds the EXISTING Prometheus pipeline (spec §5: reuse, do not
// rebuild): a prometheus.Collector that projects the LATEST snapshot into a
// handful of gauges on /metrics. It reads an atomic pointer — no lock, no
// extra I/O, nothing the scrape adds to the collector's own cost. Series are
// bounded (no per-tenant label: the resources are the process's).

type resourceCollectorProm struct {
	rc          *ResourceCollector
	attribution *prometheus.Desc
	gauges      []promGauge
	diskFree    *prometheus.Desc
	diskTotal   *prometheus.Desc
}

type promGauge struct {
	desc *prometheus.Desc
	get  func(*ResourceSnapshot) float64
}

// PromCollector returns the Prometheus projection of this collector.
func (c *ResourceCollector) PromCollector() prometheus.Collector {
	g := func(name, help string, get func(*ResourceSnapshot) float64) promGauge {
		return promGauge{desc: prometheus.NewDesc("appximo_selfmon_"+name, help, nil, nil), get: get}
	}
	return &resourceCollectorProm{
		rc:          c,
		attribution: prometheus.NewDesc("appximo_selfmon_attribution", "1 for the verdict of the latest tick (cpu_saturated|gc_pressure|cpu_throttled|pool_exhausted|db_bound|memory_pressure|lock_contention|healthy), 0 for the others", []string{"verdict"}, nil),
		diskFree:    prometheus.NewDesc("appximo_selfmon_disk_free_bytes", "bytes available to the app on a watched filesystem (statfs f_bavail)", []string{"path"}, nil),
		diskTotal:   prometheus.NewDesc("appximo_selfmon_disk_total_bytes", "size of a watched filesystem", []string{"path"}, nil),
		gauges: []promGauge{
			g("backup_age_seconds", "seconds since the last backup run wrote last-backup.status (-1 = none / not watched)", func(s *ResourceSnapshot) float64 {
				if !s.Host.Enabled || s.Host.Backup.Dir == "" || s.Host.Backup.LastAt == 0 {
					return -1
				}
				return s.Host.Backup.AgeS
			}),
			g("backup_ok", "1 = the last backup succeeded and is fresh; 0 = failed or stale; -1 = none yet / not watched", func(s *ResourceSnapshot) float64 {
				switch {
				case !s.Host.Enabled || s.Host.Backup.Dir == "":
					return -1
				case s.Host.Backup.Status == BackupOK && !s.Host.Backup.Stale:
					return 1
				case s.Host.Backup.Status == BackupNone && !s.Host.Backup.Stale:
					return -1
				}
				return 0
			}),
			g("rps", "requests per second in the latest tick", func(s *ResourceSnapshot) float64 { return s.Request.RPS }),
			g("request_p99_seconds", "request latency p99 of the latest tick", func(s *ResourceSnapshot) float64 { return s.Request.LatencyP99Ms / 1000 }),
			g("query_p99_seconds", "client-side query stage p99 of the latest tick", func(s *ResourceSnapshot) float64 { return s.DBClient.QueryLatencyP99Ms / 1000 }),
			g("sched_latency_p99_seconds", "/sched/latencies p99 of the latest tick — runnable goroutines waiting for a CPU", func(s *ResourceSnapshot) float64 { return s.Runtime.SchedLatencyP99S }),
			g("gc_pause_p99_seconds", "/sched/pauses/total/gc p99 of the latest tick", func(s *ResourceSnapshot) float64 { return s.Runtime.GCPauseTotalP99S }),
			g("gc_cpu_fraction", "GC CPU over busy CPU in the latest tick", func(s *ResourceSnapshot) float64 { return s.Runtime.GCCPUFraction }),
			g("goroutines", "live goroutines", func(s *ResourceSnapshot) float64 { return float64(s.Runtime.Goroutines) }),
			g("heap_objects_bytes", "live heap bytes", func(s *ResourceSnapshot) float64 { return float64(s.Runtime.HeapObjectsBytes) }),
			g("mutex_wait_seconds_total", "/sync/mutex/wait/total (cumulative)", func(s *ResourceSnapshot) float64 { return s.Runtime.MutexWaitTotalS }),
			g("cgroup_memory_current_bytes", "memory.current of the process cgroup (RSS when no cgroup)", func(s *ResourceSnapshot) float64 { return float64(s.Process.MemCurrentBytes) }),
			g("cgroup_memory_max_bytes", "memory.max (-1 = unlimited)", func(s *ResourceSnapshot) float64 { return float64(s.Process.MemMaxBytes) }),
			g("cgroup_cpu_throttled_seconds_total", "cpu.stat throttled_usec (cumulative)", func(s *ResourceSnapshot) float64 { return float64(s.Process.CPUThrottledUsec) / 1e6 }),
			g("cgroup_cpu_nr_throttled_total", "cpu.stat nr_throttled (cumulative)", func(s *ResourceSnapshot) float64 { return float64(s.Process.CPUNrThrottled) }),
			g("psi_cpu_some_avg10", "CPU PSI some avg10 (%), the cgroup's own when available", func(s *ResourceSnapshot) float64 { return s.Pressure.CPU.SomeAvg10 }),
			g("psi_memory_some_avg10", "memory PSI some avg10 (%)", func(s *ResourceSnapshot) float64 { return s.Pressure.Memory.SomeAvg10 }),
			g("psi_io_some_avg10", "IO PSI some avg10 (%)", func(s *ResourceSnapshot) float64 { return s.Pressure.IO.SomeAvg10 }),
			g("pool_acquired_conns", "pgxpool acquired connections", func(s *ResourceSnapshot) float64 { return float64(s.DBClient.AcquiredConns) }),
			g("pool_max_conns", "pgxpool max connections", func(s *ResourceSnapshot) float64 { return float64(s.DBClient.MaxConns) }),
			g("pool_empty_acquire_total", "pgxpool EmptyAcquireCount (cumulative)", func(s *ResourceSnapshot) float64 { return float64(s.DBClient.EmptyAcquireCount) }),
			g("pool_empty_acquire_wait_seconds_total", "pgxpool EmptyAcquireWaitTime (cumulative)", func(s *ResourceSnapshot) float64 { return s.DBClient.EmptyAcquireWaitMs / 1000 }),
		},
	}
}

func (p *resourceCollectorProm) Describe(ch chan<- *prometheus.Desc) {
	ch <- p.attribution
	ch <- p.diskFree
	ch <- p.diskTotal
	for _, g := range p.gauges {
		ch <- g.desc
	}
}

func (p *resourceCollectorProm) Collect(ch chan<- prometheus.Metric) {
	s := p.rc.latestRaw()
	if s == nil {
		return
	}
	for _, a := range Attributions {
		v := 0.0
		if a == s.Attribution {
			v = 1
		}
		ch <- prometheus.MustNewConstMetric(p.attribution, prometheus.GaugeValue, v, string(a))
	}
	for _, g := range p.gauges {
		ch <- prometheus.MustNewConstMetric(g.desc, prometheus.GaugeValue, g.get(s))
	}
	for i := 0; i < s.Host.Count; i++ {
		d := s.Host.Disks[i]
		if d.Err != "" {
			continue
		}
		ch <- prometheus.MustNewConstMetric(p.diskFree, prometheus.GaugeValue, float64(d.FreeBytes), d.Path)
		ch <- prometheus.MustNewConstMetric(p.diskTotal, prometheus.GaugeValue, float64(d.TotalBytes), d.Path)
	}
}
