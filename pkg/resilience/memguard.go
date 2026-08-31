package resilience

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// MemoryGuard — the minimal, honest write-admission guard (MIGRACION-CONFIANZA-S1,
// D-ter).
//
// WHAT IT IS NOT: capacity. It does not make the engine "hold" a bulk load on a
// small box. It makes the engine STOP ACCEPTING NEW WRITES when the HOST is about
// to run out of memory, answering a 503 that says why, instead of accepting until
// the kernel's OOM killer takes PostgreSQL — and with it every app that shares
// that PostgreSQL. Measured in the field (a Symfony migration, 46k rows, 957 MiB
// box, no swap, five apps on one Postgres): the engine kept accepting writes
// until `postgresql@14-main.service: Failed with result 'oom-kill'` and all five
// apps went down. The audit before this guard: the engine had NO notion of host
// memory pressure anywhere — GOMEMLIMIT bounds its OWN heap (which was never the
// problem: the memory that grew was Postgres's backends), the pool is a fixed
// 10 connections, the rate limiter counts requests, not bytes.
//
// WHAT IT MEASURES — and why not MemAvailable alone: on a box that runs
// PostgreSQL, `shared_buffers` shows up as Cached but is NOT reclaimable, so
// MemAvailable at rest sits at a few tens of MiB however much RAM the box has. A
// guard on MemAvailable alone would trip permanently and be switched off on day
// one. The signal is MemAvailable + SwapFree: what the kernel can still hand out
// before it has to kill something. (On a box with NO swap the two coincide — and
// the installer now warns loudly about exactly that box, scripts/install.sh.)
//
// COST: one atomic load per write request; /proc/meminfo is read at most once
// per second, by the first writer to notice the sample is stale (others keep the
// previous sample — never a stampede, never a lock on the hot path). Reads never
// consult it: a saturated host still serves what it can.
//
// KNOBS: APPXIMO_MEMORY_GUARD_MIN_MB — the floor of MemAvailable + SwapFree in
// MiB under which writes are refused. Default: max(32, 2 % of MemTotal) MiB —
// deliberately LOW, so it fires only when the kernel is genuinely about to run
// out, never as a permanent false positive on a busy but healthy box. `0`
// disables the guard. A non-integer value refuses to boot (a safety knob never
// falls back silently — the ENG-47 rule).
type MemoryGuard struct {
	minBytes   int64
	meminfo    string
	interval   time.Duration
	sampledAt  atomic.Int64 // unix nanos of the last sample
	available  atomic.Int64 // MemAvailable + SwapFree, bytes, last sample
	sampling   atomic.Bool  // one goroutine samples at a time; others use the old value
	mu         sync.Mutex   // guards lastRefuse (the rate-limited log)
	nowFn      func() time.Time
	lastRefuse time.Time
	// GraphQLMutation decides whether a POST /graphql body is a MUTATION
	// (ENG-60, DEPLOY-FLOTA-S1). Every GraphQL request is a POST to one path,
	// so a verb-keyed guard refused GraphQL READS under memory pressure while
	// the equivalent REST read kept flowing (CAOS-S1 D5). The classifier runs
	// ONLY once the guard has already decided to refuse — the hot path pays
	// nothing for it, and a healthy host never parses a body here. nil keeps
	// the old behavior (every /graphql POST is a write).
	GraphQLMutation func(body []byte) bool
}

// graphqlBodyCap mirrors the GraphQL handler's own body cap (1 MiB): a
// refused request is read up to that much and no more.
const graphqlBodyCap = 1 << 20

// MemoryGuardEnvVar is the operator knob (MiB floor; 0 disables).
const MemoryGuardEnvVar = "APPXIMO_MEMORY_GUARD_MIN_MB"

// NewMemoryGuardFromEnv builds the guard from APPXIMO_MEMORY_GUARD_MIN_MB and
// /proc/meminfo. It returns (nil, nil) when disabled (0, or a host without
// /proc/meminfo) and an error for a value that is not a non-negative integer.
func NewMemoryGuardFromEnv() (*MemoryGuard, error) {
	raw := strings.TrimSpace(os.Getenv(MemoryGuardEnvVar))
	if _, err := os.Stat("/proc/meminfo"); err != nil {
		if raw != "" && raw != "0" {
			return nil, fmt.Errorf("%s=%q but /proc/meminfo is not readable on this host — the guard cannot measure anything here; unset it", MemoryGuardEnvVar, raw)
		}
		return nil, nil
	}
	var minMB int64
	if raw == "" {
		total, _, err := readMeminfo("/proc/meminfo")
		if err != nil {
			return nil, nil // cannot measure → no guard (never a boot failure by default)
		}
		minMB = total / (1 << 20) * 2 / 100
		if minMB < 32 {
			minMB = 32
		}
	} else {
		n, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || n < 0 {
			return nil, fmt.Errorf("%s=%q is not a non-negative integer (MiB floor of MemAvailable+SwapFree under which writes answer 503; 0 disables)", MemoryGuardEnvVar, raw)
		}
		minMB = n
	}
	if minMB == 0 {
		return nil, nil
	}
	return NewMemoryGuard(minMB<<20, "/proc/meminfo", time.Second), nil
}

