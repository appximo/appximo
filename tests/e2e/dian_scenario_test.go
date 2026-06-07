//go:build e2e

package e2e_test

import (
	"encoding/hex"
	"net/http"
	"testing"

	"github.com/miguelangel/appitools/tests/helpers"
)

// TestDIANScenario exercises the Colombian DIAN/CUFE fintech flow with REAL engine
// extensions (no mocks): the Goja before_create hooks call the engine's built-in
// validateNIT (mod-11 check digit) and calculateCUFE (SHA-384) — both real
// algorithms in pkg/extensions/dian.go, wired through tests/fixtures/schemas/dian_schema.json.
//
// It also asserts the engine's OWN SpanTracker (pkg/observability/span.go, NOT
// OTel) recorded request stages, captured via buildSpanServer's span-recording tap.
func TestDIANScenario(t *testing.T) {
	const tenantID = "diantenant"
	s := helpers.FixtureSchema(t, "dian_schema.json")
	helpers.RegisterTenant(t, testPool, tenantID, s)
	srv, _, sink := buildSpanServer(t, s, testPool)
	e := newHTTPExpect(t, srv, tenantID)

	admin := "Bearer " + mintJWT(t, tenantID, "super_admin")

	// 1. Valid NIT (800197268-4, real mod-11 check digit) → hook passes → 201.
	e.POST("/api/clients").
		WithHeader("Authorization", admin).
		WithJSON(map[string]any{"name": "Distribuidora Caribe SAS", "nit": "800197268-4", "city": "Barranquilla"}).
		Expect().Status(http.StatusCreated).
		JSON().Object().Value("nit").String().IsEqual("800197268-4")

	// 2. Invalid NIT (wrong check digit) → before_create hook rejects → 422 with a
	//    NIT-mentioning error.
	e.POST("/api/clients").
		WithHeader("Authorization", admin).
		WithJSON(map[string]any{"name": "Mal NIT SAS", "nit": "800197268-5"}).
		Expect().Status(http.StatusUnprocessableEntity).
		JSON().Object().Value("error").String().Contains("NIT")

	// 3. Invoice create → the hook computes CUFE via calculateCUFE → SHA-384 = 96
	//    hex chars (384 bits = 48 bytes). Assert both the length and that it decodes
	//    as 48 raw bytes.
	cufe := e.POST("/api/invoices").
		WithHeader("Authorization", admin).
		WithJSON(map[string]any{"nit": "800197268-4", "amount": 1000.0}).
		Expect().Status(http.StatusCreated).
		JSON().Object().Value("cufe").String().NotEmpty().Raw()

	if len(cufe) != 96 {
		t.Fatalf("CUFE length = %d, want 96 (SHA-384 hex)", len(cufe))
	}
	if raw, err := hex.DecodeString(cufe); err != nil || len(raw) != 48 {
		t.Fatalf("CUFE %q is not 48 hex-decoded bytes (err=%v, len=%d)", cufe, err, len(raw))
	}

	// 4. The engine's SpanTracker recorded at least one stage for the invoice
	//    request (hook/insert/serialize/done). This is the custom tracker, not OTel.
	spans := sink.forRoute("/api/invoices")
	if len(spans) == 0 {
		t.Fatal("expected >= 1 SpanTracker span recorded for POST /api/invoices, got 0")
	}
	t.Logf("SpanTracker recorded %d spans for POST /api/invoices: %v", len(spans), spanNames(spans))
}
