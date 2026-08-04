package platformadmin

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/appximo/appximo/pkg/auth"
)

const unitSecret = "a-platform-admin-unit-test-secret-32+chars!!"

// TestPlatformTokenDistinguishable proves the two token worlds cannot be confused:
//   - a platform token parses with parsePlatformToken but, run through the ENGINE's
//     auth.ValidateToken (the per-request validator), yields NO role and NO tenant
//     → the engine RBAC denies it (a platform JWT can never act as a tenant);
//   - a tenant token (auth.Claims) fails parsePlatformToken (no platform scope)
//     → it can never authorize a super-admin route.
func TestPlatformTokenDistinguishable(t *testing.T) {
	platTok, err := signPlatformToken("admin-1", "platform_super_admin", unitSecret)
	if err != nil {
		t.Fatalf("sign platform token: %v", err)
	}
	// It validates as a platform token.
	pc, err := parsePlatformToken(platTok, unitSecret)
	if err != nil || pc.AdminID != "admin-1" || pc.Scope != platformScope {
		t.Fatalf("platform token did not round-trip: %v %+v", err, pc)
	}
	// Run through the engine validator: signed with the same secret, so it parses —
	// but it carries no engine identity (Role/TenantID empty) → RBAC deny by default.
	ec, err := auth.ValidateToken(platTok, unitSecret)
	if err != nil {
		t.Fatalf("platform token unexpectedly rejected by engine validator: %v", err)
	}
	if ec.Role != "" || ec.TenantID != "" || ec.UserID != "" {
		t.Fatalf("SECURITY: platform token carried engine identity: %+v", ec)
	}

	// A normal tenant token must NOT be accepted as a platform token.
	tenantTok, err := auth.GenerateToken(auth.Claims{UserID: "u1", Role: "admin", TenantID: "acme"}, unitSecret)
	if err != nil {
		t.Fatalf("gen tenant token: %v", err)
	}
	if _, err := parsePlatformToken(tenantTok, unitSecret); err == nil {
		t.Fatal("SECURITY: a tenant token was accepted as a platform token")
	}
}

// TestPlatformTokenRejectsWrongSecretAndAlg locks down the obvious forgeries.
func TestPlatformTokenRejectsWrongSecretAndAlg(t *testing.T) {
	tok, _ := signPlatformToken("a", "r", unitSecret)
	if _, err := parsePlatformToken(tok, "the-wrong-secret-but-also-32+chars-long"); err == nil {
		t.Fatal("wrong-secret platform token accepted")
	}
	// alg=none style forgery.
	none := jwt.NewWithClaims(jwt.SigningMethodNone, PlatformClaims{AdminID: "x", Scope: platformScope,
		RegisteredClaims: jwt.RegisteredClaims{ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour))}})
	s, _ := none.SignedString(jwt.UnsafeAllowNoneSignatureType)
	if _, err := parsePlatformToken(s, unitSecret); err == nil {
		t.Fatal("alg=none platform token accepted")
	}
}

// newUnitService builds a Service with no DB deps — safe because the methods under
// test (authorizeObs, token parsing) never touch the pool.
func newUnitService(adminKey string, tenantAdmin func(string) bool) *Service {
	s := NewService(nil, nil, nil, nil, Config{
		JWTSecret:       unitSecret,
		TenantAdminRole: tenantAdmin,
	})
	s.adminKey = adminKey
	return s
}

func reqWithBearer(tok string) *http.Request {
	r := httptest.NewRequest(http.MethodGet, "/admin/observability/tenants/acme", nil)
	if tok != "" {
		r.Header.Set("Authorization", "Bearer "+tok)
	}
	return r
}

// TestAuthorizeObs covers the observability authorization matrix (Step 7/R5).
func TestAuthorizeObs(t *testing.T) {
	adminRole := func(role string) bool { return role == "admin" }
	s := newUnitService("machine-key", adminRole)

	platTok, _ := signPlatformToken("a1", "platform_super_admin", unitSecret)
	adminAcme, _ := auth.GenerateToken(auth.Claims{UserID: "u", Role: "admin", TenantID: "acme"}, unitSecret)
	adminOther, _ := auth.GenerateToken(auth.Claims{UserID: "u", Role: "admin", TenantID: "other"}, unitSecret)
	viewerAcme, _ := auth.GenerateToken(auth.Claims{UserID: "u", Role: "viewer", TenantID: "acme"}, unitSecret)

	cases := []struct {
		name   string
		req    *http.Request
		tenant string
		want   bool
	}{
		{"platform sees any", reqWithBearer(platTok), "acme", true},
		{"platform sees other", reqWithBearer(platTok), "anything", true},
		{"tenant admin own tenant", reqWithBearer(adminAcme), "acme", true},
		{"tenant admin OTHER tenant denied", reqWithBearer(adminOther), "acme", false},
		{"non-admin role denied", reqWithBearer(viewerAcme), "acme", false},
		{"no token denied", reqWithBearer(""), "acme", false},
	}
	for _, c := range cases {
		if got := s.authorizeObs(c.req, c.tenant); got != c.want {
			t.Errorf("%s: authorizeObs=%v want %v", c.name, got, c.want)
		}
	}

	// The machine admin key authorizes any tenant.
	keyReq := reqWithBearer("")
	keyReq.Header.Set("X-Admin-Key", "machine-key")
	if !s.authorizeObs(keyReq, "acme") {
		t.Error("admin key should authorize observability for any tenant")
	}
}

func TestThrottle(t *testing.T) {
	th := newLoginThrottle(5, 3)
	allowed := 0
	for i := 0; i < 10; i++ {
		if th.allow("k") {
			allowed++
		}
	}
	if allowed != 3 { // burst 3, then throttled within the same second
		t.Fatalf("expected 3 allowed (burst), got %d", allowed)
	}
	// A different key has its own bucket.
	if !th.allow("other") {
		t.Fatal("a fresh key must be allowed")
	}
}

func TestValidEmail(t *testing.T) {
	ok := []string{"a@b.co", "x.y+z@sub.example.com"}
	bad := []string{"", "no-at", "a@b", "a@@b.co", "a@.com"}
	for _, e := range ok {
		if !validEmail(e) {
			t.Errorf("expected valid: %q", e)
		}
	}
	for _, e := range bad {
		if validEmail(e) {
			t.Errorf("expected invalid: %q", e)
		}
	}
}

func TestParseSchema(t *testing.T) {
	good := json.RawMessage(`{"$schema":"https://appximo.com/schema/v1","version":"1","name":"t",
		"resources":{"tasks":{"fields":{"title":{"type":"string"}}}},
		"rbac":{"roles":{"admin":{"resources":"*","actions":["*"]}}}}`)
	if sc, errs := parseSchema(good); len(errs) > 0 || sc == nil {
		t.Fatalf("valid schema rejected: %v", errs)
	}
	// Unknown top-level key must reject (strict keys).
	bad := json.RawMessage(`{"$schema":"x","version":"1","resources":{},"webhooks":{}}`)
	if _, errs := parseSchema(bad); len(errs) == 0 {
		t.Fatal("schema with unknown key should be rejected")
	}
	if _, errs := parseSchema(json.RawMessage(`null`)); len(errs) == 0 {
		t.Fatal("null schema should be rejected")
	}
}
