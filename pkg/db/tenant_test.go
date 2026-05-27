package db_test

import (
	"context"
	"strings"
	"testing"

	"github.com/miguelangel/appitools/pkg/db"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

func startPostgres(t *testing.T) (string, func()) {
	t.Helper()
	ctx := context.Background()

	ctr, err := postgres.Run(ctx,
		"postgres:16-alpine",
		postgres.WithDatabase("testdb"),
		postgres.WithUsername("test"),
		postgres.WithPassword("test"),
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

// Test 1: QueryTenant devuelve filas del schema correcto.
func TestQueryTenant_ReturnsCorrectRows(t *testing.T) {
	ctx := context.Background()
	connStr, cleanup := startPostgres(t)
	defer cleanup()

	pool, err := db.NewPool(ctx, connStr)
	if err != nil {
		t.Fatalf("NewPool: %v", err)
	}
	defer pool.Close()

	tdb := db.NewTenantDB(pool)

	pool.Exec(ctx, `CREATE SCHEMA tenant_a`)
	pool.Exec(ctx, `CREATE TABLE tenant_a.items (name text)`)
	pool.Exec(ctx, `INSERT INTO tenant_a.items VALUES ('hello from tenant_a')`)

	rows, err := tdb.QueryTenant(ctx, "tenant_a", "SELECT name FROM items")
	if err != nil {
		t.Fatalf("QueryTenant: %v", err)
	}
	defer rows.Close()

	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		names = append(names, name)
	}

	if len(names) != 1 || names[0] != "hello from tenant_a" {
		t.Errorf("unexpected rows: %v", names)
	}
}

// Test 2: SET LOCAL no persiste después de la transacción.
// Verifica usando una conexión explícita: después de que la tx termina,
// SHOW search_path en esa conexión vuelve al valor por defecto del servidor.
func TestQueryTenant_SetLocalDoesNotPersist(t *testing.T) {
	ctx := context.Background()
	connStr, cleanup := startPostgres(t)
	defer cleanup()

	pool, err := db.NewPool(ctx, connStr)
	if err != nil {
		t.Fatalf("NewPool: %v", err)
	}
	defer pool.Close()

	tdb := db.NewTenantDB(pool)

	pool.Exec(ctx, `CREATE SCHEMA tenant_b`)
	pool.Exec(ctx, `CREATE TABLE tenant_b.items (name text)`)
	pool.Exec(ctx, `INSERT INTO tenant_b.items VALUES ('tenant_b row')`)

	// ExecTenant realiza SET LOCAL search_path, ejecuta la query, y hace COMMIT.
	// El COMMIT libera la conexión al pool con search_path de vuelta al default.
	_, err = tdb.ExecTenant(ctx, "tenant_b", `INSERT INTO items(name) VALUES('inserted via exec')`)
	if err != nil {
		t.Fatalf("ExecTenant: %v", err)
	}

	// Adquirir una conexión explícita y verificar que search_path es el default.
	conn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	defer conn.Release()

	var searchPath string
	conn.QueryRow(ctx, "SHOW search_path").Scan(&searchPath)

	if strings.Contains(searchPath, "tenant_b") {
		t.Errorf("search_path leaked tenant_b scope: %q", searchPath)
	}
}

// Test 3: schema con caracteres especiales es rechazado antes de tocar la DB.
func TestQueryTenant_InvalidSchemaNameRejected(t *testing.T) {
	ctx := context.Background()

	// pool nil: la validación debe ocurrir antes de cualquier acceso al pool.
	tdb := db.NewTenantDB(nil)

	badNames := []string{
		"'; DROP TABLE items; --",
		"Uppercase",
		"has space",
		"has-hyphen",
		"123startsWithDigit",
		"",
	}

	for _, name := range badNames {
		_, err := tdb.QueryTenant(ctx, name, "SELECT 1")
		if err == nil {
			t.Errorf("expected error for schema name %q, got nil", name)
		}
	}
}
