package userauth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/miguelangel/appitools/pkg/auth"
	"github.com/miguelangel/appitools/pkg/tenant"
)

// fakeProvider is an httptest OAuth provider: a /token endpoint that returns a
// canned access token and a /userinfo endpoint that returns canned identity JSON.
// No real Google/GitHub/Microsoft is ever contacted.
type fakeProvider struct {
	srv      *httptest.Server
	userJSON []byte
	emails   []byte // optional GitHub /user/emails payload
	mu       sync.Mutex
	tokenHit int
	userHit  int
}

func newFakeProvider(t *testing.T, user map[string]any, emails any) *fakeProvider {
	t.Helper()
	ub, _ := json.Marshal(user)
	fp := &fakeProvider{userJSON: ub}
	if emails != nil {
		fp.emails, _ = json.Marshal(emails)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		fp.mu.Lock()
		fp.tokenHit++
		fp.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"access_token":"fake-access-token","token_type":"bearer"}`)) //nolint:errcheck
	})
	mux.HandleFunc("/userinfo", func(w http.ResponseWriter, r *http.Request) {
		fp.mu.Lock()
		fp.userHit++
		fp.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.Write(fp.userJSON) //nolint:errcheck
	})
	mux.HandleFunc("/emails", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write(fp.emails) //nolint:errcheck
	})
	fp.srv = httptest.NewServer(mux)
	t.Cleanup(fp.srv.Close)
	return fp
}

// oauthService builds a Service whose "google" (and optionally "github") provider
// points at the fake server. role is the OAuth auto-provision default.
func (fp *fakeProvider) oauthService(t *testing.T, provider, role string) *Service {
	t.Helper()
	ep := oauthEndpoints{
		authURL:     fp.srv.URL + "/auth",
		tokenURL:    fp.srv.URL + "/token",
		userInfoURL: fp.srv.URL + "/userinfo",
	}
	if provider == "github" {
		ep.emailsURL = fp.srv.URL + "/emails"
	}
	return NewService(NewStore(testPool), Config{
		JWTSecret:        testSecret,
		OAuthDefaultRole: role,
		OAuthProviders:   map[string]OAuthProviderConfig{provider: {ClientID: "cid", ClientSecret: "secret"}},
		oauthEndpoints:   map[string]oauthEndpoints{provider: ep},
		oauthHTTPClient:  fp.srv.Client(),
		TokenTTL:         time.Hour,
	})
}

// callback drives a full OAuth callback: it mints a valid state for (tenant,
// provider) and runs OAuthCallback as the handler would.
func (s *Service) testCallback(t *testing.T, tenantID, provider string) (AuthResult, error) {
	t.Helper()
	state, err := s.signOAuthState(tenantID, provider)
	if err != nil {
		t.Fatalf("sign state: %v", err)
	}
	return s.OAuthCallback(context.Background(), provider, "auth-code", state, "http://cb/auth/oauth/"+provider+"/callback")
}

func countRows(t *testing.T, tbl string) int {
	t.Helper()
	var n int
	if err := testPool.QueryRow(context.Background(), "SELECT count(*) FROM "+tbl).Scan(&n); err != nil {
		t.Fatalf("count %s: %v", tbl, err)
	}
	return n
}

func TestOAuth_NewUserCreatedWithIdentity(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test requires Postgres")
	}
	const tn = "oauthnew"
	createTenantSchema(t, tn)
	fp := newFakeProvider(t, map[string]any{"sub": "google-new-1", "email": "New.User@Example.com"}, nil)
	svc := fp.oauthService(t, "google", "viewer")

	res, err := svc.testCallback(t, tn, "google")
	if err != nil {
		t.Fatalf("callback: %v", err)
	}
	if res.User.Email != "new.user@example.com" {
		t.Fatalf("email not normalized: %q", res.User.Email)
	}
	if res.User.Role != "viewer" {
		t.Fatalf("role wrong: %q", res.User.Role)
	}
	if !res.User.EmailVerified {
		t.Fatal("OAuth user must be email_verified=true (provider verified it)")
	}
	// The token is engine-valid.
	if _, err := auth.ValidateToken(res.Token, testSecret); err != nil {
		t.Fatalf("oauth token rejected by engine validator: %v", err)
	}
	// The user has NO usable password (empty hash → cannot password-login).
	u, _ := svc.store.GetByEmail(context.Background(), tn, "new.user@example.com")
	if ok, _ := VerifyPassword("anything", u.PasswordHash); ok {
		t.Fatal("OAuth-only user should not have a usable password")
	}
}

func TestOAuth_ReturningIdentityNoDuplicate(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test requires Postgres")
	}
	const tn = "oauthret"
	createTenantSchema(t, tn)
	fp := newFakeProvider(t, map[string]any{"sub": "google-ret-1", "email": "ret@example.com"}, nil)
	svc := fp.oauthService(t, "google", "viewer")

	first, err := svc.testCallback(t, tn, "google")
	if err != nil {
		t.Fatalf("first callback: %v", err)
	}
	second, err := svc.testCallback(t, tn, "google")
	if err != nil {
		t.Fatalf("second callback: %v", err)
	}
	if first.User.ID != second.User.ID {
		t.Fatalf("returning login created a new user: %s != %s", first.User.ID, second.User.ID)
	}
	if n := countRows(t, `"tenant_`+tn+`".auth_identities`); n != 1 {
		t.Fatalf("expected exactly 1 identity row, got %d", n)
	}
	if n := countRows(t, `"tenant_`+tn+`".auth_users`); n != 1 {
		t.Fatalf("expected exactly 1 user, got %d", n)
	}
}

func TestOAuth_LinksToExistingPasswordUser(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test requires Postgres")
	}
	const tn = "oauthlink"
	createTenantSchema(t, tn)
	ctx := context.Background()
	// A password user already exists with this email.
	pwSvc := NewService(NewStore(testPool), Config{JWTSecret: testSecret, SignupRole: "viewer", LoginBurst: 100, TokenTTL: time.Hour})
	signup, err := pwSvc.Signup(ctx, tn, "both@example.com", "the-password-1")
	if err != nil {
		t.Fatalf("signup: %v", err)
	}

	fp := newFakeProvider(t, map[string]any{"sub": "google-link-1", "email": "both@example.com"}, nil)
	oaSvc := fp.oauthService(t, "google", "viewer")
	res, err := oaSvc.testCallback(t, tn, "google")
	if err != nil {
		t.Fatalf("oauth callback: %v", err)
	}
	// SAME user — the identity linked to the existing account, no duplicate.
	if res.User.ID != signup.User.ID {
		t.Fatalf("oauth created a duplicate instead of linking: %s != %s", res.User.ID, signup.User.ID)
	}
	if n := countRows(t, `"tenant_`+tn+`".auth_users`); n != 1 {
		t.Fatalf("expected 1 user after link, got %d", n)
	}
	// The original password still works (linking didn't disturb it).
	if _, err := pwSvc.Login(ctx, tn, "both@example.com", "the-password-1"); err != nil {
		t.Fatalf("password login after link failed: %v", err)
	}
}

func TestOAuth_CrossTenantIsolation(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test requires Postgres")
	}
	const tnA, tnB = "oauthisoa", "oauthisob"
	createTenantSchema(t, tnA)
	createTenantSchema(t, tnB)
	// SAME provider identity + email, but two different tenant contexts.
	fp := newFakeProvider(t, map[string]any{"sub": "google-shared-id", "email": "shared@example.com"}, nil)
	svc := fp.oauthService(t, "google", "viewer")

	a, err := svc.testCallback(t, tnA, "google")
	if err != nil {
		t.Fatalf("callback A: %v", err)
	}
	b, err := svc.testCallback(t, tnB, "google")
	if err != nil {
		t.Fatalf("callback B: %v", err)
	}
	// The same Google account is a DISTINCT user in each tenant (the advantage;
	// A's identity never authenticated B).
	if a.User.ID == b.User.ID {
		t.Fatal("same Google identity produced the same user across tenants — isolation broken")
	}
}

func TestOAuth_AutoProvisionDisabled(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test requires Postgres")
	}
	const tn = "oauthnoprov"
	createTenantSchema(t, tn)
	fp := newFakeProvider(t, map[string]any{"sub": "google-x", "email": "x@example.com"}, nil)
	// No default role AND no signup role → brand-new email cannot be provisioned.
	svc := fp.oauthService(t, "google", "")
	if _, err := svc.testCallback(t, tn, "google"); err != ErrOAuthNoAutoUser {
		t.Fatalf("auto-provision: got %v, want ErrOAuthNoAutoUser", err)
	}
}

func TestOAuth_GitHubEmailFallback(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test requires Postgres")
	}
	const tn = "oauthgh"
	createTenantSchema(t, tn)
	// GitHub userinfo hides the email (null); the /emails endpoint has the primary.
	fp := newFakeProvider(t,
		map[string]any{"id": 424242, "email": nil},
		[]map[string]any{{"email": "ghuser@example.com", "primary": true, "verified": true}})
	svc := fp.oauthService(t, "github", "viewer")
	res, err := svc.testCallback(t, tn, "github")
	if err != nil {
		t.Fatalf("github callback: %v", err)
	}
	if res.User.Email != "ghuser@example.com" {
		t.Fatalf("github email fallback failed: %q", res.User.Email)
	}
}

func TestOAuth_StateProviderMismatch(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test requires Postgres")
	}
	const tn = "oauthmism"
	createTenantSchema(t, tn)
	fp := newFakeProvider(t, map[string]any{"sub": "g", "email": "m@example.com"}, nil)
	svc := fp.oauthService(t, "google", "viewer")
	// A state minted for "github" used on the google callback must be rejected.
	state, _ := svc.signOAuthState(tn, "github")
	_, err := svc.OAuthCallback(context.Background(), "google", "code", state, "http://cb")
	if err != ErrOAuthState {
		t.Fatalf("provider-mismatched state: got %v, want ErrOAuthState", err)
	}
}

func TestOAuth_TokenWorksThroughMiddleware(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test requires Postgres")
	}
	const tn = "oauthmw"
	createTenantSchema(t, tn)
	fp := newFakeProvider(t, map[string]any{"sub": "g-mw", "email": "mw@example.com"}, nil)
	svc := fp.oauthService(t, "google", "viewer")
	res, err := svc.testCallback(t, tn, "google")
	if err != nil {
		t.Fatalf("callback: %v", err)
	}

	var gotRole, gotTenant string
	final := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if c := auth.ClaimsFromCtx(r.Context()); c != nil {
			gotRole, gotTenant = c.Role, c.TenantID
		}
		w.WriteHeader(http.StatusOK)
	})
	h := auth.JWTMiddleware(testSecret)(final)
	req := httptest.NewRequest(http.MethodGet, "/api/tasks", nil)
	req = req.WithContext(tenant.WithContext(req.Context(), &tenant.TenantCtx{ID: tn, PGSchema: "tenant_" + tn}))
	req.Header.Set("Authorization", "Bearer "+res.Token)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("oauth token did not pass JWTMiddleware: %d", rec.Code)
	}
	if gotRole != "viewer" || gotTenant != tn {
		t.Fatalf("claims wrong: role=%q tenant=%q", gotRole, gotTenant)
	}
}
