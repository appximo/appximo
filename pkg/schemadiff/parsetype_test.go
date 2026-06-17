package schemadiff_test

import (
	"testing"

	sd "github.com/miguelangel/appitools/pkg/schemadiff"
)

// TestParseType_Spellings checks that every Postgres type spelling normalizes to
// the expected canonical Type — the core of the canonicalizer.
func TestParseType_Spellings(t *testing.T) {
	cases := []struct {
		in   string
		want sd.Type
	}{
		// integers — short and long spellings collapse to one BaseType
		{"smallint", sd.Type{Base: sd.BaseSmallint}},
		{"int2", sd.Type{Base: sd.BaseSmallint}},
		{"integer", sd.Type{Base: sd.BaseInteger}},
		{"int", sd.Type{Base: sd.BaseInteger}},
		{"int4", sd.Type{Base: sd.BaseInteger}},
		{"bigint", sd.Type{Base: sd.BaseBigint}},
		{"int8", sd.Type{Base: sd.BaseBigint}},

		// floats
		{"real", sd.Type{Base: sd.BaseReal}},
		{"float4", sd.Type{Base: sd.BaseReal}},
		{"double precision", sd.Type{Base: sd.BaseDouble}},
		{"float8", sd.Type{Base: sd.BaseDouble}},

		// numeric with precision/scale
		{"numeric", sd.Type{Base: sd.BaseNumeric}},
		{"numeric(10,2)", sd.Type{Base: sd.BaseNumeric, Prec: 10, Scale: 2}},
		{"decimal(10,2)", sd.Type{Base: sd.BaseNumeric, Prec: 10, Scale: 2}},
		{"numeric(8)", sd.Type{Base: sd.BaseNumeric, Prec: 8}},

		// bool
		{"boolean", sd.Type{Base: sd.BaseBool}},
		{"bool", sd.Type{Base: sd.BaseBool}},

		// strings
		{"text", sd.Type{Base: sd.BaseText}},
		{"varchar", sd.Type{Base: sd.BaseVarchar}},
		{"varchar(255)", sd.Type{Base: sd.BaseVarchar, Size: 255}},
		{"character varying(255)", sd.Type{Base: sd.BaseVarchar, Size: 255}},
		{"character(10)", sd.Type{Base: sd.BaseChar, Size: 10}},
		{"char(10)", sd.Type{Base: sd.BaseChar, Size: 10}},
		{"bpchar", sd.Type{Base: sd.BaseChar}},

		// uuid
		{"uuid", sd.Type{Base: sd.BaseUUID}},

		// time / timestamp, with and without zone, and with precision before the zone
		{"timestamptz", sd.Type{Base: sd.BaseTimestamptz}},
		{"timestamp with time zone", sd.Type{Base: sd.BaseTimestamptz}},
		{"timestamp without time zone", sd.Type{Base: sd.BaseTimestamp}},
		{"timestamp", sd.Type{Base: sd.BaseTimestamp}},
		{"timestamp(3) with time zone", sd.Type{Base: sd.BaseTimestamptz, Prec: 3}},
		{"timestamp(6) without time zone", sd.Type{Base: sd.BaseTimestamp, Prec: 6}},
		{"time", sd.Type{Base: sd.BaseTime}},
		{"time with time zone", sd.Type{Base: sd.BaseTimetz}},
		{"timetz", sd.Type{Base: sd.BaseTimetz}},
		{"date", sd.Type{Base: sd.BaseDate}},

		// json / bytea
		{"json", sd.Type{Base: sd.BaseJSON}},
		{"jsonb", sd.Type{Base: sd.BaseJSONB}},
		{"bytea", sd.Type{Base: sd.BaseBytea}},

		// arrays
		{"integer[]", sd.Type{Base: sd.BaseInteger, Array: true}},
		{"text[]", sd.Type{Base: sd.BaseText, Array: true}},
		{"character varying(255)[]", sd.Type{Base: sd.BaseVarchar, Size: 255, Array: true}},

		// case / whitespace insensitivity
		{"  INTEGER  ", sd.Type{Base: sd.BaseInteger}},
		{"DOUBLE PRECISION", sd.Type{Base: sd.BaseDouble}},

		// unknown → user-defined, raw name preserved
		{"citext", sd.Type{Base: sd.BaseUserDefined, UserName: "citext"}},
		{"mood", sd.Type{Base: sd.BaseUserDefined, UserName: "mood"}},
		{"", sd.Type{Base: sd.BaseUnknown}},
	}
	for _, c := range cases {
		got := sd.ParseType(c.in)
		if got != c.want {
			t.Errorf("ParseType(%q) = %+v, want %+v", c.in, got, c.want)
		}
	}
}

