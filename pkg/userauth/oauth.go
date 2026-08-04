package userauth

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// OAuth sentinel errors mapped to HTTP status by the handlers.
var (
	ErrOAuthDisabled    = errors.New("userauth: oauth provider not configured")
	ErrOAuthState       = errors.New("userauth: invalid oauth state")
	ErrOAuthExchange    = errors.New("userauth: oauth code exchange failed")
	ErrOAuthNoEmail     = errors.New("userauth: oauth provider returned no email")
	ErrOAuthNoAutoUser  = errors.New("userauth: oauth auto-provisioning disabled (no default role)")
	errOAuthStateMethod = errors.New("userauth: unexpected oauth state signing method")
)

// OAuthProviderConfig holds one provider's client credentials. A provider with an
// empty ClientID is simply NOT offered (the engine never fails to boot over an
// unconfigured provider).
type OAuthProviderConfig struct {
	ClientID     string
	ClientSecret string
}

// oauthEndpoints are a provider's URLs. Defaults come from defaultEndpoints; tests
// override them (via Config.oauthEndpoints) to point at an httptest server so no
// real provider is ever called.
type oauthEndpoints struct {
	authURL, tokenURL, userInfoURL, emailsURL string
}

// oauthProvider is a fully-resolved, ready-to-use provider.
type oauthProvider struct {
	name         string
	clientID     string
	clientSecret string
	ep           oauthEndpoints
	scopes       []string
	// extractUser pulls the STABLE provider_user_id and the email out of the
	// userinfo JSON. provider_user_id (not the mutable email) is the identity key.
	extractUser func(userBody []byte) (providerUserID, email string)
}

// oauthManager holds the configured providers and the shared HTTP client.
type oauthManager struct {
	providers       map[string]*oauthProvider
	httpClient      *http.Client
	defaultRole     string // role for auto-created OAuth users ("" disables auto-provision)
	callbackURL     string // fixed callback origin override; "" → derive from request
	successRedirect string // optional: 302 here with #token=… instead of JSON
}

// defaultEndpoints returns the public OAuth endpoints for a known provider.
func defaultEndpoints(name string) (oauthEndpoints, []string, bool) {
	switch name {
	case "google":
		return oauthEndpoints{
			authURL:     "https://accounts.google.com/o/oauth2/v2/auth",
			tokenURL:    "https://oauth2.googleapis.com/token",
			userInfoURL: "https://openidconnect.googleapis.com/v1/userinfo",
		}, []string{"openid", "email", "profile"}, true
	case "github":
		return oauthEndpoints{
			authURL:     "https://github.com/login/oauth/authorize",
			tokenURL:    "https://github.com/login/oauth/access_token",
			userInfoURL: "https://api.github.com/user",
			emailsURL:   "https://api.github.com/user/emails",
		}, []string{"read:user", "user:email"}, true
	case "microsoft":
		return oauthEndpoints{
			authURL:     "https://login.microsoftonline.com/common/oauth2/v2.0/authorize",
			tokenURL:    "https://login.microsoftonline.com/common/oauth2/v2.0/token",
			userInfoURL: "https://graph.microsoft.com/v1.0/me",
		}, []string{"openid", "email", "profile", "User.Read"}, true
	default:
		return oauthEndpoints{}, nil, false
	}
}

// extractorFor returns the userinfo parser for a provider. Each pulls the stable
// id + an email; GitHub may return a null email here (a second /user/emails call,
// made by fetchIdentity, fills it in).
func extractorFor(name string) func([]byte) (string, string) {
	return func(body []byte) (string, string) {
		var m map[string]any
		if json.Unmarshal(body, &m) != nil {
			return "", ""
		}
		switch name {
		case "google":
			return asString(m["sub"]), asString(m["email"])
		case "github":
			return asString(m["id"]), asString(m["email"])
		case "microsoft":
			email := asString(m["mail"])
			if email == "" {
				email = asString(m["userPrincipalName"])
			}
			return asString(m["id"]), email
		}
		return "", ""
	}
}

// asString coerces a JSON scalar (string or number) to a string. Provider ids may
// arrive as a JSON number (GitHub) or string (Google/MS); both become the same
// stable text key.
func asString(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case float64:
		return strconv.FormatInt(int64(x), 10)
	case json.Number:
		return x.String()
	default:
		return ""
	}
}

