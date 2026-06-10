package integration_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	chi "github.com/go-chi/chi/v5"
	jwtlib "github.com/golang-jwt/jwt/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/miguelangel/appitools/pkg/auth"
	"github.com/miguelangel/appitools/pkg/codegen"
	"github.com/miguelangel/appitools/pkg/controlplane"
	"github.com/miguelangel/appitools/pkg/db"
	"github.com/miguelangel/appitools/pkg/extensions"
	"github.com/miguelangel/appitools/pkg/migration"
	rbacpkg "github.com/miguelangel/appitools/pkg/rbac"
	"github.com/miguelangel/appitools/pkg/schema"
	"github.com/miguelangel/appitools/pkg/tenant"
	"github.com/redis/go-redis/v9"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

// ── constants ─────────────────────────────────────────────────────────────────

const (
	jwtSecret   = "e2e-jwt-secret-abc123"
	adminKey    = "e2e-admin-key-xyz789"
	tenantID    = "acmetest"
	superID     = "00000000-0000-0000-0000-000000000001"
	operario1ID = "11111111-0000-0000-0000-000000000001"
	operario2ID = "22222222-0000-0000-0000-000000000002"
)

// ── schema definition ─────────────────────────────────────────────────────────

func e2eSchema() *schema.APISchema {
	return &schema.APISchema{
		Schema:  "https://appitools.dev/schema/v1",
		Version: "1",
		Name:    "logistics-e2e",
		Resources: map[string]schema.ResourceSchema{
			"guides": {
				Fields: map[string]schema.FieldDef{
					"code":        {Type: "string", Required: true},
					"status":      {Type: "string"},
					"operator_id": {Type: "uuid"},
					"created_at":  {Type: "time", Auto: true},
				},
				Hooks: map[string]schema.HookConfig{
					"before_create": {
						Type:   "js",
						Script: `if (!data.code) { result.proceed = false; result.error = "code required"; }`,
					},
				},
			},
			"clients":    {Fields: map[string]schema.FieldDef{"name": {Type: "string", Required: true}}},
			"users":      {Fields: map[string]schema.FieldDef{"email": {Type: "string", Required: true}}},
			"dispatches": {Fields: map[string]schema.FieldDef{"notes": {Type: "text"}}},
			"incidents":  {Fields: map[string]schema.FieldDef{"description": {Type: "text"}}},
		},
		RBAC: schema.RBACPolicy{
			Roles: map[string]schema.RolePolicy{
				"super_admin": {
					Resources: json.RawMessage(`"*"`),
					Actions:   []string{"*"},
				},
				"operario": {
					Resources: json.RawMessage(`["guides","dispatches","incidents"]`),
					Actions:   []string{"read", "create", "update"},
					Conditions: &schema.Condition{
						Field: "operator_id", Op: "eq", Val: "$user_id",
					},
				},
				"public": {
					Resources: json.RawMessage(`["guides"]`),
					Actions:   []string{"read"},
					Fields:    []string{"code", "status"},
				},
			},
		},
	}
}

// ── container helpers ─────────────────────────────────────────────────────────

func startPG(t *testing.T) (*pgxpool.Pool, func()) {
	t.Helper()
	if testing.Short() {
		t.Skip("integration test: needs Docker (testcontainers); skipped in -short")
	}
	ctx := context.Background()
	ctr, err := tcpostgres.Run(ctx, "postgres:16-alpine",
		tcpostgres.WithDatabase("testdb"),
		tcpostgres.WithUsername("test"),
		tcpostgres.WithPassword("test"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").WithOccurrence(2),
		),
	)
	if err != nil {
		t.Fatalf("start postgres: %v", err)
	}
	connStr, _ := ctr.ConnectionString(ctx, "sslmode=disable")
	pool, err := db.NewPool(ctx, connStr)
	if err != nil {
		ctr.Terminate(ctx)
		t.Fatalf("new pool: %v", err)
	}
	return pool, func() { pool.Close(); ctr.Terminate(ctx) }
}