// TestParseType_Equivalence asserts the canonicalizer's defining property: two
// distinct textual spellings of the same type produce the SAME Type. This is what
// lets the future diff avoid false "type changed" findings (varchar vs character
// varying, int4 vs integer, float8 vs double precision, …).
func TestParseType_Equivalence(t *testing.T) {
	groups := [][]string{
		{"int", "int4", "integer"},
		{"int8", "bigint"},
		{"int2", "smallint"},
		{"bool", "boolean"},
		{"float8", "double precision"},
		{"float4", "real"},
		{"varchar(255)", "character varying(255)"},
		{"char(10)", "character(10)"},
		{"numeric(10,2)", "decimal(10,2)"},
		{"timestamptz", "timestamp with time zone"},
		{"timestamp", "timestamp without time zone"},
		{"timetz", "time with time zone"},
	}
	for _, g := range groups {
		first := sd.ParseType(g[0])
		for _, spelling := range g[1:] {
			got := sd.ParseType(spelling)
			if got != first {
				t.Errorf("ParseType(%q)=%+v != ParseType(%q)=%+v (should be equal)", g[0], first, spelling, got)
			}
		}
	}
}

// TestType_StringRoundTrip asserts Type.String() produces a spelling ParseType
// reverses exactly — a small canonical round-trip that guards both directions.
func TestType_StringRoundTrip(t *testing.T) {
	types := []sd.Type{
		{Base: sd.BaseSmallint},
		{Base: sd.BaseInteger},
		{Base: sd.BaseBigint},
		{Base: sd.BaseNumeric, Prec: 10, Scale: 2},
		{Base: sd.BaseNumeric, Prec: 8},
		{Base: sd.BaseReal},
		{Base: sd.BaseDouble},
		{Base: sd.BaseBool},
		{Base: sd.BaseText},
		{Base: sd.BaseVarchar, Size: 255},
		{Base: sd.BaseVarchar},
		{Base: sd.BaseChar, Size: 10},
		{Base: sd.BaseUUID},
		{Base: sd.BaseTimestamptz},
		{Base: sd.BaseTimestamp},
		{Base: sd.BaseTimestamptz, Prec: 3},
		{Base: sd.BaseTime},
		{Base: sd.BaseTimetz},
		{Base: sd.BaseDate},
		{Base: sd.BaseJSON},
		{Base: sd.BaseJSONB},
		{Base: sd.BaseBytea},
		{Base: sd.BaseInteger, Array: true},
		{Base: sd.BaseText, Array: true},
		{Base: sd.BaseUserDefined, UserName: "citext"},
	}
	for _, want := range types {
		s := want.String()
		got := sd.ParseType(s)
		if got != want {
			t.Errorf("round-trip failed: %+v -> String()=%q -> ParseType()=%+v", want, s, got)
		}
	}
}

// TestTypeForAPIType maps the Appitools schema-JSON type vocabulary to canonical
// types, mirroring the engine converger's fieldTypeToPG exactly.
func TestTypeForAPIType(t *testing.T) {
	cases := map[string]sd.Type{
		"string":  {Base: sd.BaseText},
		"text":    {Base: sd.BaseText},
		"int":     {Base: sd.BaseInteger},
		"int64":   {Base: sd.BaseBigint},
		"float64": {Base: sd.BaseDouble},
		"bool":    {Base: sd.BaseBool},
		"uuid":    {Base: sd.BaseUUID},
		"time":    {Base: sd.BaseTimestamptz},
		"json":    {Base: sd.BaseText}, // engine stores json as TEXT
		"weird":   {Base: sd.BaseText}, // converger default
	}
	for in, want := range cases {
		if got := sd.TypeForAPIType(in); got != want {
			t.Errorf("TypeForAPIType(%q) = %+v, want %+v", in, got, want)
		}
	}
}

// TestTypeForAPIType_MatchesParseType ties the two halves of the canonicalizer
// together: the canonical type the engine WOULD lay down for an Appitools field
// (TypeForAPIType) equals the canonical type Introspect WOULD read back from the
// Postgres column the converger creates (ParseType of fieldTypeToPG's output).
func TestTypeForAPIType_MatchesParseType(t *testing.T) {
	// apiType → the Postgres type string the converger's fieldTypeToPG emits.
	convergerPG := map[string]string{
		"string":  "text",
		"text":    "text",
		"int":     "integer",
		"int64":   "bigint",
		"float64": "double precision",
		"bool":    "boolean",
		"uuid":    "uuid",
		"time":    "timestamp with time zone",
		"json":    "text",
	}
	for apiType, pg := range convergerPG {
		desired := sd.TypeForAPIType(apiType)
		real := sd.ParseType(pg)
		if desired != real {
			t.Errorf("api %q: desired %+v != introspected %+v (pg %q)", apiType, desired, real, pg)
		}
	}
}
