package query

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// validateFilterValue rejects a filter value that Postgres could not possibly
// cast to the field's declared type, BEFORE any SQL is built — so the error
// names the parameter and the expected type, and never comes from a masked
// pgconn.PgError (the "Postgres errors are masked" property holds by
// construction). ENG-25: `?filter[amount][gt]=abc` used to answer
// `400 {"error":"invalid request"}` — with three filters and two of them wrong,
// the caller could not tell which one was rejected.
//
// THE GOVERNING CONSTRAINT (ADR-024): do not become stricter than the layer you
// are protecting. Each per-type acceptor below reproduces POSTGRES's accepted
// input set, not Go's — measured before this existed: `?filter[done][eq]=yes`
// returns 200 on the live engine because Postgres accepts "yes" as a boolean
// while strconv.ParseBool does not. Where the two sets cannot be matched
// exactly, the acceptor errs PERMISSIVE: a value we accept that Postgres then
// rejects still gets the pre-existing 400 (db.IsBadInput), just without the
// field name — never a working request turned into an error. The conformance
// direction is pinned by TestFilterValue_PostgresConformance (unit corpus) and
// by the live-Postgres cross-check in pkg/integration.
//
// `time` is DELIBERATELY not validated here — same documented leniency as the
// write path (AGENTS.md: "time values remain validated by Postgres rather than
// in Go"). Postgres's timestamp grammar spans dozens of formats plus special
// words (`now`, `today`, `infinity`, `allballs`); reproducing it is its own
// project, and a wrong guess would reject working requests. A garbage time
// value therefore stays a 400 without the field name (recorded in the audit).
func validateFilterValue(field, op, fieldType, value string) error {
	var ok bool
	switch fieldType {
	case "int", "int64":
		ok = pgAcceptsInt(value)
	case "float64":
		ok = pgAcceptsFloat(value)
	case "bool":
		ok = pgAcceptsBool(value)
	case "uuid", "file":
		ok = pgAcceptsUUID(value)
	case "jsonb":
		// jsonb input is exactly "a valid JSON document" (RFC 7159); Go's
		// json.Valid checks the same grammar. (`json` is stored as TEXT — any
		// string is a value of it — so it is not listed here.)
		ok = json.Valid([]byte(value))
	default:
		// string/text/json: every string is a value of the type.
		// time: see the doc comment — delegated to Postgres.
		return nil
	}
	if !ok {
		return fmt.Errorf("filter[%s][%s]: %q is not a valid %s value", field, op, value, fieldType)
	}
	return nil
}

// pgAcceptsInt mirrors Postgres integer input: optional surrounding whitespace,
// optional sign, decimal digits (leading zeros are DECIMAL in Postgres, never
// octal) — plus the PG16 literal forms (0x/0o/0b and `_` digit separators),
// which strconv.ParseInt(base 0) happens to share. A value that overflows int64
// is rejected too: Postgres answers 22003 for it in every version, so naming it
// here loses nothing.
func pgAcceptsInt(s string) bool {
	t := strings.TrimSpace(s)
	if t == "" {
		return false
	}
	if isPlainDecimal(t) {
		// Base 10 explicitly: base-0 parsing would read a leading zero as
		// octal and reject "0129", which Postgres accepts as decimal 129.
		_, err := strconv.ParseInt(t, 10, 64)
		return err == nil
	}
	_, err := strconv.ParseInt(t, 0, 64)
	return err == nil
}

func isPlainDecimal(t string) bool {
	if t[0] == '+' || t[0] == '-' {
		t = t[1:]
	}
	if t == "" {
		return false
	}
	for i := 0; i < len(t); i++ {
		if t[i] < '0' || t[i] > '9' {
			return false
		}
	}
	return true
}

// pgAcceptsFloat mirrors Postgres float8 input: whitespace-tolerant, the
// special spellings Infinity/-Infinity/NaN (any case, sign allowed), and
// ordinary decimal/scientific notation. A finite literal that overflows float8
// (`1e999`) is rejected — Postgres answers "out of range" (22003) for it.
// Go-only extensions (hex floats) are ACCEPTED and left to Postgres.
func pgAcceptsFloat(s string) bool {
	t := strings.ToLower(strings.TrimSpace(s))
	if t == "" {
		return false
	}
	switch strings.TrimPrefix(strings.TrimPrefix(t, "+"), "-") {
	case "inf", "infinity", "nan":
		return true
	}
	_, err := strconv.ParseFloat(t, 64)
	if err == nil {
		return true
	}
	// PG16 permits `_` digit separators in numeric literals; Go's ParseFloat
	// does not. Stripping them is slightly MORE permissive than Postgres
	// (which requires digits on both sides) — the safe direction.
	if strings.Contains(t, "_") {
		_, err = strconv.ParseFloat(strings.ReplaceAll(t, "_", ""), 64)
		return err == nil
	}
	return false
}

// pgAcceptsBool mirrors Postgres parse_bool exactly: case-insensitive,
// whitespace-tolerant, and accepting UNIQUE PREFIXES of true/false/yes/no plus
// on/of/off and 1/0. This is the acceptor that decides "yes" is a boolean —
// the measured case that made a Go-semantics check unshippable (ADR-024).
// A bare "o" is ambiguous between on/off and is rejected, as in Postgres.
func pgAcceptsBool(s string) bool {
	t := strings.ToLower(strings.TrimSpace(s))
	if t == "" {
		return false
	}
	if strings.HasPrefix("true", t) || strings.HasPrefix("false", t) ||
		strings.HasPrefix("yes", t) || strings.HasPrefix("no", t) {
		return true
	}
	switch t {
	case "on", "of", "off", "1", "0":
		return true
	}
	return false
}

// pgAcceptsUUID accepts what Postgres uuid_in accepts — 32 hex digits, any
// case, optional surrounding braces, hyphens allowed — erring permissive on
// hyphen PLACEMENT (Postgres constrains where they may appear; we only require
// that what remains is 32 hex digits). Also covers `file` fields, whose column
// is a uuid (FILES-LINK-S1).
func pgAcceptsUUID(s string) bool {
	t := strings.TrimSpace(s)
	if strings.HasPrefix(t, "{") && strings.HasSuffix(t, "}") && len(t) >= 2 {
		t = t[1 : len(t)-1]
	}
	t = strings.ReplaceAll(t, "-", "")
	if len(t) != 32 {
		return false
	}
	for i := 0; i < len(t); i++ {
		c := t[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') && (c < 'A' || c > 'F') {
			return false
		}
	}
	return true
}
