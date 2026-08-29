//go:build linux

package observability

import (
	"bytes"
	"io"
	"os"
	"strconv"
)

// Layer 2 (process / cgroup v2, /proc/self fallback) and layer 3 (PSI).
//
// Every file is opened ONCE and re-read in place with pread(2) into a
// pre-allocated buffer (procfs/cgroupfs regenerate the content on each read
// from offset 0), so a tick does zero allocations for this layer. The cgroup
// path is re-resolved every cgroupRecheckTicks ticks: a process can be MOVED
// into another cgroup after boot (a `cpu.max` quota applied by an operator —
// or by this session's provocation of the cpu_throttled verdict), and the
// files must follow it.

const (
	cgroupMount = "/sys/fs/cgroup"
	procBufSize = 4096
)

// pfile is one re-readable pseudo-file with its own buffer.
type pfile struct {
	path string
	f    *os.File
	buf  []byte
	n    int
	ok   bool
}

func openP(path string) *pfile {
	p := &pfile{path: path, buf: make([]byte, procBufSize)}
	p.reopen()
	return p
}

func (p *pfile) reopen() {
	if p.f != nil {
		p.f.Close()
		p.f = nil
	}
	f, err := os.Open(p.path)
	if err != nil {
		p.ok = false
		return
	}
	p.f, p.ok = f, true
}

func (p *pfile) close() {
	if p.f != nil {
		p.f.Close()
		p.f = nil
	}
	p.ok = false
}

// read refreshes the buffer; returns false when the file is absent or empty.
func (p *pfile) read() bool {
	if !p.ok || p.f == nil {
		return false
	}
	total := 0
	for total < len(p.buf) {
		n, err := p.f.ReadAt(p.buf[total:], int64(total))
		total += n
		if err == io.EOF || n == 0 {
			break
		}
		if err != nil {
			p.n = 0
			return false
		}
	}
	p.n = total
	return total > 0
}

func (p *pfile) bytes() []byte {
	if p == nil || !p.ok {
		return nil
	}
	return p.buf[:p.n]
}

// parseInt64 reads a decimal (or "max" → -1) from the start of b, skipping
// leading spaces. Allocation-free.
func parseInt64(b []byte) int64 {
	b = bytes.TrimSpace(b)
	if len(b) == 0 {
		return 0
	}
	if bytes.Equal(b, []byte("max")) {
		return -1
	}
	n, err := strconv.ParseInt(string(b[:digitsLen(b)]), 10, 64)
	if err != nil {
		return 0
	}
	return n
}

func digitsLen(b []byte) int {
	i := 0
	if i < len(b) && (b[i] == '-' || b[i] == '+') {
		i++
	}
	for i < len(b) && b[i] >= '0' && b[i] <= '9' {
		i++
	}
	return i
}

// kvInt64 finds "key value" (space-separated, one per line) in a cpu.stat /
// /proc/self/status-style buffer and returns the value; -0 when absent.
func kvInt64(buf []byte, key string) (int64, bool) {
	k := []byte(key)
	for len(buf) > 0 {
		line := buf
		if i := bytes.IndexByte(buf, '\n'); i >= 0 {
			line, buf = buf[:i], buf[i+1:]
		} else {
			buf = nil
		}
		if len(line) > len(k) && bytes.HasPrefix(line, k) && (line[len(k)] == ' ' || line[len(k)] == ':') {
			return parseInt64(line[len(k)+1:]), true
		}
	}
	return 0, false
}

// psiParse fills a PSILine from a cpu.pressure-style buffer:
//
//	some avg10=0.00 avg60=0.00 avg300=0.00 total=0
//	full avg10=0.00 avg60=0.00 avg300=0.00 total=0
func psiParse(buf []byte, out *PSILine) bool {
	got := false
	for len(buf) > 0 {
		line := buf
		if i := bytes.IndexByte(buf, '\n'); i >= 0 {
			line, buf = buf[:i], buf[i+1:]
		} else {
			buf = nil
		}
		var a10, a60, a300 *float64
		var tot *int64
		switch {
		case bytes.HasPrefix(line, []byte("some ")):
			a10, a60, a300, tot = &out.SomeAvg10, &out.SomeAvg60, &out.SomeAvg300, &out.SomeTotal
		case bytes.HasPrefix(line, []byte("full ")):
			a10, a60, a300, tot = &out.FullAvg10, &out.FullAvg60, &out.FullAvg300, &out.FullTotal
		default:
			continue
		}
		got = true
		*a10 = psiField(line, "avg10=")
		*a60 = psiField(line, "avg60=")
		*a300 = psiField(line, "avg300=")
		if i := bytes.Index(line, []byte("total=")); i >= 0 {
			*tot = parseInt64(line[i+6:])
		}
	}
	return got
}

