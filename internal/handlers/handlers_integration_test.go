package handlers_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/appximo/appximo/internal/handlers"
	"github.com/appximo/appximo/pkg/db"
	"github.com/appximo/appximo/pkg/extensions"
	"github.com/appximo/appximo/pkg/rbac"
	"github.com/appximo/appximo/pkg/schema"
	"github.com/appximo/appximo/pkg/tenant"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

func startPostgres(t *testing.T) (string, func()) {
	t.Helper()
	if testing.Short() {
		t.Skip("integration test: needs Docker (testcontainers); skipped in -short")
	}
	ctx := context.Background()

	ctr, err := tcpostgres.Run(ctx,
		"postgres:16-alpine",
		tcpostgres.WithDatabase("testdb"),
		tcpostgres.WithUsername("test"),
		tcpostgres.WithPassword("test"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").WithOccurrence(2),
		),
	)
	if err != nil {
		t.Fatalf("start postgres container: %v", err)
	}

	connStr, err := ctr.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		ctr.Terminate(ctx)
		t.Fatalf("get connection string: %v", err)
	}

	return connStr, func() { ctr.Terminate(ctx) }
}

// tenantHost wraps a chi router with TenantMiddleware and sets Host header on every request.
func tenantHost(router http.Handler, host string) http.Handler {
	wrapped := tenant.TenantMiddleware(router)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Host = host
		wrapped.ServeHTTP(w, r)
	})
}

func TestHandlers_GuideCRUD(t *testing.T) {
	ctx := context.Background()
	connStr, cleanup := startPostgres(t)
	defer cleanup()

	pool, err := db.NewPool(ctx, connStr)
	if err != nil {
		t.Fatalf("NewPool: %v", err)
	}
	defer pool.Close()

	// Create the tenant schema and guides table.
	_, err = pool.Exec(ctx, `CREATE SCHEMA tenant_test`)
	if err != nil {
		t.Fatalf("create schema: %v", err)
	}
	_, err = pool.Exec(ctx, `
		CREATE TABLE tenant_test.guides (
			id         UUID DEFAULT gen_random_uuid() PRIMARY KEY,
			code       TEXT NOT NULL,
			status     TEXT NOT NULL DEFAULT 'pending',
			created_at TIMESTAMPTZ DEFAULT now()
		)
	`)
	if err != nil {
		t.Fatalf("create table: %v", err)
	}

	tdb := db.NewTenantDB(pool)
	hr := extensions.NewHookRunner(extensions.NewJSSandbox())
	testSchema := &schema.APISchema{
		Resources: map[string]schema.ResourceSchema{
			"guides": {Fields: map[string]schema.FieldDef{}},
		},
	}
	router := handlers.NewRouter(tdb, hr, testSchema)
	srv := httptest.NewServer(tenantHost(router, "test.localhost"))
	defer srv.Close()

	client := srv.Client()
	base := srv.URL

	// --- Step 1: GET /api/guides → empty list ---
	resp, err := client.Get(base + "/api/guides")
	if err != nil {
		t.Fatalf("GET /api/guides: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	// --- Step 2: POST /api/guides → create ---
	body := map[string]any{"code": "GU-001", "status": "pending"}
	bodyJSON, _ := json.Marshal(body)
	resp, err = client.Post(base+"/api/guides", "application/json", bytes.NewReader(bodyJSON))
	if err != nil {
		t.Fatalf("POST /api/guides: %v", err)
	}
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201, got %d", resp.StatusCode)
	}
	var created map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&created); err != nil {
		t.Fatalf("decode create response: %v", err)
	}
	resp.Body.Close()

	id, ok := created["id"].(string)
	if !ok || id == "" {
		t.Fatalf("expected string id in response, got %v", created["id"])
	}
	if created["code"] != "GU-001" {
		t.Errorf("expected code GU-001, got %v", created["code"])
	}

	// --- Step 3: GET /api/guides/{id} → find by id ---
	resp, err = client.Get(fmt.Sprintf("%s/api/guides/%s", base, id))
	if err != nil {
		t.Fatalf("GET /api/guides/{id}: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	var fetched map[string]any
	json.NewDecoder(resp.Body).Decode(&fetched)
	resp.Body.Close()
	if fetched["id"] != id {
		t.Errorf("fetched id %v != created id %v", fetched["id"], id)
	}

	// --- Step 4: DELETE /api/guides/{id} → 204 ---
	req, _ := http.NewRequest(http.MethodDelete, fmt.Sprintf("%s/api/guides/%s", base, id), nil)
	resp, err = client.Do(req)
	if err != nil {
		t.Fatalf("DELETE /api/guides/{id}: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", resp.StatusCode)
	}

	// --- Step 5: GET /api/guides/{id} after delete → 404 ---
	resp, err = client.Get(fmt.Sprintf("%s/api/guides/%s", base, id))
	if err != nil {
		t.Fatalf("GET after delete: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404 after delete, got %d", resp.StatusCode)
	}
}

// withRBAC wraps a router with TenantMiddleware + RBACMiddleware and injects the Host header.
func withRBAC(router http.Handler, host string, policyJSON []byte) http.Handler {
	withPolicy := rbac.RBACMiddleware(policyJSON)(router)
	withTenant := tenant.TenantMiddleware(withPolicy)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Host = host
		withTenant.ServeHTTP(w, r)
	})
}

