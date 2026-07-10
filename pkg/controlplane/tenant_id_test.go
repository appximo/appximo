package controlplane_test

import (
	"testing"

	"github.com/miguelangel/appitools/pkg/controlplane"
)

// Pure unit test (no Docker): the suggestion mirror of the tenant id rule.
func TestSuggestTenantID(t *testing.T) {
	cases := map[string]string{
		"punto-gafas-v1": "punto_gafas_v1", // the real-world bug input
		"Punto Gafas":    "punto_gafas",
		"ACME":           "acme",
		"9lives":         "lives", // leading digit trimmed (must start with a letter)
		"--":             "",      // nothing salvageable
		"a!b@c":          "abc",
		"ok_already":     "ok_already",
		"this-is-a-very-long-tenant-id-that-overflows": "this_is_a_very_long_tenant_id_", // capped at 30
	}
	for in, want := range cases {
		if got := controlplane.SuggestTenantID(in); got != want {
			t.Errorf("SuggestTenantID(%q) = %q, want %q", in, got, want)
		}
	}
}
