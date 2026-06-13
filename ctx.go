package appitools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/miguelangel/appitools/pkg/auth"
	pkghandlers "github.com/miguelangel/appitools/pkg/handlers"
	"github.com/miguelangel/appitools/pkg/outbox"
	"github.com/miguelangel/appitools/pkg/query"
	"github.com/miguelangel/appitools/pkg/rbac"
	"github.com/miguelangel/appitools/pkg/schema"
	"github.com/miguelangel/appitools/pkg/tenant"
)

// maxBodyBytes caps a custom handler's request body, matching the generated
// REST/GraphQL handlers' 1 MiB limit (OWASP API4).
const maxBodyBytes = 1 << 20

// Claims is the authenticated identity the middleware chain already resolved
// from the request's JWT — a Class-1 handler never re-parses or re-verifies it.
type Claims struct {
	UserID           string
	Role             string
	TenantID         string
	ExternalClientID string
}

// Allowlist is the field projection permitted for the caller's role on a
// resource. An empty Allowlist means no restriction (every column is visible).
type Allowlist []string

// QueryOpts narrows a Ctx.Query. Filters are equality predicates keyed by field
// name (validated against the resource schema, bound as parameters). Limit caps
// the row count (clamped to the engine's per_page maximum); OrderBy + Desc sort
// by a single field. The role's row-level RBAC condition is ALWAYS applied on
// top — QueryOpts cannot widen what the role may see.
type QueryOpts struct {
	Filters map[string]any
	Limit   int
	OrderBy string
	Desc    bool
}

// Ctx is the single argument to a Class-1 Handler (ADR-016 Decision 3). It
// carries the request context fully resolved: identity, tenant, and a pgx.Tx
// already scoped to the tenant search_path. The handler writes business logic,
// not infrastructure — it never re-authenticates, re-scopes the tenant, or
// touches the raw connection pool.
//
// EXPERIMENTAL surface — frozen at v1 (ADR-016 Decision 5).
type Ctx interface {
	// Identity — already verified by the middleware chain.
	Claims() Claims
	Tenant() string // tenant id, e.g. "acme" (from the Host subdomain)
	Role() string   // the JWT "role" claim
	// Allowlist returns the field projection the caller's role is granted on
	// resource, and whether the role may read it at all (false ⇒ denied).
	Allowlist(resource string) (Allowlist, bool)

	// Tx is the transaction opened by the middleware with the tenant
	// search_path already applied via set_config(...,true). Returning nil from
	// the Handler commits it; returning an error rolls it back.
	Tx() pgx.Tx
	// UnsafeTx returns the SAME transaction but signals to the reader (and to
	// `grep UnsafeTx`) that the RBAC-aware helpers are being bypassed. Tenant
	// isolation STILL holds — the search_path is the same. There is no API that
	// exposes the raw pool.
	UnsafeTx() pgx.Tx

	// RBAC-aware helpers — apply the role's row filter, validate against the
	// compiled schema rules, and project the permitted fields. Use by default.
	Query(resource string, opts QueryOpts) ([]map[string]any, error)
	Insert(resource string, data map[string]any) (map[string]any, error)
	Update(resource, id string, data map[string]any) (map[string]any, error)

	// Bind JSON-decodes the request body (1 MiB cap) into dst. BindResource
	// additionally validates the decoded body against the compiled schema rules
	// for resource (the same rule engine REST and GraphQL use).
	Bind(dst any) error
	BindResource(resource string, dst any) error

	// Enqueue writes an outbox job inside the current transaction (atomic with
	// the business write). A Handler error rolls back the enqueue too.
	Enqueue(topic string, payload any) (int64, error)

	// JSON buffers a success response flushed AFTER the transaction commits, so
	// a commit failure becomes a 500 rather than a false 200. Error buffers an
	// error response and returns a non-nil error so the Handler can
	// `return ctx.Error(...)`; the middleware rolls back and flushes it.
	JSON(status int, v any) error
	Error(status int, msg string, cause error) error

	Request() *http.Request
	Context() context.Context
}

// engineRefs is the read-only engine state shared by every requestCtx: the
// loaded schema, the validators compiled once at boot, and the RBAC policy.
// It is never mutated after New returns.
type engineRefs struct {
	schema     *schema.APISchema
	validators map[string]*schema.ResourceValidator
	policy     *rbac.Policy
}

// requestCtx is the per-request Ctx implementation. One is built by the custom
// route middleware after the standard chain (tenant → JWT → RBAC) has run.
type requestCtx struct {
	w   http.ResponseWriter
	r   *http.Request
	tx  pgx.Tx
	eng *engineRefs
	tc  *tenant.TenantCtx
	cl  *auth.Claims

	// Buffered response (flushed by the middleware around commit).
	status int
	body   []byte
	set    bool
}

// --- typed errors mapped to HTTP status by the middleware -------------------

// forbiddenError is returned by an RBAC-aware helper the role may not perform.
type forbiddenError struct{ msg string }

