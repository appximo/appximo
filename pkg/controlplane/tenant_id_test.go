package controlplane_test

import (
	"regexp"
	"testing"

	"github.com/miguelangel/appitools/pkg/controlplane"
)

// theRule is the id alphabet the engine accepts, duplicated here on purpose: the
// test asserts the SUGGESTION always satisfies it, so the two can never drift into
// "we recommended something we then reject" — which is exactly what ENG-11 was.
var theRule = regexp.MustCompile(`^[a-z][a-z0-9]{1,29}$`)

// Pure unit test (no Docker): the suggestion mirror of the tenant id rule.
//
// ENG-11: the rule is the INTERSECTION of the Postgres-schema alphabet (no hyphens)
// and the DNS-label alphabet (no underscores), so separators are DROPPED, not turned
// into '_'. An underscored id used to register fine and then answer 400 on every
// request, and the suggestion here was one of the places recommending it.
func TestSuggestTenantID(t *testing.T) {
	cases := map[string]string{
		"punto-gafas-v1": "puntogafasv1", // the real-world bug input
		"Punto Gafas":    "puntogafas",
		"ACME":           "acme",
		"9lives":         "lives", // leading digit trimmed (must start with a letter)
		"--":             "",      // nothing salvageable
		"a!b@c":          "abc",
		"ok_already":     "okalready", // an underscore is NOT a valid DNS label character
		"bench_blank":    "benchblank",
		"this-is-a-very-long-tenant-id-that-overflows": "thisisaverylongtenantidthatove", // capped at 30
	}
	for in, want := range cases {
		got := controlplane.SuggestTenantID(in)
		if got != want {
			t.Errorf("SuggestTenantID(%q) = %q, want %q", in, got, want)
		}
		if got != "" && !theRule.MatchString(got) {
			t.Errorf("SuggestTenantID(%q) = %q, which the engine would REJECT — a suggestion must be usable", in, got)
		}
	}
}
