package codegen

import (
	"context"
	"encoding/json"
	"errors"
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
	"github.com/miguelangel/appitools/pkg/events"
	"github.com/miguelangel/appitools/pkg/extensions"
	pkghandlers "github.com/miguelangel/appitools/pkg/handlers"
	"github.com/miguelangel/appitools/pkg/observability"
	"github.com/miguelangel/appitools/pkg/outbox"
	"github.com/miguelangel/appitools/pkg/query"
	"github.com/miguelangel/appitools/pkg/rbac"
	"github.com/miguelangel/appitools/pkg/schema"
	"github.com/miguelangel/appitools/pkg/tenant"
)

// enqueueCRUDEvent writes a transactional outbox event for a generated CRUD
// write (CRUD-EMIT-V1), on the SAME tx as the write so the two are atomic. The
// topic is "{resource}.{created|updated|deleted}" and the payload is lean — id +
// identity only (the consumer SELECTs the row if it needs more, keeping the
// outbox small). action is the present-tense form ("create"|"update"|"delete").
func enqueueCRUDEvent(ctx context.Context, tx pgx.Tx, tenantID, resource, action string, id any) error {
	topic := schema.EmitTopic(resource, action)
	payload := map[string]any{
		"id":        id,
		"tenant_id": tenantID,
		"resource":  resource,
		"action":    strings.TrimPrefix(topic, resource+"."), // past-tense suffix
	}
	_, err := outbox.Enqueue(ctx, tx, tenantID, topic, payload)
	return err
}

// policyFromSchema rebuilds an *rbac.Policy from the schema's RBAC block so the
// generated handlers can evaluate read access to RELATION TARGET resources at
// request time (the middleware only evaluates the route's own resource). Built
// once at boot in BuildRouter, never per request.
func policyFromSchema(s *schema.APISchema) *rbac.Policy {
	b, _ := json.Marshal(s.RBAC)
	var p rbac.Policy
	_ = json.Unmarshal(b, &p) //nolint:errcheck
	return &p
}

// makeRelationRBAC binds the request's identity to policy.Evaluate so the include
// compiler can decide, per relation target, whether the role may read it (else a
// 403), its field allowlist (scopes the embedded json_build_object), and its
// row-level condition (injected into the embed WHERE). RBAC-at-compilation, not
// a post-hoc filter — the SQL never selects a child the role may not see.
func makeRelationRBAC(policy *rbac.Policy, req *http.Request) query.RelationRBAC {
	// Identity resolved the SAME way the route RBAC middleware resolves it (JWT
	// claims, else X-User-* headers) — one principal across every authorization path.
	ev := rbac.EvalContextFromRequest(req)
	return func(resource string) (bool, []string, *rbac.WhereCondition) {
		r := policy.Evaluate(ev, resource, "read")
		return r.Allowed, r.AllowedFields, r.Condition
	}
}

// writeIncludeList writes the {"data":[...],"meta":{...}} envelope for a
// list-with-embeds response. data is the raw JSON array built by Postgres — it is
// written verbatim, never re-marshalled (the low-overhead direct-bytes path).
func writeIncludeList(w http.ResponseWriter, data []byte, page, perPage, n int) {
	if len(data) == 0 {
		data = []byte("[]")
	}
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprintf(w, `{"data":%s,"meta":{"page":%d,"per_page":%d,"has_next":%t,"has_prev":%t}}`,
		data, page, perPage, n == perPage, page > 1)
}

// CacheInvalidator drops a tenant's cached GET responses. *cache.ResponseCache
// implements it. BuildRouter calls it after a successful PUT/PATCH so a follow-up
// read reflects the write immediately instead of waiting out the response-cache
// TTL. May be nil (e.g. in tests or when no cache is wired).
type CacheInvalidator interface {
	Invalidate(tenantID string)
}

