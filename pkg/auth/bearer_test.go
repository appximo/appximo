package auth

import "testing"

// The scheme is case-insensitive (RFC 9110 §11.1). Before this, a perfectly legal
// `Authorization: bearer <valid token>` was answered 401 "invalid token" — and the
// engine's OWN generated OpenAPI document advertises `"scheme": "bearer"`, so a
// conformant generated SDK emitting the scheme as published would have been
// refused by the engine that published it.
func TestBearerToken(t *testing.T) {
	const tok = "abc.def.ghi"
	cases := []struct {
		header    string
		wantToken string
		wantOK    bool
		why       string
	}{
		{"Bearer " + tok, tok, true, "the canonical spelling"},
		{"bearer " + tok, tok, true, "RFC 9110 §11.1: the auth-scheme is case-insensitive"},
		{"BEARER " + tok, tok, true, "likewise"},
		{"BeArEr " + tok, tok, true, "likewise"},
		{"Bearer  " + tok, tok, true, "RFC 9110: credentials = auth-scheme 1*SP token68 — more than one space is legal"},
		{"Bearer\t" + tok, tok, true, "a tab is whitespace between scheme and credential"},
		{"Basic dXNlcjpwYXNz", "", false, "a different scheme is not a Bearer credential"},
		{tok, "", false, "no scheme at all"},
		{"", "", false, "no header"},
		{"Bearer", "", false, "scheme with no credential"},
		{"Bearer ", "", true, "scheme with an EMPTY credential parses; the JWT parser rejects it with a real message"},
		{"BearerX", "", false, "the scheme must be followed by whitespace, or 'BearerX' would yield the token 'X'"},
		{"Bear " + tok, "", false, "a prefix of the scheme is not the scheme"},
	}
	for _, c := range cases {
		got, ok := BearerToken(c.header)
		if ok != c.wantOK || got != c.wantToken {
			t.Errorf("BearerToken(%q) = (%q, %v), want (%q, %v) — %s",
				c.header, got, ok, c.wantToken, c.wantOK, c.why)
		}
	}
}

// EqualFold is Unicode-aware, so this pins that no exotic spelling of "Bearer"
// is accepted as the scheme. The Kelvin sign (U+212A) folds to "k" and the
// dotless i (U+0131) is involved in Turkish casing rules; neither may open the
// scheme. The check must stay an ASCII-equivalent one.
func TestBearerToken_NoUnicodeSchemeTricks(t *testing.T) {
	for _, h := range []string{
		"Bearer\u00a0tok", // non-breaking space is NOT the whitespace we accept
		"Beare\u0159 tok", // r with caron
		"Bearer\u200btok", // zero-width space instead of a real separator
		"\u212Aearer tok", // Kelvin sign — folds to "k", still not our scheme
	} {
		if got, ok := BearerToken(h); ok && got == "tok" {
			t.Errorf("BearerToken(%q) accepted a non-ASCII spelling of the scheme", h)
		}
	}
}
