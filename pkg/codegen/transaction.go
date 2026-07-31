package codegen

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/miguelangel/appitools/pkg/auth"
	"github.com/miguelangel/appitools/pkg/db"
	pkghandlers "github.com/miguelangel/appitools/pkg/handlers"
	"github.com/miguelangel/appitools/pkg/observability"
	"github.com/miguelangel/appitools/pkg/query"
	"github.com/miguelangel/appitools/pkg/rbac"
	"github.com/miguelangel/appitools/pkg/schema"
	"github.com/miguelangel/appitools/pkg/tenant"
)

// DefaultMaxTxOps bounds the number of operations in one POST /api/transaction
// request — a DoS guard (an unbounded batch is an attack vector). Override with
// APPITOOLS_MAX_TX_OPS.
const DefaultMaxTxOps = 100

// txGuardOps is the closed set of comparison operators a guard may use. The value
// is always a bound parameter; the operator is mapped from this allowlist (never
// interpolated from client input), so a guard can never inject SQL.
var txGuardOps = map[string]string{"eq": "=", "ne": "<>", "gt": ">", "gte": ">=", "lt": "<", "lte": "<="}

// txFieldRe validates a guard column as a bare identifier (defence-in-depth; the
// field must ALSO be a declared field of the resource).
var txFieldRe = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

// txGuard is one extra predicate on an update/delete: the row must satisfy it or
// the operation fails (→ the whole transaction rolls back). It is the optimistic-
// locking / conditional-write tool (compare-and-set): guard a value or version
// column and a concurrent change makes the op match zero rows → 409, client retries.
type txGuard struct {
	Field string `json:"field"`
	Op    string `json:"op"`
	Value any    `json:"value"`
}

// txOp is one operation in an atomic transaction request.
type txOp struct {
	Op       string         `json:"op"`              // "create" | "update" | "delete"
	Resource string         `json:"resource"`        // a schema resource (NOT "transaction")
	ID       string         `json:"id,omitempty"`    // update/delete: the row id (uuid)
	Data     map[string]any `json:"data,omitempty"`  // create/update: the field values
	Guard    []txGuard      `json:"guard,omitempty"` // update/delete: optional conditional predicates
}

// txRequest is the POST /api/transaction body.
type txRequest struct {
	Operations []txOp `json:"operations"`
}

// txError carries which operation failed, the HTTP status the whole batch returns,
// and a client-safe message (plus the failing op's resource/op for context, and the
// per-field validation failures when the cause was validation).
type txError struct {
	index    int
	status   int
	msg      string
	resource string
	op       string
	fields   []schema.FieldRuleError
}

func (e *txError) Error() string { return e.msg }

// txResource is the per-resource state the batch handler compiles ONCE at boot: the
// schema, the quoted table identifier, the validator, and the resource's outbox
// opt-in flags — so the request path never recompiles anything (same discipline as
// the per-resource generated handlers).
type txResource struct {
	res        schema.ResourceSchema
	tbl        string
	validator  *schema.ResourceValidator
	emitCreate bool
	emitUpdate bool
	emitDelete bool
}

// preparedOp is an operation that has already passed RBAC + validation + the before
// hook OUTSIDE the transaction (so a client error never opens a tx nor counts against
// the DB circuit breaker, matching the single-op path). Phase 2 only executes its SQL.
type preparedOp struct {
	kind     string // create | update | delete
	sql      string
	args     []any
	resource string
	emit     bool
	allowed  []string // field allowlist applied to the returned row
	id       string   // delete: the id (for the result + the emitted event)
}