// aggregateResponse shapes aggregate rows (alias→value maps from RowsToMaps) into
// the response body (G3). Without group_by: a single object carrying only the
// requested functions — {count, sum:{field:v}, avg:{…}, min:{…}, max:{…}}. With
// group_by: {"groups": [{<group fields>, count, sum:{…}, …}, …]}.
func aggregateResponse(aq *query.AggregateQuery, recs []map[string]any) map[string]any {
	metrics := func(rec map[string]any) map[string]any {
		out := map[string]any{}
		if aq.HasCount() {
			out["count"] = rec[query.CountAlias]
		}
		for _, m := range aq.Metrics() {
			sub, _ := out[m.Fn].(map[string]any)
			if sub == nil {
				sub = map[string]any{}
				out[m.Fn] = sub
			}
			sub[m.Field] = rec[m.Alias]
		}
		return out
	}

	if len(aq.GroupBy()) == 0 {
		rec := map[string]any{}
		if len(recs) > 0 {
			rec = recs[0]
		}
		return metrics(rec)
	}

	groups := make([]map[string]any, 0, len(recs))
	for _, rec := range recs {
		g := metrics(rec)
		for _, gb := range aq.GroupBy() {
			g[gb] = rec[gb]
		}
		groups = append(groups, g)
	}
	return map[string]any{"groups": groups}
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
// response-cache invalidator called after a successful PUT/PATCH. hub (nil-able)
// is the SSE pub/sub hub (S45): when nil, /api/{resource}/events returns 503 and
// post-commit publishes are no-ops.
func BuildRouter(s *schema.APISchema, tdb *db.TenantDB, hr *extensions.HookRunner, inv CacheInvalidator, hub *events.Hub) *chi.Mux {
	names := make([]string, 0, len(s.Resources))
	for name := range s.Resources {
		names = append(names, name)
	}
	sort.Strings(names)

	r := chi.NewMux()

	// RELATIONS-V1: policy for evaluating read access to relation TARGET resources
	// at request time. Built once here (boot), so the per-request closure is just
	// a bound Evaluate call. nil-safe: a schema with no relations never uses it.
	policy := policyFromSchema(s)

	for _, resName := range names {
		name := resName
		res := s.Resources[resName]
		// Declarative validation is compiled ONCE here — BuildRouter runs at
		// schema load/reload, never per request. The handlers below only execute
		// the precompiled closures (S44 requirement #1).
		rv := schema.CompileRules(&res)

		// Opt-in outbox emission (CRUD-EMIT-V1), decided ONCE at boot. A resource
		// that declares no events leaves these false, so its write handlers take
		// the unchanged ExecRowsTenant/ExecTenant path — zero added overhead.
		emitCreate := res.EmitsOn("create")
		emitUpdate := res.EmitsOn("update")
		emitDelete := res.EmitsOn("delete")

		// Quoted table identifier, computed ONCE at boot (BUG1 — consistent
		// quoting). The search_path write/get paths (create/get/delete/update/
		// subresource) interpolate this so a hyphenated resource name resolves;
		// the list path keeps passing the bare `name` to QueryDirect, which
		// qualifies it. Zero per-request cost (closures capture tbl).
		tbl := pgx.Identifier{name}.Sanitize()

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

			// RELATIONS-V1: opt-in nested embedding. WITHOUT ?include= this branch
			// is skipped entirely and the SQL below is byte-identical to before (the
			// no-regression gate). WITH it, one json_agg+LATERAL query serves the
			// whole nested tree in a single round-trip (no N+1), RBAC-compiled.
			if include := req.URL.Query().Get("include"); query.HasInclude(include) {
				baseSelect, _, baseArgs, _ := qb.SQL()
				of, od := qb.EffectiveOrder()
				incSQL, incArgs, ierr := query.BuildListInclude(name, include, baseSelect, baseArgs, of, od, s, schema.DefaultMaxIncludeDepth, makeRelationRBAC(policy, req))
				if ierr != nil {
					writeJSONErr(w, ierr.Status, ierr.Msg)
					return
				}
				data, n, qerr := tdb.IncludeListJSON(req.Context(), tc.PGSchema, incSQL, incArgs...)
				if qerr != nil {
					markSpan(req, "query")
					writeDBErr(w, req, qerr)
					return
				}
				markSpan(req, "query")
				writeIncludeList(w, data, qb.Page(), qb.PerPage(), int(n))
				markSpan(req, "serialize")
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

			meta := map[string]any{
				"page":     qb.Page(),
				"per_page": qb.PerPage(),
				"has_next": hasNext,
				"has_prev": hasPrev,
			}
			// Opt-in total (G3): ?count=true runs a COUNT(*) over the SAME filtered,
			// RBAC-scoped set and adds total + total_pages. Off by default, so the
			// plain list path is byte-identical (and free of the extra COUNT) — the
			// no-regression gate. Closes the REST↔GraphQL asymmetry (GraphQL already
			// returns total).
			if _, want := req.URL.Query()["count"]; want {
				_, countQ, _, countArgs := qb.SQL()
				if total, cerr := tdb.QueryScalarDirect(req.Context(), tc.PGSchema, name, countQ, countArgs...); cerr == nil {
					per := int64(qb.PerPage())
					tp := (total + per - 1) / per
					if tp == 0 {
						tp = 1
					}
					meta["total"] = total
					meta["total_pages"] = tp
				}
			}

			w.Header().Set("Content-Type", "application/json")
			pkghandlers.WriteJSON(w, map[string]any{ //nolint:errcheck
				"data": data,
				"meta": meta,
			})
			markSpan(req, "serialize")
		}))

		// --- Aggregate (G3) ---
		// GET /api/{resource}/aggregate?count&sum=f&avg=f&min=f&max=f&group_by=f&filter[...]
		// A NEW read path (the list/CRUD SQL is untouched). Scoped EXACTLY like a
		// list read: the same RBAC row condition is injected into the WHERE, the
		// same field allowlist applies (a field the role may not read cannot be
		// aggregated → 403, no leak via aggregates), the same filters, the same
		// tenant. Functions come from a fixed allowlist; field/group_by names are
		// validated against the schema (no arbitrary SQL).
		r.Get("/api/"+name+"/aggregate", pkghandlers.CachedGet(func(w http.ResponseWriter, req *http.Request) {
			tc := tenant.MustFromCtx(req.Context())
			evalResult := rbac.EvalResultFromCtx(req.Context())

			var cond *rbac.WhereCondition
			var allowedFields []string
			if evalResult != nil {
				cond = evalResult.Condition
				allowedFields = evalResult.AllowedFields
			}

			aq, err := query.BuildAggregate(name, &res, req.URL.Query(), cond, allowedFields)
			if err != nil {
				if errors.Is(err, query.ErrAggForbiddenField) {
					writeJSONErr(w, http.StatusForbidden, err.Error())
					return
				}
				writeJSONErr(w, http.StatusBadRequest, err.Error())
				return
			}

			aggSQL, aggArgs := aq.SQL()
			rows, err := tdb.QueryDirect(req.Context(), tc.PGSchema, name, aggSQL, aggArgs...)
			if err != nil {
				markSpan(req, "query")
				writeDBErr(w, req, err)
				return
			}
			defer rows.Close()
			recs, err := pkghandlers.RowsToMaps(rows)
			if err != nil {
				writeDBErr(w, req, err)
				return
			}
			markSpan(req, "query")

			w.Header().Set("Content-Type", "application/json")
			pkghandlers.WriteJSON(w, aggregateResponse(aq, recs)) //nolint:errcheck
			markSpan(req, "serialize")
		}))

		// --- Create ---
		r.Post("/api/"+name, func(w http.ResponseWriter, req *http.Request) {
			tc := tenant.MustFromCtx(req.Context())
			// Row-level condition + field allowlist for this role (if any) — applied
			// to the create below (FASE3-SEC Hallazgo 2), exactly as read/update/delete
			// already consume it. nil for an unrestricted role (rrhh-admin) or a test
			// without the RBAC middleware → enforcement is a no-op.
			evalResult := rbac.EvalResultFromCtx(req.Context())
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

			// Schema defaults (SCHEMA-CLOSE-V1): fill omitted fields that declare a
			// default BEFORE validation, so a required field with a default is
			// satisfied by it. No-op (one length check) for a resource with no
			// defaults — the create gate.
			rv.ApplyDefaults(body)

			// Declarative validation: BEFORE the before_create hook and the
			// INSERT. Create requires every required non-auto field (S44 #2).
			if verrs := rv.ValidateWrite(body, true); len(verrs) > 0 {
				markSpan(req, "validate")
				writeValidationErrs(w, verrs)
				return
			}
			// State-machine (G5): a row may only be CREATED in an initial state.
			if verrs := rv.ValidateInitialStates(body); len(verrs) > 0 {
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

			// HALLAZGO-2 / FASE3-SEC: enforce the role's row-level condition + field
			// allowlist on the create — the SAME enforcement read/update/delete apply,
			// shared with the GraphQL create via EnforceCreateRBAC so the two surfaces
			// are identical. A row-scoped role's record is forced to ITS OWN id; a body
			// claiming another principal's id is rejected (403). Runs on the post-hook
			// body so the inserted row is always consistent with the policy.
			if status, msg := EnforceCreateRBAC(body, evalResult); status != 0 {
				if t := observability.SpanTrackerFromCtx(req.Context()); t != nil {
					t.RecordError(msg)
				}
				writeJSONErr(w, status, msg)
				return
			}

			// NOTE: column identifiers are quoted by BuildInsertArgs so a client
			// key cannot break out of the identifier position (SQL injection). We do
			// NOT whitelist keys against res.Fields here: the schema can evolve at
			// runtime (a migration adds a column without rebuilding this router), so
			// the DB is the source of truth — an unknown column errors at the DB and
			// WriteDBError maps that 42703 to a 422 unknown_field (S44 shape), never
			// a 500. The row-level RBAC mass-assignment that used to live here (an
			// owner-scoped role POSTing another principal's id) is now closed by
			// EnforceCreateRBAC above; a role with no condition/allowlist still writes
			// the body as-is (the DB-as-source-of-truth design for runtime columns).
			// Shared create core (RunInsert): builds the INSERT and, when the
			// resource opted into events:["create"], enqueues the {resource}.created
			// event in the SAME tx. The GraphQL create mutation calls the same
			// RunInsert, so REST and GraphQL create emit identically; no opt-in →
			// plain ExecRowsTenant, zero added overhead.
			result, err := RunInsert(req.Context(), tdb, tbl, name, tc.ID, tc.PGSchema, body, emitCreate)
			if err != nil {
				// A unique-constraint collision (field unique:true OR a unique
				// index) is an EXPECTED conflict, not a server error — map it to
				// 409, same as the UPDATE path (G6). Only the error branch runs;
				// the success path is byte-identical.
				if field, ok := db.UniqueViolationField(err); ok {
					writeJSONErr(w, http.StatusConflict, fmt.Sprintf("field %q: value already exists", field))
					return
				}
				writeDBErr(w, req, err)
				return
			}
			markSpan(req, "insert")

			// SSE broadcast (S45): same post-commit point as the after_* webhook
			// dispatch below, but unconditional (subscriptions are not gated on a
			// hook being configured). Non-blocking; no-op with zero subscribers.
			if len(result) > 0 {
				publishEvent(hub, tc.ID, name, "create", result[0], "")
			}

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
			q := fmt.Sprintf("SELECT * FROM %s WHERE id = $1", tbl)
			args := []any{id}
			if evalResult != nil {
				var ok bool
				if q, args, ok = applyRowCondition(q, args, evalResult.Condition); !ok {
					writeDBErr(w, req, fmt.Errorf("invalid rbac condition field"))
					return
				}
			}

			// RELATIONS-V1: get-by-id with embeds. Opt-in; without ?include= the
			// path below is unchanged. The base select (q/args, row-cond applied)
			// is wrapped so the embed cannot resurrect a row the base WHERE excluded.
			if include := req.URL.Query().Get("include"); query.HasInclude(include) {
				incSQL, incArgs, ierr := query.BuildGetInclude(name, include, q, args, s, schema.DefaultMaxIncludeDepth, makeRelationRBAC(policy, req))
				if ierr != nil {
					writeJSONErr(w, ierr.Status, ierr.Msg)
					return
				}
				data, found, qerr := tdb.IncludeOneJSON(req.Context(), tc.PGSchema, incSQL, incArgs...)
				if qerr != nil {
					writeDBErr(w, req, qerr)
					return
				}
				markSpan(req, "query")
				if !found {
					writeJSONErr(w, http.StatusNotFound, "not found")
					return
				}
				w.Header().Set("Content-Type", "application/json")
				w.Write(data) //nolint:errcheck
				markSpan(req, "serialize")
				return
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
			var cond *rbac.WhereCondition
			if evalResult != nil {
				cond = evalResult.Condition
			}
			// Shared delete core (RunDelete): runs the DELETE and, when the resource
			// opted into events:["delete"], enqueues the {resource}.deleted event in
			// the SAME tx (only if a row was actually deleted). The GraphQL delete
			// mutation calls the same RunDelete, so REST and GraphQL delete emit
			// identically; no opt-in → plain ExecTenant, zero added overhead.
			affected, err := RunDelete(req.Context(), tdb, tbl, name, tc.ID, tc.PGSchema, id, cond, emitDelete)
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
			// SSE broadcast (S45). The deleted row is gone (no RETURNING on
			// purpose) so the event carries the id with a null record.
			publishEvent(hub, tc.ID, name, "delete", nil, id)
			w.WriteHeader(http.StatusNoContent)
		})

		// --- Events: GET /api/{resource}/events — SSE change stream (S45) ---
		// The static "events" segment wins over the {id} wildcard in chi, and it
		// is deliberately NOT wrapped in CachedGet (a stream is never cacheable).
		// JWT + RBAC run in the middleware chain exactly like the list endpoint
		// (read permission on the resource = permission to subscribe).
		r.Get("/api/"+name+"/events", sseHandler(name, hub))

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
				sets, status, msg := CollectUpdate(&res, body, put, writable)
				if status != 0 {
					markSpan(req, "validate")
					writeJSONErr(w, status, msg)
					return
				}

				// Confirm the row exists AND passes the row-level RBAC condition.
				// A non-owned row yields zero rows → 404 (never 403): this matches the
				// GET-by-id/DELETE pattern and the S33/S34 BOLA fixes that deliberately
				// avoid revealing the existence of another principal's row.
				selQ := fmt.Sprintf("SELECT 1 FROM %s WHERE id = $1", tbl)
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

				// Build + run the UPDATE via the shared core (SET clauses, auto
				// updated_at, row-level RBAC condition, same-tx event emission).
				var cond *rbac.WhereCondition
				if evalResult != nil {
					cond = evalResult.Condition
				}
				result, err := RunUpdate(req.Context(), tdb, &res, tbl, name, tc.ID, tc.PGSchema, id, sets, cond, emitUpdate)
				if err != nil {
					if err == ErrNoWritableUpdate {
						writeJSONErr(w, http.StatusUnprocessableEntity, "no writable fields in request")
						return
					}
					if field, ok := db.UniqueViolationField(err); ok {
						writeJSONErr(w, http.StatusConflict, fmt.Sprintf("field %q: value already exists", field))
						return
					}
					writeDBErr(w, req, err)
					return
				}
				markSpan(req, "update")
				if len(result) == 0 {
					// Zero rows: either the row vanished after the existence check (a
					// race → 404) or a state-machine transition guard rejected the move
					// (→ 422 "invalid transition"). ExplainTransitionFailure reads the
					// current state ONLY here, on the error path, to say which — and is a
					// plain 404 (no read) for a resource without a state machine.
					status, msg := ExplainTransitionFailure(req.Context(), tdb, tc.PGSchema, tbl, id, &res, sets)
					if status == http.StatusUnprocessableEntity {
						if t := observability.SpanTrackerFromCtx(req.Context()); t != nil {
							t.RecordError(msg)
						}
					}
					writeJSONErr(w, status, msg)
					return
				}
				record := result[0]

				// SSE broadcast (S45): post-commit, with the UNfiltered record —
				// each subscriber gets its own RBAC field filtering at delivery.
				publishEvent(hub, tc.ID, name, "update", record, "")

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
			// The FK targets the referenced column (MIG-F1-S5: `references`), defaulting
			// to the target's id — so the subresource JOIN follows the FK faithfully.
			refCol := fd.References
			if refCol == "" {
				refCol = "id"
			}

			r.Get("/api/"+name+"/{id}/"+relRoute, pkghandlers.CachedGet(func(w http.ResponseWriter, req *http.Request) {
				tc := tenant.MustFromCtx(req.Context())
				parentID := chi.URLParam(req, "id")
				if _, err := uuid.Parse(parentID); err != nil {
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusBadRequest)
					json.NewEncoder(w).Encode(map[string]string{"error": "invalid id format"})
					return
				}

				// SEC-AUDIT-V1 Hallazgo 2: enforce the role's RBAC on the RELATED
				// resource — not just the parent's `read` (which the route middleware
				// already checked). The route's resource is the PARENT, so without this
				// the subroute would return the related row with NO row condition and NO
				// field allowlist, leaking rows/fields the role could not see via
				// GET /api/<related>. Reuse the SAME evaluator the embeds use.
				allowed, allowedFields, cond := makeRelationRBAC(policy, req)(relResource)
				if !allowed {
					writeJSONErr(w, http.StatusForbidden, "forbidden")
					return
				}

				q := fmt.Sprintf(
					"SELECT r.* FROM %s r JOIN %s src ON src.%s = r.%s WHERE src.id = $1",
					pgx.Identifier{relResource}.Sanitize(), tbl, pgx.Identifier{fn}.Sanitize(), pgx.Identifier{refCol}.Sanitize(),
				)
				args := []any{parentID}
				// The related resource's row condition is ANDed in, qualified by the
				// `r` alias so it scopes the RELATED rows (not the parent join).
				q, args, cerr := query.AppendAliasedRowCondition(q, args, "r", cond)
				if cerr != nil {
					capture500(req, cerr)
					writeJSONErr(w, http.StatusInternalServerError, "internal error")
					return
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
				if len(result) == 0 {
					// Not found OR hidden by the related resource's row condition — a
					// 404 either way (never reveal a row the role may not see).
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusNotFound)
					json.NewEncoder(w).Encode(map[string]string{"error": "not found"})
					return
				}
				rec := result[0]
				// Apply the related resource's field allowlist (drop fields the role may
				// not read), exactly as the list/embed paths do.
				if len(allowedFields) > 0 {
					rec = pkghandlers.FilterFields(rec, allowedFields)
				}
				w.Header().Set("Content-Type", "application/json")
				pkghandlers.WriteJSON(w, rec) //nolint:errcheck
			}))
		}
	}

	// Atomic multi-resource transactions (G4): POST /api/transaction. A NEW path off
	// the per-resource handlers above — those are untouched, so the single-op
	// create/update/delete gate is preserved. The batch reuses the SAME RBAC (G2),
	// validation, before-hook and outbox cores, executed on ONE transaction. The
	// before-hook result is mapped here (where hr lives) so transaction.go stays
	// decoupled from the HookRunner's result type.
	hookEval := func(ctx context.Context, hook *schema.HookConfig, body map[string]any) (map[string]any, int, string) {
		hookRes, hookErr := hr.RunBeforeHook(ctx, hook, body, nil)
		if hookErr != nil {
			return nil, http.StatusInternalServerError, "internal error"
		}
		if !hookRes.Proceed {
			return nil, http.StatusUnprocessableEntity, hookRes.Error
		}
		return hookRes.Data, 0, ""
	}
	registerTransactionRoute(r, s, tdb, policy, inv, hookEval)

	return r
}

