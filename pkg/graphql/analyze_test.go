package graphql

import (
	"context"
	"strconv"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
)

func TestAnalyzeQuery(t *testing.T) {
	cases := []struct {
		name      string
		query     string
		isGet     bool
		isDev     bool
		wantOK    bool
		reasonHas string
	}{
		{"normal query", `{guides{data{id code}}}`, false, false, true, ""},
		{"typename allowed", `{guides{__typename data{id}}}`, false, false, true, ""},
		{"introspection blocked in prod", `{__schema{types{name}}}`, false, false, false, "introspection"},
		{"introspection allowed in dev", `{__schema{types{name}}}`, false, true, true, ""},
		{"__type blocked in prod", `{__type(name:"Guide"){name}}`, false, false, false, "introspection"},
		{"introspection via root fragment blocked", `query{...f} fragment f on Query{__schema{types{name}}}`, false, false, false, "introspection"},
		{"mutation on GET rejected", `mutation{createGuide(input:{code:"x"}){id}}`, true, false, false, "POST"},
		{"mutation on POST allowed", `mutation{createGuide(input:{code:"x"}){id}}`, false, false, true, ""},
		{"empty query passes", ``, false, false, true, ""},
		{"syntax error passes through", `{guides{`, false, false, true, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			reason, ok := analyzeQuery(c.query, c.isGet, c.isDev)
			if ok != c.wantOK {
				t.Fatalf("ok=%v want %v (reason=%q)", ok, c.wantOK, reason)
			}
			if c.reasonHas != "" && !strings.Contains(reason, c.reasonHas) {
				t.Fatalf("reason %q does not contain %q", reason, c.reasonHas)
			}
		})
	}
}

func TestAnalyzeQuery_AliasAmplificationRejected(t *testing.T) {
	var sb strings.Builder
	sb.WriteString("{")
	for i := 0; i < maxRootSelections+5; i++ {
		sb.WriteString("a" + strconv.Itoa(i) + ":guides{data{id}} ")
	}
	sb.WriteString("}")
	if reason, ok := analyzeQuery(sb.String(), false, false); ok {
		t.Fatalf("expected alias-amplification query to be rejected, got ok (reason=%q)", reason)
	}
}

func TestSafeDBErr_MasksInternals(t *testing.T) {
	// A raw unknown-column PG error must be masked to a generic message.
	raw := &pgconn.PgError{Code: "42703", Message: `column "secret_col" does not exist`}
	if got := safeDBErr(context.Background(), raw).Error(); strings.Contains(got, "secret_col") || got != "internal error" {
		t.Fatalf("safeDBErr leaked internals or wrong message: %q", got)
	}
	// Classified errors map to their safe messages.
	if got := safeDBErr(context.Background(), &pgconn.PgError{Code: "42P01"}).Error(); got != "invalid tenant" {
		t.Fatalf("missing-tenant mapping: got %q", got)
	}
	if got := safeDBErr(context.Background(), &pgconn.PgError{Code: "22P02"}).Error(); got != "invalid request" {
		t.Fatalf("bad-input mapping: got %q", got)
	}
}

// TestAnalyzeQuery_FragmentSpreadCharged pins ENG-28: a fragment's cost is
// charged at EVERY spread site, so counted >= resolved. The old counter walked
// each fragment body once globally, and the measured bypass — one 40-field
// fragment spread across 50 distinct root aliases — passed the analyzer at a
// count of ~90 while the executor resolved ~46× the advertised 2000-selection
// cap (measured: ~92,500 selections, 21.4 MB from one request).
func TestAnalyzeQuery_FragmentSpreadCharged(t *testing.T) {
	// Fragment with 45 fields, spread across 50 root aliases: true cost
	// 50×(1+45) = 2300 > 2000. The old counter saw 50 + 45 = 95 and passed it.
	var frag strings.Builder
	frag.WriteString("fragment F on Guide {")
	for i := 0; i < 45; i++ {
		frag.WriteString(" f" + strconv.Itoa(i))
	}
	frag.WriteString(" }")

	var q strings.Builder
	q.WriteString("query {")
	for i := 0; i < 50; i++ {
		q.WriteString(" a" + strconv.Itoa(i) + ":guides{...F}")
	}
	q.WriteString(" } ")
	q.WriteString(frag.String())

	if reason, ok := analyzeQuery(q.String(), false, false); ok {
		t.Fatalf("fragment-amplified document must be rejected, got ok (reason=%q)", reason)
	}

	// The same shape UNDER the cap keeps working: 10 aliases × 46 = 460.
	var small strings.Builder
	small.WriteString("query {")
	for i := 0; i < 10; i++ {
		small.WriteString(" a" + strconv.Itoa(i) + ":guides{...F}")
	}
	small.WriteString(" } ")
	small.WriteString(frag.String())
	if reason, ok := analyzeQuery(small.String(), false, false); !ok {
		t.Fatalf("under-cap fragment document must pass, got %q", reason)
	}

	// Nested fragments are charged transitively.
	nested := `query { a1:guides{...A} a2:guides{...A} } fragment A on Guide { x ...B } fragment B on Guide { y z }`
	if _, ok := analyzeQuery(nested, false, false); !ok {
		t.Fatal("small nested fragment doc must pass")
	}

	// A fragment CYCLE must not hang or panic — the executor reports it.
	cycle := `query { a:guides{...A} } fragment A on Guide { ...B } fragment B on Guide { ...A }`
	if _, ok := analyzeQuery(cycle, false, false); !ok {
		t.Fatal("cyclic fragments are the executor's error to report, not the analyzer's")
	}

	// Introspection hidden in an UNUSED fragment is still detected.
	unused := `query { __typename } fragment Z on Query { __schema { types { name } } }`
	if reason, ok := analyzeQuery(unused, false, false); ok || !strings.Contains(reason, "introspection") {
		t.Fatalf("introspection in unused fragment must stay blocked, got ok=%v reason=%q", ok, reason)
	}
}
