package resilience

import (
	"sync"

	"golang.org/x/time/rate"
)

// Tier is a subscription plan that determines the allowed request rate.
type Tier string

const (
	TierFree Tier = "free" // 100 req/s
	TierPro  Tier = "pro"  // 1000 req/s
)

var tierLimits = map[Tier]rate.Limit{
	TierFree: 100,
	TierPro:  1000,
}

// RateLimitConfig sets an explicit per-tenant token-bucket policy, independent
// of subscription tier. Used to wire the limiter from environment configuration.
type RateLimitConfig struct {
	RPS   float64 // sustained requests per second, per tenant
	Burst int     // bucket capacity for short spikes
}

// TenantLimiter provides per-tenant token-bucket rate limiting.
// Each tenant gets an independent limiter keyed by tenantID.
// With a nil cfg the burst size equals the tier rate (1-second burst capacity);
// with cfg set, every tenant uses cfg.RPS / cfg.Burst and the tier is ignored.
type TenantLimiter struct {
	mu       sync.RWMutex
	limiters map[string]*rate.Limiter
	cfg      *RateLimitConfig
}

// NewTenantLimiter creates an empty tier-based TenantLimiter.
func NewTenantLimiter() *TenantLimiter {
	return &TenantLimiter{limiters: make(map[string]*rate.Limiter)}
}

// NewConfiguredLimiter creates a TenantLimiter that applies cfg to every tenant.
func NewConfiguredLimiter(cfg RateLimitConfig) *TenantLimiter {
	return &TenantLimiter{limiters: make(map[string]*rate.Limiter), cfg: &cfg}
}

// Allow reports whether tenantID may send another request under tier constraints.
// A new limiter is lazily created on first access for each tenantID.
func (tl *TenantLimiter) Allow(tenantID string, tier Tier) bool {
	tl.mu.RLock()
	lim, ok := tl.limiters[tenantID]
	tl.mu.RUnlock()

	if !ok {
		var r rate.Limit
		var burst int
		if tl.cfg != nil {
			// Explicit config takes precedence over tier.
			r = rate.Limit(tl.cfg.RPS)
			burst = tl.cfg.Burst
		} else {
			r = tierLimits[tier]
			if r == 0 {
				r = tierLimits[TierFree]
			}
			burst = int(r)
		}
		if burst < 1 {
			burst = 1
		}
		lim = rate.NewLimiter(r, burst)
		tl.mu.Lock()
		// Double-checked: another goroutine may have written while we held the read lock.
		if existing, found := tl.limiters[tenantID]; found {
			lim = existing
		} else {
			tl.limiters[tenantID] = lim
		}
		tl.mu.Unlock()
	}

	return lim.Allow()
}