// sseHeartbeatInterval is the comment-ping cadence that keeps intermediary
// proxies from reaping an idle SSE connection.
const sseHeartbeatInterval = 25 * time.Second

// publishEvent broadcasts a post-commit change to the tenant's SSE subscribers.
// nil hub (tests, callers without SSE wiring) is a no-op. Publish never blocks
// the request path: with zero subscribers it is one RLock + two map lookups,
// and per-subscriber sends are non-blocking (slow subscribers are closed).
func publishEvent(hub *events.Hub, tenantID, resource, typ string, record map[string]any, fallbackID string) {
	if hub == nil {
		return
	}
	id := fallbackID
	if record != nil {
		if s, ok := record["id"].(string); ok {
			id = s
		}
	}
	hub.Publish(tenantID, events.Event{Type: typ, Resource: resource, ID: id, Record: record})
}

// sseHandler returns the GET /api/{resource}/events handler: a Server-Sent
// Events stream of the resource's post-commit changes for the request's tenant.
//
//	event: create|update|delete
//	data: {"resource":"tasks","id":"...","record":{...}}   (record null on delete)
//
// Per-subscriber RBAC is captured at subscribe time and applied at delivery:
// the field allowlist (same FilterFields as GET — a role never sees a field in
// an event it cannot see in a GET) and the row-level eq condition (same row
// scoping as the list endpoint). Delivery is at-most-once, no replay /
// Last-Event-ID (documented S45 limitation); slow clients are closed with a
// final error event rather than silently gapped (ADR-015).
func sseHandler(name string, hub *events.Hub) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		if hub == nil {
			writeJSONErr(w, http.StatusServiceUnavailable, "events not enabled")
			return
		}
		tc := tenant.MustFromCtx(req.Context())
		evalResult := rbac.EvalResultFromCtx(req.Context())
		var allowed []string
		condField := ""
		var condValue any
		if evalResult != nil {
			allowed = evalResult.AllowedFields
			if evalResult.Condition != nil {
				condField = evalResult.Condition.Field
				condValue = evalResult.Condition.Value
			}
		}

		sub, err := hub.Subscribe(tc.ID, name, allowed, condField, condValue)
		if err != nil {
			// Per-tenant cap (APPITOOLS_MAX_SSE_PER_TENANT) reached.
			w.Header().Set("Retry-After", "10")
			writeJSONErr(w, http.StatusTooManyRequests, "subscriber limit reached")
			return
		}
		defer hub.Unsubscribe(sub)

		// An SSE response outlives the server-wide read/write timeouts
		// (cmd_serve sets WriteTimeout=30s); clear both for THIS connection
		// only. Errors are non-fatal (recorders/H2 may not support deadlines).
		rc := http.NewResponseController(w)
		rc.SetWriteDeadline(time.Time{}) //nolint:errcheck
		rc.SetReadDeadline(time.Time{})  //nolint:errcheck

		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.Header().Set("X-Accel-Buffering", "no") // nginx: do not buffer the stream
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, ": connected\n\n")
		if err := rc.Flush(); err != nil {
			return // writer does not support streaming
		}

		heartbeat := time.NewTicker(sseHeartbeatInterval)
		defer heartbeat.Stop()

		for {
			select {
			case <-req.Context().Done():
				// Client went away (or server draining): Unsubscribe via defer.
				return
			case <-sub.Slow:
				// Slow-client policy: close with an explicit error event — never
				// silently gap the stream (the client reconnects and resyncs).
				fmt.Fprint(w, "event: error\ndata: {\"error\":\"subscriber too slow, closing\"}\n\n")
				rc.Flush() //nolint:errcheck
				return
			case <-heartbeat.C:
				fmt.Fprint(w, ": ping\n\n")
				if err := rc.Flush(); err != nil {
					return
				}
			case ev := <-sub.C:
				record := ev.Record
				if record != nil && len(sub.AllowedFields) > 0 {
					record = pkghandlers.FilterFields(record, sub.AllowedFields)
				}
				payload, merr := json.Marshal(map[string]any{"resource": ev.Resource, "id": ev.ID, "record": record})
				if merr != nil {
					continue
				}
				fmt.Fprintf(w, "event: %s\ndata: %s\n\n", ev.Type, payload)
				if err := rc.Flush(); err != nil {
					return
				}
			}
		}
	}
}

