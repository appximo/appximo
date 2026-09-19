package summary

import (
	"fmt"
	"os"
	"strings"
	"sync"
	"time"
)

// The digest's "today" — and the question layer's "hoy", "esta semana" —
// are computed in ONE declared timezone: APPXIMO_SUMMARY_TIMEZONE (an IANA
// name such as America/Bogota). Unset ⇒ the process's local zone, which on a
// production box is almost always UTC: an owner in Bogotá asking at 8 pm was
// already being answered about TOMORROW (VOZ-PREGUNTAS-S1 found it while
// wiring the questions; the digest had the same blind spot, masked because
// the scheduled run fires in the morning). An invalid name refuses to boot
// (CheckTimezone) — a silently wrong "today" is the failure this guards.

const timezoneEnv = "APPXIMO_SUMMARY_TIMEZONE"

var (
	locMu   sync.Mutex
	locName string
	locVal  *time.Location
)

// Location returns the declared timezone (or Local). The parsed zone is
// cached by the variable's VALUE, not once per process: a test suite that
// sets the variable for one test and clears it for the next must see each
// (a sync.Once cache leaked one test's zone into another's "today").
func Location() *time.Location {
	name := strings.TrimSpace(os.Getenv(timezoneEnv))
	locMu.Lock()
	defer locMu.Unlock()
	if locVal != nil && locName == name {
		return locVal
	}
	loc := time.Local
	if name != "" {
		if l, err := time.LoadLocation(name); err == nil {
			loc = l
		}
	}
	locName, locVal = name, loc
	return loc
}

// CheckTimezone validates the declared zone at boot, naming the variable.
func CheckTimezone() error {
	name := strings.TrimSpace(os.Getenv(timezoneEnv))
	if name == "" {
		return nil
	}
	if _, err := time.LoadLocation(name); err != nil {
		return fmt.Errorf("appximo: %s=%q is not an IANA timezone (e.g. America/Bogota): %v", timezoneEnv, name, err)
	}
	return nil
}

// Now is the current time in the declared zone.
func Now() time.Time { return time.Now().In(Location()) }
