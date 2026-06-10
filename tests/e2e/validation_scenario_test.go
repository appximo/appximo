//go:build e2e

package e2e_test

import (
	"net/http"
	"testing"
)

// TestValidationScenario exercises the S44 declarative validation rules
// (required / format / enum / min/max / pattern / minLength) end-to-end against
// a real Postgres through the full middleware chain:
//
//   - invalid payload → 422 with the exact multi-field error shape
//     {"error":"validation_failed","fields":[{field,rule,message}...]}
//     reporting ALL violations in ONE response;
//   - valid payload → 201;
//   - valid partial PATCH → 200 (PATCH only validates the fields present);
//   - invalid partial PATCH → 422.
//
// Fixture: tests/fixtures/schemas/validation_schema.json (resource `members`).
func TestValidationScenario(t *testing.T) {
	const tenantID = "valtenant"
	srv, _, _ := newE2EServer(t, "validation_schema.json", tenantID)
	e := newHTTPExpect(t, srv, tenantID)

	admin := "Bearer " + mintJWT(t, tenantID, "super_admin")

	// ── 1. Invalid payload → 422 with one entry PER violated field ────────────
	resp := e.POST("/api/members").
		WithHeader("Authorization", admin).
		WithJSON(map[string]any{
			// email ABSENT            → required
			"status": "archived",   // → enum
			"amount": -10.5,        // → min
			"nit":    "ABC",        // → pattern
			"code":   "xy",         // → minLength
		}).
		Expect().Status(http.StatusUnprocessableEntity).
		JSON().Object()

	resp.Value("error").String().IsEqual("validation_failed")
	fields := resp.Value("fields").Array()
	fields.Length().IsEqual(5)

	got := map[string]string{} // field → rule
	for i := 0; i < 5; i++ {
		o := fields.Value(i).Object()
		got[o.Value("field").String().Raw()] = o.Value("rule").String().Raw()
		o.Value("message").String().NotEmpty()
	}
	want := map[string]string{
		"email":  "required",
		"status": "enum",
		"amount": "min",
		"nit":    "pattern",
		"code":   "minLength",
	}
	for f, r := range want {
		if got[f] != r {
			t.Errorf("field %q: want rule %q, got %q (all: %v)", f, r, got[f], got)
		}
	}

	// ── 2. Valid payload → 201 (data round-trips) ──────────────────────────────
	id := e.POST("/api/members").
		WithHeader("Authorization", admin).
		WithJSON(map[string]any{
			"email":    "maria@andino.co",
			"status":   "pending",
			"amount":   4200.50,
			"nit":      "900123456",
			"code":     "MBR-001",
			"homepage": "https://andino.co",
		}).
		Expect().Status(http.StatusCreated).
		JSON().Object().Value("id").String().NotEmpty().Raw()

	// ── 3. Valid partial PATCH → 200 (absent required email is NOT demanded) ──
	e.PATCH("/api/members/{id}", id).
		WithHeader("Authorization", admin).
		WithJSON(map[string]any{"status": "active"}).
		Expect().Status(http.StatusOK).
		JSON().Object().Value("status").String().IsEqual("active")

	// ── 4. Invalid partial PATCH → 422 with the same shape ─────────────────────
	bad := e.PATCH("/api/members/{id}", id).
		WithHeader("Authorization", admin).
		WithJSON(map[string]any{"email": "not-an-email"}).
		Expect().Status(http.StatusUnprocessableEntity).
		JSON().Object()
	bad.Value("error").String().IsEqual("validation_failed")
	first := bad.Value("fields").Array().Value(0).Object()
	first.Value("field").String().IsEqual("email")
	first.Value("rule").String().IsEqual("format")

	// ── 5. PUT (full replace) DOES demand the required field → 422/required ───
	put := e.PUT("/api/members/{id}", id).
		WithHeader("Authorization", admin).
		WithJSON(map[string]any{"status": "done"}).
		Expect().Status(http.StatusUnprocessableEntity).
		JSON().Object()
	put.Value("error").String().IsEqual("validation_failed")
	putFirst := put.Value("fields").Array().Value(0).Object()
	putFirst.Value("field").String().IsEqual("email")
	putFirst.Value("rule").String().IsEqual("required")
}