// ErrNoWritableUpdate means an update resolved to zero writable columns (e.g.
// the role's allowlist dropped every field in the body). Callers map it to 422.
var ErrNoWritableUpdate = fmt.Errorf("no writable fields in request")

// EnforceCreateRBAC applies a role's field allowlist and row-level condition to a
// CREATE body, closing the mass-assignment gap that previously existed because the
// create path — unlike read/update/delete — applied NEITHER (FASE3-SEC, Hallazgo 2).
// It mutates body in place and is the ONE enforcement shared by the REST POST handler
// and the GraphQL create mutation, so both surfaces behave identically. Call it on the
// post-hook body, just before RunInsert.
//
//   - Field allowlist: when the role restricts writable fields, any body key outside
//     the allowlist is dropped silently (the same contract CollectUpdate uses for
//     update — drop, never error), EXCEPT the row-condition field, which is
//     server-forced below and therefore implicitly allowed.
//   - Row-level condition: a row-scoped role (e.g. user_id = $user_id) must create
//     rows attributed to ITSELF. The condition field is FORCED to the principal's
//     resolved value; if the body supplies a DIFFERENT non-null value, the create is
//     REJECTED with 403 — a client can never create a row owned by another principal.
//
// Returns (0,"") to proceed, or (403, msg) to reject. ev may be nil (no policy result
// — e.g. a test without the RBAC middleware, or the library Ctx path), and a role with
// neither an allowlist nor a condition (e.g. rrhh-admin) is a cheap no-op, so the
// unrestricted create path keeps behaving exactly as before (the GATE-WRITE case).
func EnforceCreateRBAC(body map[string]any, ev *rbac.EvalResult) (int, string) {
	if ev == nil {
		return 0, ""
	}
	condField := ""
	if ev.Condition != nil {
		condField = ev.Condition.Field
	}
	if len(ev.AllowedFields) > 0 {
		allow := make(map[string]struct{}, len(ev.AllowedFields))
		for _, f := range ev.AllowedFields {
			allow[f] = struct{}{}
		}
		for k := range body {
			if k == condField {
				continue // server-forced below; never client-droppable
			}
			if _, ok := allow[k]; !ok {
				delete(body, k)
			}
		}
	}
	if ev.Condition != nil {
		if cur, present := body[condField]; present && cur != nil {
			if fmt.Sprintf("%v", cur) != ev.Condition.Value {
				return http.StatusForbidden, fmt.Sprintf("field %q must match the authenticated principal", condField)
			}
		}
		body[condField] = ev.Condition.Value
	}
	return 0, ""
}

