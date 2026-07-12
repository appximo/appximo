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
// backend included) gets the same memory backpressure. Both knobs are opt-in
// via env; unset leaves the Go defaults untouched.
//
//   - GOMEMLIMIT — a soft memory ceiling. Go has no per-goroutine memory bound,
//     so this global soft limit is the guard against a runaway allocation OOM-ing
//     the whole multi-tenant process. Set it to ~90% of the container's memory
//     limit (accepts "900MiB", "1GiB", or raw bytes). The runtime GCs harder as
//     usage approaches the limit rather than being OOM-killed.
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
	}
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
