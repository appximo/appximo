package appitools

import (
	"log"
	"os"
	"runtime/debug"
	"strconv"
	"strings"
)

// applyRuntimeLimits wires the process-level Go runtime knobs from the
// environment (LIBRARY-HARDEN-S1). It lives in the library — not only the
// `serve` command — so EVERY binary built on appitools.New (a custom-handler
// backend included) gets the same memory backpressure.
//
//   - GOMEMLIMIT — a soft memory ceiling. Go has no per-goroutine memory bound,
//     so this global soft limit is the guard against a runaway allocation OOM-ing
//     the whole multi-tenant process. Set it to ~90% of the container's memory
//     limit (accepts "900MiB", "1GiB", or raw bytes). The runtime GCs harder as
//     usage approaches the limit rather than being OOM-killed. When UNSET, the
//     engine auto-detects an explicit cgroup memory limit and applies 90% of it
//     (autoMemoryLimit) — never a fraction of total RAM — and otherwise warns on a
//     small box. GOGC is opt-in via env; unset leaves the Go default (100).
//   - GOGC — GC target percentage (Go default 100).
//
// It is idempotent (reads env, sets the same value) so the in-process fleet
// calling New per app is harmless. GOMAXPROCS is left to the process entry point
// (the cmd binary blank-imports automaxprocs for cgroup awareness; a custom
// binary should do the same if it runs under a CPU cgroup).
func applyRuntimeLimits() {
	if v := strings.TrimSpace(os.Getenv("GOGC")); v != "" {
		if pct, err := strconv.Atoi(v); err == nil {
			debug.SetGCPercent(pct)
			log.Printf("runtime: GOGC=%d", pct)
		}
	}
	if v := strings.TrimSpace(os.Getenv("GOMEMLIMIT")); v != "" {
		if bytes, err := parseMemLimit(v); err == nil {
			debug.SetMemoryLimit(bytes)
			log.Printf("runtime: GOMEMLIMIT=%s (%d bytes soft limit)", v, bytes)
		} else {
			log.Printf("WARNING: ignoring invalid GOMEMLIMIT %q: %v", v, err)
		}
	} else {
		autoMemoryLimit()
	}
}

// autoMemoryLimit picks a sane GOMEMLIMIT when the operator set none
// (PROD-PATH-BUILD-S1). The design is deliberately conservative — "no dangerous
// magic":
//
//   - If the process runs under an EXPLICIT cgroup memory limit (a container
//     started with --memory / mem_limit, or systemd MemoryMax), set GOMEMLIMIT to
//     90 % of it. The runtime then GCs harder as it approaches the ceiling instead
//     of being OOM-killed. This is the automaxprocs equivalent for memory.
//   - It NEVER derives a limit from TOTAL system RAM. On the official single-box
//     stack Postgres and Caddy share the machine, so 90 %-of-total would starve
//     them — the classic footgun of naive auto-tuning.
//   - With no cgroup limit it only LOGS A WARNING, and only on a small box, so the
//     operator (or the official installer, which always sets GOMEMLIMIT) fixes it.
func autoMemoryLimit() {
	if limit, ok := cgroupMemoryLimit(); ok {
		soft := int64(float64(limit) * 0.9)
		debug.SetMemoryLimit(soft)
		log.Printf("runtime: GOMEMLIMIT auto-set to %d bytes (90%% of the %d-byte cgroup memory limit)", soft, limit)
		return
	}
	// ~1.25 GiB — below this, an unbounded heap on a box shared with Postgres is a
	// real OOM risk worth flagging. Above it, stay silent (no noise on big boxes).
	const smallBox = 1342177280
	if total, ok := totalSystemMemory(); ok && total <= smallBox {
		log.Printf("WARNING: GOMEMLIMIT is unset on a small box (~%d MiB RAM). With Postgres "+
			"sharing the machine, a Go heap spike can OOM the engine. Set GOMEMLIMIT (e.g. "+
			"GOMEMLIMIT=512MiB) — the official installer (scripts/install.sh) does this for you.", total/(1<<20))
	}
}

// cgroupMemoryLimit returns an EXPLICIT cgroup memory limit in bytes when present
// (cgroup v2 memory.max, else v1 memory.limit_in_bytes). "max", an unlimited
// sentinel, or an unreadable file all report ok=false — a bare host has no limit
// here (its root cgroup exposes no memory.max), so this fires only for a
// container with a real limit, which is exactly when it is safe to honour.
func cgroupMemoryLimit() (int64, bool) {
	return cgroupMemoryLimitFrom(
		"/sys/fs/cgroup/memory.max",                   // cgroup v2 (unified)
		"/sys/fs/cgroup/memory/memory.limit_in_bytes", // cgroup v1
	)
}

// cgroupMemoryLimitFrom is the testable core of cgroupMemoryLimit — the two
// controller paths are injected so a test can point them at temp files instead
// of the real /sys/fs/cgroup.
func cgroupMemoryLimitFrom(v2path, v1path string) (int64, bool) {
	// cgroup v2 (unified): under Docker's default private cgroup namespace this is
	// the container's OWN limit. A present v2 file is authoritative — do NOT fall
	// through to v1.
	if b, err := os.ReadFile(v2path); err == nil {
		s := strings.TrimSpace(string(b))
		if s != "max" {
			if n, perr := strconv.ParseInt(s, 10, 64); perr == nil && n > 0 {
				return n, true
			}
		}
		return 0, false
	}
	// cgroup v1.
	if b, err := os.ReadFile(v1path); err == nil {
		if n, perr := strconv.ParseInt(strings.TrimSpace(string(b)), 10, 64); perr == nil {
			// v1 reports a near-int64-max sentinel when there is no limit.
			if n > 0 && n < (1<<62) {
				return n, true
			}
		}
	}
	return 0, false
}

// totalSystemMemory reads MemTotal from /proc/meminfo, in bytes.
func totalSystemMemory() (int64, bool) {
	b, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return 0, false
	}
	for _, line := range strings.Split(string(b), "\n") {
		if strings.HasPrefix(line, "MemTotal:") {
			if f := strings.Fields(line); len(f) >= 2 {
				if kb, perr := strconv.ParseInt(f[1], 10, 64); perr == nil {
					return kb * 1024, true
				}
			}
			break
		}
	}
	return 0, false
}

// parseMemLimit parses "512MiB", "1GiB", "1073741824" (raw bytes) into a byte
// count. Binary units are powers of 1024; decimal units powers of 1000.
func parseMemLimit(s string) (int64, error) {
	units := []struct {
		suffix string
		mult   int64
	}{
		{"GiB", 1 << 30}, {"MiB", 1 << 20}, {"KiB", 1 << 10},
		{"GB", 1_000_000_000}, {"MB", 1_000_000}, {"KB", 1_000},
	}
	for _, u := range units {
		if strings.HasSuffix(s, u.suffix) {
			n, err := strconv.ParseInt(strings.TrimSuffix(s, u.suffix), 10, 64)
			if err != nil {
				return 0, err
			}
			return n * u.mult, nil
		}
	}
	return strconv.ParseInt(s, 10, 64)
}
