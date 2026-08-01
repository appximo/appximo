package schemadiff

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// This file renders an ordered Plan into PRODUCTION-SAFE SQL and applies it. It is
// where the diagnostic's worst hazards are cured and where the engine surpasses
// Prisma's defaults:
//
//   - lock_timeout + retry-with-backoff around every lock-taking statement, so a
//     DDL never queues behind (and blocks) live traffic indefinitely — a protection
//     Prisma/Doctrine leave to the user (docs/MIGRATION_DIAG.md §3).
//   - foreign keys added NOT VALID then VALIDATE'd (the validation scan runs under a
//     weaker lock than a plain ADD CONSTRAINT) (§D).
//   - SET NOT NULL via a CHECK (col IS NOT NULL) NOT VALID → VALIDATE → SET NOT NULL
//     → DROP CHECK, so the full-table scan avoids the long ACCESS EXCLUSIVE lock (§C).
//   - indexes created/dropped with CONCURRENTLY, which CANNOT run in a transaction,
//     so the executor PARTITIONS the plan: transactional statements run in batches
//     (atomic, with lock_timeout+retry); CONCURRENTLY statements run outside, each on
//     its own autocommit connection (§E, §G).
//   - renames are ALTER … RENAME (metadata-only, data-preserving) — NEVER drop+add,
//     the diff's worst bug (§F). (The diff already emits Rename ops; here they render
//     to the safe ALTER.)
//
// A NOT NULL column added WITHOUT a default is rendered FAITHFULLY (real NOT NULL) —
// never silently dropped (the converger's bug). On a populated table Postgres
// rejects it and the whole batch rolls back with a clear error (no partial state,
// no silent NULL-accepting column). Validate() surfaces this as a planning concern
// up front so a gate can refuse it before applying.

// safeLockTimeout / safeMaxRetries / safeBackoff are the executor defaults.
const (
	defaultLockTimeout = 5 * time.Second
	defaultMaxRetries  = 5
	defaultBackoffBase = 50 * time.Millisecond
)

// Statement is one rendered SQL statement plus how it must be executed.
type Statement struct {
	SQL string
	// Concurrent marks a statement that MUST run outside a transaction
	// (CREATE/DROP INDEX CONCURRENTLY) — the executor runs it on its own connection.
	Concurrent bool
	// CleanupSQL, if set, is run (best-effort) before a retry of a failed Concurrent
	// statement — e.g. dropping the invalid index a failed CONCURRENTLY build leaves.
	CleanupSQL string
	// Op is the source operation (for risk/diagnostics).
	Op Operation
}

// Render turns a Plan into safe SQL statements, in execution order. It does not
// touch the database; Executor.Apply runs them with the partitioning and
// lock-timeout/retry guarantees.
func Render(plan *Plan) ([]Statement, error) {
	var out []Statement
	for _, op := range plan.Ops {
		stmts, err := renderOne(op)
		if err != nil {
			return nil, err
		}
		out = append(out, stmts...)
	}
	return out, nil
}