// RunInsert builds and executes the INSERT … RETURNING * for an already-validated
// body (after any before_create hook AND EnforceCreateRBAC), emitting the outbox event
// in the SAME transaction when emitCreate is set — exactly as RunUpdate does for
// updates. It is the ONE create core shared by the REST POST handler and the GraphQL
// create mutation, so both surfaces enqueue an IDENTICAL {resource}.created event (same
// topic, same lean {id,tenant_id,resource,action} payload) atomically with the
// insert. A resource that did not opt into events:["create"] takes the plain
// ExecRowsTenant path — no emission, zero added overhead (the no-opt-in gate).
func RunInsert(ctx context.Context, tdb *db.TenantDB, tbl, name, tenantID, pgSchema string, body map[string]any, emitCreate bool) ([]map[string]any, error) {
	cols, placeholders, args := pkghandlers.BuildInsertArgs(body)
	q := fmt.Sprintf("INSERT INTO %s (%s) VALUES (%s) RETURNING *", tbl, cols, placeholders)
	if emitCreate {
		// Same-tx emission: a failed insert rolls back the event (atomic).
		return tdb.ExecRowsTenantEmit(ctx, pgSchema, q,
			func(ectx context.Context, tx pgx.Tx, rows []map[string]any) error {
				if len(rows) == 0 {
					return nil
				}
				return enqueueCRUDEvent(ectx, tx, tenantID, name, "create", rows[0]["id"])
			}, args...)
	}
	return tdb.ExecRowsTenant(ctx, pgSchema, q, args...)
}

