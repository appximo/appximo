//go:build go1.18

package auth

import "testing"

// FuzzValidateToken throws malformed/hostile tokens at the JWT validator. It must
// never panic (a panic in pre-auth token parsing is a remote crash). It must also
// never return a non-nil Claims with a nil error for a token that is not properly
// signed with the given secret — but that invariant is asserted by the unit tests;
// here we only require crash-freedom across arbitrary input.
func FuzzValidateToken(f *testing.F) {
	seeds := []string{
		"",
		"a.b.c",
		"a.b.c.d.e",
		"Bearer something",
		"eyJhbGciOiJIUzI1NiJ9.e30.x",
		"eyJ0eXAiOiJKV1QiLCJhbGciOiJub25lIn0.e30.",       // alg=none, empty sig
		"eyJhbGciOiJSUzI1NiJ9.eyJ0ZW5hbnRfaWQiOiIxMCJ9.x", // alg=RS256 (HS expected)
		"...",
		"\x00\x00\x00",
	}
	for _, s := range seeds {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, token string) {
		claims, err := ValidateToken(token, "anysecret-for-fuzzing")
		if err == nil && claims == nil {
			t.Fatalf("ValidateToken returned (nil, nil) for %q", token)
		}
	})
}
