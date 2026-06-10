package codegen

import (
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/miguelangel/appitools/pkg/auth"
	"github.com/miguelangel/appitools/pkg/db"
	"github.com/miguelangel/appitools/pkg/extensions"
	pkghandlers "github.com/miguelangel/appitools/pkg/handlers"
	"github.com/miguelangel/appitools/pkg/observability"
	"github.com/miguelangel/appitools/pkg/query"
	"github.com/miguelangel/appitools/pkg/rbac"
	"github.com/miguelangel/appitools/pkg/schema"
	"github.com/miguelangel/appitools/pkg/tenant"
)

// CacheInvalidator drops a tenant's cached GET responses. *cache.ResponseCache
// implements it. BuildRouter calls it after a successful PUT/PATCH so a follow-up
// read reflects the write immediately instead of waiting out the response-cache
// TTL. May be nil (e.g. in tests or when no cache is wired).
type CacheInvalidator interface {
	Invalidate(tenantID string)
}

// writeJSONErr writes {"error": msg} with the given status and JSON content type.
func writeJSONErr(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]string{"error": msg}) //nolint:errcheck
}

// writeValidationErrs writes the declarative-validation 422 response, reporting
// EVERY violated field/rule in one pass (never just the first):
//
//	{"error":"validation_failed","fields":[{"field":"email","rule":"format","message":"must be a valid email"}]}
func writeValidationErrs(w http.ResponseWriter, errs []schema.FieldRuleError) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnprocessableEntity)
	json.NewEncoder(w).Encode(map[string]any{"error": "validation_failed", "fields": errs}) //nolint:errcheck
}

// maxRequestBodyBytes caps the size of a write request body. Without it a client
// could stream an arbitrarily large JSON document and force unbounded allocation
// (OWASP API4). Matches the GraphQL handler's 1 MiB limit.
const maxRequestBodyBytes = 1 << 20 // 1 MiB

// markSpan records a named stage on the request's SpanTracker, if present. The
// nil-check keeps it a no-op for requests/tests that run without a tracker.
func markSpan(req *http.Request, name string) {
	if t := observability.SpanTrackerFromCtx(req.Context()); t != nil {
		t.Mark(name)
	}
}

// capture500 records the error message + a symbolized stack on the request's
// SpanTracker, enriched with the user/role/method the handler knows. Call ONLY
// for server errors (500) — it pays for runtime.Callers; never on the happy path.
func capture500(req *http.Request, err error) {
	t := observability.SpanTrackerFromCtx(req.Context())
	if t == nil {
		return
	}
	t.RecordError(err.Error())
	c := observability.CaptureError(req.Context(), err)
	c.Method = req.Method
	c.Route = req.URL.Path
	if claims := auth.ClaimsFromCtx(req.Context()); claims != nil {
		c.UserID = claims.UserID
		c.Role = claims.Role
	}
	t.SetCapture(&c)
}

// writeDBErr maps a DB error to its HTTP response, first capturing a stack trace
// when the error is a server error (500). Client/availability errors (400/503) are
// not captured.
func writeDBErr(w http.ResponseWriter, req *http.Request, err error) {
	if pkghandlers.IsServerError(err) {
		capture500(req, err)
	}
	pkghandlers.WriteDBError(w, err)
}

// rbacCondFieldRe validates an RBAC condition's Field as a bare SQL identifier
// before it is interpolated into a WHERE clause (the value is always a bound
// parameter). Condition fields come from trusted policy config, but validating
// matches the list path (query.BuildQuery) and is defence-in-depth.
var rbacCondFieldRe = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

// applyRowCondition appends a row-level RBAC WhereCondition to a single-row query
// already parameterised at $1 (the row id), so GET-by-id and DELETE enforce the
// same row-level filter the list endpoint does. Without it, a role restricted to
// its own rows (e.g. user_id = $user_id) could read or delete ANY row by guessing
// its id — a BOLA. Returns ok=false if the policy's field is not a valid
// identifier, so the caller fails closed rather than emit unfiltered SQL.
func applyRowCondition(query string, args []any, cond *rbac.WhereCondition) (string, []any, bool) {
	if cond == nil {
		return query, args, true
	}
	if !rbacCondFieldRe.MatchString(cond.Field) {
		return query, args, false
	}
	query += fmt.Sprintf(" AND %s = $%d", cond.Field, len(args)+1)
	args = append(args, cond.Value)
	return query, args, true
}