// RunUpdate builds and executes the partial/full UPDATE for the already-validated
// column set `sets` (from CollectUpdate, after any before_update hook adjustment),
// forcing updated_at=NOW() when the resource declares an auto updated_at column,
// appending the role's row-level RBAC condition (so a restricted role cannot
// touch a row it cannot see — BOLA defence), and emitting the outbox event in the
// SAME transaction when emitUpdate is set. It returns the RETURNING * rows (empty
// when no row matched → 404 / row-condition exclusion). This is the ONE update
// core shared by the REST PATCH/PUT handler and the GraphQL update mutation, so
// both surfaces enforce identical RBAC, identifier safety, and event emission.
func RunUpdate(ctx context.Context, tdb *db.TenantDB, res *schema.ResourceSchema,
	tbl, name, tenantID, pgSchema, id string, sets map[string]any,
	cond *rbac.WhereCondition, emitUpdate bool) ([]map[string]any, error) {
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
		return nil, ErrNoWritableUpdate
	}

	q := fmt.Sprintf("UPDATE %s SET %s WHERE id = $%d", tbl, strings.Join(setClauses, ", "), argIdx)
	args = append(args, id)
	q, args, err := query.AppendRowCondition(q, args, cond)
	if err != nil {
		return nil, err
	}
	// State-machine transition guard (G5): a row whose current state does not allow
	// the requested transition matches zero rows → no write (race-safe). No-op for a
	// resource without a state machine on an updated field.
	q, args, err = appendStateTransitionGuard(q, args, res, sets)
	if err != nil {
		return nil, err
	}
	q += " RETURNING *"

	if emitUpdate {
		return tdb.ExecRowsTenantEmit(ctx, pgSchema, q,
			func(ectx context.Context, tx pgx.Tx, rows []map[string]any) error {
				if len(rows) == 0 {
					return nil
				}
				return enqueueCRUDEvent(ectx, tx, tenantID, name, "update", rows[0]["id"])
			}, args...)
	}
	return tdb.ExecRowsTenant(ctx, pgSchema, q, args...)
}

