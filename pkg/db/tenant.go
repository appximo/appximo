package db

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/puddle/v2"
	"github.com/sony/gobreaker"

	"github.com/appximo/appximo/pkg/resilience"
)

// queryTimeout bounds every database operation. A paused/unreachable Postgres
// would otherwise block the request (and eventually the whole connection pool)
// indefinitely; with the deadline the query is cancelled and the request fails
// fast instead of hanging.
const queryTimeout = 5 * time.Second

// ErrUnavailable is returned when the database cannot SERVE the query (as opposed
// to rejecting it): the queryTimeout elapsed, the circuit breaker is open, the
// connection failed, or PostgreSQL reported that it cannot take the work
// (connection_exception, insufficient_resources, shutting down). Handlers map it
// to 503 + Retry-After — "retry later", not "your request is broken". See
// isUnavailableCause for the exact classification and what is deliberately
// excluded.
var ErrUnavailable = errors.New("database unavailable")

// IsUnavailable reports whether err signals an unreachable database. It matches
// both the classified sentinel (errors wrapped by this package's classify) AND a
// RAW unclassified cause (a bare pgconn/net error a CUSTOM handler got from its
// own SQL on the tenant tx) — CONSUMER-PATH-S1 (ENG-10): custom handlers never
// pass through classify, so with the sentinel-only check a stopped PostgreSQL
// surfaced as 500 "internal error" on every custom route while the generated
// routes answered the honest 503. Error-path only — never on the hot path.
func IsUnavailable(err error) bool {
	return errors.Is(err, ErrUnavailable) || isUnavailableCause(err)
}

// IsMissingTenant reports whether err is a "schema/relation does not exist"
// Postgres error (SQLSTATE 42P01 undefined_table or 3F000 invalid_schema_name).
// In this schema-per-tenant design that means the tenant is not provisioned, so
// handlers answer 400 "invalid tenant" rather than leaking a 500 SQL error.
func IsMissingTenant(err error) bool {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code == "42P01" || pgErr.Code == "3F000"
	}
	return false
}

// badInputCodes are the Postgres "data exception" (class 22) SQLSTATEs that a
// CLIENT can trigger with a badly-typed value — a filter, a cursor, or a write
// body. They are caller errors, so handlers answer 400 rather than a 500.
//
// 22P02 alone was not enough (ADR-024). Measured on the live engine: only a bad
// INTEGER took the 400 path, because integers fail with 22P02; every other type
// raises a different code in the same class and fell through to "internal
// error" — a 500 for a typo:
//
//	?filter[due][gt]=notadate      → 22007  invalid_datetime_format
//	?filter[due][gt]=2026-13-45    → 22008  datetime_field_overflow
//	?filter[amount][gt]=<24 nines> → 22003  numeric_value_out_of_range
//	POST {"due":"not-a-date"}      → 22007  (the same gap on the write path)
//
// A 500 here is worse than a bad message: it is logged as an engine fault, it
// burns the SLO error budget, and it pages someone — all of which any
// authenticated caller could trigger at will with a single typo'd parameter.
// The class is bounded and every member means the same thing ("your value does
// not fit this column"), so it is matched by class rather than enumerating
// codes one incident at a time.
func badInputCode(code string) bool {
	switch code {
	case "22P02", // invalid_text_representation  (int, uuid, bool, json)
		"22007", // invalid_datetime_format       (time)
		"22008", // datetime_field_overflow       (time, e.g. month 13)
		"22003": // numeric_value_out_of_range    (int64/float64 overflow)
		return true
	}
	return false
}

// Deliberately NOT in the set: 22001 string_data_right_truncation. It was in the
// first draft of this fix and removed before commit — every one of the four
// above was OBSERVED being returned to a client as a 500 during this session,
// and 22001 was not. The engine declares string/text columns as unbounded TEXT
// and enforces length with the declarative `maxLength` rule (a 422 long before
// any SQL), so there is no measured path that reaches it from client input.
// Adding a code on the theory that it might fire would make a genuine engine
// fault answer 400 — which is the same "guessing instead of measuring" this ADR
// exists to stop. If it is ever observed, add it WITH the reproduction.

// IsBadInput reports whether err is a client-supplied value that Postgres could
// not parse or store for a column type. These are caller errors, so handlers
// answer 400 instead of leaking a 500. See badInputCodes for the exact set and
// why it is wider than the 22P02 it started as.
func IsBadInput(err error) bool {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return badInputCode(pgErr.Code)
	}
	return false
}

