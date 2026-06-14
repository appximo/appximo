package userauth

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/miguelangel/appitools/pkg/auth"
	"github.com/miguelangel/appitools/pkg/tenant"
)

const testSecret = "an-integration-test-secret-32+chars!!"

var testPool *pgxpool.Pool

func TestMain(m *testing.M) {
	flag.Parse()
	if testing.Short() {
		os.Exit(m.Run()) // integration tests self-skip via requirePG; unit tests run
	}
	ctx := context.Background()
	ctr, err := tcpostgres.Run(ctx,
		"postgres:16-alpine",
		tcpostgres.WithDatabase("testdb"),
		tcpostgres.WithUsername("testuser"),
		tcpostgres.WithPassword("testpass"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").WithOccurrence(2),
		),
	)
	if err != nil {
		fmt.Fprintln(os.Stderr, "userauth test: start postgres:", err)
		os.Exit(1)
	}
	dsn, err := ctr.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		fmt.Fprintln(os.Stderr, "userauth test: dsn:", err)
		_ = ctr.Terminate(ctx)
		os.Exit(1)
	}
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		fmt.Fprintln(os.Stderr, "userauth test: pool:", err)
		_ = ctr.Terminate(ctx)
		os.Exit(1)
	}
	testPool = pool
	code := m.Run()
	pool.Close()
	_ = ctr.Terminate(ctx)
	os.Exit(code)
}

// requirePG skips in -short and returns a fresh Service. Each test uses unique
// tenant ids so they never see each other's rows (no shared-table truncation).
func requirePG(t *testing.T) *Service {
	t.Helper()
	if testing.Short() {
		t.Skip("integration test requires Postgres (skipped in -short)")
	}
	return NewService(NewStore(testPool), Config{
		JWTSecret:  testSecret,
		SignupRole: "viewer",
		TokenTTL:   time.Hour,
		LoginBurst: 100, // don't throttle within a test
	})
}

// createTenantSchema creates the tenant_<id> Postgres schema the engine would
// create at tenant registration; the auth_users table lives inside it.
func createTenantSchema(t *testing.T, id string) {
	t.Helper()
	_, err := testPool.Exec(context.Background(),
		fmt.Sprintf(`CREATE SCHEMA IF NOT EXISTS "tenant_%s"`, id))
	if err != nil {
		t.Fatalf("create schema tenant_%s: %v", id, err)
	}
}

func TestIntegration_SignupCreatesIsolatedUser(t *testing.T) {
	svc := requirePG(t)
	const tn = "acme1"
	createTenantSchema(t, tn)
	ctx := context.Background()

	res, err := svc.Signup(ctx, tn, "Alice@Example.com", "supersecret123")
	if err != nil {
		t.Fatalf("signup: %v", err)
	}
	if res.User.ID == "" || res.Token == "" {
		t.Fatal("signup returned empty id/token")
	}
	if res.User.EmailVerified {
		t.Fatal("a fresh signup must have email_verified=false")
	}
	if res.User.Email != "alice@example.com" {
		t.Fatalf("email not normalized to lowercase: %q", res.User.Email)
	}
	if res.User.Role != "viewer" {
		t.Fatalf("signup role should be the configured default, got %q", res.User.Role)
	}

	// The stored hash must be a real argon2id hash that verifies the password,
	// and must NEVER appear in the public response (PublicUser has no such field).
	stored, err := svc.store.GetByEmail(ctx, tn, "alice@example.com")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if !strings.HasPrefix(stored.PasswordHash, "$argon2id$") {
		t.Fatalf("password not argon2id-hashed: %q", stored.PasswordHash)
	}
	if ok, _ := VerifyPassword("supersecret123", stored.PasswordHash); !ok {
		t.Fatal("stored hash does not verify the password")
	}
}

func TestIntegration_DuplicateEmailSameTenant(t *testing.T) {
	svc := requirePG(t)
	const tn = "acme2"
	createTenantSchema(t, tn)
	ctx := context.Background()

	if _, err := svc.Signup(ctx, tn, "dup@x.com", "password111"); err != nil {
		t.Fatalf("first signup: %v", err)
	}
	// Same email, even different case, must conflict within the tenant.
	_, err := svc.Signup(ctx, tn, "DUP@x.com", "password222")
	if err != ErrEmailTaken {
		t.Fatalf("expected ErrEmailTaken on duplicate, got %v", err)
	}
}

