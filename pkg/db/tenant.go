package db

import (
	"context"
	"fmt"
	"regexp"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// TenantDB wraps a pgxpool and enforces per-tenant schema isolation via SET LOCAL search_path.
type TenantDB struct {
	pool *pgxpool.Pool
}

func NewTenantDB(pool *pgxpool.Pool) *TenantDB {
	return &TenantDB{pool: pool}
}

var schemaNameRe = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

func validateSchemaName(name string) error {
	if !schemaNameRe.MatchString(name) {
		return fmt.Errorf("invalid schema name %q: must match ^[a-z][a-z0-9_]*$", name)
	}
	return nil
}

// tenantRows wraps pgx.Rows and rolls back the enclosing transaction on Close,
// ensuring the connection is always returned to the pool when the caller is done.
type tenantRows struct {
	pgx.Rows
	tx pgx.Tx
}

func (r *tenantRows) Close() {
	r.Rows.Close()
	r.tx.Rollback(context.Background())
}

// QueryTenant executes a SELECT within a transaction scoped to schemaName.
// The transaction is rolled back automatically when the returned rows are closed.
func (tdb *TenantDB) QueryTenant(ctx context.Context, schemaName, query string, args ...any) (pgx.Rows, error) {
	if err := validateSchemaName(schemaName); err != nil {
		return nil, err
	}

	tx, err := tdb.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin tx: %w", err)
	}

	setPath := "SET LOCAL search_path TO " + pgx.Identifier{schemaName}.Sanitize() + ", public"
	if _, err := tx.Exec(ctx, setPath); err != nil {
		tx.Rollback(ctx)
		return nil, fmt.Errorf("set search_path: %w", err)
	}

	rows, err := tx.Query(ctx, query, args...)
	if err != nil {
		tx.Rollback(ctx)
		return nil, fmt.Errorf("query: %w", err)
	}

	return &tenantRows{Rows: rows, tx: tx}, nil
}

// ExecTenant executes an INSERT/UPDATE/DELETE within a transaction scoped to schemaName.
// The transaction is committed on success and rolled back on any error.
func (tdb *TenantDB) ExecTenant(ctx context.Context, schemaName, query string, args ...any) error {
	if err := validateSchemaName(schemaName); err != nil {
		return err
	}

	tx, err := tdb.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx)

	setPath := "SET LOCAL search_path TO " + pgx.Identifier{schemaName}.Sanitize() + ", public"
	if _, err := tx.Exec(ctx, setPath); err != nil {
		return fmt.Errorf("set search_path: %w", err)
	}

	if _, err := tx.Exec(ctx, query, args...); err != nil {
		return fmt.Errorf("exec: %w", err)
	}

	return tx.Commit(ctx)
}
