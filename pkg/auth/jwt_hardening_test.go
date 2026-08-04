package auth_test

import (
	"testing"
	"time"

	"github.com/appximo/appximo/pkg/auth"
	"github.com/golang-jwt/jwt/v5"
)

const hardeningSecret = "jwt-hardening-test-secret"

func signWith(method jwt.SigningMethod, c auth.Claims) string {
	tok, _ := jwt.NewWithClaims(method, c).SignedString([]byte(hardeningSecret))
	return tok
}

// TestValidateToken_RequiresExpiration: a correctly-signed HS256 token with no exp
// claim must be rejected (WithExpirationRequired) — no immortal tokens.
func TestValidateToken_RequiresExpiration(t *testing.T) {
	tok := signWith(jwt.SigningMethodHS256, auth.Claims{TenantID: "10", Role: "super_admin"})
	if _, err := auth.ValidateToken(tok, hardeningSecret); err == nil {
		t.Fatal("token without exp must be rejected")
	}
}

// TestValidateToken_PinsHS256: a token signed with another HMAC family (HS384),
// even with a valid exp and the correct secret, must be rejected.
func TestValidateToken_PinsHS256(t *testing.T) {
	c := auth.Claims{TenantID: "10", Role: "super_admin"}
	c.RegisteredClaims.ExpiresAt = jwt.NewNumericDate(time.Now().Add(time.Hour))
	tok := signWith(jwt.SigningMethodHS384, c)
	if _, err := auth.ValidateToken(tok, hardeningSecret); err == nil {
		t.Fatal("HS384 token must be rejected (pinned to HS256)")
	}
}

// TestValidateToken_ValidStillPasses: a normal GenerateToken output (HS256 + exp)
// continues to validate.
func TestValidateToken_ValidStillPasses(t *testing.T) {
	tok, err := auth.GenerateToken(auth.Claims{TenantID: "10", Role: "super_admin"}, hardeningSecret)
	if err != nil {
		t.Fatal(err)
	}
	got, err := auth.ValidateToken(tok, hardeningSecret)
	if err != nil || got.TenantID != "10" {
		t.Fatalf("valid token failed: err=%v claims=%+v", err, got)
	}
}
