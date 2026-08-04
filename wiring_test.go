package appximo

import (
	"os"
	"strings"
	"testing"
)

// These guard tests fail if a security/resilience fix is disconnected from the
// engine's middleware wiring during a future refactor. They assert the call
// sites exist in non-comment source of app.go (the bootstrap moved here from
// cmd/appximo/cmd_serve.go in the ADR-016 library extraction — same guard,
// new home). A purely behavioural test would require standing up the full
// DB-backed production router; a source guard is a cheap, deterministic
// backstop for the one-line wiring that is otherwise easy to delete by
// accident. The behaviour of each fix is covered by unit tests in pkg/cache,
// pkg/resilience and pkg/db.

func TestWiring_RateLimiterMounted(t *testing.T) {
	requireWired(t, "app.go", "resilience.RateLimit(", "per-tenant rate-limit middleware (FIX 3)")
	requireWired(t, "app.go", "NewConfiguredLimiter(", "configured per-tenant limiter (FIX 3)")
}

func TestWiring_RBACCacheGateMounted(t *testing.T) {
	requireWired(t, "app.go", "SetRoleCacheGate(", "RBAC response-cache role gate (FIX 1)")
}

func TestWiring_AdminReloadMounted(t *testing.T) {
	requireWired(t, "app.go", `"/admin/tenants/{id}/reload"`, "ADR-014 manual schema reload endpoint")
	requireWired(t, "app.go", "reloadHandler(", "ADR-014 reload handler wiring")
}

// TestWiring_CustomRoutesShareChain guards the ADR-016 invariant that custom
// routes mount INSIDE the same /api group as the generated router (no
// duplicated middleware chain).
func TestWiring_CustomRoutesShareChain(t *testing.T) {
	requireWired(t, "app.go", "a.customHandler(rt)", "custom routes mounted in the shared middleware chain (ADR-016)")
	requireWired(t, "app.go", "codegen.BuildRouter(", "generated router mounted alongside custom routes")
}

// requireWired fails unless needle appears on a non-comment line of file.
func requireWired(t *testing.T, file, needle, what string) {
	t.Helper()
	src, err := os.ReadFile(file)
	if err != nil {
		t.Fatalf("read %s: %v", file, err)
	}
	for _, line := range strings.Split(string(src), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "//") {
			continue
		}
		if strings.Contains(line, needle) {
			return
		}
	}
	t.Fatalf("WIRING REGRESSION: %q (%s) not found in non-comment %s — the fix may be disconnected", needle, what, file)
}
