package controlplane

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestTenantIDRuleSingleSource pins field-report finding T1: the tenant-id rule
// lives ONCE (tenantIDRe above — the creation authority) and every UI mirror
// must carry the IDENTICAL pattern. The evaluator hit a Studio message claiming
// underscores were legal while the API rejected them — a hand-maintained copy
// that diverged silently. This test makes that class of divergence fail the
// build instead: it reads the two SPA sources and compares their literal
// pattern against the authority, and rejects any resurrection of the stale
// "digits or '_'" wording near a tenant-id message.
//
// (pkg/platformadmin's LOOKUP-side tenantIDRe is deliberately looser — legacy
// hyphenated tenants must remain deletable — and is documented there; it is a
// guard before Sanitize(), never a creation rule, so it is not pinned here.)
func TestTenantIDRuleSingleSource(t *testing.T) {
	authority := tenantIDRe.String() // ^[a-z][a-z0-9]{1,29}$

	mirrors := []string{
		filepath.Join("..", "editorui", "web", "src", "lib", "stores", "deploy.svelte.ts"),
		filepath.Join("..", "adminui", "web", "src", "routes", "Tenants.jsx"),
	}
	// A JS regex literal /^[a-z]...$/ carrying the same pattern body.
	want := "/" + authority + "/"
	reLiteral := regexp.MustCompile(`TENANT_ID_RE\s*=\s*(/[^/]+/)`)

	for _, path := range mirrors {
		src, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read UI mirror %s: %v (moved? update this pin)", path, err)
		}
		m := reLiteral.FindSubmatch(src)
		if m == nil {
			t.Fatalf("%s: no TENANT_ID_RE literal found (renamed? update this pin)", path)
		}
		if got := string(m[1]); got != want {
			t.Errorf("%s: TENANT_ID_RE %s diverges from the controlplane authority %s (T1: one rule, compiled to every surface)", path, got, want)
		}
		if strings.Contains(string(src), "digits or '_'") {
			t.Errorf("%s: carries the stale \"digits or '_'\" tenant-id wording — underscores are NOT legal (T1)", path)
		}
	}
}
