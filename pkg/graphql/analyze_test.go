package graphql

import (
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
	if got := safeDBErr(raw).Error(); strings.Contains(got, "secret_col") || got != "internal error" {
		t.Fatalf("safeDBErr leaked internals or wrong message: %q", got)
	}
	// Classified errors map to their safe messages.
	if got := safeDBErr(&pgconn.PgError{Code: "42P01"}).Error(); got != "invalid tenant" {
		t.Fatalf("missing-tenant mapping: got %q", got)
	}
	if got := safeDBErr(&pgconn.PgError{Code: "22P02"}).Error(); got != "invalid request" {
		t.Fatalf("bad-input mapping: got %q", got)
	}
}