func psiField(line []byte, key string) float64 {
	i := bytes.Index(line, []byte(key))
	if i < 0 {
		return 0
	}
	v := line[i+len(key):]
	if j := bytes.IndexByte(v, ' '); j >= 0 {
		v = v[:j]
	}
	f, err := strconv.ParseFloat(string(v), 64)
	if err != nil {
		return 0
	}
	return f
}

// processReader owns the layer-2/3 files.
type processReader struct {
	cgroupPath                           string // relative ("/system.slice/x.service"); "" = none/unknown
	cgroupFile                           *pfile // /proc/self/cgroup, re-read to detect a move
	memCurrent, memMax, memPeak, memSwap *pfile
	cpuStat, cpuMax, pidsCur, pidsMax    *pfile
	cgPSI                                [3]*pfile // cpu, memory, io of the cgroup
	hostPSI                              [3]*pfile // /proc/pressure/{cpu,memory,io}
	status                               *pfile    // /proc/self/status
	ticks                                int
	prevUsage, prevThrottled, prevNrThr  int64
	primed                               bool
}

func newProcessReader() *processReader {
	r := &processReader{
		cgroupFile: openP("/proc/self/cgroup"),
		status:     openP("/proc/self/status"),
	}
	for i, n := range [3]string{"cpu", "memory", "io"} {
		r.hostPSI[i] = openP("/proc/pressure/" + n)
	}
	r.resolveCgroup()
	return r
}

// resolveCgroup reads /proc/self/cgroup ("0::/path") and (re)opens the
// cgroup files when the path changed. The comparison is done on the bytes
// (no string is built while the path is unchanged — the steady state); the
// one path string is built only on a change.
func (r *processReader) resolveCgroup() {
	var pathB []byte
	if r.cgroupFile.read() {
		b := r.cgroupFile.bytes()
		for len(b) > 0 {
			line := b
			if i := bytes.IndexByte(b, '\n'); i >= 0 {
				line, b = b[:i], b[i+1:]
			} else {
				b = nil
			}
			if bytes.HasPrefix(line, []byte("0::")) {
				pathB = bytes.TrimSpace(line[3:])
				break
			}
		}
	}
	if string(pathB) == r.cgroupPath && r.cpuStat != nil { // string(pathB) here does not allocate
		return
	}
	path := string(pathB)
	r.cgroupPath = path
	for _, p := range []*pfile{r.memCurrent, r.memMax, r.memPeak, r.memSwap, r.cpuStat, r.cpuMax, r.pidsCur, r.pidsMax, r.cgPSI[0], r.cgPSI[1], r.cgPSI[2]} {
		if p != nil {
			p.close()
		}
	}
	base := cgroupMount + path
	r.memCurrent = openP(base + "/memory.current")
	r.memMax = openP(base + "/memory.max")
	r.memPeak = openP(base + "/memory.peak")
	r.memSwap = openP(base + "/memory.swap.current")
	r.cpuStat = openP(base + "/cpu.stat")
	r.cpuMax = openP(base + "/cpu.max")
	r.pidsCur = openP(base + "/pids.current")
	r.pidsMax = openP(base + "/pids.max")
	r.cgPSI[0] = openP(base + "/cpu.pressure")
	r.cgPSI[1] = openP(base + "/memory.pressure")
	r.cgPSI[2] = openP(base + "/io.pressure")
	r.primed = false
}

func (r *processReader) read() {
	r.ticks++
	r.resolveCgroup()
}