// uniqueViolationDetailRe extracts the offending column from a Postgres
// unique_violation Detail line: "Key (email)=(x@y.z) already exists." → "email".
var uniqueViolationDetailRe = regexp.MustCompile(`Key \(([^)]+)\)=`)

// UniqueViolationField reports whether err is a Postgres unique_violation
// (SQLSTATE 23505) and, if so, returns the offending column name parsed from the
// error Detail (falling back to the constraint name). Handlers map this to 409
// without leaking raw SQL. ok is false for any other error.
func UniqueViolationField(err error) (field string, ok bool) {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "23505" {
		return "", false
	}
	if m := uniqueViolationDetailRe.FindStringSubmatch(pgErr.Detail); len(m) == 2 {
		return m[1], true
	}
	return pgErr.ConstraintName, true
}

// fkStillReferencedRe / fkNotPresentRe parse the two Postgres foreign_key_violation
// Detail shapes: a RESTRICT/NO-ACTION delete of a still-referenced parent
// ("…is still referenced from table \"empleados\".") and an insert/update of a child
// whose parent is absent ("…is not present in table \"departamentos\".").
// fkKeyColumnsRe extracts the violating column list from the same Detail
// ("Key (formula)=(…) is not present…" → "formula").
var (
	fkStillReferencedRe = regexp.MustCompile(`is still referenced from table "([^"]+)"`)
	fkNotPresentRe      = regexp.MustCompile(`is not present in table "([^"]+)"`)
	fkKeyColumnsRe      = regexp.MustCompile(`^Key \(([^)]+)\)=`)
)

// FileReferenceViolation reports whether err is a foreign_key_violation on a
// `file` field (FILES-LINK-S1): a write whose file_id references no row of the
// tenant's files table. It returns the violating column (the file field's name,
// parsed from the error Detail) so handlers can answer the write with the SAME
// field-addressed 422 shape the declarative validator uses — a bad file
// reference is field-level input validation to the client, even though the
// guard is the real FK. A delete blocked by a file FK (the other direction) is
// NOT this case — that stays the generic 409 (ForeignKeyViolation).
func FileReferenceViolation(err error) (column string, ok bool) {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "23503" {
		return "", false
	}
	m := fkNotPresentRe.FindStringSubmatch(pgErr.Detail)
	if len(m) != 2 || m[1] != "files" {
		return "", false
	}
	if cm := fkKeyColumnsRe.FindStringSubmatch(pgErr.Detail); len(cm) == 2 {
		return cm[1], true
	}
	return "", true // file FK violation with an unparseable column — still classifiable
}

// ForeignKeyViolation reports whether err is a Postgres foreign_key_violation
// (SQLSTATE 23503) and, if so, returns a safe, human-readable message naming the
// related resource (parsed from the error Detail, never raw SQL). Handlers map this
// to 409 Conflict — a delete blocked by a RESTRICT FK, or a write referencing a row
// that does not exist — instead of leaking a masked 500. ok is false for any other
// error. The resource names exposed are the tenant's OWN schema resources, not
// engine internals.
func ForeignKeyViolation(err error) (msg string, ok bool) {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "23503" {
		return "", false
	}
	if m := fkStillReferencedRe.FindStringSubmatch(pgErr.Detail); len(m) == 2 {
		return fmt.Sprintf("cannot delete: still referenced by %q record(s)", m[1]), true
	}
	if m := fkNotPresentRe.FindStringSubmatch(pgErr.Detail); len(m) == 2 {
		return fmt.Sprintf("invalid reference: no matching %q record", m[1]), true
	}
	return "referential integrity violation", true
}

// undefinedColumnMsgRe extracts the column from a Postgres undefined_column
// message: `column "priority" of relation "tasks" does not exist` → "priority".
var undefinedColumnMsgRe = regexp.MustCompile(`column "([^"]+)"`)