// RunDelete executes the DELETE for the row id (with the role's row-level RBAC
// condition appended — BOLA defence, so a restricted role cannot delete a row it
// cannot see), emitting the outbox event in the SAME transaction when emitDelete is
// set — exactly as RunInsert/RunUpdate do for create/update. It returns the
// affected-row count (0 → 404 / row-condition exclusion → no event). This is the
// ONE delete core shared by the REST DELETE handler and the GraphQL delete
// mutation, so both surfaces enqueue an IDENTICAL {resource}.deleted event (same
// topic, same lean {id,tenant_id,resource,action} payload) atomically with the
// delete. A resource that did not opt into events:["delete"] takes the plain
// ExecTenant path — no emission, zero added overhead (the no-opt-in gate).
func RunDelete(ctx context.Context, tdb *db.TenantDB, tbl, name, tenantID, pgSchema, id string, cond *rbac.WhereCondition, emitDelete bool) (int64, error) {
	q := fmt.Sprintf("DELETE FROM %s WHERE id = $1", tbl)
	args := []any{id}
	q, args, err := query.AppendRowCondition(q, args, cond)
	if err != nil {
		return 0, err
	}
	if emitDelete {
		// Same-tx emission, but only when a row was actually deleted (affected > 0).
		return tdb.ExecTenantEmit(ctx, pgSchema, q,
			func(ectx context.Context, tx pgx.Tx, affected int64) error {
				if affected == 0 {
					return nil
				}
				return enqueueCRUDEvent(ectx, tx, tenantID, name, "delete", id)
			}, args...)
	}
	return tdb.ExecTenant(ctx, pgSchema, q, args...)
}

