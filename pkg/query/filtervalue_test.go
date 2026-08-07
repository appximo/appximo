package query

import (
	"net/url"
	"strings"
	"testing"

	"github.com/appximo/appximo/pkg/schema"
)

func typedResource() *schema.ResourceSchema {
	return &schema.ResourceSchema{
		Fields: map[string]schema.FieldDef{
			"title":  {Type: "string"},
			"amount": {Type: "int64"},
			"ratio":  {Type: "float64"},
			"done":   {Type: "bool"},
			"due":    {Type: "time"},
			"ref":    {Type: "uuid"},
			"attach": {Type: "file"},
			"attrs":  {Type: "jsonb"},
			"blob":   {Type: "json"},
		},
	}
}

// TestFilterValue_PostgresConformance pins the ENG-25 direction that matters:
// every value POSTGRES accepts for the type must be accepted here. The corpus
// below is drawn from Postgres's own input grammars (parse_bool, int8in,
// float8in, uuid_in) — including the spellings that made a Go-semantics check
// unshippable (`yes` as a boolean was MEASURED returning 200 pre-fix). The
// reverse direction is deliberately loose: a value we accept that Postgres
// rejects still gets the pre-existing 400, just unnamed. The live cross-check
// against a real Postgres is TestFilterValueLivePostgresConformance in
// pkg/integration.
func TestFilterValue_PostgresConformance(t *testing.T) {
	accepted := map[string][]string{
		"int64": {"7", "+7", "-7", " 42 ", "\t42\n", "0129", "007",
			"9223372036854775807", "-9223372036854775808",
			"0x2A", "0o17", "0b101", "1_000_000"},
		"float64": {"1.5", "-1.5e10", ".5", "5.", "+3", " 2.5 ",
			"Infinity", "-Infinity", "inf", "-inf", "+inf", "NaN", "nan", "-NaN",
			"1e5", "1E-5", "0129.5"},
		"bool": {"t", "tr", "tru", "true", "TRUE", "True",
			"f", "fa", "fal", "fals", "false", "FALSE",
			"y", "ye", "yes", "YES", "n", "no", "NO",
			"on", "ON", "of", "off", "OFF", "1", "0", " true ", "\tyes\n"},
		"uuid": {"a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a11",
			"A0EEBC99-9C0B-4EF8-BB6D-6BB9BD380A11",
			"{a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a11}",
			"a0eebc999c0b4ef8bb6d6bb9bd380a11"},
		"file":  {"a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a11"},
		"jsonb": {`{"brand":"Acme"}`, `[1,2,3]`, `"str"`, `7`, `true`, `null`},
	}
	for fieldType, values := range accepted {
		for _, v := range values {
			if err := validateFilterValue("f", "eq", fieldType, v); err != nil {
				t.Errorf("%s: Postgres accepts %q, validator rejected it: %v", fieldType, v, err)
			}
		}
	}

	// Values NO Postgres version accepts — these must be rejected with a message
	// naming the parameter, the value and the type (the whole point of ENG-25).
	rejected := map[string][]string{
		"int64":   {"abc", "1.5", "1e5", "", " ", "12abc", "9999999999999999999999999", "--5"},
		"float64": {"abc", "1.2.3", "", "infinite", "1e999"},
		"bool":    {"o", "yep", "si", "2", "-1", "truefalse", ""},
		"uuid":    {"abc", "a0eebc99", "zzeebc99-9c0b-4ef8-bb6d-6bb9bd380a11", ""},
		"jsonb":   {`{"brand":`, `{'single':1}`, ``},
	}
	for fieldType, values := range rejected {
		for _, v := range values {
			err := validateFilterValue("amount", "gt", fieldType, v)
			if err == nil {
				t.Errorf("%s: %q should be rejected", fieldType, v)
				continue
			}
			for _, want := range []string{"filter[amount][gt]", fieldType} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("%s %q: error %q does not name %q", fieldType, v, err.Error(), want)
				}
			}
		}
	}

	// time and string/text/json are DELIBERATELY not validated (documented
	// leniency — Postgres's timestamp grammar is not reproducible safely).
	for _, ft := range []string{"time", "string", "text", "json"} {
		if err := validateFilterValue("f", "eq", ft, "anything at all"); err != nil {
			t.Errorf("%s must not be validated in Go, got: %v", ft, err)
		}
	}
}

