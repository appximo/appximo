package platformadmin

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/miguelangel/appitools/pkg/auth"
	"github.com/miguelangel/appitools/pkg/controlplane"
	"github.com/miguelangel/appitools/pkg/db"
	"github.com/miguelangel/appitools/pkg/schema"
	"github.com/miguelangel/appitools/pkg/userauth"
)

const itSecret = "platformadmin-integration-secret-32+chars!!"

var testPool *pgxpool.Pool

// quickSchema is a minimal valid schema for tenant creation in tests.
const quickSchema = `{"$schema":"https://appitools.dev/schema/v1","version":"1","name":"todo-api",
"resources":{"tasks":{"fields":{"title":{"type":"string","required":true}}}},
"rbac":{"roles":{"admin":{"resources":"*","actions":["*"]},
"viewer":{"resources":["tasks"],"actions":["read"]}}}}`

func TestMain(m *testing.M) {
	flag.Parse()
	if testing.Short() {
		os.Exit(m.Run())
	}
	ctx := context.Background()
	ctr, err := tcpostgres.Run(ctx,
		"postgres:16-alpine",
		tcpostgres.WithDatabase("testdb"),
		tcpostgres.WithUsername("testuser"),
		tcpostgres.WithPassword("testpass"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").WithOccurrence(2)),
	)
	if err != nil {
		fmt.Fprintln(os.Stderr, "platformadmin test: start postgres:", err)
		os.Exit(1)
	}
	dsn, err := ctr.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		fmt.Fprintln(os.Stderr, "platformadmin test: dsn:", err)
		_ = ctr.Terminate(ctx)
		os.Exit(1)
	}
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		fmt.Fprintln(os.Stderr, "platformadmin test: pool:", err)
		_ = ctr.Terminate(ctx)
		os.Exit(1)
	}
	if err := applyControlPlaneSchema(ctx, pool); err != nil {
		fmt.Fprintln(os.Stderr, "platformadmin test: control-plane schema:", err)
		pool.Close()
		_ = ctr.Terminate(ctx)
		os.Exit(1)
	}
	testPool = pool
	code := m.Run()
	pool.Close()
	_ = ctr.Terminate(ctx)
	os.Exit(code)
}

// applyControlPlaneSchema creates public.tenants + tenant_policies + migration_log
// (the control-plane tables RegisterTenant writes to).
func applyControlPlaneSchema(ctx context.Context, pool *pgxpool.Pool) error {
	b, err := os.ReadFile("../../migrations/001_control_plane.sql")
	if err != nil {
		return err
	}
	_, err = pool.Exec(ctx, string(b))
	return err
}

func requirePG(t *testing.T) {
	t.Helper()
	if testing.Short() {
		t.Skip("integration test requires Postgres (skipped in -short)")
	}
}

// newSvc builds a platform admin Service wired like app.go (role checker + tenant
// admin checker over a tiny fixed RBAC: "admin" is admin-grade, "viewer" is not).
func newSvc(t *testing.T) *Service {
	t.Helper()
	svc := NewService(NewStore(testPool), userauth.NewStore(testPool),
		controlplane.NewService(testPool, nil), testPool,
		Config{
			JWTSecret:       itSecret,
			RoleExists:      func(r string) bool { return r == "admin" || r == "viewer" },
			TenantAdminRole: func(r string) bool { return r == "admin" },
		})
	svc.SetTenantDB(db.NewTenantDB(testPool))
	return svc
}

func ctxBG() context.Context { return context.Background() }

// --- bootstrap + auth -------------------------------------------------------

