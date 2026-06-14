package userauth

import (
	"context"
	"testing"
	"time"

	"github.com/miguelangel/appitools/pkg/auth"
)

func TestValidEmail(t *testing.T) {
	t.Parallel()
	good := []string{"a@b.co", "user.name+tag@sub.example.com", "x@y.io"}
	bad := []string{"", "noat", "no@domain", "a@b", "spaces in@x.com", "two@@x.com"}
	for _, e := range good {
		if !validEmail(normalizeEmail(e)) {
			t.Errorf("expected %q valid", e)
		}
	}
	for _, e := range bad {
		if validEmail(normalizeEmail(e)) {
			t.Errorf("expected %q invalid", e)
		}
	}
}

func TestLoginLimiter_Throttles(t *testing.T) {
	t.Parallel()
	l := newLoginLimiter(5, 3) // burst 3
	const tenant, email = "acme", "u@x.com"
	allowed := 0
	for i := 0; i < 10; i++ {
		if l.allow(tenant, email) {
			allowed++
		}
	}
	if allowed != 3 {
		t.Fatalf("burst=3 should allow exactly 3 immediate attempts, got %d", allowed)
	}
	// A different identity has its own independent bucket.
	if !l.allow(tenant, "other@x.com") {
		t.Fatal("a different email must not be throttled by another's attempts")
	}
	// A different tenant, same email, is independent too.
	if !l.allow("globex", email) {
		t.Fatal("a different tenant must not share the bucket")
	}
}

// Refresh needs no database — it only validates and re-mints a JWT.
func newRefreshService(secret string) *Service {
	return NewService(nil, Config{JWTSecret: secret, TokenTTL: time.Hour})
}

func TestRefresh_RemintsValidToken(t *testing.T) {
	t.Parallel()
	const secret = "a-test-secret-of-at-least-32-characters!!"
	svc := newRefreshService(secret)

	orig, err := svc.mint(User{ID: "user-1", Role: "viewer"}, "acme")
	if err != nil {
		t.Fatalf("mint: %v", err)
	}
	newTok, err := svc.Refresh(context.Background(), "acme", orig)
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}
	claims, err := auth.ValidateToken(newTok, secret)
	if err != nil {
		t.Fatalf("refreshed token did not validate: %v", err)
	}
	if claims.UserID != "user-1" || claims.Role != "viewer" || claims.TenantID != "acme" {
		t.Fatalf("refreshed claims drifted: %+v", claims)
	}
}

func TestRefresh_RejectsCrossTenant(t *testing.T) {
	t.Parallel()
	const secret = "a-test-secret-of-at-least-32-characters!!"
	svc := newRefreshService(secret)
	tok, _ := svc.mint(User{ID: "u", Role: "admin"}, "acme")

	if _, err := svc.Refresh(context.Background(), "globex", tok); err != ErrTenantMismatch {
		t.Fatalf("expected ErrTenantMismatch refreshing acme token under globex, got %v", err)
	}
}

func TestRefresh_RejectsGarbage(t *testing.T) {
	t.Parallel()
	svc := newRefreshService("a-test-secret-of-at-least-32-characters!!")
	if _, err := svc.Refresh(context.Background(), "acme", "not.a.jwt"); err != ErrInvalidToken {
		t.Fatalf("expected ErrInvalidToken, got %v", err)
	}
}

func TestMint_ProducesEngineValidToken(t *testing.T) {
	t.Parallel()
	const secret = "a-test-secret-of-at-least-32-characters!!"
	svc := NewService(nil, Config{JWTSecret: secret, TokenTTL: time.Hour})
	tok, err := svc.mint(User{ID: "abc", Role: "admin"}, "acme")
	if err != nil {
		t.Fatalf("mint: %v", err)
	}
	// The login token must be indistinguishable from an externally-minted one:
	// the engine's own validator must accept it.
	claims, err := auth.ValidateToken(tok, secret)
	if err != nil {
		t.Fatalf("engine validator rejected login token: %v", err)
	}
	if claims.Subject != "abc" || claims.UserID != "abc" {
		t.Fatalf("subject/user_id not set for audit: %+v", claims)
	}
}

func TestSignupDisabledByDefault(t *testing.T) {
	t.Parallel()
	svc := NewService(nil, Config{JWTSecret: "x"})
	if svc.SignupEnabled() {
		t.Fatal("signup must be DISABLED when no SignupRole is configured")
	}
	if _, err := svc.Signup(context.Background(), "acme", "a@b.co", "longenough"); err != ErrSignupDisabled {
		t.Fatalf("expected ErrSignupDisabled, got %v", err)
	}
}