func (e *forbiddenError) Error() string { return e.msg }

// ValidationError carries the per-field declarative-validation failures, in the
// same shape the 422 REST/GraphQL responses use.
type ValidationError struct{ Fields []schema.FieldRuleError }

func (e *ValidationError) Error() string { return "validation_failed" }

// handledError means the Handler already buffered a response via Ctx.Error; the
// middleware just rolls back and flushes the buffer (no generic mapping).
type handledError struct {
	cause error
	msg   string
}

func (e *handledError) Error() string {
	if e.cause != nil {
		return e.msg + ": " + e.cause.Error()
	}
	return e.msg
}

// --- identity ---------------------------------------------------------------

func (c *requestCtx) Claims() Claims {
	if c.cl == nil {
		return Claims{}
	}
	return Claims{
		UserID:           c.cl.UserID,
		Role:             c.cl.Role,
		TenantID:         c.cl.TenantID,
		ExternalClientID: c.cl.ExternalClientID,
	}
}

func (c *requestCtx) Tenant() string { return c.tc.ID }

func (c *requestCtx) Role() string {
	if c.cl == nil {
		return ""
	}
	return c.cl.Role
}

func (c *requestCtx) Allowlist(resource string) (Allowlist, bool) {
	eval := c.eng.policy.Evaluate(c.evalCtx(), resource, "read")
	if !eval.Allowed {
		return nil, false
	}
	return Allowlist(eval.AllowedFields), true
}

func (c *requestCtx) evalCtx() rbac.EvalContext {
	if c.cl == nil {
		return rbac.EvalContext{}
	}
	return rbac.EvalContext{
		Role:             c.cl.Role,
		UserID:           c.cl.UserID,
		ExternalClientID: c.cl.ExternalClientID,
	}
}

// --- database ---------------------------------------------------------------

func (c *requestCtx) Tx() pgx.Tx       { return c.tx }
func (c *requestCtx) UnsafeTx() pgx.Tx { return c.tx }

