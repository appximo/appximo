package resilience

import (
	"fmt"
	"testing"
)

// TestTenantLimiter_CapBoundsMemory verifies that the per-tenant limiter map does
// not grow without bound when many distinct tenant ids are seen (the attack is an
// unauthenticated client rotating the Host subdomain). Past the cap, unknown
// tenants share one overflow bucket.
func TestTenantLimiter_CapBoundsMemory(t *testing.T) {
	tl := NewConfiguredLimiter(RateLimitConfig{RPS: 1000, Burst: 1000})
	tl.maxLimiters = 4 // shrink the cap for the test

	for i := 0; i < 1000; i++ {
		tl.Allow(fmt.Sprintf("tenant-%d", i), TierPro)
	}

	tl.mu.RLock()
	n := len(tl.limiters)
	overflow := tl.overflow
	tl.mu.RUnlock()

	if n > tl.maxLimiters {
		t.Fatalf("limiter map grew past cap: got %d entries, cap %d", n, tl.maxLimiters)
	}
	if overflow == nil {
		t.Fatal("expected a shared overflow limiter once the cap was reached")
	}
}

// TestTenantLimiter_KnownTenantsKeepOwnBucket confirms that under the cap each
// tenant still gets its own limiter (no behavior change for real deployments).
func TestTenantLimiter_KnownTenantsKeepOwnBucket(t *testing.T) {
	tl := NewConfiguredLimiter(RateLimitConfig{RPS: 1000, Burst: 1000})
	for i := 0; i < 50; i++ {
		tl.Allow(fmt.Sprintf("real-%d", i), TierPro)
	}
	tl.mu.RLock()
	n := len(tl.limiters)
	tl.mu.RUnlock()
	if n != 50 {
		t.Fatalf("expected 50 distinct limiters under the cap, got %d", n)
	}
}