// UndefinedColumnField reports whether err is a Postgres undefined_column
// (SQLSTATE 42703) and, if so, returns the column name parsed from the message.
// On the write path this means the client sent a field that neither the schema
// nor the live table knows: handlers map it to 422 unknown_field instead of a
// masked 500. The DB stays the source of truth for the writable column set
// (the schema can grow columns at runtime — see the no-whitelist NOTE in
// codegen.BuildRouter), so this classification happens on the error, never as
// a pre-write whitelist.
func UndefinedColumnField(err error) (field string, ok bool) {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "42703" {
		return "", false
	}
	if m := undefinedColumnMsgRe.FindStringSubmatch(pgErr.Message); len(m) == 2 {
		return m[1], true
	}
	return "", true
}

// qualifyReCache memoizes the per-table FROM/JOIN regexp. The table name is a
// validated resource name (a small, fixed set per schema — never client-supplied),
// so this cache is bounded. It removes a regexp.MustCompile from every QueryDirect
// call, which is the list-API hot path (compilation, not matching, dominated it).
var qualifyReCache sync.Map // table string → *regexp.Regexp

// qualifyTableNames rewrites FROM/JOIN references to the unqualified tableName
// into schema-qualified form using pgx.Identifier for safe quoting. It accepts
// the table name either bare (FROM tasks) or already double-quoted (FROM "tasks")
// — the query builder now emits the quoted form for consistent identifier quoting
// (BUG1), while older callers still pass bare names; both qualify identically.
// Example: `SELECT * FROM "tasks" WHERE x=$1` → `SELECT * FROM "tenant_acme"."tasks" WHERE x=$1`
func qualifyTableNames(query, schema, table string) string {
	qualified := pgx.Identifier{schema, table}.Sanitize()
	re, ok := qualifyReCache.Load(table)
	if !ok {
		qt := regexp.QuoteMeta(table)
		// Match `FROM "tasks"` (quoted) OR `FROM tasks` (bare, word-bounded).
		compiled := regexp.MustCompile(`(?i)(FROM|JOIN)\s+(?:"` + qt + `"|` + qt + `\b)`)
		re, _ = qualifyReCache.LoadOrStore(table, compiled)
	}
	return re.(*regexp.Regexp).ReplaceAllString(query, "${1} "+qualified)
}

// directRows wraps pgx.Rows and releases the acquired pool connection on Close.
// cancel ends the per-query timeout context, which must outlive the caller's row
// iteration, so it is called here rather than when the query method returns.
type directRows struct {
	pgx.Rows
	conn   *pgxpool.Conn
	cancel context.CancelFunc
}

func (r *directRows) Close() {
	r.Rows.Close()
	r.conn.Release()
	if r.cancel != nil {
		r.cancel()
	}
}

// TenantDB wraps a pgxpool and enforces per-tenant schema isolation via SET LOCAL search_path.
type TenantDB struct {
	pool    *pgxpool.Pool
	breaker *gobreaker.CircuitBreaker
}

func NewTenantDB(pool *pgxpool.Pool) *TenantDB {
	// The breaker counts ONLY unavailability (ENG-49): the same predicate that
	// turns an error into a 503 decides whether it counts against the database.
	return &TenantDB{pool: pool, breaker: resilience.NewQueryBreakerWith("tenant-db", isUnavailableCause)}
}

// exec runs fn through the circuit breaker (if configured). When the breaker is
// open it returns immediately without touching the pool.
func (tdb *TenantDB) exec(fn func() (any, error)) (any, error) {
	if tdb.breaker == nil {
		return fn()
	}
	return tdb.breaker.Execute(fn)
}

// classify maps "the database cannot serve this right now" errors to
// ErrUnavailable so handlers answer 503; everything else passes through
// unchanged (and is masked as 500).
//
// The distinction is not cosmetic. A 500 tells a client "this request is broken,
// do not retry"; a 503 tells it "I am saturated or down, retry later" — which is
// what a load balancer, a retrying SDK and an on-call human all need to react
// correctly. Only the timeout and breaker cases were classified before, so the
// production-stack benchmark recorded PostgreSQL being stopped for 15 s as a run
// of clean 500s ("one honest wart", docs/BENCHMARKS.md §6) when the truthful
// answer was 503.
func classify(err error) error {
	if err == nil {
		return nil
	}
	if isUnavailableCause(err) {
		// BOTH wrapped (%w twice): the sentinel drives the 503, and the original
		// cause stays unwrappable so the operator log keeps the SQLSTATE and the
		// other classifiers still see what actually happened.
		return fmt.Errorf("%w: %w", ErrUnavailable, err)
	}
	return err
}