// BuildRouter creates a chi.Mux with real SQL handlers for every resource in the schema.
// Used by `appitools serve` — no code generation required. inv (nil-able) is the
// response-cache invalidator called after a successful PUT/PATCH.
func BuildRouter(s *schema.APISchema, tdb *db.TenantDB, hr *extensions.HookRunner, inv CacheInvalidator) *chi.Mux {
	names := make([]string, 0, len(s.Resources))
	for name := range s.Resources {
		names = append(names, name)
	}
	sort.Strings(names)

	r := chi.NewMux()

	for _, resName := range names {
		name := resName
		res := s.Resources[resName]
		// Declarative validation is compiled ONCE here — BuildRouter runs at
		// schema load/reload, never per request. The handlers below only execute
		// the precompiled closures (S44 requirement #1).
		rv := schema.CompileRules(&res)

		// --- List ---
		r.Get("/api/"+name, pkghandlers.CachedGet(func(w http.ResponseWriter, req *http.Request) {
			tc := tenant.MustFromCtx(req.Context())
			evalResult := rbac.EvalResultFromCtx(req.Context())

			var cond *rbac.WhereCondition
			if evalResult != nil {
				cond = evalResult.Condition
			}

			qb, err := query.BuildQuery(name, &res, req.URL.Query(), cond)
			if err != nil {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusBadRequest)
				json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
				return
			}

			selectQ, _, selectArgs, _ := qb.SQL()

			// QueryDirect: schema-qualified table name — no transaction, no SET LOCAL.
			rows, err := tdb.QueryDirect(req.Context(), tc.PGSchema, name, selectQ, selectArgs...)
			if err != nil {
				markSpan(req, "query") // mark the attempted query so 5xx traces show it
				writeDBErr(w, req, err)
				return
			}
			defer rows.Close()
			data, err := pkghandlers.RowsToMaps(rows)
			if err != nil {
				writeDBErr(w, req, err)
				return
			}
			markSpan(req, "query")

			if evalResult != nil && len(evalResult.AllowedFields) > 0 {
				for i, rec := range data {
					data[i] = pkghandlers.FilterFields(rec, evalResult.AllowedFields)
				}
			}
			if data == nil {
				data = []map[string]any{}
			}

			hasNext := len(data) == qb.PerPage()
			hasPrev := qb.Page() > 1

			w.Header().Set("Content-Type", "application/json")
			pkghandlers.WriteJSON(w, map[string]any{ //nolint:errcheck
				"data": data,
				"meta": map[string]any{
					"page":     qb.Page(),
					"per_page": qb.PerPage(),
					"has_next": hasNext,
					"has_prev": hasPrev,
				},
			})
			markSpan(req, "serialize")
		}))

		// --- Create ---
		r.Post("/api/"+name, func(w http.ResponseWriter, req *http.Request) {
			tc := tenant.MustFromCtx(req.Context())
			// Parse body (1 MiB cap → 413; malformed → 400). Mirrors the PUT/PATCH
			// handlers and the OpenAPI contract (Error413), which already document a
			// 413 for an oversized create body.
			req.Body = http.MaxBytesReader(w, req.Body, maxRequestBodyBytes)
			var body map[string]any
			if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
				if strings.Contains(err.Error(), "request body too large") {
					writeJSONErr(w, http.StatusRequestEntityTooLarge, "request body too large")
					return
				}
				writeJSONErr(w, http.StatusBadRequest, "invalid JSON body")
				return
			}
			if len(body) == 0 {
				writeJSONErr(w, http.StatusBadRequest, "empty body")
				return
			}

			// Declarative validation: BEFORE the before_create hook and the
			// INSERT. Create requires every required non-auto field (S44 #2).
			if verrs := rv.ValidateWrite(body, true); len(verrs) > 0 {
				markSpan(req, "validate")
				writeValidationErrs(w, verrs)
				return
			}

			var beforeHook *schema.HookConfig
			if hc, ok := res.Hooks["before_create"]; ok {
				c := hc
				beforeHook = &c
			}
			hookRes, hookErr := hr.RunBeforeHook(req.Context(), beforeHook, body, nil)
			if hookErr != nil {
				// before_create hook failed to execute → 500: capture the stack.
				capture500(req, hookErr)
				http.Error(w, hookErr.Error(), http.StatusInternalServerError)
				return
			}
			if !hookRes.Proceed {
				// The before_create hook rejected (422, a client error): mark the
				// stage and record the message, but capture NO stack (not a bug).
				markSpan(req, "hook")
				if t := observability.SpanTrackerFromCtx(req.Context()); t != nil {
					t.RecordError(hookRes.Error)
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusUnprocessableEntity)
				json.NewEncoder(w).Encode(map[string]string{"error": hookRes.Error})
				return
			}
			body = hookRes.Data
			if beforeHook != nil {
				markSpan(req, "hook")
			}

			// NOTE: column identifiers are quoted by BuildInsertArgs so a client
			// key cannot break out of the identifier position (SQL injection). We do
			// NOT whitelist keys against res.Fields here: the schema can evolve at
			// runtime (a migration adds a column without rebuilding this router), so
			// the DB is the source of truth — an unknown column simply errors at the
			// DB. Residual mass-assignment (e.g. client-set id) is low-impact for the
			// current schema (no privilege/tenant columns) and tracked separately.
			cols, placeholders, args := pkghandlers.BuildInsertArgs(body)
			insertQ := fmt.Sprintf("INSERT INTO %s (%s) VALUES (%s) RETURNING *", name, cols, placeholders)
			result, err := tdb.ExecRowsTenant(req.Context(), tc.PGSchema, insertQ, args...)
			if err != nil {
				writeDBErr(w, req, err)
				return
			}
			markSpan(req, "insert")

			if hc, ok := res.Hooks["after_create"]; ok {
				afterHook := hc
				var record map[string]any
				if len(result) > 0 {
					record = result[0]
				}
				// Bounded async dispatch: returns immediately, never blocks the
				// 201 on webhook latency, and caps in-flight dispatches so a
				// create storm cannot spawn unbounded goroutines.
				hr.FireAfterHook(&afterHook, record, tc.ID)
			}

			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			if len(result) > 0 {
				pkghandlers.WriteJSON(w, result[0]) //nolint:errcheck
			}
			markSpan(req, "serialize")
		})

		// --- Get by ID ---
		r.Get("/api/"+name+"/{id}", pkghandlers.CachedGet(func(w http.ResponseWriter, req *http.Request) {
			tc := tenant.MustFromCtx(req.Context())
			id := chi.URLParam(req, "id")
			if _, err := uuid.Parse(id); err != nil {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusBadRequest)
				json.NewEncoder(w).Encode(map[string]string{"error": "invalid id format"})
				return
			}
			// Enforce the row-level RBAC condition (if any) so a restricted role
			// cannot read another principal's row by guessing its id.
			evalResult := rbac.EvalResultFromCtx(req.Context())
			q := fmt.Sprintf("SELECT * FROM %s WHERE id = $1", name)
			args := []any{id}
			if evalResult != nil {
				var ok bool
				if q, args, ok = applyRowCondition(q, args, evalResult.Condition); !ok {
					writeDBErr(w, req, fmt.Errorf("invalid rbac condition field"))
					return
				}
			}
			rows, err := tdb.QueryTenant(req.Context(), tc.PGSchema, q, args...)
			if err != nil {
				writeDBErr(w, req, err)
				return
			}
			defer rows.Close()
			result, err := pkghandlers.RowsToMaps(rows)
			if err != nil {
				writeDBErr(w, req, err)
				return
			}
			markSpan(req, "query")
			if len(result) == 0 {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusNotFound)
				json.NewEncoder(w).Encode(map[string]string{"error": "not found"})
				return
			}
			record := result[0]
			if evalResult != nil && len(evalResult.AllowedFields) > 0 {
				record = pkghandlers.FilterFields(record, evalResult.AllowedFields)
			}
			w.Header().Set("Content-Type", "application/json")
			pkghandlers.WriteJSON(w, record) //nolint:errcheck
			markSpan(req, "serialize")
		}))

		// --- Delete ---
		r.Delete("/api/"+name+"/{id}", func(w http.ResponseWriter, req *http.Request) {
			tc := tenant.MustFromCtx(req.Context())
			id := chi.URLParam(req, "id")
			if _, err := uuid.Parse(id); err != nil {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusBadRequest)
				json.NewEncoder(w).Encode(map[string]string{"error": "invalid id format"})
				return
			}
			// Enforce the row-level RBAC condition (if any) so a restricted role
			// cannot delete another principal's row by guessing its id.
			evalResult := rbac.EvalResultFromCtx(req.Context())
			q := fmt.Sprintf("DELETE FROM %s WHERE id = $1", name)
			args := []any{id}
			if evalResult != nil {
				var ok bool
				if q, args, ok = applyRowCondition(q, args, evalResult.Condition); !ok {
					writeDBErr(w, req, fmt.Errorf("invalid rbac condition field"))
					return
				}
			}
			affected, err := tdb.ExecTenant(req.Context(), tc.PGSchema, q, args...)
			if err != nil {
				writeDBErr(w, req, err)
				return
			}
			if affected == 0 {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusNotFound)
				json.NewEncoder(w).Encode(map[string]string{"error": "not found"})
				return
			}
			w.WriteHeader(http.StatusNoContent)
		})

		// --- Update (PUT = full replace, PATCH = partial) ---
		// RBAC action="update" is enforced by RBACMiddleware before the handler runs
		// (actionFromMethod maps PUT/PATCH → "update"), exactly like create/delete.
		// The handler only consumes the row-level Condition and field-level
		// AllowedFields from the evaluation result.
		updateHandler := func(put bool) http.HandlerFunc {
			return func(w http.ResponseWriter, req *http.Request) {
				tc := tenant.MustFromCtx(req.Context())
				id := chi.URLParam(req, "id")
				if _, err := uuid.Parse(id); err != nil {
					writeJSONErr(w, http.StatusBadRequest, "invalid id format")
					return
				}
				evalResult := rbac.EvalResultFromCtx(req.Context())

				// Parse body (1 MiB cap → 413; malformed → 400).
				req.Body = http.MaxBytesReader(w, req.Body, maxRequestBodyBytes)
				var body map[string]any
				if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
					if strings.Contains(err.Error(), "request body too large") {
						writeJSONErr(w, http.StatusRequestEntityTooLarge, "request body too large")
						return
					}
					writeJSONErr(w, http.StatusBadRequest, "invalid JSON body")
					return
				}
				if len(body) == 0 {
					writeJSONErr(w, http.StatusBadRequest, "empty body")
					return
				}

				// Declarative validation: BEFORE the before_update hook and the
				// UPDATE. PUT (full replace) enforces required fields; PATCH only
				// validates the fields present in the body (S44 #4).
				if verrs := rv.ValidateWrite(body, put); len(verrs) > 0 {
					markSpan(req, "validate")
					writeValidationErrs(w, verrs)
					return
				}

				// Field-level RBAC: when the role has an allowlist, only those columns
				// may be written; others are dropped silently (not an error).
				writable := func(string) bool { return true }
				if evalResult != nil && len(evalResult.AllowedFields) > 0 {
					allow := make(map[string]struct{}, len(evalResult.AllowedFields))
					for _, f := range evalResult.AllowedFields {
						allow[f] = struct{}{}
					}
					writable = func(f string) bool { _, ok := allow[f]; return ok }
				}

				// Validate against the schema and collect the columns to set.
				sets, status, msg := collectUpdate(&res, body, put, writable)
				if status != 0 {
					markSpan(req, "validate")
					writeJSONErr(w, status, msg)
					return
				}

				// Confirm the row exists AND passes the row-level RBAC condition.
				// A non-owned row yields zero rows → 404 (never 403): this matches the
				// GET-by-id/DELETE pattern and the S33/S34 BOLA fixes that deliberately
				// avoid revealing the existence of another principal's row.
				selQ := fmt.Sprintf("SELECT 1 FROM %s WHERE id = $1", name)
				selArgs := []any{id}
				if evalResult != nil {
					var ok bool
					if selQ, selArgs, ok = applyRowCondition(selQ, selArgs, evalResult.Condition); !ok {
						writeDBErr(w, req, fmt.Errorf("invalid rbac condition field"))
						return
					}
				}
				existRows, err := tdb.QueryTenant(req.Context(), tc.PGSchema, selQ, selArgs...)
				if err != nil {
					writeDBErr(w, req, err)
					return
				}
				exists := existRows.Next()
				existRows.Close()
				if rerr := existRows.Err(); rerr != nil {
					writeDBErr(w, req, rerr)
					return
				}
				if !exists {
					writeJSONErr(w, http.StatusNotFound, "not found")
					return
				}
				markSpan(req, "query")

				// before_update hook (Goja/WASM/webhook), same contract as before_create.
				var beforeHook *schema.HookConfig
				if hc, ok := res.Hooks["before_update"]; ok {
					c := hc
					beforeHook = &c
				}
				hookRes, hookErr := hr.RunBeforeHook(req.Context(), beforeHook, body, nil)
				if hookErr != nil {
					capture500(req, hookErr)
					writeJSONErr(w, http.StatusInternalServerError, "internal error")
					return
				}
				if !hookRes.Proceed {
					markSpan(req, "hook")
					if t := observability.SpanTrackerFromCtx(req.Context()); t != nil {
						t.RecordError(hookRes.Error)
					}
					writeJSONErr(w, http.StatusUnprocessableEntity, hookRes.Error)
					return
				}
				if beforeHook != nil {
					markSpan(req, "hook")
					// Adopt the hook's transformed values, but only for columns we
					// already decided to write — the hook cannot inject id/auto columns.
					for col := range sets {
						if nv, present := hookRes.Data[col]; present {
							sets[col] = nv
						}
					}
				}

				// Build UPDATE: every column name via pgx.Identifier.Sanitize(), every
				// value as a bound parameter. updated_at is forced to NOW() — but only
				// when the resource actually declares an auto updated_at column (most
				// logistics resources do not), otherwise the column does not exist.
				cols := make([]string, 0, len(sets))
				for c := range sets {
					cols = append(cols, c)
				}
				sort.Strings(cols) // deterministic SQL
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
					writeJSONErr(w, http.StatusUnprocessableEntity, "no writable fields in request")
					return
				}

				q := fmt.Sprintf("UPDATE %s SET %s WHERE id = $%d", name, strings.Join(setClauses, ", "), argIdx)
				args = append(args, id)
				if evalResult != nil {
					var ok bool
					if q, args, ok = applyRowCondition(q, args, evalResult.Condition); !ok {
						writeDBErr(w, req, fmt.Errorf("invalid rbac condition field"))
						return
					}
				}
				q += " RETURNING *"

				result, err := tdb.ExecRowsTenant(req.Context(), tc.PGSchema, q, args...)
				if err != nil {
					if field, ok := db.UniqueViolationField(err); ok {
						writeJSONErr(w, http.StatusConflict, fmt.Sprintf("field %q: value already exists", field))
						return
					}
					writeDBErr(w, req, err)
					return
				}
				markSpan(req, "update")
				if len(result) == 0 {
					// Row vanished between the existence check and the UPDATE (race).
					writeJSONErr(w, http.StatusNotFound, "not found")
					return
				}
				record := result[0]

				// Drop this tenant's cached GETs so the next read is fresh (no TTL wait).
				if inv != nil {
					inv.Invalidate(tc.ID)
				}

				if hc, ok := res.Hooks["after_update"]; ok {
					afterHook := hc
					hr.FireAfterHook(&afterHook, record, tc.ID)
				}

				if evalResult != nil && len(evalResult.AllowedFields) > 0 {
					record = pkghandlers.FilterFields(record, evalResult.AllowedFields)
				}
				w.Header().Set("Content-Type", "application/json")
				pkghandlers.WriteJSON(w, record) //nolint:errcheck
				markSpan(req, "serialize")
			}
		}
		r.Put("/api/"+name+"/{id}", updateHandler(true))
		r.Patch("/api/"+name+"/{id}", updateHandler(false))

		// --- Subresources: GET /api/{resource}/{id}/{relName} ---
		for fieldName, fd := range res.Fields {
			if fd.Relation == "" {
				continue
			}
			fn := fieldName // capture
			relResource := fd.Relation
			relRoute := strings.TrimSuffix(fn, "_id")

			r.Get("/api/"+name+"/{id}/"+relRoute, pkghandlers.CachedGet(func(w http.ResponseWriter, req *http.Request) {
				tc := tenant.MustFromCtx(req.Context())
				parentID := chi.URLParam(req, "id")
				if _, err := uuid.Parse(parentID); err != nil {
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusBadRequest)
					json.NewEncoder(w).Encode(map[string]string{"error": "invalid id format"})
					return
				}
				q := fmt.Sprintf(
					"SELECT r.* FROM %s r JOIN %s src ON src.%s = r.id WHERE src.id = $1",
					relResource, name, fn,
				)
				rows, err := tdb.QueryTenant(req.Context(), tc.PGSchema, q, parentID)
				if err != nil {
					writeDBErr(w, req, err)
					return
				}
				defer rows.Close()
				result, err := pkghandlers.RowsToMaps(rows)
				if err != nil {
					writeDBErr(w, req, err)
					return
				}
				if len(result) == 0 {
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusNotFound)
					json.NewEncoder(w).Encode(map[string]string{"error": "not found"})
					return
				}
				w.Header().Set("Content-Type", "application/json")
				pkghandlers.WriteJSON(w, result[0]) //nolint:errcheck
			}))
		}
	}

	return r
}