func startRedisContainer(t *testing.T) (*redis.Client, func()) {
	t.Helper()
	ctx := context.Background()
	rc, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image:        "redis:7-alpine",
			ExposedPorts: []string{"6379/tcp"},
			WaitingFor:   wait.ForLog("Ready to accept connections"),
		},
		Started: true,
	})
	if err != nil {
		t.Fatalf("start redis: %v", err)
	}
	host, _ := rc.Host(ctx)
	port, _ := rc.MappedPort(ctx, "6379/tcp")
	rdb := redis.NewClient(&redis.Options{Addr: fmt.Sprintf("%s:%s", host, port.Port())})
	return rdb, func() { rdb.Close(); rc.Terminate(ctx) }
}

func applyControlPlane(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	sql, err := os.ReadFile("../../migrations/001_control_plane.sql")
	if err != nil {
		t.Fatalf("read migration: %v", err)
	}
	if _, err := pool.Exec(context.Background(), string(sql)); err != nil {
		t.Fatalf("apply control plane: %v", err)
	}
}

// ── server helpers ────────────────────────────────────────────────────────────

// buildDP builds the full data-plane router stack (same as cmd_serve).
func buildDP(s *schema.APISchema, pool *pgxpool.Pool, host string) http.Handler {
	policyJSON, _ := json.Marshal(s.RBAC)
	tdb := db.NewTenantDB(pool)
	hr := extensions.NewHookRunner(extensions.NewJSSandbox())

	r := chi.NewMux()
	r.Use(tenant.TenantMiddleware)
	r.Use(auth.JWTMiddleware(jwtSecret))
	r.Use(rbacpkg.RBACMiddleware(policyJSON))
	r.Mount("/", codegen.BuildRouter(s, tdb, hr, nil, nil))

	// Inject Host header so TenantMiddleware extracts the tenant.
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		req.Host = host
		r.ServeHTTP(w, req)
	})
}

func genToken(role, userID string) string {
	tok, _ := auth.GenerateToken(auth.Claims{
		UserID:   userID,
		Role:     role,
		TenantID: tenantID,
	}, jwtSecret)
	return tok
}

func genExpiredToken(role, userID string) string {
	c := auth.Claims{
		UserID:   userID,
		Role:     role,
		TenantID: tenantID,
		RegisteredClaims: jwtlib.RegisteredClaims{
			ExpiresAt: jwtlib.NewNumericDate(time.Now().Add(-time.Hour)),
			IssuedAt:  jwtlib.NewNumericDate(time.Now().Add(-2 * time.Hour)),
		},
	}
	tok, _ := jwtlib.NewWithClaims(jwtlib.SigningMethodHS256, c).SignedString([]byte(jwtSecret))
	return tok
}

// ── request helpers ───────────────────────────────────────────────────────────

func dpDo(t *testing.T, srv *httptest.Server, method, path, token string, body any, wantStatus int) map[string]any {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		json.NewEncoder(&buf).Encode(body)
	}
	req, _ := http.NewRequest(method, srv.URL+path, &buf)
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != wantStatus {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("%s %s: expected %d, got %d — body: %s", method, path, wantStatus, resp.StatusCode, b)
	}
	var out map[string]any
	json.NewDecoder(resp.Body).Decode(&out)
	return out
}

func dpList(t *testing.T, srv *httptest.Server, path, token string) []map[string]any {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, srv.URL+path, nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s: expected 200, got %d", path, resp.StatusCode)
	}
	var page struct {
		Data []map[string]any `json:"data"`
	}
	json.NewDecoder(resp.Body).Decode(&page)
	if page.Data == nil {
		return []map[string]any{}
	}
	return page.Data
}

func dpStatus(t *testing.T, srv *httptest.Server, method, path, token string, body any) int {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		json.NewEncoder(&buf).Encode(body)
	}
	req, _ := http.NewRequest(method, srv.URL+path, &buf)
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, _ := srv.Client().Do(req)
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	return resp.StatusCode
}

// ── main test ─────────────────────────────────────────────────────────────────