func renderOne(op Operation) ([]Statement, error) {
	tx := func(sql string) Statement { return Statement{SQL: sql, Op: op} }
	switch o := op.(type) {
	case CreateTable:
		return []Statement{tx(renderCreateTable(o.Table))}, nil
	case DropTable:
		return []Statement{tx("DROP TABLE " + ident(o.Table.Name))}, nil
	case RenameTable:
		return []Statement{tx("ALTER TABLE " + ident(o.From) + " RENAME TO " + ident(o.To))}, nil
	case AddColumn:
		return []Statement{tx("ALTER TABLE " + ident(o.Table) + " ADD COLUMN " + renderColumnDef(o.Column))}, nil
	case DropColumn:
		return []Statement{tx("ALTER TABLE " + ident(o.Table) + " DROP COLUMN " + ident(o.Column.Name))}, nil
	case AlterColumn:
		return renderAlterColumn(o), nil
	case RenameColumn:
		return []Statement{tx("ALTER TABLE " + ident(o.Table) + " RENAME COLUMN " + ident(o.From) + " TO " + ident(o.To))}, nil
	case AddPrimaryKey:
		return []Statement{tx("ALTER TABLE " + ident(o.Table) + " ADD CONSTRAINT " + ident(o.PK.Name) + " PRIMARY KEY (" + identCols(o.PK.Columns) + ")")}, nil
	case DropPrimaryKey:
		return []Statement{tx("ALTER TABLE " + ident(o.Table) + " DROP CONSTRAINT " + ident(o.PK.Name))}, nil
	case AddForeignKey:
		// NOT VALID (brief lock, no scan) then VALIDATE (scan under a weaker lock).
		add := "ALTER TABLE " + ident(o.Table) + " ADD CONSTRAINT " + ident(o.FK.Symbol) +
			" FOREIGN KEY (" + identCols(o.FK.Columns) + ") REFERENCES " + ident(o.FK.RefTable) +
			" (" + identCols(o.FK.RefColumns) + ")"
		if o.FK.OnDelete != NoAction {
			add += " ON DELETE " + o.FK.OnDelete.String()
		}
		if o.FK.OnUpdate != NoAction {
			add += " ON UPDATE " + o.FK.OnUpdate.String()
		}
		add += " NOT VALID"
		return []Statement{
			tx(add),
			tx("ALTER TABLE " + ident(o.Table) + " VALIDATE CONSTRAINT " + ident(o.FK.Symbol)),
		}, nil
	case DropForeignKey:
		return []Statement{tx("ALTER TABLE " + ident(o.Table) + " DROP CONSTRAINT " + ident(o.FK.Symbol))}, nil
	case AddUnique:
		return []Statement{tx("ALTER TABLE " + ident(o.Table) + " ADD CONSTRAINT " + ident(o.Unique.Symbol) + " UNIQUE (" + identCols(o.Unique.Columns) + ")")}, nil
	case DropUnique:
		return []Statement{tx("ALTER TABLE " + ident(o.Table) + " DROP CONSTRAINT " + ident(o.Unique.Symbol))}, nil
	case AddCheck:
		// NOT VALID then VALIDATE — the check scan runs under a weaker lock.
		return []Statement{
			tx("ALTER TABLE " + ident(o.Table) + " ADD CONSTRAINT " + ident(o.Check.Symbol) + " CHECK " + o.Check.Expression + " NOT VALID"),
			tx("ALTER TABLE " + ident(o.Table) + " VALIDATE CONSTRAINT " + ident(o.Check.Symbol)),
		}, nil
	case DropCheck:
		return []Statement{tx("ALTER TABLE " + ident(o.Table) + " DROP CONSTRAINT " + ident(o.Check.Symbol))}, nil
	case AddIndex:
		u := ""
		if o.Index.Unique {
			u = "UNIQUE "
		}
		s := "CREATE " + u + "INDEX CONCURRENTLY " + ident(o.Index.Name) + " ON " + ident(o.Table)
		if o.Index.Method != "" && o.Index.Method != "btree" {
			s += " USING " + o.Index.Method
		}
		s += " (" + identColsWithOpclass(o.Index.Columns, o.Index.Opclass) + ")"
		if o.Index.Predicate != "" {
			s += " WHERE " + o.Index.Predicate
		}
		return []Statement{{
			SQL: s, Concurrent: true, Op: op,
			// a failed CONCURRENTLY build leaves an invalid index; drop it before retry
			CleanupSQL: "DROP INDEX CONCURRENTLY IF EXISTS " + ident(o.Index.Name),
		}}, nil
	case DropIndex:
		return []Statement{{SQL: "DROP INDEX CONCURRENTLY IF EXISTS " + ident(o.Index.Name), Concurrent: true, Op: op}}, nil
	default:
		return nil, fmt.Errorf("schemadiff: Render: unhandled operation %T", op)
	}
}

