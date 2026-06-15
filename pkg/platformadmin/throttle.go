package platformadmin

import (
	"sync"

	"golang.org/x/time/rate"
)

// loginThrottle bounds login / mfa-verify attempts per key (an admin email or id)
// — an online brute-force defence on a privileged credential, on top of any
// network-level limiting. It is the same shape as the per-tenant request limiter
// (token bucket per key) but tiny: the key space is the handful of platform
// admins, so a plain map under a mutex is enough (bounded, no eviction needed).
type loginThrottle struct {
	mu      sync.Mutex
	buckets map[string]*rate.Limiter
	perMin  rate.Limit
	burst   int
}

func newLoginThrottle(perMinute, burst int) *loginThrottle {
	if perMinute <= 0 {
		perMinute = 5
	}
	if burst <= 0 {
		burst = 5
	}
	return &loginThrottle{
		buckets: make(map[string]*rate.Limiter),
		perMin:  rate.Limit(float64(perMinute) / 60.0),
		burst:   burst,
	}
}

// allow reports whether an attempt for key is permitted right now.
func (t *loginThrottle) allow(key string) bool {
	t.mu.Lock()
	lim, ok := t.buckets[key]
	if !ok {
		lim = rate.NewLimiter(t.perMin, t.burst)
		t.buckets[key] = lim
	}
	t.mu.Unlock()
	return lim.Allow()
}