func TestHandlers_WhereCondition(t *testing.T) {
	ctx := context.Background()
	connStr, cleanup := startPostgres(t)
	defer cleanup()

	pool, err := db.NewPool(ctx, connStr)
	if err != nil {
		t.Fatalf("NewPool: %v", err)
	}
	defer pool.Close()

	_, err = pool.Exec(ctx, `CREATE SCHEMA tenant_wc`)
	if err != nil {
		t.Fatalf("create schema: %v", err)
	}
	_, err = pool.Exec(ctx, `
		CREATE TABLE tenant_wc.guides (
			id          UUID DEFAULT gen_random_uuid() PRIMARY KEY,
			code        TEXT NOT NULL,
			operator_id UUID,
			created_at  TIMESTAMPTZ DEFAULT now()
		)
	`)
	if err != nil {
		t.Fatalf("create table: %v", err)
	}

	// Deterministic UUIDs for two operators.
	const user1 = "aaaaaaaa-0000-0000-0000-000000000001"
	const user2 = "aaaaaaaa-0000-0000-0000-000000000002"

	_, err = pool.Exec(ctx, `INSERT INTO tenant_wc.guides (code, operator_id) VALUES ('GU-U1', $1), ('GU-U2', $2)`, user1, user2)
	if err != nil {
		t.Fatalf("seed guides: %v", err)
	}

	policyJSON := []byte(`{
		"roles": {
			"operario": {
				"resources": "*",
				"actions": ["read","create"],
				"conditions": {"field": "operator_id", "op": "eq", "val": "$user_id"}
			}
		}
	}`)

	tdb := db.NewTenantDB(pool)
	hr := extensions.NewHookRunner(extensions.NewJSSandbox())
	testSchema := &schema.APISchema{
		Resources: map[string]schema.ResourceSchema{
			"guides": {Fields: map[string]schema.FieldDef{}},
		},
	}
	router := handlers.NewRouter(tdb, hr, testSchema)
	srv := httptest.NewServer(withRBAC(router, "wc.localhost", policyJSON))
	defer srv.Close()

	get := func(userID string) []map[string]any {
		t.Helper()
		req, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/guides", nil)
		req.Header.Set("X-User-Role", "operario")
		req.Header.Set("X-User-ID", userID)
		resp, err := srv.Client().Do(req)
		if err != nil {
			t.Fatalf("GET /api/guides: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("expected 200, got %d", resp.StatusCode)
		}
		var page struct {
			Data []map[string]any `json:"data"`
		}
		json.NewDecoder(resp.Body).Decode(&page)
		return page.Data
	}

	// user1 should see only GU-U1.
	rows := get(user1)
	if len(rows) != 1 {
		t.Fatalf("user1: expected 1 row, got %d", len(rows))
	}
	if rows[0]["code"] != "GU-U1" {
		t.Errorf("user1: expected code GU-U1, got %v", rows[0]["code"])
	}

	// user2 should see only GU-U2.
	rows = get(user2)
	if len(rows) != 1 {
		t.Fatalf("user2: expected 1 row, got %d", len(rows))
	}
	if rows[0]["code"] != "GU-U2" {
		t.Errorf("user2: expected code GU-U2, got %v", rows[0]["code"])
	}
}

func TestHandlers_FieldStripping(t *testing.T) {
	ctx := context.Background()
	connStr, cleanup := startPostgres(t)
	defer cleanup()

	pool, err := db.NewPool(ctx, connStr)
	if err != nil {
		t.Fatalf("NewPool: %v", err)
	}
	defer pool.Close()

	_, err = pool.Exec(ctx, `CREATE SCHEMA tenant_fs`)
	if err != nil {
		t.Fatalf("create schema: %v", err)
	}
	_, err = pool.Exec(ctx, `
		CREATE TABLE tenant_fs.guides (
			id         UUID DEFAULT gen_random_uuid() PRIMARY KEY,
			code       TEXT NOT NULL,
			status     TEXT NOT NULL DEFAULT 'pending',
			created_at TIMESTAMPTZ DEFAULT now()
		)
	`)
	if err != nil {
		t.Fatalf("create table: %v", err)
	}

	_, err = pool.Exec(ctx, `INSERT INTO tenant_fs.guides (code, status) VALUES ('GU-PUB', 'pending')`)
	if err != nil {
		t.Fatalf("seed guides: %v", err)
	}

	// public role can only see code and status — not id or created_at.
	policyJSON := []byte(`{
		"roles": {
			"public": {
				"resources": "*",
				"actions": ["read"],
				"fields": ["code", "status"]
			}
		}
	}`)

	tdb := db.NewTenantDB(pool)
	hr := extensions.NewHookRunner(extensions.NewJSSandbox())
	testSchema := &schema.APISchema{
		Resources: map[string]schema.ResourceSchema{
			"guides": {Fields: map[string]schema.FieldDef{}},
		},
	}
	router := handlers.NewRouter(tdb, hr, testSchema)
	srv := httptest.NewServer(withRBAC(router, "fs.localhost", policyJSON))
	defer srv.Close()

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/guides", nil)
	req.Header.Set("X-User-Role", "public")
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("GET /api/guides: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var page struct {
		Data []map[string]any `json:"data"`
	}
	json.NewDecoder(resp.Body).Decode(&page)

	if len(page.Data) != 1 {
		t.Fatalf("expected 1 row, got %d", len(page.Data))
	}
	row := page.Data[0]

	// Allowed fields must be present.
	if _, ok := row["code"]; !ok {
		t.Error("expected field 'code' in response")
	}
	if _, ok := row["status"]; !ok {
		t.Error("expected field 'status' in response")
	}
	// Stripped fields must be absent.
	if _, ok := row["id"]; ok {
		t.Error("field 'id' must be stripped for public role")
	}
	if _, ok := row["created_at"]; ok {
		t.Error("field 'created_at' must be stripped for public role")
	}
	if len(row) != 2 {
		t.Errorf("expected exactly 2 fields in response, got %d: %v", len(row), row)
	}
}

// ── TAREA 2: pagination meta ────────────────────────────────────────────────

func TestHandlers_Pagination(t *testing.T) {
	ctx := context.Background()
	connStr, cleanup := startPostgres(t)
	defer cleanup()

	pool, err := db.NewPool(ctx, connStr)
	if err != nil {
		t.Fatalf("NewPool: %v", err)
	}
	defer pool.Close()

	_, err = pool.Exec(ctx, `CREATE SCHEMA tenant_pg`)
	if err != nil {
		t.Fatalf("create schema: %v", err)
	}
	_, err = pool.Exec(ctx, `
		CREATE TABLE tenant_pg.guides (
			id   UUID DEFAULT gen_random_uuid() PRIMARY KEY,
			code TEXT NOT NULL
		)
	`)
	if err != nil {
		t.Fatalf("create table: %v", err)
	}

	// Insert 25 records.
	for i := 0; i < 25; i++ {
		_, err = pool.Exec(ctx, `INSERT INTO tenant_pg.guides (code) VALUES ($1)`, fmt.Sprintf("GU-%03d", i))
		if err != nil {
			t.Fatalf("seed guide %d: %v", i, err)
		}
	}

	tdb := db.NewTenantDB(pool)
	hr := extensions.NewHookRunner(extensions.NewJSSandbox())
	testSchema := &schema.APISchema{
		Resources: map[string]schema.ResourceSchema{
			"guides": {Fields: map[string]schema.FieldDef{"code": {Type: "string"}}},
		},
	}
	router := handlers.NewRouter(tdb, hr, testSchema)
	srv := httptest.NewServer(tenantHost(router, "pg.localhost"))
	defer srv.Close()

	type paginatedResp struct {
		Data []map[string]any `json:"data"`
		Meta struct {
			Page       int  `json:"page"`
			PerPage    int  `json:"per_page"`
			Total      int  `json:"total"`
			TotalPages int  `json:"total_pages"`
			HasNext    bool `json:"has_next"`
			HasPrev    bool `json:"has_prev"`
		} `json:"meta"`
	}

	getPage := func(page, perPage int) paginatedResp {
		t.Helper()
		resp, err := srv.Client().Get(fmt.Sprintf("%s/api/guides?page=%d&per_page=%d", srv.URL, page, perPage))
		if err != nil {
			t.Fatalf("GET guides: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("expected 200, got %d", resp.StatusCode)
		}
		var r paginatedResp
		json.NewDecoder(resp.Body).Decode(&r)
		return r
	}

	// Page 1: 25 rows, per_page=10 → 10 items, total=25, total_pages=3, has_next=true, has_prev=false
	p1 := getPage(1, 10)
	if len(p1.Data) != 10 {
		t.Errorf("page1: expected 10 items, got %d", len(p1.Data))
	}
	if p1.Meta.Total != 25 {
		t.Errorf("page1: total=%d, want 25", p1.Meta.Total)
	}
	if p1.Meta.TotalPages != 3 {
		t.Errorf("page1: total_pages=%d, want 3", p1.Meta.TotalPages)
	}
	if !p1.Meta.HasNext {
		t.Error("page1: has_next should be true")
	}
	if p1.Meta.HasPrev {
		t.Error("page1: has_prev should be false")
	}

	// Page 4: beyond last page → empty data, has_next=false
	p4 := getPage(4, 10)
	if len(p4.Data) != 0 {
		t.Errorf("page4: expected 0 items, got %d", len(p4.Data))
	}
	if p4.Meta.HasNext {
		t.Error("page4: has_next should be false")
	}

	// per_page=200 → silently capped to 100
	pBig := getPage(1, 200)
	if pBig.Meta.PerPage != 100 {
		t.Errorf("per_page=200 should be capped to 100, got %d", pBig.Meta.PerPage)
	}

	// ?page=abc → 400
	resp, err := srv.Client().Get(srv.URL + "/api/guides?page=abc")
	if err != nil {
		t.Fatalf("GET /api/guides?page=abc: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("page=abc: expected 400, got %d", resp.StatusCode)
	}
}

// ── TAREA 5: ETag / cache headers ──────────────────────────────────────────

func TestHandlers_ETag(t *testing.T) {
	ctx := context.Background()
	connStr, cleanup := startPostgres(t)
	defer cleanup()

	pool, err := db.NewPool(ctx, connStr)
	if err != nil {
		t.Fatalf("NewPool: %v", err)
	}
	defer pool.Close()

	_, err = pool.Exec(ctx, `CREATE SCHEMA tenant_etag`)
	if err != nil {
		t.Fatalf("create schema: %v", err)
	}
	_, err = pool.Exec(ctx, `
		CREATE TABLE tenant_etag.guides (
			id   UUID DEFAULT gen_random_uuid() PRIMARY KEY,
			code TEXT NOT NULL
		)
	`)
	if err != nil {
		t.Fatalf("create table: %v", err)
	}
	_, err = pool.Exec(ctx, `INSERT INTO tenant_etag.guides (code) VALUES ('GU-001')`)
	if err != nil {
		t.Fatalf("seed: %v", err)
	}

	tdb := db.NewTenantDB(pool)
	hr := extensions.NewHookRunner(extensions.NewJSSandbox())
	testSchema := &schema.APISchema{
		Resources: map[string]schema.ResourceSchema{
			"guides": {Fields: map[string]schema.FieldDef{"code": {Type: "string"}}},
		},
	}
	router := handlers.NewRouter(tdb, hr, testSchema)
	srv := httptest.NewServer(tenantHost(router, "etag.localhost"))
	defer srv.Close()

	// First request — must return ETag and Cache-Control.
	resp, err := srv.Client().Get(srv.URL + "/api/guides")
	if err != nil {
		t.Fatalf("GET /api/guides: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	etag := resp.Header.Get("ETag")
	if etag == "" {
		t.Fatal("expected ETag header in response")
	}
	if resp.Header.Get("Cache-Control") == "" {
		t.Error("expected Cache-Control header in response")
	}

	// Second request with matching If-None-Match → 304, no body.
	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/guides", nil)
	req.Header.Set("If-None-Match", etag)
	resp2, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("conditional GET: %v", err)
	}
	resp2.Body.Close()
	if resp2.StatusCode != http.StatusNotModified {
		t.Errorf("expected 304 with matching ETag, got %d", resp2.StatusCode)
	}

	// Insert another record — ETag must change.
	_, err = pool.Exec(ctx, `INSERT INTO tenant_etag.guides (code) VALUES ('GU-002')`)
	if err != nil {
		t.Fatalf("insert: %v", err)
	}
	resp3, err := srv.Client().Get(srv.URL + "/api/guides")
	if err != nil {
		t.Fatalf("GET after insert: %v", err)
	}
	resp3.Body.Close()
	if resp3.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 after insert, got %d", resp3.StatusCode)
	}
	etag2 := resp3.Header.Get("ETag")
	if etag2 == etag {
		t.Error("ETag should change after data changes")
	}
}
