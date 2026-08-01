package auth

import "strings"

// BearerToken extracts the token from an Authorization header value.
//
// The auth-scheme is matched CASE-INSENSITIVELY, because RFC 9110 §11.1 says it
// is: "the scheme is case-insensitive". Every site in the engine used to compare
// against the literal "Bearer " with strings.HasPrefix, so a perfectly legal
// `Authorization: bearer <valid token>` was answered 401 {"error":"invalid
// token"} — the engine rejected a correct request and told the caller their
// token was bad. Verified live before the fix (ADR-024):
//
//	Bearer <tok>  → 200
//	bearer <tok>  → 401 invalid token
//	BEARER <tok>  → 401 invalid token
//
// RFC 9110 also allows more than one space between the scheme and the
// credentials (`credentials = auth-scheme 1*SP token68`), so leading spaces are
// trimmed rather than assuming exactly one.
//
// ok is false when the header is empty or carries a DIFFERENT scheme (Basic,
// Negotiate, …). Callers distinguish that from a malformed token so the error
// can say which of the two happened — "invalid token" is a lie when the caller
// never sent a Bearer token at all.
//
// This is the single parser for the whole engine. It exists as one function
// because the same three lines were duplicated at eight call sites (the JWT
// middleware twice, the platform admin twice, tenant auth twice, the response
// cache, and the admin tenant-token check); a fix applied to some of them and
// not the rest would be worse than the bug, since the response cache keys on the
// token it extracts and would simply stop caching for any caller the middleware
// had started accepting.
func BearerToken(header string) (token string, ok bool) {
	const scheme = "Bearer"
	if len(header) < len(scheme) || !strings.EqualFold(header[:len(scheme)], scheme) {
		return "", false
	}
	rest := header[len(scheme):]
	// The scheme must be followed by whitespace — otherwise "BearerXYZ" would
	// parse as the token "XYZ".
	if rest == "" || (rest[0] != ' ' && rest[0] != '\t') {
		return "", false
	}
	return strings.TrimLeft(rest, " \t"), true
}

// errUnsupportedScheme is the 401 reason for an Authorization header that is
// present but is not a Bearer credential (Basic, Digest, a bare token with no
// scheme). It deliberately differs from the malformed-token message: both used
// to be "invalid token", which sent a caller who had sent NO token at all off to
// debug a token that was never the problem (ADR-024 — an error names what is
// actually wrong). It names no secret: it describes only what the caller sent.
const errUnsupportedScheme = `unsupported authorization scheme: expected "Authorization: Bearer <token>"`