// renderAlterColumn renders the (possibly several) statements for an in-place
// column change, each guaranteeing safety: a SET NOT NULL goes through a validated
// CHECK so the table scan avoids a long ACCESS EXCLUSIVE lock.
func renderAlterColumn(o AlterColumn) []Statement {
	var out []Statement
	tbl, c := ident(o.Table), ident(o.To.Name)
	add := func(sql string) { out = append(out, Statement{SQL: sql, Op: o}) }

	if o.TypeChanged() {
		ty := o.To.Type.String()
		add("ALTER TABLE " + tbl + " ALTER COLUMN " + c + " TYPE " + ty + " USING " + c + "::" + ty)
	}
	if o.NullabilityAdded() {
		chk := ident(o.Table + "_" + o.To.Name + "_nn_chk")
		add("ALTER TABLE " + tbl + " ADD CONSTRAINT " + chk + " CHECK (" + c + " IS NOT NULL) NOT VALID")
		add("ALTER TABLE " + tbl + " VALIDATE CONSTRAINT " + chk)
		add("ALTER TABLE " + tbl + " ALTER COLUMN " + c + " SET NOT NULL")
		add("ALTER TABLE " + tbl + " DROP CONSTRAINT " + chk)
	}
	if o.NullabilityDropped() {
		add("ALTER TABLE " + tbl + " ALTER COLUMN " + c + " DROP NOT NULL")
	}
	if o.DefaultChanged() {
		if o.To.Default == nil {
			add("ALTER TABLE " + tbl + " ALTER COLUMN " + c + " DROP DEFAULT")
		} else {
			add("ALTER TABLE " + tbl + " ALTER COLUMN " + c + " SET DEFAULT " + o.To.Default.Raw)
		}
	}
	return out
}

// renderCreateTable renders a CREATE TABLE with columns (faithful NOT NULL/DEFAULT/
// identity) and an inline PRIMARY KEY. Constraints/indexes/FKs are separate ops.
func renderCreateTable(t *Table) string {
	cols := make([]string, 0, len(t.ColumnOrder)+1)
	for _, name := range t.ColumnOrder {
		cols = append(cols, renderColumnDef(t.Columns[name]))
	}
	if t.PK != nil {
		cols = append(cols, "PRIMARY KEY ("+identCols(t.PK.Columns)+")")
	}
	return "CREATE TABLE " + ident(t.Name) + " (\n  " + strings.Join(cols, ",\n  ") + "\n)"
}

// renderColumnDef renders one column definition, faithfully (a NOT NULL without a
// default is emitted as real NOT NULL — never silently dropped).
func renderColumnDef(c *Column) string {
	if c.Identity != nil && c.Identity.Generated == "serial" {
		return ident(c.Name) + " " + serialFor(c.Type.Base)
	}
	def := ident(c.Name) + " " + c.Type.String()
	if c.Identity != nil {
		switch c.Identity.Generated {
		case "always":
			return def + " GENERATED ALWAYS AS IDENTITY"
		case "by_default":
			return def + " GENERATED BY DEFAULT AS IDENTITY"
		}
	}
	if c.NotNull {
		def += " NOT NULL"
	}
	if c.Default != nil {
		def += " DEFAULT " + c.Default.Raw
	}
	return def
}

func serialFor(b BaseType) string {
	switch b {
	case BaseBigint:
		return "bigserial"
	case BaseSmallint:
		return "smallserial"
	default:
		return "serial"
	}
}

func ident(name string) string { return pgx.Identifier{name}.Sanitize() }

func identCols(cols []string) string {
	parts := make([]string, len(cols))
	for i, c := range cols {
		parts[i] = ident(c)
	}
	return strings.Join(parts, ", ")
}

// identColsWithOpclass renders an index's key list, appending the operator class
// to EVERY column when one is set (`"atributos" jsonb_path_ops`). The opclass is
// never user free-form text — the schema validator accepts it only from a closed
// per-method allowlist (schema.validIndexOpclasses) — so it is safe to emit
// verbatim; identifiers are still quoted.
func identColsWithOpclass(cols []string, opclass string) string {
	if opclass == "" {
		return identCols(cols)
	}
	parts := make([]string, len(cols))
	for i, c := range cols {
		parts[i] = ident(c) + " " + opclass
	}
	return strings.Join(parts, ", ")
}

// ── planning concerns (the safety gate's data) ─────────────────────────────────

// Concern flags an operation in a plan that a human/gate should review before
// applying: data loss, a backfill requirement, or a data rewrite.
type Concern struct {
	Op      Operation
	Risk    RiskClass
	Message string
}

