package main

import (
	"os"
	"strings"
	"testing"
)

// These guard tests fail if a security/resilience fix is disconnected from the
// server's middleware wiring during a future refactor. They assert the call
// sites exist in non-comment source of cmd_serve.go. A purely behavioural test
// would require standing up the full DB-backed production router; a source guard
// is a cheap, deterministic backstop for the one-line wiring that is otherwise
// easy to delete by accident. The behaviour of each fix is covered by unit tests
// in pkg/cache, pkg/resilience and pkg/db.

func TestWiring_RateLimiterMounted(t *testing.T) {
	requireWired(t, "cmd_serve.go", "resilience.RateLimit(", "per-tenant rate-limit middleware (FIX 3)")
	requireWired(t, "cmd_serve.go", "NewConfiguredLimiter(", "configured per-tenant limiter (FIX 3)")
}

func TestWiring_RBACCacheGateMounted(t *testing.T) {
	requireWired(t, "cmd_serve.go", "SetRoleCacheGate(", "RBAC response-cache role gate (FIX 1)")
}

func TestWiring_AdminReloadMounted(t *testing.T) {
	requireWired(t, "cmd_serve.go", `"/admin/tenants/{id}/reload"`, "ADR-014 manual schema reload endpoint")
	requireWired(t, "cmd_serve.go", "reloadHandler(cpSvc, responseCache, schemaCache, s)", "ADR-014 reload handler wiring")
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
