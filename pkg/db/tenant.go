package db

import (
	"context"
	"fmt"
	"regexp"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// qualifyTableNames rewrites FROM/JOIN references to unqualified tableName
// into schema-qualified form using pgx.Identifier for safe quoting.
// Example: "SELECT * FROM guides WHERE x=$1" → "SELECT * FROM "tenant_10"."guides" WHERE x=$1"
func qualifyTableNames(query, schema, table string) string {
	qualified := pgx.Identifier{schema, table}.Sanitize()
	re := regexp.MustCompile(`(?i)\b(FROM|JOIN)\s+` + regexp.QuoteMeta(table) + `\b`)
	return re.ReplaceAllString(query, "${1} "+qualified)
}

// directRows wraps pgx.Rows and releases the acquired pool connection on Close.
type directRows struct {
	pgx.Rows
	conn *pgxpool.Conn
}

func (r *directRows) Close() {
	r.Rows.Close()
	r.conn.Release()
}

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
// Returns the number of rows affected. The transaction is committed on success.
func (tdb *TenantDB) ExecTenant(ctx context.Context, schemaName, query string, args ...any) (int64, error) {
	if err := validateSchemaName(schemaName); err != nil {
		return 0, err
	}

	tx, err := tdb.pool.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx)

	setPath := "SET LOCAL search_path TO " + pgx.Identifier{schemaName}.Sanitize() + ", public"
	if _, err := tx.Exec(ctx, setPath); err != nil {
		return 0, fmt.Errorf("set search_path: %w", err)
	}

	tag, err := tx.Exec(ctx, query, args...)
	if err != nil {
		return 0, fmt.Errorf("exec: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("commit: %w", err)
	}

	return tag.RowsAffected(), nil
}

// ExecRowsTenant runs a write query with RETURNING * within a tenant schema,
// reads all returned rows into memory, and commits the transaction.
// UUID columns ([16]byte) are converted to hyphenated UUID strings.
func (tdb *TenantDB) ExecRowsTenant(ctx context.Context, schemaName, query string, args ...any) ([]map[string]any, error) {
	if err := validateSchemaName(schemaName); err != nil {
		return nil, err
	}

	tx, err := tdb.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx)

	setPath := "SET LOCAL search_path TO " + pgx.Identifier{schemaName}.Sanitize() + ", public"
	if _, err := tx.Exec(ctx, setPath); err != nil {
		return nil, fmt.Errorf("set search_path: %w", err)
	}

	rows, err := tx.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query: %w", err)
	}

	descs := rows.FieldDescriptions()
	result := make([]map[string]any, 0)
	for rows.Next() {
		vals, scanErr := rows.Values()
		if scanErr != nil {
			rows.Close()
			return nil, fmt.Errorf("scan values: %w", scanErr)
		}
		row := make(map[string]any, len(descs))
		for i, desc := range descs {
			row[string(desc.Name)] = normalizeDBValue(vals[i])
		}
		result = append(result, row)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit: %w", err)
	}

	return result, nil
}

// QueryScalarTenant runs a query that returns a single int64 value (e.g. COUNT(*))
// within the tenant schema. Returns 0 if no rows are returned.
func (tdb *TenantDB) QueryScalarTenant(ctx context.Context, schemaName, query string, args ...any) (int64, error) {
	rows, err := tdb.QueryTenant(ctx, schemaName, query, args...)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	var n int64
	if rows.Next() {
		if err := rows.Scan(&n); err != nil {
			return 0, fmt.Errorf("scan scalar: %w", err)
		}
	}
	return n, rows.Err()
}

// QueryDirect runs a SELECT using a schema-qualified table name — no transaction,
// no SET LOCAL. One roundtrip instead of four. Use for read-only list/get handlers.
// tableName must be the unqualified resource name (e.g. "guides"); pgSchema the
// tenant schema (e.g. "tenant_10"). Both are validated before use.
func (tdb *TenantDB) QueryDirect(ctx context.Context, pgSchema, tableName, query string, args ...any) (pgx.Rows, error) {
	if err := validateSchemaName(pgSchema); err != nil {
		return nil, err
	}
	if err := validateSchemaName(tableName); err != nil {
		return nil, fmt.Errorf("invalid table name %q: %w", tableName, err)
	}

	qualified := qualifyTableNames(query, pgSchema, tableName)

	conn, err := tdb.pool.Acquire(ctx)
	if err != nil {
		return nil, fmt.Errorf("acquire conn: %w", err)
	}

	rows, err := conn.Query(ctx, qualified, args...)
	if err != nil {
		conn.Release()
		return nil, fmt.Errorf("query: %w", err)
	}

	return &directRows{Rows: rows, conn: conn}, nil
}

// normalizeDBValue converts pgx-specific types to JSON-friendly Go types.
func normalizeDBValue(v any) any {
	if uuid, ok := v.([16]byte); ok {
		return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
			uuid[0:4], uuid[4:6], uuid[6:8], uuid[8:10], uuid[10:16])
	}
	return v
}
