package main

import (
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// CPU accounting exists because of the box this laboratory runs on: ONE vCPU,
// shared by the engine, PostgreSQL and the load generator itself. Any capacity
// measured that way is confounded — the generator eats part of the machine it
// is measuring — and the only honest answer is to MEASURE the share instead of
// hiding it.
//
// It also buys the second, generator-free estimate. The service demand law
// (Denning & Buzen, 1978) says the ceiling of a resource is
//
//	X_max = C / D,   D = CPU-seconds consumed by that resource per request
//
// so the engine's own CPU-seconds per completed request, divided into the
// CPUs a dedicated box would give it, is an UPPER BOUND that does not depend
// on how much CPU the generator stole. The two estimates bracket the truth:
// the USL fit from below (pessimistic, generator included), the service demand
// from above (optimistic, everything else assumed free).

// CPUReport is the CPU-seconds each participant burned during a run.
type CPUReport struct {
	WallS        float64 `json:"wall_s"`
	CPUs         int     `json:"cpus"`
	EngineS      float64 `json:"engine_cpu_s"`
	PostgresS    float64 `json:"postgres_cpu_s"`
	GeneratorS   float64 `json:"generator_cpu_s"`
	OtherS       float64 `json:"other_cpu_s"` // whole box minus the three above
	BoxS         float64 `json:"box_cpu_s"`
	EngineShare  float64 `json:"engine_share"`
	PGShare      float64 `json:"postgres_share"`
	GenShare     float64 `json:"generator_share"`
	IdleShare    float64 `json:"idle_share"`
	StealShare   float64 `json:"steal_share"`
	EngineDemand float64 `json:"engine_cpu_ms_per_request"` // D_engine
	PGDemand     float64 `json:"postgres_cpu_ms_per_request"`
	TotalDemand  float64 `json:"total_cpu_ms_per_request"` // engine + postgres (the app's true service demand)
}

// cpuSampler reads the counters that matter before and after a run.
type cpuSampler struct {
	enginePID int
	pgMatch   string // process comm to sum (e.g. "postgres") — the fallback
	pgCgroup  string // cpu.stat of PostgreSQL's cgroup — the authority
	selfPID   int
}

// pgCgroupOf resolves the cgroup cpu.stat of the process that owns pid. Summing
// PostgreSQL by process comm UNDERCOUNTS and can even go NEGATIVE across a
// window: backends and autovacuum workers come and go, and a process that
// exited takes its ticks out of the next sum. The cgroup counter is monotonic
// and includes everything the container ran, which is what the service-demand
// law needs.
func pgCgroupOf(pid int) string {
	b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/cgroup")
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(b), "\n") {
		f := strings.SplitN(line, ":", 3)
		if len(f) != 3 || f[0] != "0" {
			continue
		}
		p := "/sys/fs/cgroup" + f[2] + "/cpu.stat"
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}

// cgroupCPUSeconds reads usage_usec out of a cgroup v2 cpu.stat.
func cgroupCPUSeconds(path string) float64 {
	if path == "" {
		return 0
	}
	b, err := os.ReadFile(path) //nolint:gosec
	if err != nil {
		return 0
	}
	for _, line := range strings.Split(string(b), "\n") {
		if !strings.HasPrefix(line, "usage_usec ") {
			continue
		}
		v, _ := strconv.ParseFloat(strings.TrimSpace(strings.TrimPrefix(line, "usage_usec ")), 64)
		return v / 1e6
	}
	return 0
}

type cpuSnap struct {
	engine, pg, self           float64
	boxBusy, boxIdle, boxSteal float64
	wall                       float64
}

var clkTck = 100.0 // Linux USER_HZ; constant on every supported platform

func (s cpuSampler) snap(nowS float64) cpuSnap {
	c := cpuSnap{wall: nowS}
	c.engine = procCPU(s.enginePID)
	c.self = procCPU(s.selfPID)
	if s.pgCgroup != "" {
		c.pg = cgroupCPUSeconds(s.pgCgroup)
	} else {
		c.pg = sumCPUByComm(s.pgMatch)
	}
	c.boxBusy, c.boxIdle, c.boxSteal = statCPU()
	return c
}

func (s cpuSampler) report(a, b cpuSnap, completed int64, cpus int) CPUReport {
	r := CPUReport{WallS: b.wall - a.wall, CPUs: cpus}
	r.EngineS = b.engine - a.engine
	r.PostgresS = math.Max(0, b.pg-a.pg)
	r.GeneratorS = b.self - a.self
	busy := b.boxBusy - a.boxBusy
	idle := b.boxIdle - a.boxIdle
	steal := b.boxSteal - a.boxSteal
	r.BoxS = busy
	r.OtherS = busy - r.EngineS - r.PostgresS - r.GeneratorS
	if r.OtherS < 0 {
		r.OtherS = 0
	}
	if tot := busy + idle; tot > 0 {
		r.EngineShare = r.EngineS / tot
		r.PGShare = r.PostgresS / tot
		r.GenShare = r.GeneratorS / tot
		r.IdleShare = idle / tot
		r.StealShare = steal / tot
	}
	if completed > 0 {
		r.EngineDemand = r.EngineS / float64(completed) * 1000
		r.PGDemand = r.PostgresS / float64(completed) * 1000
		r.TotalDemand = r.EngineDemand + r.PGDemand
	}
	return r
}

// procCPU returns utime+stime of a pid, in seconds.
func procCPU(pid int) float64 {
	if pid <= 0 {
		return 0
	}
	b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return 0
	}
	return parseProcStatCPU(string(b))
}