// collectUpdate validates body against the resource schema for an update and
// returns the columns→values to write (excluding the auto updated_at, which the
// handler forces to NOW()). On a validation failure it returns a non-zero HTTP
// status (always 422 here) and a client-safe message.
//
//   - PUT (put=true): every non-auto required field must be present and non-null;
//     optional fields absent from the body are written as NULL (full replacement).
//   - PATCH (put=false): only fields present in the body are written; the rest are
//     left untouched in the DB.
//
// Both reject "id" and any auto-managed field appearing in the body, reject unknown
// fields, type-check values, and validate enums. Fields the role may not write
// (per writable) are silently dropped from the result.
func collectUpdate(res *schema.ResourceSchema, body map[string]any, put bool, writable func(string) bool) (map[string]any, int, string) {
	// Reject id / unknown / auto-managed keys, then type-check each present value.
	for k, v := range body {
		if k == "id" {
			return nil, http.StatusUnprocessableEntity, `field "id" cannot be set`
		}
		fd, known := res.Fields[k]
		if !known {
			return nil, http.StatusUnprocessableEntity, fmt.Sprintf("unknown field: %q", k)
		}
		if fd.Auto {
			return nil, http.StatusUnprocessableEntity, fmt.Sprintf("field %q is set automatically and cannot be written", k)
		}
		if v == nil {
			if fd.Required {
				return nil, http.StatusUnprocessableEntity, fmt.Sprintf("field %q is required and cannot be null", k)
			}
			continue // null on an optional field → NULL in DB
		}
		if msg, ok := validateFieldValue(k, fd, v); !ok {
			return nil, http.StatusUnprocessableEntity, msg
		}
	}

	// PUT requires every non-auto required field to be present and non-null.
	if put {
		for name, fd := range res.Fields {
			if fd.Auto || !fd.Required {
				continue
			}
			if v, present := body[name]; !present || v == nil {
				return nil, http.StatusUnprocessableEntity, fmt.Sprintf("missing required field: %q", name)
			}
		}
	}

	sets := make(map[string]any)
	if put {
		// Full replacement: write every non-auto field — body value or NULL.
		for name, fd := range res.Fields {
			if fd.Auto || !writable(name) {
				continue
			}
			sets[name] = body[name] // absent key → nil → NULL
		}
	} else {
		// Partial: only the validated keys present in the body.
		for name, v := range body {
			if !writable(name) {
				continue
			}
			sets[name] = v
		}
	}
	return sets, 0, ""
}