// newOAuthManager builds the manager from cfg, registering ONLY providers that
// have a client id. Returns nil when none are configured (OAuth fully off).
func newOAuthManager(cfg Config) *oauthManager {
	providers := map[string]*oauthProvider{}
	for name, pc := range cfg.OAuthProviders {
		if pc.ClientID == "" {
			continue // not configured → not offered
		}
		ep, scopes, ok := defaultEndpoints(name)
		if !ok {
			continue // unknown provider name ignored
		}
		if ov, has := cfg.oauthEndpoints[name]; has { // test override
			ep = ov
		}
		providers[name] = &oauthProvider{
			name: name, clientID: pc.ClientID, clientSecret: pc.ClientSecret,
			ep: ep, scopes: scopes, extractUser: extractorFor(name),
		}
	}
	if len(providers) == 0 {
		return nil
	}
	hc := cfg.oauthHTTPClient
	if hc == nil {
		hc = &http.Client{Timeout: 15 * time.Second}
	}
	return &oauthManager{
		providers:       providers,
		httpClient:      hc,
		defaultRole:     cfg.OAuthDefaultRole,
		callbackURL:     strings.TrimRight(cfg.OAuthCallbackURL, "/"),
		successRedirect: cfg.OAuthSuccessRedirect,
	}
}

// OAuthEnabled reports whether any OAuth provider is configured.
func (s *Service) OAuthEnabled() bool { return s.oauth != nil }

// OAuthProviderConfigured reports whether the named provider is offered.
func (s *Service) OAuthProviderConfigured(name string) bool {
	return s.oauth != nil && s.oauth.providers[name] != nil
}

// OAuthSuccessRedirect returns the configured post-login redirect URL ("" → the
// callback returns JSON).
func (s *Service) OAuthSuccessRedirect() string {
	if s.oauth == nil {
		return ""
	}
	return s.oauth.successRedirect
}

// oauthStateClaims is the SIGNED state param: it carries the tenant across the
// provider round-trip (the callback's Host is the fixed callback domain, NOT the
// tenant subdomain — so the tenant CANNOT come from the Host) and is the CSRF
// token (an attacker cannot forge a validly-signed state). Short-lived; the random
// nonce adds entropy. Signed with the engine's JWT secret (HS256), but it is a
// distinct claims shape and is never accepted as an auth token.
type oauthStateClaims struct {
	Tenant   string `json:"t"`
	Provider string `json:"p"`
	Nonce    string `json:"n"`
	jwt.RegisteredClaims
}

func (s *Service) signOAuthState(tenant, provider string) (string, error) {
	nb := make([]byte, 16)
	if _, err := rand.Read(nb); err != nil {
		return "", err
	}
	now := time.Now()
	c := oauthStateClaims{
		Tenant: tenant, Provider: provider, Nonce: base64.RawURLEncoding.EncodeToString(nb),
		RegisteredClaims: jwt.RegisteredClaims{
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(10 * time.Minute)), // a flow must complete promptly
		},
	}
	return jwt.NewWithClaims(jwt.SigningMethodHS256, c).SignedString([]byte(s.cfg.JWTSecret))
}

func (s *Service) parseOAuthState(stateStr string) (*oauthStateClaims, error) {
	tok, err := jwt.ParseWithClaims(stateStr, &oauthStateClaims{}, func(t *jwt.Token) (any, error) {
		if t.Method != jwt.SigningMethodHS256 {
			return nil, errOAuthStateMethod
		}
		return []byte(s.cfg.JWTSecret), nil
	}, jwt.WithExpirationRequired())
	if err != nil {
		return nil, ErrOAuthState
	}
	c, ok := tok.Claims.(*oauthStateClaims)
	if !ok || !tok.Valid || c.Tenant == "" {
		return nil, ErrOAuthState
	}
	return c, nil
}