func parseProcStatCPU(s string) float64 {
	// field 2 is comm in parentheses and may itself contain spaces
	i := strings.LastIndex(s, ")")
	if i < 0 {
		return 0
	}
	f := strings.Fields(s[i+1:])
	// after ")": state is f[0]; utime is field 14 overall => f[11], stime f[12]
	if len(f) < 13 {
		return 0
	}
	u, _ := strconv.ParseFloat(f[11], 64)
	k, _ := strconv.ParseFloat(f[12], 64)
	return (u + k) / clkTck
}

// sumCPUByComm sums utime+stime over every process whose comm equals name.
// Used for PostgreSQL, whose work is spread over one backend per connection.
func sumCPUByComm(name string) float64 {
	if name == "" {
		return 0
	}
	ents, err := os.ReadDir("/proc")
	if err != nil {
		return 0
	}
	total := 0.0
	for _, e := range ents {
		if !e.IsDir() {
			continue
		}
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		b, err := os.ReadFile(filepath.Join("/proc", e.Name(), "stat"))
		if err != nil {
			continue
		}
		s := string(b)
		l, r := strings.Index(s, "("), strings.LastIndex(s, ")")
		if l < 0 || r <= l {
			continue
		}
		if s[l+1:r] != name {
			continue
		}
		_ = pid
		total += parseProcStatCPU(s)
	}
	return total
}

// statCPU returns (busy, idle, steal) seconds since boot, whole box.
func statCPU() (busy, idle, steal float64) {
	b, err := os.ReadFile("/proc/stat")
	if err != nil {
		return 0, 0, 0
	}
	for _, line := range strings.Split(string(b), "\n") {
		if !strings.HasPrefix(line, "cpu ") {
			continue
		}
		f := strings.Fields(line)[1:]
		var v []float64
		for _, x := range f {
			n, _ := strconv.ParseFloat(x, 64)
			v = append(v, n/clkTck)
		}
		// user nice system idle iowait irq softirq steal guest guest_nice
		for i, n := range v {
			switch i {
			case 3:
				idle += n
			case 4:
				idle += n // iowait: the CPU was available to somebody
			case 7:
				steal = n
				busy += n
			default:
				if i < 8 {
					busy += n
				}
			}
		}
		return busy, idle, steal
	}
	return 0, 0, 0
}

// findPIDByListenPort resolves the process listening on a TCP port, so the
// sweep never has to be told the engine's pid by hand.
func findPIDByListenPort(port int) int {
	inodes := map[string]bool{}
	for _, f := range []string{"/proc/net/tcp", "/proc/net/tcp6"} {
		b, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(b), "\n")[1:] {
			fl := strings.Fields(line)
			if len(fl) < 10 || fl[3] != "0A" { // 0A = LISTEN
				continue
			}
			p := strings.Split(fl[1], ":")
			if len(p) != 2 {
				continue
			}
			n, err := strconv.ParseInt(p[1], 16, 32)
			if err != nil || int(n) != port {
				continue
			}
			inodes[fl[9]] = true
		}
	}
	if len(inodes) == 0 {
		return 0
	}
	ents, err := os.ReadDir("/proc")
	if err != nil {
		return 0
	}
	for _, e := range ents {
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		fds, err := os.ReadDir(filepath.Join("/proc", e.Name(), "fd"))
		if err != nil {
			continue
		}
		for _, fd := range fds {
			l, err := os.Readlink(filepath.Join("/proc", e.Name(), "fd", fd.Name()))
			if err != nil {
				continue
			}
			if strings.HasPrefix(l, "socket:[") && inodes[strings.TrimSuffix(strings.TrimPrefix(l, "socket:["), "]")] {
				return pid
			}
		}
	}
	return 0
}