func (c *requestCtx) Query(resource string, opts QueryOpts) ([]map[string]any, error) {
	res, ok := c.eng.schema.Resources[resource]
	if !ok {
		return nil, fmt.Errorf("appitools: unknown resource %q", resource)
	}
	eval := c.eng.policy.Evaluate(c.evalCtx(), resource, "read")
	if !eval.Allowed {
		return nil, &forbiddenError{"forbidden"}
	}

	// Reuse the engine's validated, injection-safe query builder: it checks
	// every filter field/op against the schema and emits bound parameters, and
	// it folds in the role's row-level RBAC condition exactly as the list path.
	params := url.Values{}
	for k, v := range opts.Filters {
		params.Set("filter["+k+"]", fmt.Sprint(v))
	}
	if opts.Limit > 0 {
		params.Set("per_page", strconv.Itoa(opts.Limit))
	}
	if opts.OrderBy != "" {
		params.Set("sort", opts.OrderBy)
		if opts.Desc {
			params.Set("order", "desc")
		} else {
			params.Set("order", "asc")
		}
	}

	qb, err := query.BuildQuery(resource, &res, params, eval.Condition)
	if err != nil {
		return nil, err
	}
	selectQ, _, selectArgs, _ := qb.SQL()

	rows, err := c.tx.Query(c.r.Context(), selectQ, selectArgs...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out, err := pkghandlers.RowsToMaps(rows)
	if err != nil {
		return nil, err
	}
	projectAll(out, eval.AllowedFields)
	return out, nil
}

func (c *requestCtx) Insert(resource string, data map[string]any) (map[string]any, error) {
	if _, ok := c.eng.schema.Resources[resource]; !ok {
		return nil, fmt.Errorf("appitools: unknown resource %q", resource)
	}
	eval := c.eng.policy.Evaluate(c.evalCtx(), resource, "create")
	if !eval.Allowed {
		return nil, &forbiddenError{"forbidden"}
	}
	if rv := c.eng.validators[resource]; rv != nil {
		if verrs := rv.ValidateWrite(data, true); len(verrs) > 0 {
			return nil, &ValidationError{Fields: verrs}
		}
	}
	// A field-restricted role may only write the fields it is allowed to read.
	data = applyWriteAllowlist(data, eval.AllowedFields)
	if len(data) == 0 {
		return nil, &ValidationError{Fields: []schema.FieldRuleError{{Rule: "empty", Message: "no writable fields"}}}
	}

	cols, ph, args := pkghandlers.BuildInsertArgs(data)
	q := fmt.Sprintf("INSERT INTO %s (%s) VALUES (%s) RETURNING *",
		pgx.Identifier{resource}.Sanitize(), cols, ph)
	row, err := c.queryOne(q, args)
	if err != nil {
		return nil, err
	}
	return pkghandlers.FilterFields(row, eval.AllowedFields), nil
}

func (c *requestCtx) Update(resource, id string, data map[string]any) (map[string]any, error) {
	if _, ok := c.eng.schema.Resources[resource]; !ok {
		return nil, fmt.Errorf("appitools: unknown resource %q", resource)
	}
	eval := c.eng.policy.Evaluate(c.evalCtx(), resource, "update")
	if !eval.Allowed {
		return nil, &forbiddenError{"forbidden"}
	}
	if rv := c.eng.validators[resource]; rv != nil {
		// Partial (PATCH) semantics: validate only the fields present.
		if verrs := rv.ValidateWrite(data, false); len(verrs) > 0 {
			return nil, &ValidationError{Fields: verrs}
		}
	}
	data = applyWriteAllowlist(data, eval.AllowedFields)
	if len(data) == 0 {
		return nil, &ValidationError{Fields: []schema.FieldRuleError{{Rule: "empty", Message: "no writable fields"}}}
	}

	keys := sortedKeys(data)
	args := []any{id} // $1 = id
	setParts := make([]string, len(keys))
	for i, k := range keys {
		args = append(args, data[k])
		setParts[i] = fmt.Sprintf("%s = $%d", pgx.Identifier{k}.Sanitize(), len(args))
	}
	q := fmt.Sprintf("UPDATE %s SET %s WHERE id = $1",
		pgx.Identifier{resource}.Sanitize(), strings.Join(setParts, ", "))
	// Append the role's row-level RBAC condition so a restricted role cannot
	// update a row it could not read (BOLA defence — same as the REST path).
	q, args, err := query.AppendRowCondition(q, args, eval.Condition)
	if err != nil {
		return nil, err
	}
	q += " RETURNING *"
	row, err := c.queryOne(q, args)
	if err != nil {
		return nil, err
	}
	if row == nil {
		return nil, nil // id not found, or excluded by the row condition
	}
	return pkghandlers.FilterFields(row, eval.AllowedFields), nil
}

// queryOne runs a RETURNING * write on the tenant transaction and returns the
// first row (nil if none). UUID columns are normalised to strings by RowsToMaps.
func (c *requestCtx) queryOne(q string, args []any) (map[string]any, error) {
	rows, err := c.tx.Query(c.r.Context(), q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out, err := pkghandlers.RowsToMaps(rows)
	if err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return nil, nil
	}
	return out[0], nil
}

// --- request binding --------------------------------------------------------

func (c *requestCtx) Bind(dst any) error {
	c.r.Body = http.MaxBytesReader(c.w, c.r.Body, maxBodyBytes)
	return json.NewDecoder(c.r.Body).Decode(dst)
}

func (c *requestCtx) BindResource(resource string, dst any) error {
	rv := c.eng.validators[resource]
	if rv == nil {
		return fmt.Errorf("appitools: unknown resource %q", resource)
	}
	c.r.Body = http.MaxBytesReader(c.w, c.r.Body, maxBodyBytes)
	b, err := io.ReadAll(c.r.Body)
	if err != nil {
		return err
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		return err
	}
	if verrs := rv.ValidateWrite(m, true); len(verrs) > 0 {
		return &ValidationError{Fields: verrs}
	}
	if dst != nil {
		return json.Unmarshal(b, dst)
	}
	return nil
}

// --- outbox -----------------------------------------------------------------

func (c *requestCtx) Enqueue(topic string, payload any) (int64, error) {
	return outbox.Enqueue(c.r.Context(), c.tx, c.tc.ID, topic, payload)
}

// --- response ---------------------------------------------------------------

func (c *requestCtx) JSON(status int, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	c.status, c.body, c.set = status, b, true
	return nil
}

func (c *requestCtx) Error(status int, msg string, cause error) error {
	b, _ := json.Marshal(map[string]string{"error": msg})
	c.status, c.body, c.set = status, b, true
	return &handledError{cause: cause, msg: msg}
}

func (c *requestCtx) Request() *http.Request   { return c.r }
func (c *requestCtx) Context() context.Context { return c.r.Context() }

// --- helpers ----------------------------------------------------------------

func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// applyWriteAllowlist drops any key not in allowed when the role is field
// restricted. An empty allowed list means no restriction (returns m unchanged).
func applyWriteAllowlist(m map[string]any, allowed []string) map[string]any {
	if len(allowed) == 0 {
		return m
	}
	set := make(map[string]struct{}, len(allowed))
	for _, f := range allowed {
		set[f] = struct{}{}
	}
	out := make(map[string]any, len(m))
	for k, v := range m {
		if _, ok := set[k]; ok {
			out[k] = v
		}
	}
	return out
}

func projectAll(rows []map[string]any, allowed []string) {
	if len(allowed) == 0 {
		return
	}
	for i := range rows {
		rows[i] = pkghandlers.FilterFields(rows[i], allowed)
	}
}

// asValidationError reports whether err carries field-level validation failures.
func asValidationError(err error) (*ValidationError, bool) {
	var ve *ValidationError
	if errors.As(err, &ve) {
		return ve, true
	}
	return nil, false
}