// isUnavailableCause reports whether err means the database could not serve the
// request, as opposed to rejecting it. Three families:
//
//   - The engine's own backpressure: the query deadline elapsed, or the circuit
//     breaker is open / probing.
//   - The connection failed: nothing to talk to (refused, reset, EOF mid-query,
//     DNS/dial failure), or PostgreSQL reported a class-08 connection_exception.
//   - PostgreSQL is up but cannot take the work: class 53 insufficient_resources
//     (too_many_connections, out_of_memory, disk_full) or 57P01/02/03
//     (admin shutdown, crash shutdown, cannot_connect_now — e.g. a standby still
//     recovering).
//
// Deliberately NOT here: 40001 serialization_failure and 40P01 deadlock_detected.
// Those mean "your transaction lost a race" — a retry of THAT statement is the
// caller's decision, and a 503 would tell a load balancer to shed traffic that is
// perfectly serveable.
func isUnavailableCause(err error) bool {
	if errors.Is(err, context.DeadlineExceeded) ||
		errors.Is(err, gobreaker.ErrOpenState) ||
		errors.Is(err, gobreaker.ErrTooManyRequests) {
		return true
	}
	// A PostgreSQL-reported condition (the server answered, then said it cannot).
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch {
		case strings.HasPrefix(pgErr.Code, "08"): // connection_exception
			return true
		case strings.HasPrefix(pgErr.Code, "53"): // insufficient_resources
			return true
		case pgErr.Code == "57P01", pgErr.Code == "57P02", pgErr.Code == "57P03":
			return true
		}
		// Any other SQLSTATE is a real error about the statement — never a 503.
		return false
	}
	// The transport failed before/while talking to PostgreSQL.
	var connErr *pgconn.ConnectError
	if errors.As(err, &connErr) {
		return true
	}
	var netErr *net.OpError
	if errors.As(err, &netErr) {
		return true
	}
	if errors.Is(err, syscall.ECONNREFUSED) || errors.Is(err, syscall.ECONNRESET) ||
		errors.Is(err, syscall.EHOSTUNREACH) || errors.Is(err, syscall.ENETUNREACH) ||
		errors.Is(err, syscall.EPIPE) {
		return true
	}
	// pgx surfaces a connection that died mid-query as io.ErrUnexpectedEOF (or a
	// bare EOF from the wire), and a pool whose every connection is broken as
	// ErrClosedPool.
	if errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, io.EOF) ||
		errors.Is(err, puddle.ErrClosedPool) {
		return true
	}
	return false
}

var schemaNameRe = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

func validateSchemaName(name string) error {
	if !schemaNameRe.MatchString(name) {
		return fmt.Errorf("invalid schema name %q: must match ^[a-z][a-z0-9_]*$", name)
	}
	return nil
}

// tableNameRe matches a resource/table identifier. A declared resource name is
// ^[a-z][a-z0-9_]*$ (no hyphen since G1), but a many_to_many JUNCTION table that
// is not a declared resource may still carry a hyphen (throughNameRe at schema
// load allows it), so a separate, hyphen-tolerant validator is used for the table
// argument of QueryDirect — otherwise a valid hyphenated junction (e.g.
// "order-products") is wrongly rejected. The name is still safely quoted via
// pgx.Identifier downstream; this is defence-in-depth.
var tableNameRe = regexp.MustCompile(`^[a-z][a-z0-9_-]*$`)

func validateTableName(name string) error {
	if !tableNameRe.MatchString(name) {
		return fmt.Errorf("invalid table name %q: must match ^[a-z][a-z0-9_-]*$", name)
	}
	return nil
}

// tenantRows wraps pgx.Rows and rolls back the enclosing transaction on Close,
// ensuring the connection is always returned to the pool when the caller is done.
type tenantRows struct {
	pgx.Rows
	tx     pgx.Tx
	cancel context.CancelFunc
}

func (r *tenantRows) Close() {
	r.Rows.Close()
	r.tx.Rollback(context.Background())
	if r.cancel != nil {
		r.cancel()
	}
}