func TestIntegration_BootstrapAndLogin(t *testing.T) {
	requirePG(t)
	svc := newSvc(t)
	ctx := ctxBG()

	admin, err := svc.CreateAdmin(ctx, "Root@Example.com", "a-strong-passphrase-123", "")
	if err != nil {
		t.Fatalf("bootstrap create: %v", err)
	}
	if admin.Email != "root@example.com" || admin.Role != DefaultSuperAdminRole {
		t.Fatalf("admin shape wrong: %+v", admin)
	}
	// Duplicate → ErrAdminEmailTaken.
	if _, err := svc.CreateAdmin(ctx, "root@example.com", "another-strong-pass-456", ""); err != ErrAdminEmailTaken {
		t.Fatalf("expected ErrAdminEmailTaken, got %v", err)
	}

	// Login → platform token that is a PLATFORM token but NOT an engine identity.
	res, err := svc.Login(ctx, "root@example.com", "a-strong-passphrase-123")
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	if res.Token == "" || res.MFARequired {
		t.Fatalf("expected a token, no MFA: %+v", res)
	}
	if _, err := parsePlatformToken(res.Token, itSecret); err != nil {
		t.Fatalf("login token is not a valid platform token: %v", err)
	}
	ec, _ := auth.ValidateToken(res.Token, itSecret)
	if ec.Role != "" || ec.TenantID != "" {
		t.Fatalf("SECURITY: platform login token carries engine identity: %+v", ec)
	}

	// Wrong password / unknown admin → uniform invalid credentials.
	if _, err := svc.Login(ctx, "root@example.com", "WRONG"); err != ErrInvalidCredentials {
		t.Fatalf("wrong pw: got %v", err)
	}
	if _, err := svc.Login(ctx, "ghost@example.com", "whatever-pass-123"); err != ErrInvalidCredentials {
		t.Fatalf("unknown admin: got %v", err)
	}
}

// --- tenant management ------------------------------------------------------

func createTenant(t *testing.T, svc *Service, id string) {
	t.Helper()
	if _, err := svc.CreateTenant(ctxBG(), controlplane.RegisterRequest{
		TenantID: id, DisplayName: id, Email: id + "@x.com", Plan: "free", Schema: mustSchema(t),
	}); err != nil {
		t.Fatalf("create tenant %s: %v", id, err)
	}
}

func mustSchema(t *testing.T) *schema.APISchema {
	t.Helper()
	sc, errs := parseSchema(json.RawMessage(quickSchema))
	if len(errs) > 0 {
		t.Fatalf("quickSchema invalid: %v", errs)
	}
	return sc
}

