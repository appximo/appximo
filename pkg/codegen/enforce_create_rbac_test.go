package codegen

import (
	"net/http"
	"testing"

	"github.com/miguelangel/appitools/pkg/rbac"
)

// TestEnforceCreateRBAC covers the shared create-enforcement core (FASE3-SEC
// Hallazgo 2) that BOTH the REST POST handler and the GraphQL create mutation call
// on the post-hook body — so a single table proves the security property for both
// surfaces. The mutation is in place; each case asserts the resulting body + status.
func TestEnforceCreateRBAC(t *testing.T) {
	const me = "11111111-1111-1111-1111-111111111111"
	const other = "22222222-2222-2222-2222-222222222222"

	ownerCond := &rbac.EvalResult{
		Allowed:   true,
		Condition: &rbac.WhereCondition{Field: "owner_id", Op: "eq", Value: me},
	}

	t.Run("nil eval is a no-op (no policy result / library / test path)", func(t *testing.T) {
		body := map[string]any{"title": "x", "owner_id": other}
		if st, _ := EnforceCreateRBAC(body, nil); st != 0 {
			t.Fatalf("nil eval must proceed, got status %d", st)
		}
		if body["owner_id"] != other {
			t.Fatalf("nil eval must not mutate body, got %v", body["owner_id"])
		}
	})

	t.Run("unrestricted role (no allowlist, no condition) is a no-op", func(t *testing.T) {
		// rrhh-admin / admin: Allowed with neither restriction — the GATE-WRITE case.
		ev := &rbac.EvalResult{Allowed: true}
		body := map[string]any{"title": "x", "owner_id": other}
		if st, _ := EnforceCreateRBAC(body, ev); st != 0 {
			t.Fatalf("unrestricted role must proceed, got status %d", st)
		}
		if body["owner_id"] != other || body["title"] != "x" {
			t.Fatalf("unrestricted role must not mutate body, got %v", body)
		}
	})

	t.Run("owner-scoped: omitted condition field is forced to the principal", func(t *testing.T) {
		body := map[string]any{"title": "mine"}
		if st, _ := EnforceCreateRBAC(body, ownerCond); st != 0 {
			t.Fatalf("own create must proceed, got status %d", st)
		}
		if body["owner_id"] != me {
			t.Fatalf("owner_id must be forced to %q, got %v", me, body["owner_id"])
		}
	})

	t.Run("owner-scoped: own id supplied explicitly is accepted", func(t *testing.T) {
		body := map[string]any{"title": "mine", "owner_id": me}
		if st, _ := EnforceCreateRBAC(body, ownerCond); st != 0 {
			t.Fatalf("own id must proceed, got status %d", st)
		}
		if body["owner_id"] != me {
			t.Fatalf("owner_id must stay %q, got %v", me, body["owner_id"])
		}
	})

	t.Run("owner-scoped: foreign id is REJECTED (mass-assignment blocked)", func(t *testing.T) {
		body := map[string]any{"title": "theirs", "owner_id": other}
		st, msg := EnforceCreateRBAC(body, ownerCond)
		if st != http.StatusForbidden {
			t.Fatalf("foreign owner_id must be 403, got status %d (%s)", st, msg)
		}
	})

	t.Run("field allowlist drops fields outside it (silently, like update)", func(t *testing.T) {
		ev := &rbac.EvalResult{Allowed: true, AllowedFields: []string{"id", "title", "status"}}
		body := map[string]any{"title": "x", "status": "open", "secret_note": "leak"}
		if st, _ := EnforceCreateRBAC(body, ev); st != 0 {
			t.Fatalf("allowlist drop must proceed, got status %d", st)
		}
		if _, ok := body["secret_note"]; ok {
			t.Fatalf("field outside allowlist must be dropped, body=%v", body)
		}
		if body["title"] != "x" || body["status"] != "open" {
			t.Fatalf("allowed fields must survive, body=%v", body)
		}
	})

	t.Run("allowlist + condition: condition field survives the allowlist and is forced", func(t *testing.T) {
		// owner_id is NOT in the allowlist, but it is the condition field, so it must
		// be kept and server-forced (the row must satisfy the role's own row filter).
		ev := &rbac.EvalResult{
			Allowed:       true,
			AllowedFields: []string{"id", "title", "status"},
			Condition:     &rbac.WhereCondition{Field: "owner_id", Op: "eq", Value: me},
		}
		body := map[string]any{"title": "x", "secret_note": "leak"}
		if st, _ := EnforceCreateRBAC(body, ev); st != 0 {
			t.Fatalf("must proceed, got status %d", st)
		}
		if _, ok := body["secret_note"]; ok {
			t.Fatalf("non-allowed field must be dropped, body=%v", body)
		}
		if body["owner_id"] != me {
			t.Fatalf("condition field must be forced to %q even though not in allowlist, body=%v", me, body)
		}
	})

	t.Run("allowlist + condition: foreign condition field still rejected", func(t *testing.T) {
		ev := &rbac.EvalResult{
			Allowed:       true,
			AllowedFields: []string{"id", "title", "status"},
			Condition:     &rbac.WhereCondition{Field: "owner_id", Op: "eq", Value: me},
		}
		body := map[string]any{"title": "x", "owner_id": other}
		if st, _ := EnforceCreateRBAC(body, ev); st != http.StatusForbidden {
			t.Fatalf("foreign condition field must be 403, got %d", st)
		}
	})
}
