package observability

import (
	"hash/fnv"
	"regexp"
	"strings"
)

// Fingerprinting — what turns "175 traces" into "four problems"
// (OBSERVABILIDAD-ERRORES-S1, Parte E).
//
// Two occurrences of the same defect must land in the same group even though
// their messages differ in the parts that vary per request: the row id, the
// value the driver rejected, the number that was out of range, the tenant's
// schema name inside a quoted identifier. NormalizeMessage strips exactly
// those, in an order that keeps the STRUCTURE of the message (which is what
// identifies the defect) and drops the DATA (which is what identifies the
// occurrence). The fingerprint then hashes the route template (already
// {id}-shaped), the normalized message and — when a stack is known — the top
// application frame, so the same message from two different code sites stays
// two groups.

var (
	reUUID   = regexp.MustCompile(`[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}`)
	reHex    = regexp.MustCompile(`\b[0-9a-fA-F]{16,}\b`)
	reQuoted = regexp.MustCompile(`"[^"]*"|'[^']*'`)
	reNumber = regexp.MustCompile(`\b\d+(?:\.\d+)?\b`)
	reSpace  = regexp.MustCompile(`\s+`)
	// SQLSTATE codes are structure, not data — keep them (they are the most
	// precise class of a driver error). They are restored after number
	// scrubbing via a placeholder.
	reSQLState = regexp.MustCompile(`SQLSTATE [0-9A-Z]{5}`)
)

// NormalizeMessage returns the message with per-occurrence data replaced by
// placeholders: uuids → <uuid>, long hex ids → <hex>, quoted literals → <q>,
// numbers → <n>; whitespace collapsed; SQLSTATE codes preserved.
func NormalizeMessage(msg string) string {
	if msg == "" {
		return ""
	}
	states := reSQLState.FindAllString(msg, -1)
	m := reSQLState.ReplaceAllString(msg, "SQLSTATE §")
	m = reUUID.ReplaceAllString(m, "<uuid>")
	m = reHex.ReplaceAllString(m, "<hex>")
	m = reQuoted.ReplaceAllString(m, "<q>")
	m = reNumber.ReplaceAllString(m, "<n>")
	for _, st := range states {
		m = strings.Replace(m, "SQLSTATE §", st, 1)
	}
	m = reSpace.ReplaceAllString(strings.TrimSpace(m), " ")
	if len(m) > 200 {
		m = m[:200]
	}
	return m
}

// Fingerprint hashes (route, normalized message, top frame) into the group key.
// topFrame may be "" (no stack known); route is the matched template.
func Fingerprint(route, msg, topFrame string) uint64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(route))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(NormalizeMessage(msg)))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(topFrame))
	return h.Sum64()
}

// TopFrame returns the first application frame of a stack ("" when none).
func TopFrame(stack []Frame) string {
	if len(stack) == 0 {
		return ""
	}
	return stack[0].Function
}