// Validate returns the concerns in a plan (destructive, backfill-requiring, or
// transformational ops) with clear messages — the data a future approval gate uses
// to refuse or warn BEFORE applying. The executor itself renders faithfully and
// fails loudly on truly-inapplicable DDL; Validate is the up-front signal.
func Validate(plan *Plan) []Concern {
	var out []Concern
	for _, op := range plan.Ops {
		switch op.Risk() {
		case RiskDestructive:
			out = append(out, Concern{op, RiskDestructive, op.String() + " is destructive — it loses data"})
		case RiskBackfill:
			msg := op.String() + " requires existing rows to already satisfy it; on a populated table it will fail unless backfilled"
			if ac, ok := op.(AddColumn); ok && ac.RequiresBackfill() {
				msg = fmt.Sprintf("ADD COLUMN %s.%s is NOT NULL without a default — on a non-empty table this fails; provide a default or a backfill value", ac.Table, ac.Column.Name)
			}
			out = append(out, Concern{op, RiskBackfill, msg})
		case RiskTransformational:
			out = append(out, Concern{op, RiskTransformational, op.String() + " rewrites the column data (type change) and may not be losslessly reversible"})
		}
	}
	return out
}

// ── Executor ───────────────────────────────────────────────────────────────────

// Executor applies a Plan to one tenant schema with production-safety guarantees.
// The zero value is unusable; set Pool and Schema. The timing knobs default when
// zero.
type Executor struct {
	Pool   *pgxpool.Pool
	Schema string // tenant schema name (the migration's search_path)

	LockTimeout time.Duration // per-statement lock wait before failing fast (default 5s)
	MaxRetries  int           // retries after a lock-timeout failure (default 5)
	BackoffBase time.Duration // first retry backoff, doubling each attempt (default 50ms)

	// OnRetry, if set, is called before each retry with the attempt number (1-based)
	// and the lock-timeout error that triggered it (used by tests/observability).
	OnRetry func(attempt int, err error)
}

func (e *Executor) lockTimeout() time.Duration {
	if e.LockTimeout > 0 {
		return e.LockTimeout
	}
	return defaultLockTimeout
}

func (e *Executor) maxRetries() int {
	if e.MaxRetries > 0 {
		return e.MaxRetries
	}
	return defaultMaxRetries
}

func (e *Executor) backoffBase() time.Duration {
	if e.BackoffBase > 0 {
		return e.BackoffBase
	}
	return defaultBackoffBase
}

// Apply orders the plan (dependency-safe), renders it to safe SQL, and executes it:
// transactional statements run in atomic batches (with lock_timeout + retry), and
// CONCURRENTLY statements run between batches on their own autocommit connection.
func (e *Executor) Apply(ctx context.Context, plan *Plan) error {
	if e.Pool == nil || e.Schema == "" {
		return errors.New("schemadiff: Executor needs Pool and Schema")
	}
	ordered, err := OrderPlan(plan)
	if err != nil {
		return err
	}
	stmts, err := Render(ordered)
	if err != nil {
		return err
	}

	var batch []Statement
	flush := func() error {
		if len(batch) == 0 {
			return nil
		}
		b := batch
		batch = nil
		return e.runTxnBatch(ctx, b)
	}
	for _, st := range stmts {
		if st.Concurrent {
			if err := flush(); err != nil {
				return err
			}
			if err := e.runConcurrent(ctx, st); err != nil {
				return err
			}
			continue
		}
		batch = append(batch, st)
	}
	return flush()
}

// Exec runs ONE DDL statement in its own transaction with this executor's
// search_path, lock_timeout and retry-on-lock-timeout — the same safety wrapper
// Apply uses for a transactional batch. It is exposed so a caller can apply a
// single statement with those guarantees OUTSIDE a full Plan: e.g. the migration
// layer's transition-tolerant foreign-key policy, which commits an
// `ADD CONSTRAINT … NOT VALID` and then attempts `VALIDATE CONSTRAINT` as a
// separate, failure-tolerant step over pre-existing data.
func (e *Executor) Exec(ctx context.Context, sql string) error {
	if e.Pool == nil || e.Schema == "" {
		return errors.New("schemadiff: Executor needs Pool and Schema")
	}
	return e.runTxnBatch(ctx, []Statement{{SQL: sql}})
}

// ExecBatch runs several DDL statements in ONE transaction, atomically: either all
// of them land or none does. Same safety wrapper as Exec (search_path, lock_timeout,
// retry-on-lock-timeout).
//
// It exists for operations that are only correct as a PAIR — above all replacing a
// foreign key whose definition changed (ENG-13): dropping the old constraint and
// adding the new one must not be separable, or a failure of the second half would
// leave the table with no foreign key at all.
func (e *Executor) ExecBatch(ctx context.Context, sqls ...string) error {
	if e.Pool == nil || e.Schema == "" {
		return errors.New("schemadiff: Executor needs Pool and Schema")
	}
	if len(sqls) == 0 {
		return nil
	}
	batch := make([]Statement, 0, len(sqls))
	for _, s := range sqls {
		batch = append(batch, Statement{SQL: s})
	}
	return e.runTxnBatch(ctx, batch)
}