// OAuthAuthCodeURL builds the provider authorize URL for the tenant, embedding a
// freshly-signed state. redirectURI MUST equal the one used at the callback's token
// exchange (OAuth requires it to match).
func (s *Service) OAuthAuthCodeURL(tenantID, provider, redirectURI string) (string, error) {
	if s.oauth == nil {
		return "", ErrOAuthDisabled
	}
	p := s.oauth.providers[provider]
	if p == nil {
		return "", ErrOAuthDisabled
	}
	state, err := s.signOAuthState(tenantID, provider)
	if err != nil {
		return "", err
	}
	q := url.Values{}
	q.Set("client_id", p.clientID)
	q.Set("redirect_uri", redirectURI)
	q.Set("response_type", "code")
	q.Set("scope", strings.Join(p.scopes, " "))
	q.Set("state", state)
	sep := "?"
	if strings.Contains(p.ep.authURL, "?") {
		sep = "&"
	}
	return p.ep.authURL + sep + q.Encode(), nil
}

// OAuthCallback validates the state, exchanges the code, resolves (or creates) the
// user, and returns an engine-valid JWT. The tenant comes from the SIGNED STATE,
// never from the request Host. redirectURI must match the one used at initiate.
func (s *Service) OAuthCallback(ctx context.Context, provider, code, stateStr, redirectURI string) (AuthResult, error) {
	if s.oauth == nil {
		return AuthResult{}, ErrOAuthDisabled
	}
	p := s.oauth.providers[provider]
	if p == nil {
		return AuthResult{}, ErrOAuthDisabled
	}
	st, err := s.parseOAuthState(stateStr)
	if err != nil {
		return AuthResult{}, err
	}
	if st.Provider != provider {
		return AuthResult{}, ErrOAuthState // state was issued for a different provider
	}
	if code == "" {
		return AuthResult{}, ErrOAuthExchange
	}

	accessToken, err := s.oauth.exchange(ctx, p, code, redirectURI)
	if err != nil {
		return AuthResult{}, ErrOAuthExchange
	}
	providerUserID, email, err := s.oauth.fetchIdentity(ctx, p, accessToken)
	if err != nil {
		return AuthResult{}, err
	}

	user, err := s.resolveOAuthUser(ctx, st.Tenant, provider, providerUserID, email)
	if err != nil {
		return AuthResult{}, err
	}
	token, err := s.mint(user, st.Tenant)
	if err != nil {
		return AuthResult{}, err
	}
	return AuthResult{User: toPublic(user), Token: token}, nil
}