// TestIntegration_SameEmailDifferentTenants is THE advantage over Supabase: the
// same email is a DISTINCT user in two tenants (email unique per-schema, not
// global). Both signups succeed, the users are independent, and each tenant's
// password authenticates only against its own user.
func TestIntegration_SameEmailDifferentTenants(t *testing.T) {
	svc := requirePG(t)
	const tnA, tnB = "alpha", "beta"
	createTenantSchema(t, tnA)
	createTenantSchema(t, tnB)
	ctx := context.Background()

	const email = "shared@example.com"
	a, err := svc.Signup(ctx, tnA, email, "alpha-password")
	if err != nil {
		t.Fatalf("signup in tenant A: %v", err)
	}
	b, err := svc.Signup(ctx, tnB, email, "beta-password")
	if err != nil {
		t.Fatalf("signup in tenant B with the SAME email must succeed (the advantage): %v", err)
	}
	if a.User.ID == b.User.ID {
		t.Fatal("same email across tenants produced the same user id — not isolated")
	}

	// Each tenant's user authenticates only with its own password.
	if _, err := svc.Login(ctx, tnA, email, "alpha-password"); err != nil {
		t.Fatalf("tenant A login with A's password failed: %v", err)
	}
	if _, err := svc.Login(ctx, tnB, email, "beta-password"); err != nil {
		t.Fatalf("tenant B login with B's password failed: %v", err)
	}
	// Tenant A's password must NOT authenticate the (distinct) tenant B user.
	if _, err := svc.Login(ctx, tnB, email, "alpha-password"); err != ErrInvalidCredentials {
		t.Fatalf("A's password authenticated B's user — isolation broken (err=%v)", err)
	}
}

func TestIntegration_LoginFlows(t *testing.T) {
	svc := requirePG(t)
	const tn = "loginflows"
	createTenantSchema(t, tn)
	ctx := context.Background()

	if _, err := svc.Signup(ctx, tn, "bob@x.com", "bobs-password"); err != nil {
		t.Fatalf("signup: %v", err)
	}

	// Correct credentials → engine-valid token.
	res, err := svc.Login(ctx, tn, "bob@x.com", "bobs-password")
	if err != nil {
		t.Fatalf("valid login: %v", err)
	}
	if _, err := auth.ValidateToken(res.Token, testSecret); err != nil {
		t.Fatalf("login token rejected by engine validator: %v", err)
	}

	// Wrong password and unknown email must return the SAME generic error.
	_, wrongPw := svc.Login(ctx, tn, "bob@x.com", "not-the-password")
	_, unknown := svc.Login(ctx, tn, "ghost@x.com", "whatever-pass")
	if wrongPw != ErrInvalidCredentials || unknown != ErrInvalidCredentials {
		t.Fatalf("anti-enumeration broken: wrongPw=%v unknown=%v (both must be ErrInvalidCredentials)", wrongPw, unknown)
	}
}

// TestIntegration_CrossTenantLoginIsolation proves a login in tenant B cannot
// authenticate a user that only exists in tenant A — the schema search scope
// makes it structurally impossible.
func TestIntegration_CrossTenantLoginIsolation(t *testing.T) {
	svc := requirePG(t)
	const tnA, tnB = "isoa", "isob"
	createTenantSchema(t, tnA)
	createTenantSchema(t, tnB)
	ctx := context.Background()

	if _, err := svc.Signup(ctx, tnA, "only-in-a@x.com", "secretpass99"); err != nil {
		t.Fatalf("signup in A: %v", err)
	}
	// Same email + correct A-password, but attempted in tenant B's context.
	if _, err := svc.Login(ctx, tnB, "only-in-a@x.com", "secretpass99"); err != ErrInvalidCredentials {
		t.Fatalf("cross-tenant login leaked: got %v, want ErrInvalidCredentials", err)
	}
}