// NewMemoryGuard is the testable constructor: floor in bytes, the meminfo path,
// and the minimum interval between two samples.
func NewMemoryGuard(minBytes int64, meminfoPath string, interval time.Duration) *MemoryGuard {
	g := &MemoryGuard{minBytes: minBytes, meminfo: meminfoPath, interval: interval, nowFn: time.Now}
	g.available.Store(-1) // unknown until the first sample
	return g
}

// MinBytes reports the configured floor.
func (g *MemoryGuard) MinBytes() int64 { return g.minBytes }

// Sample reads MemAvailable + SwapFree now and returns it (bytes).
func (g *MemoryGuard) Sample() (int64, error) {
	_, avail, err := readMeminfo(g.meminfo)
	if err != nil {
		return 0, err
	}
	g.available.Store(avail)
	g.sampledAt.Store(g.nowFn().UnixNano())
	return avail, nil
}

// available returns the freshest known MemAvailable+SwapFree, refreshing at most
// once per interval, by at most one goroutine at a time. -1 = unknown (never
// refuses: an unreadable meminfo must not turn into a 503).
func (g *MemoryGuard) current() int64 {
	now := g.nowFn().UnixNano()
	stale := g.available.Load() < 0 || now-g.sampledAt.Load() >= int64(g.interval)
	if stale && g.sampling.CompareAndSwap(false, true) {
		defer g.sampling.Store(false)
		if _, err := g.Sample(); err != nil {
			g.available.Store(-1)
			g.sampledAt.Store(now)
		}
	}
	return g.available.Load()
}

// Allow reports whether a write may proceed, with the measured value.
func (g *MemoryGuard) Allow() (ok bool, availableBytes int64) {
	a := g.current()
	if a < 0 {
		return true, a
	}
	return a >= g.minBytes, a
}

// isWrite: the guard covers the data-plane write verbs only. Reads keep flowing
// on a saturated host; a health probe or a login is not a bulk load.
func isGuardedWrite(r *http.Request) bool {
	switch r.Method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
	default:
		return false
	}
	p := r.URL.Path
	return strings.HasPrefix(p, "/api/") || p == "/graphql"
}

// Middleware refuses data-plane writes while the host is under memory pressure:
// 503 + Retry-After + a body that names the measurement, the floor and the knob.
func (g *MemoryGuard) Middleware(next http.Handler) http.Handler {
	if g == nil {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !isGuardedWrite(r) {
			next.ServeHTTP(w, r)
			return
		}
		ok, avail := g.Allow()
		if ok {
			next.ServeHTTP(w, r)
			return
		}
		// Under pressure, and the request is GraphQL: a QUERY is a read and
		// reads keep flowing — only a mutation is refused. The body is read
		// once (bounded) and handed back to the handler untouched.
		if r.URL.Path == "/graphql" && g.GraphQLMutation != nil && r.Body != nil {
			body, rerr := io.ReadAll(io.LimitReader(r.Body, graphqlBodyCap+1))
			_ = r.Body.Close()
			r.Body = io.NopCloser(bytes.NewReader(body))
			if rerr == nil && len(body) <= graphqlBodyCap && !g.GraphQLMutation(body) {
				next.ServeHTTP(w, r)
				return
			}
		}
		g.logRefusal(avail)
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Retry-After", "5")
		w.WriteHeader(http.StatusServiceUnavailable)
		json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
			"error": fmt.Sprintf("host memory pressure: MemAvailable+SwapFree is %d MiB, under the %d MiB floor — new writes are refused so the kernel does not OOM-kill the shared PostgreSQL; reads continue. Retry in a moment, load in smaller batches, add swap to the host, or tune %s (0 disables)",
				avail>>20, g.minBytes>>20, MemoryGuardEnvVar),
			"memory_available_mib": avail >> 20,
			"memory_floor_mib":     g.minBytes >> 20,
		})
	})
}

// logRefusal writes at most one line every 10 s — a saturated host does not need
// a log flood on top.
func (g *MemoryGuard) logRefusal(avail int64) {
	g.mu.Lock()
	defer g.mu.Unlock()
	now := g.nowFn()
	if now.Sub(g.lastRefuse) < 10*time.Second {
		return
	}
	g.lastRefuse = now
	log.Printf("WARNING: memory guard refusing writes — MemAvailable+SwapFree %d MiB < floor %d MiB (%s). The host is close to the kernel OOM killer; add swap or load in smaller batches.", avail>>20, g.minBytes>>20, MemoryGuardEnvVar)
}

// readMeminfo returns MemTotal and MemAvailable+SwapFree in bytes.
func readMeminfo(path string) (total, available int64, err error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, 0, err
	}
	defer f.Close() //nolint:errcheck // read-only handle
	var memAvail, swapFree int64
	var haveAvail bool
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		key, rest, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		fields := strings.Fields(rest)
		if len(fields) == 0 {
			continue
		}
		kb, perr := strconv.ParseInt(fields[0], 10, 64)
		if perr != nil {
			continue
		}
		switch key {
		case "MemTotal":
			total = kb << 10
		case "MemAvailable":
			memAvail = kb << 10
			haveAvail = true
		case "SwapFree":
			swapFree = kb << 10
		}
	}
	if err := sc.Err(); err != nil {
		return 0, 0, err
	}
	if !haveAvail {
		return 0, 0, fmt.Errorf("%s has no MemAvailable line (kernel < 3.14?)", path)
	}
	return total, memAvail + swapFree, nil
}
