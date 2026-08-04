package worker

import (
	"testing"
	"time"

	"github.com/appximo/appximo/pkg/auth"
)

// TestMintTokenAcceptedByEngineValidator mints a service token via the EngineClient
// path and validates it with the ENGINE's real validator (auth.ValidateToken) — not
// a mock — so the claims contract can never silently diverge.
func TestMintTokenAcceptedByEngineValidator(t *testing.T) {
	const secret = "service-jwt-test-secret-at-least-32-chars"
	c := NewEngineClient("http://localhost:8080", "localhost", secret, "service_worker", DefaultServiceTokenTTL)

	tok, err := c.mintToken("acme")
	if err != nil {
		t.Fatalf("mintToken: %v", err)
	}
	claims, err := auth.ValidateToken(tok, secret)
	if err != nil {
		t.Fatalf("engine validator rejected a freshly minted token: %v", err)
	}
	if claims.TenantID != "acme" {
		t.Fatalf("tenant_id = %q, want acme", claims.TenantID)
	}
	if claims.Role != "service_worker" {
		t.Fatalf("role = %q, want service_worker", claims.Role)
	}
	if claims.Subject != "service:worker" {
		t.Fatalf("sub = %q, want service:worker", claims.Subject)
	}
	// Short-lived: exp must be ~60s out, never the 24h of GenerateToken.
	if claims.ExpiresAt == nil {
		t.Fatal("token has no exp (engine requires one)")
	}
	if d := time.Until(claims.ExpiresAt.Time); d <= 0 || d > 2*time.Minute {
		t.Fatalf("exp is %v out; want a short (~60s) TTL", d)
	}
}

func TestMintTokenWrongSecretRejected(t *testing.T) {
	c := NewEngineClient("http://x", "localhost", "secret-A-at-least-32-characters-long!", "service_worker", DefaultServiceTokenTTL)
	tok, err := c.mintToken("acme")
	if err != nil {
		t.Fatalf("mintToken: %v", err)
	}
	if _, err := auth.ValidateToken(tok, "secret-B-at-least-32-characters-long!"); err == nil {
		t.Fatal("a token signed with secret A must NOT validate under secret B")
	}
}

func TestExpiredServiceTokenRejected(t *testing.T) {
	const secret = "service-jwt-test-secret-at-least-32-chars"
	// Negative TTL → already expired.
	c := NewEngineClient("http://x", "localhost", secret, "service_worker", -1)
	// NewEngineClient floors ttl<=0 to the default, so mint directly with a past exp.
	tok, err := auth.GenerateTokenWithTTL(auth.Claims{Role: "service_worker", TenantID: "acme"}, secret, -1*time.Second)
	if err != nil {
		t.Fatalf("mint: %v", err)
	}
	if _, err := auth.ValidateToken(tok, secret); err == nil {
		t.Fatal("an expired token must be rejected by the engine validator")
	}
	_ = c
}
