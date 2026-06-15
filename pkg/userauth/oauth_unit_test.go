package userauth

import (
	"net/url"
	"strings"
	"testing"
)

const oauthSecret = "an-oauth-test-secret-of-32+characters!!"

func oauthUnitService(providers map[string]OAuthProviderConfig) *Service {
	return NewService(nil, Config{JWTSecret: oauthSecret, OAuthProviders: providers})
}

func TestOAuthState_RoundTrip(t *testing.T) {
	t.Parallel()
	svc := oauthUnitService(map[string]OAuthProviderConfig{"google": {ClientID: "cid"}})
	state, err := svc.signOAuthState("acme", "google")
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	c, err := svc.parseOAuthState(state)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if c.Tenant != "acme" || c.Provider != "google" || c.Nonce == "" {
		t.Fatalf("state round-trip wrong: %+v", c)
	}
}

func TestOAuthState_TamperedRejected(t *testing.T) {
	t.Parallel()
	svc := oauthUnitService(map[string]OAuthProviderConfig{"google": {ClientID: "cid"}})
	state, _ := svc.signOAuthState("acme", "google")

	// Flip a character in the payload → signature no longer matches.
	tampered := state[:len(state)-3] + "AAA"
	if _, err := svc.parseOAuthState(tampered); err != ErrOAuthState {
		t.Fatalf("tampered state accepted: %v", err)
	}
	// A state signed with a DIFFERENT secret must be rejected (anti-forgery).
	other := NewService(nil, Config{JWTSecret: "a-totally-different-secret-32+chars!!"})
	foreign, _ := other.signOAuthState("acme", "google")
	if _, err := svc.parseOAuthState(foreign); err != ErrOAuthState {
		t.Fatalf("foreign-signed state accepted: %v", err)
	}
	// Garbage.
	if _, err := svc.parseOAuthState("not.a.jwt"); err != ErrOAuthState {
		t.Fatalf("garbage state accepted: %v", err)
	}
}

func TestOAuthAuthCodeURL(t *testing.T) {
	t.Parallel()
	svc := oauthUnitService(map[string]OAuthProviderConfig{"google": {ClientID: "my-client-id"}})
	raw, err := svc.OAuthAuthCodeURL("acme", "google", "https://cb.example.com/auth/oauth/google/callback")
	if err != nil {
		t.Fatalf("authcodeurl: %v", err)
	}
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("parse url: %v", err)
	}
	q := u.Query()
	if q.Get("client_id") != "my-client-id" {
		t.Fatalf("client_id wrong: %q", q.Get("client_id"))
	}
	if q.Get("response_type") != "code" {
		t.Fatalf("response_type wrong")
	}
	if q.Get("redirect_uri") != "https://cb.example.com/auth/oauth/google/callback" {
		t.Fatalf("redirect_uri wrong: %q", q.Get("redirect_uri"))
	}
	// The state must itself be a valid signed state for this tenant/provider.
	c, err := svc.parseOAuthState(q.Get("state"))
	if err != nil || c.Tenant != "acme" || c.Provider != "google" {
		t.Fatalf("state in url invalid: %v / %+v", err, c)
	}
	if !strings.Contains(q.Get("scope"), "email") {
		t.Fatalf("scope missing email: %q", q.Get("scope"))
	}
}

func TestOAuth_UnconfiguredProvider(t *testing.T) {
	t.Parallel()
	// Only google configured.
	svc := oauthUnitService(map[string]OAuthProviderConfig{
		"google": {ClientID: "cid"}, "github": {ClientID: ""},
	})
	if !svc.OAuthProviderConfigured("google") {
		t.Fatal("google should be configured")
	}
	if svc.OAuthProviderConfigured("github") {
		t.Fatal("github (no client id) must NOT be configured")
	}
	if _, err := svc.OAuthAuthCodeURL("acme", "github", "http://cb"); err != ErrOAuthDisabled {
		t.Fatalf("unconfigured provider url: got %v, want ErrOAuthDisabled", err)
	}
}

func TestOAuth_FullyDisabled(t *testing.T) {
	t.Parallel()
	svc := NewService(nil, Config{JWTSecret: oauthSecret}) // no providers
	if svc.OAuthEnabled() {
		t.Fatal("OAuth must be disabled with no providers")
	}
}

func TestAsString(t *testing.T) {
	t.Parallel()
	if asString("abc") != "abc" {
		t.Fatal("string")
	}
	if asString(float64(12345)) != "12345" {
		t.Fatalf("number → %q", asString(float64(12345)))
	}
	if asString(nil) != "" || asString(true) != "" {
		t.Fatal("non-scalar should be empty")
	}
}

func TestExtractors(t *testing.T) {
	t.Parallel()
	id, email := extractorFor("google")([]byte(`{"sub":"g-9","email":"a@b.co"}`))
	if id != "g-9" || email != "a@b.co" {
		t.Fatalf("google: %q %q", id, email)
	}
	id, email = extractorFor("github")([]byte(`{"id":777,"email":null}`))
	if id != "777" || email != "" {
		t.Fatalf("github: %q %q", id, email)
	}
	id, email = extractorFor("microsoft")([]byte(`{"id":"m-1","userPrincipalName":"u@corp.com"}`))
	if id != "m-1" || email != "u@corp.com" {
		t.Fatalf("microsoft upn fallback: %q %q", id, email)
	}
}

func TestPickPrimaryEmail(t *testing.T) {
	t.Parallel()
	got := pickPrimaryEmail([]byte(`[{"email":"sec@x.com","primary":false,"verified":true},{"email":"main@x.com","primary":true,"verified":true}]`))
	if got != "main@x.com" {
		t.Fatalf("primary pick: %q", got)
	}
	// No primary → first verified.
	got = pickPrimaryEmail([]byte(`[{"email":"unv@x.com","primary":true,"verified":false},{"email":"ver@x.com","primary":false,"verified":true}]`))
	if got != "ver@x.com" {
		t.Fatalf("first-verified pick: %q", got)
	}
}
