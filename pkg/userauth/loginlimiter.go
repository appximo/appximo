package userauth

import (
	"strings"
	"sync"

	"golang.org/x/time/rate"
)

// loginLimiter throttles login attempts per (tenant, email) to blunt online
// brute-force / credential-stuffing against a single account. It is layered ON
// TOP of the engine's per-tenant request rate limiter (resilience.TenantLimiter,
// ~1000 RPS) — that one bounds total tenant traffic; this one bounds repeated
// guesses at one identity at a far tighter rate.
//
// The key includes the email, which is client-controlled and evaluated BEFORE
// authentication, so the map MUST be bounded — otherwise an attacker rotating the
// email would grow it without limit (pre-auth memory exhaustion). Past the cap,
// extra keys share one overflow bucket: memory stays bounded and the limit still
// applies (same defence as resilience.TenantLimiter).
type loginLimiter struct {
	mu       sync.Mutex
	buckets  map[string]*rate.Limiter
	overflow *rate.Limiter
	limit    rate.Limit
	burst    int
	maxKeys  int
}

// newLoginLimiter builds a limiter allowing `burst` immediate attempts and then
// `perMinute` sustained attempts per (tenant, email).
func newLoginLimiter(perMinute, burst int) *loginLimiter {
	if perMinute < 1 {
		perMinute = 5
	}
	if burst < 1 {
		burst = perMinute
	}
	return &loginLimiter{
		buckets: make(map[string]*rate.Limiter),
		limit:   rate.Limit(float64(perMinute) / 60.0),
		burst:   burst,
		maxKeys: 50_000,
	}
}

// allow reports whether another login attempt for (tenantID, email) is permitted.
func (l *loginLimiter) allow(tenantID, email string) bool {
	key := tenantID + "|" + strings.ToLower(strings.TrimSpace(email))
	l.mu.Lock()
	lim, ok := l.buckets[key]
	if !ok {
		if len(l.buckets) >= l.maxKeys {
			if l.overflow == nil {
				l.overflow = rate.NewLimiter(l.limit, l.burst)
			}
			lim = l.overflow
		} else {
			lim = rate.NewLimiter(l.limit, l.burst)
			l.buckets[key] = lim
		}
	}
	l.mu.Unlock()
	return lim.Allow()
}
