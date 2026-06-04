//go:build race

package extensions_test

import "time"

// watchdogBudget under -race: the race detector inflates wall-clock latency, so
// the strict 20ms margin above the 80ms watchdog is not reliable. 250ms still
// proves the 80ms watchdog (not the 500ms hard fallback) interrupted the loop,
// while removing the timing flakiness that only manifests under instrumentation.
const watchdogBudget = 250 * time.Millisecond