func TestE2E_FullLifecycle(t *testing.T) {
	if testing.Short() {
		t.Skip("e2e: skipping in -short mode")
	}

	ctx := context.Background()
	pool, cleanPG := startPG(t)
	defer cleanPG()
	rdb, cleanRedis := startRedisContainer(t)
	defer cleanRedis()

	applyControlPlane(t, pool)
	s := e2eSchema()

	// ── Step 1: Register tenant ───────────────────────────────────────────────
	t.Log("Step 1: register tenant")
	_, err := controlplane.RegisterTenant(ctx, pool, controlplane.RegisterRequest{
		TenantID:    tenantID,
		DisplayName: "Acme Test Co",
		Email:       "admin@acmetest.com",
		Plan:        "free",
		Schema:      s,
	})
	if err != nil {
		t.Fatalf("step 1 RegisterTenant: %v", err)
	}

	// ── Step 2: Verify 5 tables ───────────────────────────────────────────────
	t.Log("Step 2: verify tables")
	for _, tbl := range []string{"guides", "clients", "users", "dispatches", "incidents"} {
		var exists bool
		pool.QueryRow(ctx,
			`SELECT EXISTS(SELECT 1 FROM information_schema.tables
			 WHERE table_schema=$1 AND table_name=$2)`,
			"tenant_"+tenantID, tbl,
		).Scan(&exists)
		if !exists {
			t.Fatalf("step 2: table tenant_%s.%s not created", tenantID, tbl)
		}
	}

	// ── Step 3: Start data-plane server ──────────────────────────────────────
	t.Log("Step 3: start server")
	dpSrv := httptest.NewServer(buildDP(s, pool, tenantID+".localhost"))
	t.Cleanup(dpSrv.Close)

	// ── Step 4: Generate tokens ───────────────────────────────────────────────
	superTok := genToken("super_admin", superID)
	op1Tok := genToken("operario", operario1ID)
	op2Tok := genToken("operario", operario2ID)
	pubTok := genToken("public", "")
	expiredTok := genExpiredToken("super_admin", superID)

	// ── Step 5: Full CRUD on guides ───────────────────────────────────────────
	t.Log("Step 5: CRUD")

	guide := dpDo(t, dpSrv, "POST", "/api/guides", superTok, map[string]any{
		"code": "GU-001", "status": "pending", "operator_id": operario1ID,
	}, http.StatusCreated)
	guideID, _ := guide["id"].(string)
	if guideID == "" {
		t.Fatal("step 5 POST: no id in response")
	}

	list := dpList(t, dpSrv, "/api/guides", superTok)
	if len(list) != 1 {
		t.Fatalf("step 5 GET list: expected 1, got %d", len(list))
	}

	dpDo(t, dpSrv, "GET", "/api/guides/"+guideID, superTok, nil, http.StatusOK)
	dpStatus(t, dpSrv, "DELETE", "/api/guides/"+guideID, superTok, nil)
	if got := dpStatus(t, dpSrv, "GET", "/api/guides/"+guideID, superTok, nil); got != http.StatusNotFound {
		t.Fatalf("step 5 GET after delete: expected 404, got %d", got)
	}

	// ── Step 6: RBAC ─────────────────────────────────────────────────────────
	t.Log("Step 6: RBAC")

	// Create a guide owned by operario1.
	dpDo(t, dpSrv, "POST", "/api/guides", superTok, map[string]any{
		"code": "GU-RBAC", "operator_id": operario1ID,
	}, http.StatusCreated)

	rows1 := dpList(t, dpSrv, "/api/guides", op1Tok)
	if len(rows1) != 1 {
		t.Fatalf("step 6: operario1 expected 1 guide, got %d", len(rows1))
	}

	rows2 := dpList(t, dpSrv, "/api/guides", op2Tok)
	if len(rows2) != 0 {
		t.Fatalf("step 6: operario2 expected 0 guides, got %d", len(rows2))
	}

	// Public only sees allowed fields.
	pubRows := dpList(t, dpSrv, "/api/guides", pubTok)
	if len(pubRows) == 0 {
		t.Fatal("step 6: public should see guides")
	}
	for _, row := range pubRows {
		for k := range row {
			if k != "code" && k != "status" {
				t.Errorf("step 6: public must not see field %q", k)
			}
		}
	}

	// No token → 401.
	if got := dpStatus(t, dpSrv, "GET", "/api/guides", "", nil); got != http.StatusUnauthorized {
		t.Fatalf("step 6 no-token: expected 401, got %d", got)
	}

	// Expired token → 401.
	if got := dpStatus(t, dpSrv, "GET", "/api/guides", expiredTok, nil); got != http.StatusUnauthorized {
		t.Fatalf("step 6 expired-token: expected 401, got %d", got)
	}

	// ── Step 7: JS hook ───────────────────────────────────────────────────────
	t.Log("Step 7: hooks")

	if got := dpStatus(t, dpSrv, "POST", "/api/guides", superTok,
		map[string]any{"status": "pending"}); got != http.StatusUnprocessableEntity {
		t.Fatalf("step 7 no-code: expected 422, got %d", got)
	}
	dpDo(t, dpSrv, "POST", "/api/guides", superTok, map[string]any{"code": "GU-HOOK"}, http.StatusCreated)

	// ── Step 8: Schema change via control plane ───────────────────────────────
	t.Log("Step 8: schema change")

	// Start migration worker with fast timings.
	worker := migration.NewMigrationWorker(rdb, pool, tenant.NewSchemaCache())
	worker.BlockTime = 100 * time.Millisecond
	worker.BackoffBase = 10 * time.Millisecond
	workerCtx, workerCancel := context.WithCancel(ctx)
	t.Cleanup(workerCancel)
	go worker.Run(workerCtx)

	// Build updated schema with phone field.
	updated := e2eSchema()
	updated.Resources["guides"].Fields["phone"] = schema.FieldDef{Type: "string"}

	cpSvc := controlplane.NewService(pool, rdb)
	cpSrv := httptest.NewServer(controlplane.NewControlPlaneRouter(cpSvc, adminKey))
	t.Cleanup(cpSrv.Close)

	cpBody, _ := json.Marshal(map[string]any{"schema": updated})
	cpReq, _ := http.NewRequest(http.MethodPut, cpSrv.URL+"/tenants/"+tenantID+"/schema", bytes.NewReader(cpBody))
	cpReq.Header.Set("Content-Type", "application/json")
	cpReq.Header.Set("X-Admin-Key", adminKey)
	cpResp, err := cpSrv.Client().Do(cpReq)
	if err != nil {
		t.Fatalf("step 8 PUT schema: %v", err)
	}
	io.Copy(io.Discard, cpResp.Body)
	cpResp.Body.Close()
	if cpResp.StatusCode != http.StatusOK {
		t.Fatalf("step 8 PUT schema: expected 200, got %d", cpResp.StatusCode)
	}

	// ── Step 9: Wait for worker to apply migration (max 10s) ─────────────────
	t.Log("Step 9: waiting for migration worker")
	migStart := time.Now()
	deadline := time.Now().Add(10 * time.Second)
	migrated := false
	for time.Now().Before(deadline) {
		var count int
		pool.QueryRow(ctx,
			`SELECT COUNT(*) FROM public.migration_log
			 WHERE tenant_id=$1 AND status='ok' AND started_at > $2`,
			tenantID, migStart,
		).Scan(&count)
		if count > 0 {
			migrated = true
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if !migrated {
		t.Fatal("step 9: migration did not complete within 10s")
	}

	// Verify column was added.
	var colExists bool
	pool.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM information_schema.columns
		 WHERE table_schema='tenant_acmetest' AND table_name='guides' AND column_name='phone')`,
	).Scan(&colExists)
	if !colExists {
		t.Fatal("step 9: phone column not found in tenant_acmetest.guides")
	}

	// ── Step 10: New field in POST response ───────────────────────────────────
	t.Log("Step 10: new field in response")
	resp := dpDo(t, dpSrv, "POST", "/api/guides", superTok, map[string]any{
		"code": "GU-PHONE", "phone": "555-1234",
	}, http.StatusCreated)
	if resp["phone"] != "555-1234" {
		t.Errorf("step 10: expected phone=555-1234, got %v", resp["phone"])
	}

	t.Log("E2E: all 10 steps passed ✓")
}
