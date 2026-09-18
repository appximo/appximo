package main

import (
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/rs/zerolog"
)

// envSet is the worker's STRICT environment reader (AUTO-2 — the OPS-13 class,
// worker edition). The old worker fell back to defaults in silence: a mistyped
// APPXIMO_WORKER_MODE ran echo (which used to ack everything), a bad SMTP_PORT
// ran the wrong port, and a MISSPELLED variable was simply never read. In the
// worker that silence is worse than in the engine, because the mode and the
// topic decide WHICH events are consumed and which are lost. The contract now:
//
//   - an INVALID value (bad int, bad duration, out-of-set enum) refuses to boot,
//     naming every offending variable, its value and what was expected;
//   - an UNSET variable falls back to its default AND is reported in one boot
//     line ("defaults in effect: …"), so the effective config is always written;
//   - an unknown APPXIMO_WORKER_* variable (a typo — it would never be read)
//     also refuses to boot, listing the known set.
type envSet struct {
	invalid  []string
	defaults []string
	known    map[string]bool
}

func newEnvSet() *envSet { return &envSet{known: map[string]bool{}} }

// Str reads a string with a default.
func (e *envSet) Str(key, def string) string {
	e.known[key] = true
	v := os.Getenv(key)
	if v == "" {
		if def != "" {
			e.defaults = append(e.defaults, fmt.Sprintf("%s=%s", key, def))
		}
		return def
	}
	return v
}

// Opt reads an optional string ("" default, not reported).
func (e *envSet) Opt(key string) string {
	e.known[key] = true
	return os.Getenv(key)
}

// Require reads a mandatory string.
func (e *envSet) Require(key, why string) string {
	e.known[key] = true
	v := os.Getenv(key)
	if v == "" {
		e.invalid = append(e.invalid, fmt.Sprintf("%s is required (%s)", key, why))
	}
	return v
}

// Int reads an integer with a default and an optional minimum.
func (e *envSet) Int(key string, def, min int) int {
	e.known[key] = true
	v := os.Getenv(key)
	if v == "" {
		e.defaults = append(e.defaults, fmt.Sprintf("%s=%d", key, def))
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < min {
		e.invalid = append(e.invalid, fmt.Sprintf("%s=%q is not an integer ≥ %d", key, v, min))
		return def
	}
	return n
}

// Dur reads a Go duration with a default.
func (e *envSet) Dur(key string, def time.Duration) time.Duration {
	e.known[key] = true
	v := os.Getenv(key)
	if v == "" {
		e.defaults = append(e.defaults, fmt.Sprintf("%s=%s", key, def))
		return def
	}
	d, err := time.ParseDuration(v)
	if err != nil || d <= 0 {
		e.invalid = append(e.invalid, fmt.Sprintf("%s=%q is not a positive Go duration (e.g. 5s, 1m)", key, v))
		return def
	}
	return d
}

// Enum reads a closed-set string with a default.
func (e *envSet) Enum(key, def string, allowed ...string) string {
	e.known[key] = true
	v := os.Getenv(key)
	if v == "" {
		e.defaults = append(e.defaults, fmt.Sprintf("%s=%s", key, def))
		return def
	}
	for _, a := range allowed {
		if v == a {
			return v
		}
	}
	e.invalid = append(e.invalid, fmt.Sprintf("%s=%q is not one of %s", key, v, strings.Join(allowed, "|")))
	return def
}

// CheckUnknown refuses variables under prefix that no reader registered — a
// typo'd variable is one nothing will ever read.
func (e *envSet) CheckUnknown(prefix string) {
	for _, kv := range os.Environ() {
		key, _, _ := strings.Cut(kv, "=")
		if strings.HasPrefix(key, prefix) && !e.known[key] {
			knownList := make([]string, 0, len(e.known))
			for k := range e.known {
				if strings.HasPrefix(k, prefix) {
					knownList = append(knownList, k)
				}
			}
			sort.Strings(knownList)
			e.invalid = append(e.invalid, fmt.Sprintf("%s is not a variable this worker reads (known %s*: %s)", key, prefix, strings.Join(knownList, ", ")))
		}
	}
}

// FailFast exits (naming EVERY invalid variable at once) when anything is wrong.
func (e *envSet) FailFast(log zerolog.Logger) {
	if len(e.invalid) == 0 {
		return
	}
	sort.Strings(e.invalid)
	log.Fatal().Msgf("appximo-worker: refusing to start over invalid configuration:\n  - %s",
		strings.Join(e.invalid, "\n  - "))
}

// Report prints the one boot line naming which values fell to defaults.
func (e *envSet) Report(log zerolog.Logger) {
	if len(e.defaults) == 0 {
		return
	}
	sort.Strings(e.defaults)
	log.Info().Msgf("worker: defaults in effect (unset variables): %s", strings.Join(e.defaults, " "))
}
