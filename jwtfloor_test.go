package appximo

import (
	"strings"
	"testing"
)

// SEC-6: the boot refuses a JWT secret under the documented 32-character floor,
// loudly and actionably — naming the variable, the length it got and the floor.
// The check runs before the schema is loaded or the pool dialed, so this needs
// neither a schema file nor a database.
func TestNew_RefusesShortJWTSecret(t *testing.T) {
	_, err := New(Config{
		SchemaPath: "does-not-matter.json",
		DSN:        "postgres://unused",
		JWTSecret:  "short",
		AdminKey:   "k",
	})
	if err == nil {
		t.Fatal("New accepted a 5-char JWT secret — SEC-6 floor missing")
	}
	msg := err.Error()
	for _, want := range []string{"JWT_SECRET", "too short", "32", "5 characters"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error must contain %q for the operator to act on it; got: %s", want, msg)
		}
	}
}

// The floor is inclusive: exactly 32 characters must pass the length check
// (the boot then proceeds to schema load, which fails on the fake path — that
// failure proves the secret check no longer fires).
func TestNew_AcceptsExactly32CharSecret(t *testing.T) {
	_, err := New(Config{
		SchemaPath: "does-not-exist-on-purpose.json",
		DSN:        "postgres://unused",
		JWTSecret:  strings.Repeat("s", MinJWTSecretLen),
		AdminKey:   "k",
	})
	if err == nil {
		t.Fatal("expected a schema-load error (the path is fake)")
	}
	if strings.Contains(err.Error(), "too short") {
		t.Fatalf("a %d-char secret must pass the floor: %v", MinJWTSecretLen, err)
	}
}
