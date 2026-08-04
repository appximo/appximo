//go:build e2e

package e2e_test

import (
	"net/http"
	"testing"

	"github.com/gavv/httpexpect/v2"
)

// TestCRMScenario drives the "Agencia CRM" client flow end-to-end against a real
// Postgres: Client → Contact → Deal, a status filter that reflects a PATCH, and
// the RBAC boundary (read-only role cannot create) with a metric assertion.
//
// Fixture: tests/fixtures/schemas/crm_schema.json
//
//	roles: gerente (full CRUD), operario (read-only on clients/contacts/deals)
//	deals.status enum = [pending, won, lost]
//
// Deviation from the brief: the brief writes status="closed", but the crm fixture's
// enum is [pending, won, lost] and the engine validates enums on PATCH
// (collectUpdate → validateFieldValue), so "closed" would be a 422. We transition
// pending → "won", which exercises the same filter behaviour with a valid value.
func TestCRMScenario(t *testing.T) {
	const tenantID = "crmtenant"
	srv, metrics, _ := newE2EServer(t, "crm_schema.json", tenantID)
	e := newHTTPExpect(t, srv, tenantID)

	gerente := "Bearer " + mintJWT(t, tenantID, "gerente")
	operario := "Bearer " + mintJWT(t, tenantID, "operario")

	// 1. Create a client.
	clientID := e.POST("/api/clients").
		WithHeader("Authorization", gerente).
		WithJSON(map[string]any{"name": "Cliente Andino SAS", "nit": "900123456-1", "city": "Bogotá"}).
		Expect().Status(http.StatusCreated).
		JSON().Object().Value("id").String().NotEmpty().Raw()

	// 2. Create a contact linked to that client.
	e.POST("/api/contacts").
		WithHeader("Authorization", gerente).
		WithJSON(map[string]any{"client_id": clientID, "name": "María Restrepo", "email": "maria@andino.co"}).
		Expect().Status(http.StatusCreated).
		JSON().Object().Value("client_id").String().IsEqual(clientID)

	// 3. Create a deal with status=pending.
	dealID := e.POST("/api/deals").
		WithHeader("Authorization", gerente).
		WithJSON(map[string]any{"client_id": clientID, "title": "Integración API", "status": "pending", "amount": 4200.0}).
		Expect().Status(http.StatusCreated).
		JSON().Object().Value("id").String().NotEmpty().Raw()

	// 4. Filter deals by status=pending → must include the new deal.
	if !listContainsID(t, e, gerente, "pending", dealID) {
		t.Fatalf("step 4: deal %s should appear in filter[status][eq]=pending", dealID)
	}

	// 5. PATCH the deal to status=won.
	e.PATCH("/api/deals/{id}", dealID).
		WithHeader("Authorization", gerente).
		WithJSON(map[string]any{"status": "won"}).
		Expect().Status(http.StatusOK).
		JSON().Object().Value("status").String().IsEqual("won")

	// 6. Filter deals by status=pending → must NOT include the deal any more.
	if listContainsID(t, e, gerente, "pending", dealID) {
		t.Fatalf("step 6: deal %s must NOT appear in filter[status][eq]=pending after PATCH→won", dealID)
	}

	// 7. RBAC: the read-only operario cannot create a deal → 403.
	e.POST("/api/deals").
		WithHeader("Authorization", operario).
		WithJSON(map[string]any{"client_id": clientID, "title": "No permitido"}).
		Expect().Status(http.StatusForbidden).
		JSON().Object().Value("error").String().IsEqual("forbidden")

	// 8. Exactly one 403 was produced this run → the Prometheus counter proves it.
	assertMetric(t, metrics.Gatherer(), "appximo_requests_total",
		map[string]string{"status": "403"}, 1)
}

// listContainsID GETs /api/deals filtered by status and reports whether any row's
// id equals want. The list endpoint envelopes rows under "data".
func listContainsID(t *testing.T, e *httpexpect.Expect, auth, status, want string) bool {
	t.Helper()
	arr := e.GET("/api/deals").
		WithHeader("Authorization", auth).
		WithQuery("filter[status][eq]", status).
		Expect().Status(http.StatusOK).
		JSON().Object().Value("data").Array()
	for _, v := range arr.Iter() {
		if v.Object().Value("id").String().Raw() == want {
			return true
		}
	}
	return false
}
