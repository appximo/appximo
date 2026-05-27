package handlers_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/miguelangel/appitools/internal/handlers"
	"github.com/miguelangel/appitools/pkg/db"
	"github.com/miguelangel/appitools/pkg/tenant"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

func startPostgres(t *testing.T) (string, func()) {
	t.Helper()
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
	router := handlers.NewRouter(tdb)
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