// QueryTenant executes a SELECT within a transaction scoped to schemaName.
// The transaction is rolled back automatically when the returned rows are closed.
func (tdb *TenantDB) QueryTenant(ctx context.Context, schemaName, query string, args ...any) (pgx.Rows, error) {
	if err := validateSchemaName(schemaName); err != nil {
		return nil, err
	}

	cctx, cancel := context.WithTimeout(ctx, queryTimeout)
	res, err := tdb.exec(func() (any, error) {
		tx, err := tdb.pool.Begin(cctx)
		if err != nil {
			return nil, fmt.Errorf("begin tx: %w", err)
		}
		setPath := "SET LOCAL search_path TO " + pgx.Identifier{schemaName}.Sanitize() + ", public"
		if _, err := tx.Exec(cctx, setPath); err != nil {
			tx.Rollback(cctx)
			return nil, fmt.Errorf("set search_path: %w", err)
		}
		rows, err := tx.Query(cctx, query, args...)
		if err != nil {
			tx.Rollback(cctx)
			return nil, fmt.Errorf("query: %w", err)
		}
		return &tenantRows{Rows: rows, tx: tx, cancel: cancel}, nil
	})
	if err != nil {
		cancel()
		return nil, classify(err)
	}
	return res.(*tenantRows), nil
}

// ExecTenant executes an INSERT/UPDATE/DELETE within a transaction scoped to schemaName.
// Returns the number of rows affected. The transaction is committed on success.
func (tdb *TenantDB) ExecTenant(ctx context.Context, schemaName, query string, args ...any) (int64, error) {
	if err := validateSchemaName(schemaName); err != nil {
		return 0, err
	}

	cctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()
	res, err := tdb.exec(func() (any, error) {
		tx, err := tdb.pool.Begin(cctx)
		if err != nil {
			return int64(0), fmt.Errorf("begin tx: %w", err)
		}
		defer tx.Rollback(cctx)

		setPath := "SET LOCAL search_path TO " + pgx.Identifier{schemaName}.Sanitize() + ", public"
		if _, err := tx.Exec(cctx, setPath); err != nil {
			return int64(0), fmt.Errorf("set search_path: %w", err)
		}
		tag, err := tx.Exec(cctx, query, args...)
		if err != nil {
			return int64(0), fmt.Errorf("exec: %w", err)
		}
		if err := tx.Commit(cctx); err != nil {
			return int64(0), fmt.Errorf("commit: %w", err)
		}
		return tag.RowsAffected(), nil
	})
	if err != nil {
		return 0, classify(err)
	}
	return res.(int64), nil
}

// ExecRowsTenant runs a write query with RETURNING * within a tenant schema,
// reads all returned rows into memory, and commits the transaction.
// UUID columns ([16]byte) are converted to hyphenated UUID strings.
func (tdb *TenantDB) ExecRowsTenant(ctx context.Context, schemaName, query string, args ...any) ([]map[string]any, error) {
	if err := validateSchemaName(schemaName); err != nil {
		return nil, err
	}

	cctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()
	res, err := tdb.exec(func() (any, error) {
		tx, err := tdb.pool.Begin(cctx)
		if err != nil {
			return nil, fmt.Errorf("begin tx: %w", err)
		}
		defer tx.Rollback(cctx)

		setPath := "SET LOCAL search_path TO " + pgx.Identifier{schemaName}.Sanitize() + ", public"
		if _, err := tx.Exec(cctx, setPath); err != nil {
			return nil, fmt.Errorf("set search_path: %w", err)
		}

		rows, err := tx.Query(cctx, query, args...)
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
		if err := tx.Commit(cctx); err != nil {
			return nil, fmt.Errorf("commit: %w", err)
		}
		return result, nil
	})
	if err != nil {
		return nil, classify(err)
	}
	return res.([]map[string]any), nil
}