// resolveOAuthUser maps a verified (provider, providerUserID, email) to a tenant
// user, creating or linking as needed:
//  1. identity already linked → that user (a returning social login).
//  2. email already a user → LINK the identity to it (no duplicate; one person,
//     one account, multiple sign-in methods).
//  3. brand-new email → CREATE an external user (no password, email_verified=true
//     since the provider verified it) + link — IF auto-provisioning has a role.
func (s *Service) resolveOAuthUser(ctx context.Context, tenantID, provider, providerUserID, email string) (User, error) {
	email = normalizeEmail(email)
	if email == "" {
		return User{}, ErrOAuthNoEmail
	}
	if err := s.store.ensure(ctx, tenantID); err != nil {
		return User{}, err
	}
	if err := s.store.ensureIdentities(ctx, tenantID); err != nil {
		return User{}, err
	}

	// 1. Returning identity.
	if uid, found, err := s.store.GetIdentityUserID(ctx, tenantID, provider, providerUserID); err != nil {
		return User{}, err
	} else if found {
		return s.store.GetByID(ctx, tenantID, uid)
	}

	// 2. Existing user by email → link.
	existing, err := s.store.GetByEmail(ctx, tenantID, email)
	if err == nil {
		if lerr := s.linkInTx(ctx, tenantID, existing.ID, provider, providerUserID, email); lerr != nil {
			if errors.Is(lerr, ErrIdentityExists) { // concurrent link won — re-resolve
				return s.reresolveIdentity(ctx, tenantID, provider, providerUserID)
			}
			return User{}, lerr
		}
		return existing, nil
	}
	if !errors.Is(err, ErrUserNotFound) {
		return User{}, err
	}

	// 3. New external user (+ identity) atomically — needs an auto-provision role.
	role := s.oauth.defaultRole
	if role == "" {
		role = s.cfg.SignupRole // fall back to the signup default
	}
	if role == "" {
		return User{}, ErrOAuthNoAutoUser
	}
	tx, err := s.store.pool.Begin(ctx)
	if err != nil {
		return User{}, err
	}
	defer tx.Rollback(context.Background()) //nolint:errcheck
	user, err := s.store.createExternalUserTx(ctx, tx, tenantID, email, role)
	if err != nil {
		if errors.Is(err, ErrEmailTaken) { // concurrent create won — re-resolve by email
			_ = tx.Rollback(context.Background())
			return s.resolveOAuthUser(ctx, tenantID, provider, providerUserID, email)
		}
		return User{}, err
	}
	if err := s.store.linkIdentityTx(ctx, tx, tenantID, user.ID, provider, providerUserID, email); err != nil {
		return User{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return User{}, err
	}
	return user, nil
}

// linkInTx links an identity to an existing user in its own tx.
func (s *Service) linkInTx(ctx context.Context, tenantID, userID, provider, providerUserID, email string) error {
	tx, err := s.store.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(context.Background()) //nolint:errcheck
	if err := s.store.linkIdentityTx(ctx, tx, tenantID, userID, provider, providerUserID, email); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Service) reresolveIdentity(ctx context.Context, tenantID, provider, providerUserID string) (User, error) {
	uid, found, err := s.store.GetIdentityUserID(ctx, tenantID, provider, providerUserID)
	if err != nil {
		return User{}, err
	}
	if !found {
		return User{}, ErrOAuthExchange
	}
	return s.store.GetByID(ctx, tenantID, uid)
}

// --- provider HTTP (token exchange + userinfo) ------------------------------

// exchange swaps an authorization code for an access token at the provider's token
// endpoint. Accept: application/json makes every provider (incl. GitHub, which
// otherwise replies form-encoded) return JSON.
func (m *oauthManager) exchange(ctx context.Context, p *oauthProvider, code, redirectURI string) (string, error) {
	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("code", code)
	form.Set("redirect_uri", redirectURI)
	form.Set("client_id", p.clientID)
	form.Set("client_secret", p.clientSecret)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.ep.tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	resp, err := m.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close() //nolint:errcheck
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("token endpoint status %d", resp.StatusCode)
	}
	var tr struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.Unmarshal(body, &tr); err != nil || tr.AccessToken == "" {
		return "", fmt.Errorf("no access_token in token response")
	}
	return tr.AccessToken, nil
}

// fetchIdentity calls the provider's userinfo endpoint and returns the stable
// provider_user_id + email. For GitHub, when the primary userinfo hides the email,
// it makes the documented second call to /user/emails and picks the primary,
// verified address.
func (m *oauthManager) fetchIdentity(ctx context.Context, p *oauthProvider, accessToken string) (providerUserID, email string, err error) {
	body, err := m.authedGet(ctx, p.ep.userInfoURL, accessToken)
	if err != nil {
		return "", "", ErrOAuthExchange
	}
	providerUserID, email = p.extractUser(body)
	if providerUserID == "" {
		return "", "", ErrOAuthExchange
	}
	if email == "" && p.ep.emailsURL != "" {
		if eb, gerr := m.authedGet(ctx, p.ep.emailsURL, accessToken); gerr == nil {
			email = pickPrimaryEmail(eb)
		}
	}
	if email == "" {
		return "", "", ErrOAuthNoEmail
	}
	return providerUserID, email, nil
}

func (m *oauthManager) authedGet(ctx context.Context, urlStr, accessToken string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, urlStr, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "appximo") // GitHub's API requires a User-Agent
	resp, err := m.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close() //nolint:errcheck
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("userinfo status %d", resp.StatusCode)
	}
	return body, nil
}

// pickPrimaryEmail selects the primary verified address from GitHub's /user/emails
// list, falling back to the first verified one.
func pickPrimaryEmail(body []byte) string {
	var emails []struct {
		Email    string `json:"email"`
		Primary  bool   `json:"primary"`
		Verified bool   `json:"verified"`
	}
	if json.Unmarshal(body, &emails) != nil {
		return ""
	}
	var firstVerified string
	for _, e := range emails {
		if e.Primary && e.Verified {
			return e.Email
		}
		if firstVerified == "" && e.Verified {
			firstVerified = e.Email
		}
	}
	return firstVerified
}
