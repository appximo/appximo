package integration_test

import (
	"context"
	"net/url"
	"testing"

	"github.com/appximo/appximo/pkg/query"
	"github.com/appximo/appximo/pkg/schema"
)

// TestFilterValueLivePostgresConformance is the live half of the ENG-25
// guarantee: the filter-value acceptors must be AT LEAST as permissive as
// Postgres itself. For every (type, value) pair below it asks the real
// database whether the cast succeeds, then asserts the one direction that
// matters — Postgres accepts ⇒ the validator accepts. (The reverse is the
// documented safe direction: a value the validator lets through that Postgres
// then rejects gets the pre-existing masked 400, just without the field name.)
//
// The corpus deliberately includes the spellings that made a Go-semantics
// check unshippable ("yes" as a boolean), plus whitespace, prefixes, special
// floats, version-dependent literal forms (0x…, digit underscores — accepted
// by PG16, rejected by PG15; the assertion adapts because it ASKS the server),
// and garbage. The validator is exercised through BuildQuery — the real
// surface — never called directly.
func TestFilterValueLivePostgresConformance(t *testing.T) {
	if testing.Short() {
		t.Skip("filter conformance: skipping in -short mode")
	}
	pool, cleanPG := startPG(t)
	defer cleanPG()
	ctx := context.Background()

	corpus := map[string]struct {
		pgType string
		values []string
	}{
		"int64": {"int8", []string{
			"7", "+7", "-7", " 42 ", "\t42\n", "0129", "007", "0", "-0",
			"9223372036854775807", "-9223372036854775808", "9223372036854775808",
			"0x2A", "0o17", "0b101", "1_000_000", "1_0",
			"abc", "1.5", "1e5", " ", "12abc", "--5", "+-5", "١٢٣",
		}},
		"int": {"int4", []string{
			"7", "-2147483648", "2147483647", "2147483648", " 5 ", "0099", "abc", "5.0",
		}},
		"float64": {"float8", []string{
			"1.5", "-1.5e10", ".5", "5.", "+3", " 2.5 ", "0129.5",
			"Infinity", "-Infinity", "inf", "-inf", "+inf", "NaN", "nan", "-NaN", "+NaN",
			"1e5", "1E-5", "1e308", "1e999", "1_000.5",
			"abc", "1.2.3", "infinite", "0x1p3",
		}},
		"bool": {"boolean", []string{
			"t", "tr", "tru", "true", "TRUE", "True", "f", "fa", "fal", "fals", "false",
			"y", "ye", "yes", "YES", "n", "no", "NO", "on", "ON", "o", "of", "off", "OFF",
			"1", "0", " true ", "\tyes\n", "2", "-1", "yep", "si", "truefalse",
		}},
		"uuid": {"uuid", []string{
			"a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a11",
			"A0EEBC99-9C0B-4EF8-BB6D-6BB9BD380A11",
			"{a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a11}",
			"a0eebc999c0b4ef8bb6d6bb9bd380a11",
			"a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a1", "not-a-uuid", "",
		}},
		"jsonb": {"jsonb", []string{
			`{"a":1}`, `[1,2]`, `"s"`, `7`, `true`, `null`, `{"a":`, `{'a':1}`, `nope`,
		}},
	}

	for fieldType, c := range corpus {
		res := &schema.ResourceSchema{Fields: map[string]schema.FieldDef{
			"f": {Type: fieldType},
		}}
		for _, v := range c.values {
			// Does Postgres accept this value for the type? Bound as text and
			// cast server-side — the same conversion the filter's bound
			// parameter goes through.
			var pgAccepts bool
			row := pool.QueryRow(ctx, "SELECT ($1::text)::"+c.pgType+" IS NOT NULL OR TRUE", v)
			pgAccepts = row.Scan(new(bool)) == nil

			// Does the engine accept it? Through the real surface.
			params := url.Values{}
			params.Set("filter[f][eq]", v)
			_, err := query.BuildQuery("things", res, params, nil)
			engineAccepts := err == nil

			if pgAccepts && !engineAccepts {
				t.Errorf("%s: STRICTER THAN POSTGRES — PG accepts %q, engine rejects it: %v",
					fieldType, v, err)
			}
			// Not asserted: engineAccepts && !pgAccepts (documented safe
			// direction — the DB still rejects it, unnamed). The "" entries
			// exercise the empty-value rule: both sides reject, so the
			// conformance direction holds trivially.
		}
	}
}