// validateFieldValue checks a single decoded JSON value against a field definition.
// JSON decoding yields float64 for all numbers, so integer fields require an
// integral float64. Returns a client-safe message and false on mismatch.
func validateFieldValue(name string, fd schema.FieldDef, v any) (string, bool) {
	if len(fd.Enum) > 0 {
		s, ok := v.(string)
		if !ok {
			return fmt.Sprintf("field %q must be one of the allowed values", name), false
		}
		for _, e := range fd.Enum {
			if e == s {
				return "", true
			}
		}
		return fmt.Sprintf("field %q: invalid enum value", name), false
	}

	switch fd.Type {
	case "string", "text":
		if _, ok := v.(string); !ok {
			return fmt.Sprintf("field %q must be a string", name), false
		}
	case "uuid":
		s, ok := v.(string)
		if !ok {
			return fmt.Sprintf("field %q must be a uuid", name), false
		}
		if _, err := uuid.Parse(s); err != nil {
			return fmt.Sprintf("field %q must be a uuid", name), false
		}
	case "int", "int64":
		f, ok := v.(float64)
		if !ok || f != math.Trunc(f) {
			return fmt.Sprintf("field %q must be an integer", name), false
		}
	case "float64":
		if _, ok := v.(float64); !ok {
			return fmt.Sprintf("field %q must be a number", name), false
		}
	case "bool":
		if _, ok := v.(bool); !ok {
			return fmt.Sprintf("field %q must be a boolean", name), false
		}
	case "time":
		if _, ok := v.(string); !ok {
			return fmt.Sprintf("field %q must be a timestamp string", name), false
		}
		// The exact timestamp format is validated by Postgres on write; accepting any
		// string here keeps the handler lenient (documented deviation).
	case "json":
		// Any JSON value is acceptable for a json column.
	}
	return "", true
}
