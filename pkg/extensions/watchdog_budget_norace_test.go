//go:build !race

package extensions_test

import "time"

// watchdogBudget is the upper bound the 80ms JS watchdog interrupt must arrive
// within. In a normal build the original strict <100ms guarantee applies.
const watchdogBudget = 100 * time.Millisecond