// ExecRowsTenantEmit is ExecRowsTenant plus a same-transaction emit step
// (CRUD-EMIT-V1): after the write's RETURNING rows are read and BEFORE commit,
// it calls emit(ctx, tx, rows) on the SAME pgx.Tx. If emit returns an error the
// transaction rolls back, so the business write and its emitted event are
// atomic — either both commit or neither does. emit may be nil (then this is
// exactly ExecRowsTenant). db stays decoupled from pkg/outbox: the caller
// supplies a closure that enqueues using the handed-in tx.
func (tdb *TenantDB) ExecRowsTenantEmit(ctx context.Context, schemaName, query string, emit func(ctx context.Context, tx pgx.Tx, rows []map[string]any) error, args ...any) ([]map[string]any, error) {
	if err := validateSchemaName(schemaName); err != nil {
		return nil, err
	}

	cctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()
	res, err := tdb.exec(func() (any, error) {
		tx, err := tdb.pool.Begin(cctx)
		if err != nil {
			return nil, fmt.Errorf("begin tx: %w", err)
		}
		defer tx.Rollback(cctx)

		setPath := "SET LOCAL search_path TO " + pgx.Identifier{schemaName}.Sanitize() + ", public"
		if _, err := tx.Exec(cctx, setPath); err != nil {
			return nil, fmt.Errorf("set search_path: %w", err)
		}

		rows, err := tx.Query(cctx, query, args...)
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

		// Same-tx emission: a failure here rolls the whole write back.
		if emit != nil {
			if err := emit(cctx, tx, result); err != nil {
				return nil, fmt.Errorf("emit: %w", err)
			}
		}

		if err := tx.Commit(cctx); err != nil {
			return nil, fmt.Errorf("commit: %w", err)
		}
		return result, nil
	})
	if err != nil {
		return nil, classify(err)
	}
	return res.([]map[string]any), nil
}

// ExecTenantEmit is ExecTenant plus a same-transaction emit step (CRUD-EMIT-V1).
// After the write executes and BEFORE commit it calls emit(ctx, tx, affected) on
// the SAME tx — the affected-row count lets the caller skip emission when nothing
// changed (e.g. a DELETE that matched no row). An emit error rolls the write back.
// emit may be nil (then this is exactly ExecTenant).
func (tdb *TenantDB) ExecTenantEmit(ctx context.Context, schemaName, query string, emit func(ctx context.Context, tx pgx.Tx, affected int64) error, args ...any) (int64, error) {
	if err := validateSchemaName(schemaName); err != nil {
		return 0, err
	}

	cctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()
	res, err := tdb.exec(func() (any, error) {
		tx, err := tdb.pool.Begin(cctx)
		if err != nil {
			return int64(0), fmt.Errorf("begin tx: %w", err)
		}
		defer tx.Rollback(cctx)

		setPath := "SET LOCAL search_path TO " + pgx.Identifier{schemaName}.Sanitize() + ", public"
		if _, err := tx.Exec(cctx, setPath); err != nil {
			return int64(0), fmt.Errorf("set search_path: %w", err)
		}
		tag, err := tx.Exec(cctx, query, args...)
		if err != nil {
			return int64(0), fmt.Errorf("exec: %w", err)
		}
		affected := tag.RowsAffected()

		if emit != nil {
			if err := emit(cctx, tx, affected); err != nil {
				return int64(0), fmt.Errorf("emit: %w", err)
			}
		}

		if err := tx.Commit(cctx); err != nil {
			return int64(0), fmt.Errorf("commit: %w", err)
		}
		return affected, nil
	})
	if err != nil {
		return 0, classify(err)
	}
	return res.(int64), nil
}

// txTimeout bounds a whole multi-operation transaction (WithTenantTx). It is more
// generous than queryTimeout (which bounds ONE statement) because an atomic batch
// runs N statements on the same connection; it still fail-fast caps a transaction
// that hangs (e.g. an unreachable Postgres) instead of pinning a pool connection.
const txTimeout = 15 * time.Second

