package auth

import (
	"context"
	"testing"
)

// TestHookUserContext proves SEC-AUDIT-V2 Hallazgo B: a lifecycle hook's `user`
// binding is built from the request's JWT claims (so a before-hook knows WHO acted),
// and is nil when there are no claims.
func TestHookUserContext(t *testing.T) {
	if uc := HookUserContext(context.Background()); uc != nil {
		t.Fatalf("no claims should yield a nil user context, got %v", uc)
	}

	ctx := context.WithValue(context.Background(), claimsKey{}, &Claims{
		UserID: "u1", Role: "empleado", TenantID: "acme",
	})
	uc := HookUserContext(ctx)
	if uc["user_id"] != "u1" || uc["role"] != "empleado" || uc["tenant_id"] != "acme" {
		t.Fatalf("unexpected user ctx: %v", uc)
	}
	if _, has := uc["external_client_id"]; has {
		t.Fatalf("external_client_id must be omitted when empty: %v", uc)
	}

	ctx2 := context.WithValue(context.Background(), claimsKey{}, &Claims{UserID: "u2", ExternalClientID: "ext9"})
	if HookUserContext(ctx2)["external_client_id"] != "ext9" {
		t.Fatalf("external_client_id must be present when set")
	}
}