// runTxnBatch runs a contiguous group of transactional statements in ONE
// transaction (atomic: any failure rolls back the whole batch), under a short
// lock_timeout, retrying the whole batch with backoff on a lock-timeout.
func (e *Executor) runTxnBatch(ctx context.Context, batch []Statement) error {
	return e.withRetry(ctx, func() error {
		tx, err := e.Pool.Begin(ctx)
		if err != nil {
			return err
		}
		defer tx.Rollback(ctx) //nolint:errcheck // no-op after a successful Commit
		if _, err := tx.Exec(ctx, "SET LOCAL search_path TO "+ident(e.Schema)); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, fmt.Sprintf("SET LOCAL lock_timeout = '%dms'", e.lockTimeout().Milliseconds())); err != nil {
			return err
		}
		for _, st := range batch {
			if _, err := tx.Exec(ctx, st.SQL); err != nil {
				return fmt.Errorf("apply %q: %w", st.SQL, err)
			}
		}
		return tx.Commit(ctx)
	})
}

// runConcurrent runs one CONCURRENTLY statement on a dedicated autocommit
// connection (CONCURRENTLY cannot run in a transaction), with lock_timeout + retry.
// Before each retry it runs CleanupSQL (best-effort) to drop an invalid index a
// failed build may have left. RESET ALL clears the session settings before the
// connection returns to the pool.
func (e *Executor) runConcurrent(ctx context.Context, st Statement) error {
	conn, err := e.Pool.Acquire(ctx)
	if err != nil {
		return err
	}
	defer func() {
		_, _ = conn.Exec(ctx, "RESET ALL")
		conn.Release()
	}()
	if _, err := conn.Exec(ctx, "SET search_path TO "+ident(e.Schema)); err != nil {
		return err
	}
	if _, err := conn.Exec(ctx, fmt.Sprintf("SET lock_timeout = '%dms'", e.lockTimeout().Milliseconds())); err != nil {
		return err
	}

	maxR, base := e.maxRetries(), e.backoffBase()
	var lastErr error
	for attempt := 0; attempt <= maxR; attempt++ {
		if attempt > 0 && st.CleanupSQL != "" {
			_, _ = conn.Exec(ctx, st.CleanupSQL) // best-effort: clear an invalid leftover
		}
		if _, err := conn.Exec(ctx, st.SQL); err == nil {
			return nil
		} else {
			lastErr = err
		}
		if !isLockTimeout(lastErr) {
			return fmt.Errorf("apply %q: %w", st.SQL, lastErr)
		}
		if attempt == maxR {
			break
		}
		if e.OnRetry != nil {
			e.OnRetry(attempt+1, lastErr)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(backoffDelay(base, attempt)):
		}
	}
	return fmt.Errorf("apply %q: lock not acquired after %d attempts: %w", st.SQL, maxR+1, lastErr)
}

// withRetry runs fn, retrying with exponential backoff only on a lock-timeout
// (SQLSTATE 55P03). Any other error returns immediately (a constraint violation is
// not retryable).
func (e *Executor) withRetry(ctx context.Context, fn func() error) error {
	maxR, base := e.maxRetries(), e.backoffBase()
	var err error
	for attempt := 0; attempt <= maxR; attempt++ {
		if err = fn(); err == nil {
			return nil
		}
		if !isLockTimeout(err) {
			return err
		}
		if attempt == maxR {
			break
		}
		if e.OnRetry != nil {
			e.OnRetry(attempt+1, err)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(backoffDelay(base, attempt)):
		}
	}
	return fmt.Errorf("lock not acquired after %d attempts: %w", maxR+1, err)
}

// backoffDelay is base · 2^attempt, capped at 2s.
func backoffDelay(base time.Duration, attempt int) time.Duration {
	d := base << attempt
	const cap = 2 * time.Second
	if d <= 0 || d > cap {
		return cap
	}
	return d
}

// isLockTimeout reports whether err is a Postgres lock_timeout (SQLSTATE 55P03).
func isLockTimeout(err error) bool {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code == "55P03"
	}
	return false
}
