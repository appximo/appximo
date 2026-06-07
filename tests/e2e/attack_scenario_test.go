//go:build e2e

package e2e_test

import (
	"net/http"
	"testing"
)

// TestAttackScenario fires adversarial payloads at the real middleware chain — the
// most launch-critical scenario for the OSS release. Every assertion reflects the
// engine's ACTUAL behaviour (verified against source), including one documented
// bug we assert rather than paper over.
//
// Fixture: tests/fixtures/schemas/logistics_schema.json (has a `guides` resource).
func TestAttackScenario(t *testing.T) {
	const tenantID = "atktenant"
	srv, metrics, _ := newE2EServer(t, "logistics_schema.json", tenantID)
	e := newHTTPExpect(t, srv, tenantID)

	admin := "Bearer " + mintJWT(t, tenantID, "super_admin")

	// ── SQL injection in a filter value ─────────────────────────────────────────
	// The query builder binds filter values as parameters (never string-concatenated),
	// so the payload is treated as a literal `code` to match → 0 rows, 200, and the
	// table is untouched.
	e.GET("/api/guides").
		WithHeader("Authorization", admin).
		WithQuery("filter[code][eq]", "'; DROP TABLE guides; --").
		Expect().Status(http.StatusOK)

	// The table is still alive after the injection attempt.
	e.GET("/api/guides").
		WithHeader("Authorization", admin).
		Expect().Status(http.StatusOK).
		JSON().Object().ContainsKey("data")

	// ── JWT alg:none (alg-confusion bypass) → 401 ───────────────────────────────
	// auth.ValidateToken pins HS256, so an unsigned alg:none token is rejected.
	e.GET("/api/guides").
		WithHeader("Authorization", "Bearer "+mintAlgNoneJWT(t, tenantID, "super_admin")).
		Expect().Status(http.StatusUnauthorized)

	// ── JWT expired → 401 ───────────────────────────────────────────────────────
	// Correctly signed, but exp is in the past; auth.WithExpirationRequired enforces it.
	e.GET("/api/guides").
		WithHeader("Authorization", "Bearer "+mintExpiredJWT(t, tenantID, "super_admin")).
		Expect().Status(http.StatusUnauthorized)

	// ── Oversized body (1.1 MB) → 400 (NOT 413) ─────────────────────────────────
	// DOCUMENTED BUG (confirmed by the S33/S34 audit and re-verified in
	// pkg/codegen/builder.go: the POST/create handler decodes the body and reports
	// the MaxBytesReader overflow via http.Error(..., StatusBadRequest), so it
	// returns 400. The PUT/PATCH handlers DO distinguish and return 413. We assert
	// the real 400 here and do NOT change engine code in this session.
	bigBody := make([]byte, 1_100_000)
	e.POST("/api/guides").
		WithHeader("Authorization", admin).
		WithBytes(bigBody).
		Expect().Status(http.StatusBadRequest)

	// ── Cross-tenant token → 401 ────────────────────────────────────────────────
	// A token whose tenant_id is a DIFFERENT tenant, sent at atktenant's Host. The
	// JWT signature is valid, but JWTMiddleware rejects the tenant mismatch with 401
	// ("token tenant mismatch"). NOTE: the brief allows 401 OR 403; the engine
	// returns 401 (verified in pkg/auth/middleware.go), so we assert 401.
	e.GET("/api/guides").
		WithHeader("Authorization", "Bearer "+mintJWT(t, "ghosttenant", "super_admin")).
		Expect().Status(http.StatusUnauthorized)

	// ── Metric evidence: the 401 counter went up ────────────────────────────────
	// Three distinct paths returned 401 (alg:none, expired, cross-tenant), all under
	// tenant atktenant. There is NO security_blocked_total in this engine (S37
	// finding) — we assert appitools_requests_total{status="401"} instead.
	assertMetricAtLeast(t, metrics.Gatherer(), "appitools_requests_total",
		map[string]string{"status": "401"}, 3)
}