// WithTenantTx opens ONE transaction scoped to schemaName (search_path set once via
// SET LOCAL, exactly like the single-statement helpers), runs fn against that tx,
// and COMMITS iff fn returns nil — otherwise it ROLLS BACK. It is the atomic
// multi-resource primitive (G4): a caller runs several writes through the one tx and
// they are all-or-nothing. fn must use ONLY the handed-in tx (never the pool) so
// every statement shares the tenant search_path and the atomic boundary; a panic or
// error in fn leaves nothing partially committed (the deferred Rollback runs). The
// tenant cannot be crossed — the search_path is fixed for the transaction's life.
func (tdb *TenantDB) WithTenantTx(ctx context.Context, schemaName string, fn func(ctx context.Context, tx pgx.Tx) error) error {
	if err := validateSchemaName(schemaName); err != nil {
		return err
	}
	cctx, cancel := context.WithTimeout(ctx, txTimeout)
	defer cancel()
	_, err := tdb.exec(func() (any, error) {
		tx, err := tdb.pool.Begin(cctx)
		if err != nil {
			return nil, fmt.Errorf("begin tx: %w", err)
		}
		defer tx.Rollback(cctx) //nolint:errcheck // no-op after a successful Commit
		setPath := "SET LOCAL search_path TO " + pgx.Identifier{schemaName}.Sanitize() + ", public"
		if _, err := tx.Exec(cctx, setPath); err != nil {
			return nil, fmt.Errorf("set search_path: %w", err)
		}
		if err := fn(cctx, tx); err != nil {
			return nil, err // deferred Rollback discards every prior write
		}
		if err := tx.Commit(cctx); err != nil {
			return nil, fmt.Errorf("commit: %w", err)
		}
		return nil, nil
	})
	return classify(err)
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

// QueryScalarDirect runs a single-int64 query (e.g. COUNT(*)) against the
// schema-qualified table, mirroring QueryDirect (no transaction, no SET LOCAL) —
// used by the REST list's opt-in ?count=true total, which shares the list's
// QueryDirect path.
func (tdb *TenantDB) QueryScalarDirect(ctx context.Context, pgSchema, tableName, query string, args ...any) (int64, error) {
	rows, err := tdb.QueryDirect(ctx, pgSchema, tableName, query, args...)
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

// IncludeListJSON runs a RELATIONS-V1 list-with-embeds query (built by
// query.BuildListInclude) inside the tenant search_path and scans its single row
// of (data text, n bigint): data is the JSON array of nested rows (the engine
// writes it straight to the client, no Go round-trip), n the row count for
// has_next. The query references multiple tables unqualified — the tenant
// search_path (set by QueryTenant) resolves them, so this path supports embeds
// across the tenant's tables without per-table qualification.
func (tdb *TenantDB) IncludeListJSON(ctx context.Context, pgSchema, query string, args ...any) (data []byte, n int64, err error) {
	rows, err := tdb.QueryTenant(ctx, pgSchema, query, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	if rows.Next() {
		if err := rows.Scan(&data, &n); err != nil {
			return nil, 0, fmt.Errorf("scan include list: %w", err)
		}
	}
	return data, n, rows.Err()
}

// IncludeOneJSON runs a RELATIONS-V1 get-by-id-with-embeds query (built by
// query.BuildGetInclude) and scans its single JSON object column. found is false
// when no row matched (→ 404), so an embed never resurrects a row the base WHERE
// (including the row-level RBAC condition) excluded.
func (tdb *TenantDB) IncludeOneJSON(ctx context.Context, pgSchema, query string, args ...any) (data []byte, found bool, err error) {
	rows, err := tdb.QueryTenant(ctx, pgSchema, query, args...)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	if rows.Next() {
		if err := rows.Scan(&data); err != nil {
			return nil, false, fmt.Errorf("scan include row: %w", err)
		}
		found = true
	}
	return data, found, rows.Err()
}

// QueryDirect runs a SELECT using a schema-qualified table name — no transaction,
// no SET LOCAL. One roundtrip instead of four. Use for read-only list/get handlers.
// tableName must be the unqualified resource name (e.g. "tasks"); pgSchema the
// tenant schema (e.g. "tenant_acme"). Both are validated before use.
func (tdb *TenantDB) QueryDirect(ctx context.Context, pgSchema, tableName, query string, args ...any) (pgx.Rows, error) {
	if err := validateSchemaName(pgSchema); err != nil {
		return nil, err
	}
	if err := validateTableName(tableName); err != nil {
		return nil, err
	}

	qualified := qualifyTableNames(query, pgSchema, tableName)

	cctx, cancel := context.WithTimeout(ctx, queryTimeout)
	res, err := tdb.exec(func() (any, error) {
		conn, err := tdb.pool.Acquire(cctx)
		if err != nil {
			return nil, fmt.Errorf("acquire conn: %w", err)
		}
		rows, err := conn.Query(cctx, qualified, args...)
		if err != nil {
			conn.Release()
			return nil, fmt.Errorf("query: %w", err)
		}
		return &directRows{Rows: rows, conn: conn, cancel: cancel}, nil
	})
	if err != nil {
		cancel()
		return nil, classify(err)
	}
	return res.(*directRows), nil
}

// normalizeDBValue converts pgx-specific types to JSON-friendly Go types.
func normalizeDBValue(v any) any {
	if uuid, ok := v.([16]byte); ok {
		return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
			uuid[0:4], uuid[4:6], uuid[6:8], uuid[8:10], uuid[10:16])
	}
	return v
}