// TestBuildQuery_WronglyTypedFilterValueIsNamed pins the request-level contract:
// the 400 names the parameter, the offending value and the expected type, and a
// request with several filters is told WHICH one was rejected (the original
// ENG-25 complaint — `400 invalid request` named nothing).
func TestBuildQuery_WronglyTypedFilterValueIsNamed(t *testing.T) {
	params, _ := url.ParseQuery("filter[title][eq]=ok&filter[amount][gt]=abc")
	_, err := BuildQuery("things", typedResource(), params, nil, nil)
	if err == nil {
		t.Fatal("expected error for filter[amount][gt]=abc")
	}
	for _, want := range []string{"filter[amount][gt]", `"abc"`, "int64"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q does not contain %q", err.Error(), want)
		}
	}

	// The measured constraint that shaped the whole design: `yes` IS a boolean
	// in Postgres, so it must keep working (it returned 200 pre-fix).
	params, _ = url.ParseQuery("filter[done][eq]=yes")
	if _, err := BuildQuery("things", typedResource(), params, nil, nil); err != nil {
		t.Fatalf("filter[done][eq]=yes must stay accepted (Postgres accepts it): %v", err)
	}
}

// TestBuildQuery_FilterByID pins ENG-26: `id` — the implicit primary key — is
// filterable like the uuid column it is, consistent with `?sort=id` and the
// keyset cursors, and it composes with other filters.
func TestBuildQuery_FilterByID(t *testing.T) {
	params, _ := url.ParseQuery("filter[id][eq]=a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a11&filter[title][eq]=x")
	qb, err := BuildQuery("things", typedResource(), params, nil, nil)
	if err != nil {
		t.Fatalf("filter[id][eq]=<uuid> must be accepted: %v", err)
	}
	sel, _, _, _ := qb.SQL()
	if !strings.Contains(sel, "id = $") {
		t.Fatalf("SQL missing id filter: %s", sel)
	}

	// Non-eq ops stay rejected (uuid's operator set), naming the set.
	params, _ = url.ParseQuery("filter[id][gt]=a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a11")
	if _, err := BuildQuery("things", typedResource(), params, nil, nil); err == nil {
		t.Fatal("filter[id][gt] must be rejected (uuid: eq only)")
	}

	// A wrongly-typed id value is the named ENG-25 error, not a DB error.
	params, _ = url.ParseQuery("filter[id][eq]=not-a-uuid")
	err = BuildQuery2Err(t, params)
	if err == nil || !strings.Contains(err.Error(), "uuid") {
		t.Fatalf("bad id value must be a named 400: %v", err)
	}

	// And the unknown-field message may name id as available, truthfully now.
	params, _ = url.ParseQuery("filter[ghost][eq]=x")
	_, err = BuildQuery("things", typedResource(), params, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "id") {
		t.Fatalf("available list should include id: %v", err)
	}
}

func BuildQuery2Err(t *testing.T, params url.Values) error {
	t.Helper()
	_, err := BuildQuery("things", typedResource(), params, nil, nil)
	return err
}

// TestBuildQuery_EmptyOwnedParamsAreRejected pins ENG-30: an EMPTY value on a
// parameter the engine owns is rejected by the same rule as its siblings —
// `?page=` (an empty form field) was silently served as page 1 while `?page=0`
// was already a named 400.
func TestBuildQuery_EmptyOwnedParamsAreRejected(t *testing.T) {
	cases := []struct {
		query    string
		wantSubs []string
	}{
		{"page=", []string{"page", "positive integer"}},
		{"per_page=", []string{"per_page", "positive integer"}},
		{"sort=", []string{"sort", "available:"}},
		{"sort=title&order=", []string{"sort direction", "asc or desc"}},
		// A direction with no field was read by nothing and silently dropped.
		{"order=desc", []string{"order", "requires sort"}},
		{"order=", []string{"order", "requires sort"}},
	}
	for _, c := range cases {
		params, _ := url.ParseQuery(c.query)
		_, err := BuildQuery("things", typedResource(), params, nil, nil)
		if err == nil {
			t.Errorf("?%s: expected a named 400, got nil", c.query)
			continue
		}
		for _, want := range c.wantSubs {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("?%s: error %q does not contain %q", c.query, err.Error(), want)
			}
		}
	}

	// Absent parameters still default silently — presence is the gate.
	params, _ := url.ParseQuery("filter[title][eq]=x")
	qb, err := BuildQuery("things", typedResource(), params, nil, nil)
	if err != nil || qb.Page() != DefaultPage || qb.PerPage() != DefaultPerPage {
		t.Fatalf("absent params must keep their defaults: %v", err)
	}
	// And ?sort=field&order=desc keeps working.
	params, _ = url.ParseQuery("sort=title&order=desc")
	if _, err := BuildQuery("things", typedResource(), params, nil, nil); err != nil {
		t.Fatalf("sort+order must keep working: %v", err)
	}
}
