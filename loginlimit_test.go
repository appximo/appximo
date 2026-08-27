package appximo

import (
	"strings"
	"testing"

	"github.com/appximo/appximo/pkg/userauth"
)

// ENG-47: the login limiter's valve resolves Config → env → the historical
// default, and a set-but-invalid value refuses to boot instead of silently
// falling back on a security knob.
func TestResolveLoginLimit(t *testing.T) {
	t.Run("defaults untouched when nothing is set", func(t *testing.T) {
		t.Setenv("APPXIMO_AUTH_LOGIN_ATTEMPTS_PER_MINUTE", "")
		t.Setenv("APPXIMO_AUTH_LOGIN_BURST", "")
		pm, b, err := resolveLoginLimit(0, 0)
		if err != nil || pm != userauth.DefaultLoginAttemptsPerMinute || b != userauth.DefaultLoginBurst {
			t.Fatalf("got %d/%d %v, want %d/%d", pm, b, err, userauth.DefaultLoginAttemptsPerMinute, userauth.DefaultLoginBurst)
		}
	})
	t.Run("env raises it; burst follows per-minute when unset", func(t *testing.T) {
		t.Setenv("APPXIMO_AUTH_LOGIN_ATTEMPTS_PER_MINUTE", "60")
		t.Setenv("APPXIMO_AUTH_LOGIN_BURST", "")
		pm, b, err := resolveLoginLimit(0, 0)
		if err != nil || pm != 60 || b != 60 {
			t.Fatalf("got %d/%d %v, want 60/60", pm, b, err)
		}
	})
	t.Run("Config wins over env", func(t *testing.T) {
		t.Setenv("APPXIMO_AUTH_LOGIN_ATTEMPTS_PER_MINUTE", "60")
		t.Setenv("APPXIMO_AUTH_LOGIN_BURST", "7")
		pm, b, err := resolveLoginLimit(20, 0)
		if err != nil || pm != 20 || b != 7 {
			t.Fatalf("got %d/%d %v, want 20/7", pm, b, err)
		}
	})
	for _, bad := range []string{"abc", "0", "-3", "5.5"} {
		t.Run("invalid "+bad+" is a boot error naming the variable", func(t *testing.T) {
			t.Setenv("APPXIMO_AUTH_LOGIN_ATTEMPTS_PER_MINUTE", bad)
			t.Setenv("APPXIMO_AUTH_LOGIN_BURST", "")
			_, _, err := resolveLoginLimit(0, 0)
			if err == nil || !strings.Contains(err.Error(), "APPXIMO_AUTH_LOGIN_ATTEMPTS_PER_MINUTE") || !strings.Contains(err.Error(), bad) {
				t.Fatalf("want an error naming the variable and the value, got %v", err)
			}
		})
	}
}