// TestIntegration_TokenWorksThroughEngineMiddleware is the e2e proof that a login
// token drives a real authenticated request: it passes the engine's actual
// JWTMiddleware (the per-request hot-path validator) with the right tenant and
// carries the role RBAC will enforce.
func TestIntegration_TokenWorksThroughEngineMiddleware(t *testing.T) {
	svc := requirePG(t)
	const tn = "e2etok"
	createTenantSchema(t, tn)
	ctx := context.Background()

	res, err := svc.Signup(ctx, tn, "carol@x.com", "carols-password")
	if err != nil {
		t.Fatalf("signup: %v", err)
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
		t.Fatalf("login token did not pass JWTMiddleware: status %d", rec.Code)
	}
	if gotRole != "viewer" || gotTenant != tn {
		t.Fatalf("claims wrong through middleware: role=%q tenant=%q", gotRole, gotTenant)
	}
}

// withTenant wraps a handler to inject the TenantCtx the engine's TenantMiddleware
// would set from the Host subdomain.
func withTenant(id string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := tenant.WithContext(r.Context(), &tenant.TenantCtx{ID: id, PGSchema: "tenant_" + id})
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func TestIntegration_HTTPEndpoints(t *testing.T) {
	svc := requirePG(t)
	const tn = "httptest1"
	createTenantSchema(t, tn)
	h := withTenant(tn, svc.Router())

	post := func(path, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}

	// Signup → 201, and the password hash must NEVER appear in the response body.
	rec := post("/signup", `{"email":"dave@x.com","password":"daves-password"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("signup status %d: %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(strings.ToLower(rec.Body.String()), "hash") ||
		strings.Contains(rec.Body.String(), "daves-password") {
		t.Fatalf("signup response leaked hash/password: %s", rec.Body.String())
	}

	// Client-supplied role must be IGNORED (no privilege escalation to admin).
	rec = post("/signup", `{"email":"mallory@x.com","password":"mallory-pass","role":"admin"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("signup-with-role status %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"role":"viewer"`) {
		t.Fatalf("client role was honored — escalation! body: %s", rec.Body.String())
	}

	// Duplicate → 409.
	if rec = post("/signup", `{"email":"dave@x.com","password":"other-password"}`); rec.Code != http.StatusConflict {
		t.Fatalf("duplicate signup status %d, want 409", rec.Code)
	}

	// Login correct → 200; wrong → 401 with the generic message.
	if rec = post("/login", `{"email":"dave@x.com","password":"daves-password"}`); rec.Code != http.StatusOK {
		t.Fatalf("login status %d: %s", rec.Code, rec.Body.String())
	}
	rec = post("/login", `{"email":"dave@x.com","password":"WRONG"}`)
	if rec.Code != http.StatusUnauthorized || !strings.Contains(rec.Body.String(), "invalid credentials") {
		t.Fatalf("wrong-password login: status %d body %s", rec.Code, rec.Body.String())
	}

	// Refresh with the login token in the Authorization header → 200 new token.
	login := post("/login", `{"email":"dave@x.com","password":"daves-password"}`)
	var lr struct{ Token string }
	mustJSON(t, login.Body.String(), &lr)
	req := httptest.NewRequest(http.MethodPost, "/refresh", strings.NewReader("{}"))
	req.Header.Set("Authorization", "Bearer "+lr.Token)
	rrec := httptest.NewRecorder()
	h.ServeHTTP(rrec, req)
	if rrec.Code != http.StatusOK || !strings.Contains(rrec.Body.String(), "token") {
		t.Fatalf("refresh: status %d body %s", rrec.Code, rrec.Body.String())
	}
}

func TestIntegration_SignupDisabledReturns403(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test requires Postgres")
	}
	svc := NewService(NewStore(testPool), Config{JWTSecret: testSecret}) // no SignupRole
	h := withTenant("disabledtn", svc.Router())
	req := httptest.NewRequest(http.MethodPost, "/signup", strings.NewReader(`{"email":"a@b.co","password":"longenough"}`))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("disabled signup status %d, want 403", rec.Code)
	}
}

func mustJSON(t *testing.T, body string, dst any) {
	t.Helper()
	if err := json.Unmarshal([]byte(body), dst); err != nil {
		t.Fatalf("unmarshal %q: %v", body, err)
	}
}
