package schemadiff

import (
	"strconv"
	"strings"
)

// baseAlias maps every Postgres type spelling (and the short internal names) to a
// canonical BaseType. This is the heart of canonicalization: int4/int/integer all
// collapse to BaseInteger, varchar/character varying to BaseVarchar, float8/double
// precision to BaseDouble, and so on — so types compare by meaning, not spelling.
//
// Multi-word keys ("double precision", "character varying", "timestamp", "time")
// rely on ParseType collapsing internal whitespace and stripping the
// "with/without time zone" phrase before the lookup.
var baseAlias = map[string]BaseType{
	"int2": BaseSmallint, "smallint": BaseSmallint,
	"int4": BaseInteger, "int": BaseInteger, "integer": BaseInteger,
	"int8": BaseBigint, "bigint": BaseBigint,
	"numeric": BaseNumeric, "decimal": BaseNumeric,
	"float4": BaseReal, "real": BaseReal,
	"float8": BaseDouble, "double precision": BaseDouble,
	"bool": BaseBool, "boolean": BaseBool,
	"text":    BaseText,
	"varchar": BaseVarchar, "character varying": BaseVarchar,
	"char": BaseChar, "character": BaseChar, "bpchar": BaseChar,
	"uuid":        BaseUUID,
	"timestamptz": BaseTimestamptz,
	"timestamp":   BaseTimestamp,
	"date":        BaseDate,
	"timetz":      BaseTimetz,
	"time":        BaseTime,
	"json":        BaseJSON,
	"jsonb":       BaseJSONB,
	"bytea":       BaseBytea,
	"interval":    BaseInterval,
	"inet":        BaseInet,
}

const (
	tzUnknown = iota
	tzWith
	tzWithout
)

// ParseType normalizes any Postgres type spelling — the output of
// format_type(atttypid, atttypmod) or a hand-written type string — into the
// canonical Type. Unknown types (enums, domains, extensions) become
// {Base: BaseUserDefined, UserName: <raw>}; nothing is lost.
//
// It handles: short and long spellings (int4 vs integer, varchar vs character
// varying, float8 vs double precision), the time-zone phrase appearing after an
// optional precision ("timestamp(3) with time zone"), length/precision/scale
// arguments, and array suffixes ("integer[]").
func ParseType(pgType string) Type {
	s := strings.ToLower(strings.TrimSpace(pgType))
	if s == "" {
		return Type{Base: BaseUnknown}
	}

	// Array suffix(es): "integer[]", "text[][]".
	array := false
	for strings.HasSuffix(s, "[]") {
		array = true
		s = strings.TrimSpace(s[:len(s)-2])
	}

	// Time-zone phrase first: the optional precision can sit BEFORE it
	// ("timestamp(3) with time zone"), so we cannot rely on a trailing "(N)".
	tz := tzUnknown
	switch {
	case strings.Contains(s, "with time zone"):
		tz = tzWith
		s = strings.Replace(s, "with time zone", "", 1)
	case strings.Contains(s, "without time zone"):
		tz = tzWithout
		s = strings.Replace(s, "without time zone", "", 1)
	}

	// Extract "(args)" wherever it appears, then collapse whitespace.
	var args []int
	if i := strings.Index(s, "("); i >= 0 {
		if j := strings.Index(s[i:], ")"); j >= 0 {
			args = parseIntArgs(s[i+1 : i+j])
			s = s[:i] + s[i+j+1:]
		}
	}
	s = strings.Join(strings.Fields(s), " ")

	base, ok := baseAlias[s]
	if !ok {
		// Unknown base name → user-defined; preserve the raw (array-stripped) name.
		return Type{Base: BaseUserDefined, UserName: s, Array: array}
	}

	// A bare "timestamp"/"time" with an explicit "with time zone" phrase resolves
	// to the tz-aware family.
	if tz == tzWith {
		switch base {
		case BaseTimestamp:
			base = BaseTimestamptz
		case BaseTime:
			base = BaseTimetz
		}
	}

	return finalizeType(base, args, array)
}

// finalizeType applies the parsed (args) to the right slots for the base family.
func finalizeType(base BaseType, args []int, array bool) Type {
	t := Type{Base: base, Array: array}
	switch base {
	case BaseVarchar, BaseChar:
		if len(args) >= 1 {
			t.Size = args[0]
		}
	case BaseNumeric:
		if len(args) >= 1 {
			t.Prec = args[0]
		}
		if len(args) >= 2 {
			t.Scale = args[1]
		}
	case BaseTimestamp, BaseTimestamptz, BaseTime, BaseTimetz:
		if len(args) >= 1 {
			t.Prec = args[0]
		}
	}
	return t
}

// parseIntArgs parses a comma-separated argument list ("10,2") into ints,
// silently skipping any non-integer token.
func parseIntArgs(s string) []int {
	parts := strings.Split(s, ",")
	out := make([]int, 0, len(parts))
	for _, p := range parts {
		if n, err := strconv.Atoi(strings.TrimSpace(p)); err == nil {
			out = append(out, n)
		}
	}
	return out
}

// TypeForAPIType maps one Appximo schema-JSON field type to its canonical
// Postgres Type, mirroring exactly what the engine's converger lays down
// (pkg/migration.fieldTypeToPG): string/text → text, int → integer, int64 →
// bigint, float64 → double precision, bool → boolean, uuid → uuid, time →
// timestamptz, json → text (the engine stores json as TEXT), and anything else →
// text. This is the canonicalizer covering the desired (schema) side, the
// counterpart to ParseType covering the real (introspected) side — the two halves
// the future diff compares. It is an independent copy of the mapping (no import of
// the engine), kept deliberately in lockstep with it.
func TypeForAPIType(apiType string) Type {
	switch apiType {
	case "string", "text":
		return Type{Base: BaseText}
	case "int":
		return Type{Base: BaseInteger}
	case "int64":
		return Type{Base: BaseBigint}
	case "float64":
		return Type{Base: BaseDouble}
	case "bool":
		return Type{Base: BaseBool}
	case "uuid", "file":
		// A file field (FILES-LINK-S1) stores a file_id — physically a UUID
		// column (its FK to the tenant files table is modeled separately).
		return Type{Base: BaseUUID}
	case "time":
		return Type{Base: BaseTimestamptz}
	case "json":
		return Type{Base: BaseText}
	case "jsonb":
		// A REAL jsonb column (LIBRARY-GAPS-S1) — the one type that supports
		// containment (@>) and a GIN index. Deliberately distinct from "json",
		// which stays TEXT so every existing tenant column is unchanged.
		return Type{Base: BaseJSONB}
	default:
		return Type{Base: BaseText}
	}
}

// parseRefAction maps a pg_constraint.confdeltype / confupdtype code to a
// RefAction ('c' CASCADE, 'r' RESTRICT, 'n' SET NULL, 'd' SET DEFAULT, anything
// else — including 'a' — NO ACTION).
func parseRefAction(code string) RefAction {
	switch strings.TrimSpace(code) {
	case "c":
		return Cascade
	case "r":
		return Restrict
	case "n":
		return SetNull
	case "d":
		return SetDefault
	default:
		return NoAction
	}
}