// fill writes layers 2 and 3 for a tick of secs seconds.
func (r *processReader) fill(ps *ProcessStats, pr *PressureStats, secs float64) {
	r.read()
	ps.CgroupPath = r.cgroupPath
	ps.MemMaxBytes, ps.MemPeakBytes, ps.PidsMax, ps.CPUQuotaUsec = -1, -1, -1, -1
	// /proc/self/status: RSS, HWM, threads — always (the fallback AND the classic number).
	if r.status.read() {
		b := r.status.bytes()
		if v, ok := kvInt64(b, "VmRSS"); ok {
			ps.RSSBytes = v * 1024
		}
		if v, ok := kvInt64(b, "Threads"); ok {
			ps.Threads = v
		}
		if v, ok := kvInt64(b, "VmHWM"); ok {
			ps.MemPeakBytes = v * 1024
		}
	}
	cg := false
	if r.cpuStat.read() {
		b := r.cpuStat.bytes()
		usage, ok := kvInt64(b, "usage_usec")
		if ok {
			cg = true
			ps.CPUUsageUsec = usage
			ps.CPUUserUsec, _ = kvInt64(b, "user_usec")
			ps.CPUSystemUsec, _ = kvInt64(b, "system_usec")
			ps.CPUNrPeriods, _ = kvInt64(b, "nr_periods")
			ps.CPUNrThrottled, _ = kvInt64(b, "nr_throttled")
			ps.CPUThrottledUsec, _ = kvInt64(b, "throttled_usec")
			if r.primed {
				ps.CPUUsageDeltaUsec = max64(0, usage-r.prevUsage)
				ps.CPUThrottledDelta = max64(0, ps.CPUThrottledUsec-r.prevThrottled)
				ps.CPUNrThrottledDlt = max64(0, ps.CPUNrThrottled-r.prevNrThr)
			}
			r.prevUsage, r.prevThrottled, r.prevNrThr = usage, ps.CPUThrottledUsec, ps.CPUNrThrottled
			r.primed = true
		}
	}
	if cg {
		ps.Source = "cgroup"
		if r.memCurrent.read() {
			ps.MemCurrentBytes = parseInt64(r.memCurrent.bytes())
		}
		if r.memMax.read() {
			ps.MemMaxBytes = parseInt64(r.memMax.bytes())
		}
		if r.memPeak.read() {
			ps.MemPeakBytes = parseInt64(r.memPeak.bytes())
		}
		if r.memSwap.read() {
			ps.MemSwapBytes = parseInt64(r.memSwap.bytes())
		}
		if r.cpuMax.read() {
			b := bytes.TrimSpace(r.cpuMax.bytes())
			if i := bytes.IndexByte(b, ' '); i > 0 {
				ps.CPUQuotaUsec = parseInt64(b[:i])
				ps.CPUPeriodUsec = parseInt64(b[i+1:])
			}
		}
		if r.pidsCur.read() {
			ps.PidsCurrent = parseInt64(r.pidsCur.bytes())
		}
		if r.pidsMax.read() {
			ps.PidsMax = parseInt64(r.pidsMax.bytes())
		}
		ps.CgroupShared = ps.PidsCurrent > 0 && ps.Threads > 0 && ps.PidsCurrent > ps.Threads
		// A cgroup without the memory controller (a session scope, some
		// containers) reports 0 — fall back to RSS so the card is never blank.
		if ps.MemCurrentBytes == 0 {
			ps.MemCurrentBytes = ps.RSSBytes
		}
	} else {
		ps.Source = "proc"
		ps.MemCurrentBytes = ps.RSSBytes
	}
	// Layer 3 — PSI: the cgroup's own first, the host's second.
	pr.Source = "unavailable"
	if cg && r.cgPSI[0].read() && psiParse(r.cgPSI[0].bytes(), &pr.CPU) {
		pr.Source = "cgroup"
		if r.cgPSI[1].read() {
			psiParse(r.cgPSI[1].bytes(), &pr.Memory)
		}
		if r.cgPSI[2].read() {
			psiParse(r.cgPSI[2].bytes(), &pr.IO)
		}
	} else if r.hostPSI[0].read() && psiParse(r.hostPSI[0].bytes(), &pr.CPU) {
		pr.Source = "host"
		if r.hostPSI[1].read() {
			psiParse(r.hostPSI[1].bytes(), &pr.Memory)
		}
		if r.hostPSI[2].read() {
			psiParse(r.hostPSI[2].bytes(), &pr.IO)
		}
	}
	pr.CPUSomeAvg10, pr.MemSomeAvg10, pr.IOSomeAvg10 = pr.CPU.SomeAvg10, pr.Memory.SomeAvg10, pr.IO.SomeAvg10
	_ = secs
}

func max64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}