// registerTransactionRoute mounts POST /api/transaction (G4): an atomic
// multi-resource batch. Every operation is authorized (per-resource RBAC, G2),
// validated, and emitted EXACTLY as its single-op counterpart, but all run in ONE
// Postgres transaction — all-or-nothing. It is a NEW path: the per-resource
// generated handlers are untouched (the single-op create/update/delete gate is
// preserved byte-for-byte).
func registerTransactionRoute(r chi.Router, s *schema.APISchema, tdb *db.TenantDB, policy *rbac.Policy, inv CacheInvalidator, hookEval func(ctx context.Context, hook *schema.HookConfig, body map[string]any) (map[string]any, int, string)) {
	refs := make(map[string]*txResource, len(s.Resources))
	for name, res := range s.Resources {
		rc := res
		refs[name] = &txResource{
			res:        rc,
			tbl:        pgx.Identifier{name}.Sanitize(),
			validator:  schema.CompileRules(&rc),
			emitCreate: rc.EmitsOn("create"),
			emitUpdate: rc.EmitsOn("update"),
			emitDelete: rc.EmitsOn("delete"),
		}
	}

	maxOps := DefaultMaxTxOps
	if v := os.Getenv("APPITOOLS_MAX_TX_OPS"); v != "" {
		if n, perr := strconv.Atoi(v); perr == nil && n > 0 {
			maxOps = n
		}
	}

	r.Post("/api/"+rbac.TransactionRoute, func(w http.ResponseWriter, req *http.Request) {
		tc := tenant.MustFromCtx(req.Context())
		claims := auth.ClaimsFromCtx(req.Context())
		if claims == nil {
			writeJSONErr(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		evalCtx := rbac.EvalContext{Role: claims.Role, UserID: claims.UserID, ExternalClientID: claims.ExternalClientID}

		req.Body = http.MaxBytesReader(w, req.Body, maxRequestBodyBytes)
		var reqBody txRequest
		if err := json.NewDecoder(req.Body).Decode(&reqBody); err != nil {
			if strings.Contains(err.Error(), "request body too large") {
				writeJSONErr(w, http.StatusRequestEntityTooLarge, "request body too large")
				return
			}
			writeJSONErr(w, http.StatusBadRequest, "invalid JSON body")
			return
		}
		ops := reqBody.Operations
		if len(ops) == 0 {
			writeJSONErr(w, http.StatusBadRequest, `no operations: provide a non-empty "operations" array`)
			return
		}
		if len(ops) > maxOps {
			writeJSONErr(w, http.StatusBadRequest, fmt.Sprintf("too many operations: %d (max %d) — split into smaller transactions", len(ops), maxOps))
			return
		}

		// Phase 1 — authorize + validate + run before hooks + build SQL, all OUTSIDE
		// the transaction. A client error (403/422/400) returns here without ever
		// opening a tx (so it never counts against the DB breaker, exactly like the
		// single-op path validates before touching the pool).
		prepared := make([]preparedOp, len(ops))
		for i := range ops {
			p, terr := prepareTxOp(req.Context(), &ops[i], refs, policy, evalCtx, hookEval)
			if terr != nil {
				terr.index = i
				writeTxError(w, req, terr)
				return
			}
			prepared[i] = *p
		}

		// Phase 2 — execute every prepared op on ONE tenant-scoped transaction.
		// Any failure returns an error from the closure → WithTenantTx ROLLS BACK,
		// so nothing is persisted (atomic, no partial state).
		results := make([]map[string]any, len(prepared))
		err := tdb.WithTenantTx(req.Context(), tc.PGSchema, func(ctx context.Context, tx pgx.Tx) error {
			for i := range prepared {
				out, terr := execPreparedOp(ctx, tx, tc.ID, &prepared[i])
				if terr != nil {
					terr.index = i
					return terr
				}
				results[i] = out
			}
			return nil
		})
		if err != nil {
			writeTxError(w, req, err)
			return
		}

		// The batch committed: drop this tenant's cached GETs so a follow-up read
		// reflects the writes immediately (the single-op write path does the same —
		// without this a cached read after a transaction is stale).
		if inv != nil {
			inv.Invalidate(tc.ID)
		}

		markSpan(req, "transaction")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		pkghandlers.WriteJSON(w, map[string]any{"results": results}) //nolint:errcheck
	})
}

// prepareTxOp authorizes and validates one operation and builds its SQL, returning a
// preparedOp ready to execute on the shared tx, or a txError (the whole batch fails).
// It reuses the SAME cores the single-op handlers use — policy.Evaluate (per-resource
// RBAC, G2), the compiled validator, EnforceCreateRBAC (create mass-assignment
// block), CollectUpdate, the before_create/before_update hook — so a batch op is
// authorized and validated identically to its standalone counterpart.
func prepareTxOp(ctx context.Context, op *txOp, refs map[string]*txResource, policy *rbac.Policy, evalCtx rbac.EvalContext,
	hookEval func(ctx context.Context, hook *schema.HookConfig, body map[string]any) (map[string]any, int, string)) (*preparedOp, *txError) {

	action, ok := map[string]string{"create": "create", "update": "update", "delete": "delete"}[op.Op]
	if !ok {
		return nil, &txError{status: http.StatusBadRequest, op: op.Op, resource: op.Resource, msg: fmt.Sprintf("unknown op %q: must be create, update or delete", op.Op)}
	}
	ref, known := refs[op.Resource]
	if !known {
		return nil, &txError{status: http.StatusBadRequest, op: op.Op, resource: op.Resource, msg: fmt.Sprintf("unknown resource %q", op.Resource)}
	}

	// Per-operation RBAC: deny-by-default. A role that may not perform this action on
	// this resource fails the WHOLE transaction with 403 (its per-resource condition
	// + field allowlist from G2 are carried in eval and applied below).
	eval := policy.Evaluate(evalCtx, op.Resource, action)
	if !eval.Allowed {
		return nil, &txError{status: http.StatusForbidden, op: op.Op, resource: op.Resource, msg: "forbidden"}
	}

	switch op.Op {
	case "create":
		if len(op.Data) == 0 {
			return nil, &txError{status: http.StatusBadRequest, op: op.Op, resource: op.Resource, msg: "create requires a non-empty data object"}
		}
		data := cloneData(op.Data)
		ref.validator.ApplyDefaults(data)
		if verrs := ref.validator.ValidateWrite(data, true); len(verrs) > 0 {
			return nil, &txError{status: http.StatusUnprocessableEntity, op: op.Op, resource: op.Resource, msg: "validation_failed", fields: verrs}
		}
		if verrs := ref.validator.ValidateInitialStates(data); len(verrs) > 0 { // G5: create in an initial state
			return nil, &txError{status: http.StatusUnprocessableEntity, op: op.Op, resource: op.Resource, msg: "validation_failed", fields: verrs}
		}
		if hc, has := ref.res.Hooks["before_create"]; has {
			c := hc
			nd, st, msg := hookEval(ctx, &c, data)
			if st != 0 {
				return nil, &txError{status: st, op: op.Op, resource: op.Resource, msg: msg}
			}
			data = nd
		}
		if st, msg := EnforceCreateRBAC(data, &eval); st != 0 {
			return nil, &txError{status: st, op: op.Op, resource: op.Resource, msg: msg}
		}
		cols, ph, args := pkghandlers.BuildInsertArgs(data)
		return &preparedOp{
			kind:     "create",
			sql:      fmt.Sprintf("INSERT INTO %s (%s) VALUES (%s) RETURNING *", ref.tbl, cols, ph),
			args:     args,
			resource: op.Resource,
			emit:     ref.emitCreate,
			allowed:  eval.AllowedFields,
		}, nil

	case "update":
		if err := validateOpID(op.ID); err != nil {
			return nil, &txError{status: http.StatusBadRequest, op: op.Op, resource: op.Resource, msg: err.Error()}
		}
		if len(op.Data) == 0 {
			return nil, &txError{status: http.StatusBadRequest, op: op.Op, resource: op.Resource, msg: "update requires a non-empty data object"}
		}
		// Declarative rules (min/max/pattern/format) + state-machine known-state, the
		// SAME ValidateWrite the single-op PATCH runs before CollectUpdate — a batched
		// update is validated identically to a standalone one.
		if verrs := ref.validator.ValidateWrite(op.Data, false); len(verrs) > 0 {
			return nil, &txError{status: http.StatusUnprocessableEntity, op: op.Op, resource: op.Resource, msg: "validation_failed", fields: verrs}
		}
		writable := writableFn(eval.AllowedFields)
		sets, st, msg := CollectUpdate(&ref.res, op.Data, false, writable)
		if st != 0 {
			return nil, &txError{status: st, op: op.Op, resource: op.Resource, msg: msg}
		}
		if hc, has := ref.res.Hooks["before_update"]; has {
			c := hc
			nd, hst, hmsg := hookEval(ctx, &c, op.Data)
			if hst != 0 {
				return nil, &txError{status: hst, op: op.Op, resource: op.Resource, msg: hmsg}
			}
			for col := range sets {
				if nv, present := nd[col]; present {
					sets[col] = nv
				}
			}
		}
		q, args, terr := buildUpdateSQL(&ref.res, ref.tbl, op.ID, sets, eval.Condition, op.Guard)
		if terr != nil {
			terr.op, terr.resource = op.Op, op.Resource
			return nil, terr
		}
		return &preparedOp{kind: "update", sql: q, args: args, resource: op.Resource, emit: ref.emitUpdate, allowed: eval.AllowedFields}, nil

	default: // delete
		if err := validateOpID(op.ID); err != nil {
			return nil, &txError{status: http.StatusBadRequest, op: op.Op, resource: op.Resource, msg: err.Error()}
		}
		q := fmt.Sprintf("DELETE FROM %s WHERE id = $1", ref.tbl)
		args := []any{op.ID}
		var cerr error
		if q, args, cerr = query.AppendRowCondition(q, args, eval.Condition); cerr != nil {
			return nil, &txError{status: http.StatusInternalServerError, op: op.Op, resource: op.Resource, msg: "invalid rbac condition"}
		}
		var terr *txError
		if q, args, terr = appendGuards(q, args, op.Guard, &ref.res); terr != nil {
			terr.op, terr.resource = op.Op, op.Resource
			return nil, terr
		}
		return &preparedOp{kind: "delete", sql: q, args: args, resource: op.Resource, emit: ref.emitDelete, id: op.ID}, nil
	}
}

// execPreparedOp runs ONE prepared op on the shared tx and emits its outbox event in
// the SAME tx. A unique-violation maps to 409 and an undefined column to 422 (same as
// the single-op path); an update/delete that matches no row (not found, excluded by
// the role's row condition, or a guard that failed) fails the transaction so a batch
// can never silently no-op a write it was asked to make.
func execPreparedOp(ctx context.Context, tx pgx.Tx, tenantID string, p *preparedOp) (map[string]any, *txError) {
	if p.kind == "delete" {
		tag, err := tx.Exec(ctx, p.sql, p.args...)
		if err != nil {
			return nil, dbTxError(err)
		}
		if tag.RowsAffected() == 0 {
			return nil, &txError{status: http.StatusNotFound, op: p.kind, resource: p.resource, msg: notFoundMsg(p)}
		}
		if p.emit {
			if eerr := enqueueCRUDEvent(ctx, tx, tenantID, p.resource, "delete", p.id); eerr != nil {
				return nil, &txError{status: http.StatusInternalServerError, op: p.kind, resource: p.resource, msg: "event emission failed"}
			}
		}
		return map[string]any{"deleted": true, "id": p.id}, nil
	}

	rows, err := tx.Query(ctx, p.sql, p.args...)
	if err != nil {
		return nil, dbTxError(err)
	}
	out, rerr := pkghandlers.RowsToMaps(rows)
	rows.Close()
	if rerr != nil {
		return nil, dbTxError(rerr)
	}
	if len(out) == 0 {
		if p.kind == "update" {
			// A state-machine transition guard rejecting the move is a 422
			// (unprocessable), not a 404 — distinguish by the guard's marker in the SQL.
			status := http.StatusNotFound
			if strings.Contains(p.sql, " = ANY($") {
				status = http.StatusUnprocessableEntity
			}
			return nil, &txError{status: status, op: p.kind, resource: p.resource, msg: notFoundMsg(p)}
		}
		return nil, &txError{status: http.StatusInternalServerError, op: p.kind, resource: p.resource, msg: "insert returned no row"}
	}
	if p.emit {
		if eerr := enqueueCRUDEvent(ctx, tx, tenantID, p.resource, p.kind, out[0]["id"]); eerr != nil {
			return nil, &txError{status: http.StatusInternalServerError, op: p.kind, resource: p.resource, msg: "event emission failed"}
		}
	}
	return pkghandlers.FilterFields(out[0], p.allowed), nil
}

// buildUpdateSQL builds UPDATE … SET … WHERE id = $n [AND <rbac cond>] [AND <guards>]
// RETURNING *. It mirrors RunUpdate's SET construction (sorted columns, forced
// updated_at when the resource declares an auto one) so a batched update is
// byte-equivalent to a single-op PATCH, then folds in the role's row condition (BOLA)
// and any optimistic-lock guards.
func buildUpdateSQL(res *schema.ResourceSchema, tbl, id string, sets map[string]any, cond *rbac.WhereCondition, guards []txGuard) (string, []any, *txError) {
	cols := make([]string, 0, len(sets))
	for c := range sets {
		cols = append(cols, c)
	}
	sort.Strings(cols)
	setClauses := make([]string, 0, len(cols)+1)
	args := make([]any, 0, len(cols)+2)
	argIdx := 1
	for _, c := range cols {
		setClauses = append(setClauses, fmt.Sprintf("%s = $%d", pgx.Identifier{c}.Sanitize(), argIdx))
		args = append(args, sets[c])
		argIdx++
	}
	if fd, ok := res.Fields["updated_at"]; ok && fd.Auto {
		setClauses = append(setClauses, fmt.Sprintf("updated_at = $%d", argIdx))
		args = append(args, time.Now().UTC())
		argIdx++
	}
	if len(setClauses) == 0 {
		return "", nil, &txError{status: http.StatusUnprocessableEntity, msg: "no writable fields in request"}
	}
	q := fmt.Sprintf("UPDATE %s SET %s WHERE id = $%d", tbl, strings.Join(setClauses, ", "), argIdx)
	args = append(args, id)
	var cerr error
	if q, args, cerr = query.AppendRowCondition(q, args, cond); cerr != nil {
		return "", nil, &txError{status: http.StatusInternalServerError, msg: "invalid rbac condition"}
	}
	var terr *txError
	if q, args, terr = appendGuards(q, args, guards, res); terr != nil {
		return "", nil, terr
	}
	// State-machine transition guard (G5) — same race-safe predicate the single-op
	// RunUpdate appends, so a batched update enforces transitions identically.
	var serr error
	if q, args, serr = AppendStateTransitionGuard(q, args, res, sets); serr != nil {
		return "", nil, &txError{status: http.StatusUnprocessableEntity, msg: serr.Error()}
	}
	return q + " RETURNING *", args, nil
}

// appendGuards folds each guard into the WHERE as "AND <field> <op> $n" with the
// value bound as a parameter. The field must be a declared column of the resource
// and the op from the fixed allowlist, and the value is type-checked against the
// field — so a guard cannot inject SQL nor raise a raw DB type error.
func appendGuards(sql string, args []any, guards []txGuard, res *schema.ResourceSchema) (string, []any, *txError) {
	for _, g := range guards {
		opSQL, ok := txGuardOps[g.Op]
		if !ok {
			return sql, args, &txError{status: http.StatusBadRequest, msg: fmt.Sprintf("invalid guard op %q: must be one of eq, ne, gt, gte, lt, lte", g.Op)}
		}
		fd, known := res.Fields[g.Field]
		if !known || !txFieldRe.MatchString(g.Field) {
			return sql, args, &txError{status: http.StatusBadRequest, msg: fmt.Sprintf("unknown guard field %q", g.Field)}
		}
		if g.Value == nil {
			return sql, args, &txError{status: http.StatusBadRequest, msg: fmt.Sprintf("guard on %q requires a value", g.Field)}
		}
		if msg, valid := validateFieldValue(g.Field, fd, g.Value); !valid {
			return sql, args, &txError{status: http.StatusUnprocessableEntity, msg: "guard " + msg}
		}
		sql += fmt.Sprintf(" AND %s %s $%d", g.Field, opSQL, len(args)+1)
		args = append(args, g.Value)
	}
	return sql, args, nil
}

// dbTxError classifies a DB error from inside the transaction into a txError. A
// unique violation is a 409, an undefined column a 422, a bad `file` reference a
// field-addressed 422 and any other FK violation a 409 (exactly the single-op
// mapping); anything else is an unexpected 500 (the raw SQL is never exposed).
func dbTxError(err error) *txError {
	if field, ok := db.UniqueViolationField(err); ok {
		return &txError{status: http.StatusConflict, msg: fmt.Sprintf("field %q: value already exists", field)}
	}
	if field, ok := db.UndefinedColumnField(err); ok {
		return &txError{status: http.StatusUnprocessableEntity, msg: fmt.Sprintf("unknown field: %q", field)}
	}
	// A `file` field referencing no file of the tenant (FILES-LINK-S1) — the same
	// field-addressed 422 the single-op path answers.
	if column, ok := db.FileReferenceViolation(err); ok {
		if column == "" {
			column = "file"
		}
		return &txError{status: http.StatusUnprocessableEntity, msg: "validation_failed",
			fields: []schema.FieldRuleError{{Field: column, Rule: "file_not_found", Message: "does not reference an existing file of this tenant"}}}
	}
	// Any other referential conflict (a bad relation reference, a RESTRICT delete)
	// — the same clean 409 as the single-op path (previously a masked 500).
	if fkMsg, ok := db.ForeignKeyViolation(err); ok {
		return &txError{status: http.StatusConflict, msg: fkMsg}
	}
	return &txError{status: http.StatusInternalServerError, msg: "internal error", fields: dbUnavailableMarker(err)}
}

// dbUnavailableMarker is nil normally; when err signals an unreachable DB it carries
// a sentinel so writeTxError can answer 503 instead of 500.
func dbUnavailableMarker(err error) []schema.FieldRuleError {
	if db.IsUnavailable(err) || db.IsMissingTenant(err) {
		return []schema.FieldRuleError{{Rule: "__unavailable__"}}
	}
	return nil
}

// writeTxError maps the batch failure to an HTTP response that names WHICH operation
// failed and why (never an opaque 500). A *txError uses its own status; an infra
// error becomes 503; anything else is a captured 500.
func writeTxError(w http.ResponseWriter, req *http.Request, err error) {
	te, ok := err.(*txError)
	if !ok {
		if db.IsUnavailable(err) || db.IsMissingTenant(err) {
			writeJSONErr(w, http.StatusServiceUnavailable, "database unavailable")
			return
		}
		capture500(req, err)
		writeJSONErr(w, http.StatusInternalServerError, "internal error")
		return
	}
	if len(te.fields) == 1 && te.fields[0].Rule == "__unavailable__" {
		writeJSONErr(w, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	body := map[string]any{
		"error":            te.msg,
		"failed_operation": te.index,
	}
	if te.op != "" {
		body["op"] = te.op
	}
	if te.resource != "" {
		body["resource"] = te.resource
	}
	if len(te.fields) > 0 {
		body["fields"] = te.fields
	}
	if t := observability.SpanTrackerFromCtx(req.Context()); t != nil {
		t.RecordError(te.msg)
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(te.status)
	pkghandlers.WriteJSON(w, body) //nolint:errcheck
}

// --- small helpers ----------------------------------------------------------

func cloneData(m map[string]any) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

func writableFn(allowed []string) func(string) bool {
	if len(allowed) == 0 {
		return func(string) bool { return true }
	}
	set := make(map[string]struct{}, len(allowed))
	for _, f := range allowed {
		set[f] = struct{}{}
	}
	return func(f string) bool { _, ok := set[f]; return ok }
}

func validateOpID(id string) error {
	if id == "" {
		return fmt.Errorf("id is required")
	}
	if _, err := uuid.Parse(id); err != nil {
		return fmt.Errorf("invalid id format")
	}
	return nil
}

// notFoundMsg explains a zero-row update/delete: it lists the possible causes a
// guard / state-machine transition adds (a precise per-field message would need a
// read inside the rolling-back tx; the single-op path gives that, the batch names
// the failing operation + the cause set).
func notFoundMsg(p *preparedOp) string {
	if strings.Contains(p.sql, " = ANY($") { // state-machine transition guard present
		return "not found, excluded by access policy, or an invalid state transition"
	}
	if strings.Contains(p.sql, " AND ") && (strings.Contains(p.sql, " <> ") || strings.Contains(p.sql, " >= ") || strings.Contains(p.sql, " <= ") || strings.Contains(p.sql, " > ") || strings.Contains(p.sql, " < ")) {
		return "not found, excluded by access policy, or a guard condition was not met"
	}
	return "not found or excluded by access policy"
}