func TestIntegration_TenantLifecycle(t *testing.T) {
	requirePG(t)
	svc := newSvc(t)
	ctx := ctxBG()

	createTenant(t, svc, "acmeco")
	createTenant(t, svc, "globex")

	ts, err := svc.ListTenants(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if !containsTenant(ts, "acmeco") || !containsTenant(ts, "globex") {
		t.Fatalf("list missing tenants: %+v", ts)
	}
	for _, ti := range ts {
		if ti.ID == "acmeco" && ti.ResourceCount != 1 {
			t.Fatalf("acmeco resource_count = %d, want 1", ti.ResourceCount)
		}
	}

	// Suspend → reflected in detail; blocks login (IsTenantActive false).
	if err := svc.SuspendTenant(ctx, "acmeco"); err != nil {
		t.Fatalf("suspend: %v", err)
	}
	d, err := svc.GetTenant(ctx, "acmeco")
	if err != nil || !d.Suspended {
		t.Fatalf("suspend not reflected: %+v err=%v", d, err)
	}
	if svc.IsTenantActive(ctx, "acmeco") {
		t.Fatal("suspended tenant reported active")
	}
	if err := svc.ActivateTenant(ctx, "acmeco"); err != nil {
		t.Fatalf("activate: %v", err)
	}
	if !svc.IsTenantActive(ctx, "acmeco") {
		t.Fatal("reactivated tenant reported inactive")
	}

	// Delete requires the exact confirmation.
	if err := svc.DeleteTenant(ctx, "globex", ""); err != ErrConfirmRequired {
		t.Fatalf("delete without confirm should be refused, got %v", err)
	}
	if err := svc.DeleteTenant(ctx, "globex", "wrong"); err != ErrConfirmRequired {
		t.Fatalf("delete with wrong confirm should be refused, got %v", err)
	}
	if err := svc.DeleteTenant(ctx, "globex", "globex"); err != nil {
		t.Fatalf("delete with confirm: %v", err)
	}
	if _, err := svc.GetTenant(ctx, "globex"); err != ErrTenantNotFound {
		t.Fatalf("deleted tenant still present: %v", err)
	}
	// The Postgres schema must be gone (DROP SCHEMA CASCADE).
	var exists bool
	_ = testPool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM information_schema.schemata WHERE schema_name=$1)`, "tenant_globex").Scan(&exists)
	if exists {
		t.Fatal("tenant schema not dropped on delete")
	}
}

// --- tenant user management + isolation -------------------------------------

func TestIntegration_UserManagementAndIsolation(t *testing.T) {
	requirePG(t)
	svc := newSvc(t)
	ctx := ctxBG()
	createTenant(t, svc, "usera")
	createTenant(t, svc, "userb")

	u, err := svc.CreateUser(ctx, "usera", "alice@x.com", "alices-password", "viewer")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	if u.Role != "viewer" {
		t.Fatalf("role not honored: %+v", u)
	}
	// Unknown role → rejected (inherits the schema RBAC).
	if _, err := svc.CreateUser(ctx, "usera", "bob@x.com", "bobs-password", "nope"); err != ErrUnknownRole {
		t.Fatalf("unknown role should be rejected, got %v", err)
	}

	// Tenant B does NOT see tenant A's user (isolation).
	usB, _ := svc.ListUsers(ctx, "userb")
	if len(usB) != 0 {
		t.Fatalf("tenant B sees %d users, expected 0 (isolation broken)", len(usB))
	}
	usA, _ := svc.ListUsers(ctx, "usera")
	if len(usA) != 1 || usA[0].Email != "alice@x.com" {
		t.Fatalf("tenant A user list wrong: %+v", usA)
	}

	// Operating on A's user through tenant B must not find it (cross-tenant op).
	if err := svc.SetUserSuspended(ctx, "userb", u.ID, true); err != userauth.ErrUserNotFound {
		t.Fatalf("cross-tenant suspend should be not-found, got %v", err)
	}
	// Suspend in the correct tenant works, then a login is blocked.
	if err := svc.SetUserSuspended(ctx, "usera", u.ID, true); err != nil {
		t.Fatalf("suspend user: %v", err)
	}
	ua := userauth.NewService(userauth.NewStore(testPool), userauth.Config{
		JWTSecret: itSecret, LoginBurst: 100, TenantActive: svc.IsTenantActive,
	})
	if _, err := ua.Login(ctx, "usera", "alice@x.com", "alices-password"); err != userauth.ErrUserSuspended {
		t.Fatalf("suspended user login should be blocked, got %v", err)
	}
	// Role change + delete.
	if err := svc.UpdateUserRole(ctx, "usera", u.ID, "admin"); err != nil {
		t.Fatalf("update role: %v", err)
	}
	if err := svc.DeleteUser(ctx, "usera", u.ID); err != nil {
		t.Fatalf("delete user: %v", err)
	}
	if err := svc.DeleteUser(ctx, "usera", u.ID); err != userauth.ErrUserNotFound {
		t.Fatalf("double delete should be not-found, got %v", err)
	}
}

// --- HTTP authorization: platform vs tenant token ---------------------------

func TestIntegration_HTTPAuthorization(t *testing.T) {
	requirePG(t)
	svc := newSvc(t)
	ctx := ctxBG()
	if _, err := svc.CreateAdmin(ctx, "http@x.com", "a-strong-passphrase-123", ""); err != nil {
		t.Fatalf("create admin: %v", err)
	}
	createTenant(t, svc, "httpten")

	r := chi.NewRouter()
	svc.Register(r, &obsStub{}, "machine-key")

	// platform token via login.
	lr, _ := svc.Login(ctx, "http@x.com", "a-strong-passphrase-123")
	platTok := lr.Token
	// a tenant admin token (engine identity, role admin).
	tenantTok, _ := auth.GenerateToken(auth.Claims{UserID: "u", Role: "admin", TenantID: "httpten"}, itSecret)

	do := func(method, path, bearer, key string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, nil)
		if bearer != "" {
			req.Header.Set("Authorization", "Bearer "+bearer)
		}
		if key != "" {
			req.Header.Set("X-Admin-Key", key)
		}
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		return rec
	}

	// Super admin lists tenants (200); machine key also works (200).
	if rec := do("GET", "/admin/tenants", platTok, ""); rec.Code != 200 {
		t.Fatalf("platform list tenants: %d %s", rec.Code, rec.Body.String())
	}
	if rec := do("GET", "/admin/tenants", "", "machine-key"); rec.Code != 200 {
		t.Fatalf("machine-key list tenants: %d", rec.Code)
	}
	// A tenant token must NOT list all tenants (403).
	if rec := do("GET", "/admin/tenants", tenantTok, ""); rec.Code != http.StatusForbidden {
		t.Fatalf("tenant token listed all tenants: %d (want 403)", rec.Code)
	}
	// No credentials → 403.
	if rec := do("GET", "/admin/tenants", "", ""); rec.Code != http.StatusForbidden {
		t.Fatalf("anon list tenants: %d (want 403)", rec.Code)
	}

	// Observability: platform sees any; tenant admin sees own; tenant admin OTHER → 403.
	if rec := do("GET", "/admin/observability/tenants/httpten", platTok, ""); rec.Code != 200 {
		t.Fatalf("platform obs: %d", rec.Code)
	}
	if rec := do("GET", "/admin/observability/tenants/httpten", tenantTok, ""); rec.Code != 200 {
		t.Fatalf("tenant-admin obs own tenant: %d", rec.Code)
	}
	if rec := do("GET", "/admin/observability/tenants/othertenant", tenantTok, ""); rec.Code != http.StatusForbidden {
		t.Fatalf("tenant-admin obs OTHER tenant: %d (want 403)", rec.Code)
	}

	// MFA enable needs a platform token specifically — a tenant token is 401.
	if rec := do("POST", "/admin/auth/mfa/enable", tenantTok, ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("tenant token on mfa/enable: %d (want 401)", rec.Code)
	}
	// And the admin key alone is NOT enough for mfa/enable (no identity).
	if rec := do("POST", "/admin/auth/mfa/enable", "", "machine-key"); rec.Code != http.StatusUnauthorized {
		t.Fatalf("admin key on mfa/enable: %d (want 401)", rec.Code)
	}
}

// --- MFA flow ---------------------------------------------------------------

func TestIntegration_SuperAdminMFA(t *testing.T) {
	requirePG(t)
	svc := newSvc(t)
	ctx := ctxBG()
	admin, err := svc.CreateAdmin(ctx, "mfa@x.com", "a-strong-passphrase-123", "")
	if err != nil {
		t.Fatalf("create admin: %v", err)
	}

	secret, _, err := svc.EnableMFA(ctx, admin.ID)
	if err != nil {
		t.Fatalf("enable mfa: %v", err)
	}
	code, err := userauth.TOTPCodeNow(secret)
	if err != nil {
		t.Fatalf("totp code: %v", err)
	}
	backups, err := svc.ConfirmMFA(ctx, admin.ID, code)
	if err != nil || len(backups) != 10 {
		t.Fatalf("confirm mfa: %v (codes=%d)", err, len(backups))
	}

	// Now login returns an MFA challenge, not a token.
	lr, err := svc.Login(ctx, "mfa@x.com", "a-strong-passphrase-123")
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	if !lr.MFARequired || lr.Token != "" || lr.MFAToken == "" {
		t.Fatalf("expected MFA challenge: %+v", lr)
	}
	// Completing it with a fresh TOTP code mints the final token.
	code2, _ := userauth.TOTPCodeNow(secret)
	fin, err := svc.MFAVerify(ctx, lr.MFAToken, code2)
	if err != nil || fin.Token == "" {
		t.Fatalf("mfa verify: %v %+v", err, fin)
	}
	if _, err := parsePlatformToken(fin.Token, itSecret); err != nil {
		t.Fatalf("final token invalid: %v", err)
	}
	// A backup code also completes a fresh challenge (one-time).
	lr2, _ := svc.Login(ctx, "mfa@x.com", "a-strong-passphrase-123")
	if _, err := svc.MFAVerify(ctx, lr2.MFAToken, backups[0]); err != nil {
		t.Fatalf("mfa verify with backup code: %v", err)
	}
	// The same backup code cannot be reused.
	lr3, _ := svc.Login(ctx, "mfa@x.com", "a-strong-passphrase-123")
	if _, err := svc.MFAVerify(ctx, lr3.MFAToken, backups[0]); err != ErrMFAInvalidCode {
		t.Fatalf("reused backup code accepted: %v", err)
	}
}

// --- data navigation (read-only browse) -------------------------------------

func TestIntegration_DataBrowsing(t *testing.T) {
	requirePG(t)
	svc := newSvc(t)
	ctx := ctxBG()
	createTenant(t, svc, "databrowse")

	// Insert a couple of rows directly into the tenant's table (the control plane
	// created tenant_databrowse.tasks at registration).
	for i, title := range []string{"first", "second"} {
		if _, err := testPool.Exec(ctx,
			`INSERT INTO tenant_databrowse.tasks (title) VALUES ($1)`, title); err != nil {
			t.Fatalf("seed row %d: %v", i, err)
		}
	}

	// ListResources reflects the schema (tasks, with id first + title).
	rs, err := svc.ListResources(ctx, "databrowse")
	if err != nil {
		t.Fatalf("list resources: %v", err)
	}
	var tasks *ResourceInfo
	for i := range rs {
		if rs[i].Name == "tasks" {
			tasks = &rs[i]
		}
	}
	if tasks == nil {
		t.Fatalf("tasks resource not listed: %+v", rs)
	}
	if tasks.Fields[0].Name != "id" {
		t.Fatalf("first field should be id, got %+v", tasks.Fields)
	}

	// ListData returns the rows with meta.
	res, err := svc.ListData(ctx, "databrowse", "tasks", url.Values{})
	if err != nil {
		t.Fatalf("list data: %v", err)
	}
	data, _ := res["data"].([]map[string]any)
	if len(data) != 2 {
		t.Fatalf("expected 2 rows, got %d (%+v)", len(data), res)
	}

	// Unknown resource → ErrResourceNotFound.
	if _, err := svc.ListData(ctx, "databrowse", "nope", url.Values{}); err != ErrResourceNotFound {
		t.Fatalf("unknown resource: got %v", err)
	}

	// A filter is honored (and validated by the reused query builder).
	filtered, err := svc.ListData(ctx, "databrowse", "tasks", url.Values{"filter[title]": {"first"}})
	if err != nil {
		t.Fatalf("filtered list: %v", err)
	}
	fd, _ := filtered["data"].([]map[string]any)
	if len(fd) != 1 {
		t.Fatalf("filter[title]=first expected 1 row, got %d", len(fd))
	}
}

// --- e2e --------------------------------------------------------------------

func TestIntegration_EndToEnd(t *testing.T) {
	requirePG(t)
	svc := newSvc(t)
	ctx := ctxBG()

	// bootstrap → login → create tenant → create user → user logs in.
	if _, err := svc.CreateAdmin(ctx, "e2e@x.com", "a-strong-passphrase-123", ""); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	lr, err := svc.Login(ctx, "e2e@x.com", "a-strong-passphrase-123")
	if err != nil || lr.Token == "" {
		t.Fatalf("login: %v", err)
	}
	createTenant(t, svc, "e2eten")
	if _, err := svc.CreateUser(ctx, "e2eten", "user@x.com", "users-password", "admin"); err != nil {
		t.Fatalf("create user: %v", err)
	}
	ua := userauth.NewService(userauth.NewStore(testPool), userauth.Config{
		JWTSecret: itSecret, LoginBurst: 100, TenantActive: svc.IsTenantActive,
	})
	res, err := ua.Login(ctx, "e2eten", "user@x.com", "users-password")
	if err != nil || res.Token == "" {
		t.Fatalf("tenant user login: %v", err)
	}
	// The minted token is a real engine token for that tenant.
	ec, err := auth.ValidateToken(res.Token, itSecret)
	if err != nil || ec.TenantID != "e2eten" || ec.Role != "admin" {
		t.Fatalf("tenant user token wrong: %v %+v", err, ec)
	}
}

// --- helpers ----------------------------------------------------------------

func containsTenant(ts []TenantInfo, id string) bool {
	for _, t := range ts {
		if t.ID == id {
			return true
		}
	}
	return false
}

// obsStub is a minimal ObsHandler that records it was called.
type obsStub struct{}

func (o *obsStub) ServeTenantData(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]string{"tenant": chi.URLParam(r, "id")})
}
