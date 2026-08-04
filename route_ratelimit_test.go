package appximo

import (
	"strings"
	"testing"

	"github.com/appximo/appximo/pkg/resilience"
)

// LIBRARY-GAPS-S1 — Route.RateLimit: a per-endpoint throttle.
//
// The default (5 rps / burst 10) is right for a public WRITE endpoint and wrong
// for a public READ one — a storefront catalogue trips it under ordinary traffic.
// The fix must be opt-in per route and must NOT move the conservative default.

func TestRouteLimiter_Selection(t *testing.T) {
	app := &App{
		schema:        tasksSchema(),
		publicLimiter: resilience.NewConfiguredLimiter(resilience.RateLimitConfig{RPS: 5, Burst: 10}),
	}

	t.Run("an authenticated route with no declaration has NO dedicated limiter", func(t *testing.T) {
		if lim := app.routeLimiter(Route{Method: "GET", Path: "/api/reports"}); lim != nil {
			t.Fatal("unchanged behavior: only the per-tenant limiter applies")
		}
	})

	t.Run("a public route with no declaration uses the SHARED conservative limiter", func(t *testing.T) {
		lim := app.routeLimiter(Route{Method: "POST", Path: "/api/register", Public: true})
		if lim != app.publicLimiter {
			t.Fatal("a public route must keep the engine default when it declares nothing")
		}
	})

	t.Run("a declaration gives the route its OWN limiter", func(t *testing.T) {
		a := app.routeLimiter(Route{Method: "GET", Path: "/api/catalogue", Public: true,
			RateLimit: &RateLimit{RPS: 200, Burst: 400}})
		b := app.routeLimiter(Route{Method: "GET", Path: "/api/other", Public: true,
			RateLimit: &RateLimit{RPS: 200, Burst: 400}})
		if a == nil || a == app.publicLimiter {
			t.Fatal("a declared limit must not fall back to the shared bucket")
		}
		if a == b {
			t.Fatal("two routes must never share a bucket (one route's burst would starve the other)")
		}
	})
}

// The two coexist in one engine: the declared route absorbs a burst the default
// route rejects, at the same instant, in the same tenant.
func TestRouteLimiter_HighAndDefaultCoexist(t *testing.T) {
	app := &App{
		schema:        tasksSchema(),
		publicLimiter: resilience.NewConfiguredLimiter(resilience.RateLimitConfig{RPS: 5, Burst: 10}),
	}
	catalogue := app.routeLimiter(Route{Method: "GET", Path: "/api/catalogue", Public: true,
		RateLimit: &RateLimit{RPS: 500, Burst: 500}})
	register := app.routeLimiter(Route{Method: "POST", Path: "/api/register", Public: true})

	const burst = 100
	key := "acme|10.0.0.1"
	allowedCatalogue, allowedRegister := 0, 0
	for i := 0; i < burst; i++ {
		if catalogue.Allow(key, "") {
			allowedCatalogue++
		}
		if register.Allow(key, "") {
			allowedRegister++
		}
	}
	if allowedCatalogue != burst {
		t.Fatalf("the high-limit catalogue must absorb the burst: %d/%d allowed", allowedCatalogue, burst)
	}
	if allowedRegister > 15 { // burst 10 + whatever the clock refilled
		t.Fatalf("the default-limit route must still throttle: %d/%d allowed", allowedRegister, burst)
	}
}

func TestRouteLimit_PartialDeclarationRejectedAtBoot(t *testing.T) {
	for _, rl := range []*RateLimit{{RPS: 0, Burst: 10}, {RPS: 100, Burst: 0}, {}} {
		app := &App{schema: tasksSchema()}
		err := app.Register(Route{Method: "GET", Path: "/api/catalogue", Handler: noopHandler, RateLimit: rl})
		if err == nil || !strings.Contains(err.Error(), "RateLimit requires RPS > 0 and Burst > 0") {
			t.Fatalf("a half-filled RateLimit must fail at boot (it would silently mean 'reject everything'), got: %v", err)
		}
	}
}

func TestRouteLimit_ValidDeclarationRegisters(t *testing.T) {
	app := &App{schema: tasksSchema()}
	if err := app.Register(Route{Method: "GET", Path: "/api/catalogue", Public: true,
		Handler: noopHandler, RateLimit: &RateLimit{RPS: 200, Burst: 400}}); err != nil {
		t.Fatalf("a well-formed RateLimit must register: %v", err)
	}
}