// appendStateTransitionGuard appends, for each state-machine field being SET, a
// race-safe transition guard to an UPDATE's WHERE: the row's CURRENT state must
// either already equal the new value (a no-op self-set, so a full-object PUT/PATCH
// that re-sends the unchanged state still succeeds) OR be a state from which the new
// state is a DECLARED transition. A row whose current state allows neither matches
// ZERO rows — so an invalid transition (or a terminal state) atomically writes
// nothing, with no read-modify-write race. It appends NO clause for an update that
// touches no state-machine field, so a resource without one is byte-identical (the
// gate). The new value and the origin set are always bound parameters.
func appendStateTransitionGuard(sql string, args []any, res *schema.ResourceSchema, sets map[string]any) (string, []any, error) {
	fields := make([]string, 0, len(sets))
	for f := range sets {
		fields = append(fields, f)
	}
	sort.Strings(fields) // deterministic SQL
	for _, f := range fields {
		fd, ok := res.Fields[f]
		if !ok || fd.StateMachine == nil {
			continue
		}
		newState, ok := sets[f].(string)
		if !ok {
			return sql, args, fmt.Errorf("state field %q must be a string", f)
		}
		origins := fd.StateMachine.OriginsOf(newState)
		if origins == nil {
			origins = []string{} // bind '{}' (empty text[]), never NULL
		}
		col := pgx.Identifier{f}.Sanitize()
		sql += fmt.Sprintf(" AND (%s = $%d OR %s = ANY($%d))", col, len(args)+1, col, len(args)+2)
		args = append(args, newState, origins)
	}
	return sql, args, nil
}

// stateFieldsUpdated returns the state-machine fields present in sets (the columns
// whose transition was guarded), sorted — used only on the error path to explain a
// rejected update. Empty when the update touched no state field.
func stateFieldsUpdated(res *schema.ResourceSchema, sets map[string]any) []string {
	var out []string
	for f := range sets {
		if fd, ok := res.Fields[f]; ok && fd.StateMachine != nil {
			out = append(out, f)
		}
	}
	sort.Strings(out)
	return out
}

// ExplainTransitionFailure runs on the 0-rows update path when a state field was set
// (the row existed + passed RBAC, per the prior existence check): it reads the row's
// CURRENT state and returns a precise 422 "invalid transition from X to Y", or a 404
// if the row vanished (a race). It is an ERROR-PATH read only — never the hot path.
func ExplainTransitionFailure(ctx context.Context, tdb *db.TenantDB, pgSchema, tbl, id string, res *schema.ResourceSchema, sets map[string]any) (int, string) {
	stateFields := stateFieldsUpdated(res, sets)
	if len(stateFields) == 0 {
		return http.StatusNotFound, "not found"
	}
	cols := make([]string, len(stateFields))
	for i, f := range stateFields {
		cols[i] = pgx.Identifier{f}.Sanitize()
	}
	q := fmt.Sprintf("SELECT %s FROM %s WHERE id = $1", strings.Join(cols, ", "), tbl)
	rows, err := tdb.QueryTenant(ctx, pgSchema, q, id)
	if err != nil {
		return http.StatusNotFound, "not found"
	}
	defer rows.Close()
	if !rows.Next() {
		return http.StatusNotFound, "not found" // row vanished (race)
	}
	vals, err := rows.Values()
	if err != nil || len(vals) != len(stateFields) {
		return http.StatusConflict, "the resource changed during the update; retry"
	}
	for i, f := range stateFields {
		cur, _ := vals[i].(string)
		want, _ := sets[f].(string)
		if cur != want { // this field's transition is the one that failed
			return http.StatusUnprocessableEntity,
				fmt.Sprintf("invalid transition for %q: from %q to %q is not allowed", f, cur, want)
		}
	}
	// Every state field already equalled its target — the 0 rows was a concurrent
	// change after the existence check (race), not a bad transition.
	return http.StatusConflict, "the resource changed during the update; retry"
}

// CollectUpdate validates body against the resource schema for an update and
// returns the columns→values to write (excluding the auto updated_at, which
// RunUpdate forces to NOW()). On a validation failure it returns a non-zero HTTP
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
func CollectUpdate(res *schema.ResourceSchema, body map[string]any, put bool, writable func(string) bool) (map[string]any, int, string) {
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
