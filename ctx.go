package appximo

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/appximo/appximo/pkg/auth"
	"github.com/appximo/appximo/pkg/codegen"
	"github.com/appximo/appximo/pkg/db"
	"github.com/appximo/appximo/pkg/files"
	pkghandlers "github.com/appximo/appximo/pkg/handlers"
	"github.com/appximo/appximo/pkg/outbox"
	"github.com/appximo/appximo/pkg/query"
	"github.com/appximo/appximo/pkg/rbac"
	"github.com/appximo/appximo/pkg/schema"
	"github.com/appximo/appximo/pkg/tenant"
	"github.com/appximo/appximo/pkg/userauth"
)

// maxBodyBytes caps a custom handler's request body, matching the generated
// REST/GraphQL handlers' 1 MiB limit (OWASP API4). Exported as MaxBodyBytes so a
// handler can size its own buffers to the engine's real limit instead of
// guessing (or re-implementing) it.
const maxBodyBytes = 1 << 20

// MaxBodyBytes is the request-body cap Ctx.Bind / Ctx.BindResource / Ctx.RawBody
// enforce on a custom route — the same 1 MiB the generated REST and GraphQL
// handlers use. A body over it fails with ErrBodyTooLarge (→ 413).
const MaxBodyBytes = maxBodyBytes

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
	// Get loads ONE row by id, with the role's row-level condition applied and
	// the role's field allowlist projected — the read counterpart of Update.
	//
	// It exists because `id` is NOT a filterable field: QueryOpts.Filters is
	// validated against the resource's DECLARED fields, and the implicit primary
	// key is not one of them, so `Query(r, QueryOpts{Filters: {"id": x}})` fails
	// with `unknown filter field: id`. That cost a real integration a debugging
	// round (docs/AUTHORING_JOURNEY.md 5-7), and the workaround people reach for —
	// UnsafeTx plus a hand-written SELECT — silently drops the row rule.
	//
	// A row the role may not see is indistinguishable from one that does not
	// exist: both return (nil, nil) — never a 403 — which is the same
	// anti-enumeration contract Ctx.Update and the generated GET/PATCH/DELETE
	// follow. A role denied `read` on the resource entirely is a forbidden error.
	Get(resource, id string) (map[string]any, error)
	Insert(resource string, data map[string]any) (map[string]any, error)
	Update(resource, id string, data map[string]any) (map[string]any, error)

	// Bind JSON-decodes the request body (1 MiB cap) into dst. BindResource
	// additionally validates the decoded body against the compiled schema rules
	// for resource (the same rule engine REST and GraphQL use).
	Bind(dst any) error
	BindResource(resource string, dst any) error

	// RawBody returns the request body's EXACT bytes, under the SAME 1 MiB cap
	// Bind applies (MaxBodyBytes) — the handler never re-implements the limit.
	//
	// USE IT FOR WEBHOOKS. A gateway signature (Stripe, Wompi, GitHub…) is
	// computed over the bytes as sent: parse-then-reserialize changes key order
	// and whitespace and breaks every signature, so Bind is the WRONG tool for a
	// signed payload. Verify over RawBody FIRST, then Bind (or unmarshal the same
	// bytes) once the signature checks out — parsing before verifying is the #1
	// documented payment-integration bug.
	//
	// The body is read ONCE and buffered: RawBody and Bind may be used together
	// in any order and both see the whole body (a plain io.ReadAll on
	// Request().Body would leave Bind an empty reader). The returned slice is the
	// engine's buffer — treat it as read-only. A body over the cap returns
	// ErrBodyTooLarge, which the middleware maps to 413 if the handler returns it.
	RawBody() ([]byte, error)

	// Enqueue writes an outbox job inside the current transaction (atomic with
	// the business write). A Handler error rolls back the enqueue too.
	Enqueue(topic string, payload any) (int64, error)

	// SafeGo launches fn in a NEW goroutine — the ONLY sanctioned way to start a
	// goroutine from a handler (LIBRARY-HARDEN-S1). A raw `go func(){…}()` whose
	// body panics crashes the ENTIRE multi-tenant process: recover() never
	// crosses a goroutine boundary, so the request-chain Recoverer cannot save a
	// child goroutine. SafeGo wraps fn in recover() + a structured log (tenant +
	// request id) + the goroutine_panics_total metric, so a panicking background
	// task degrades to a logged incident instead of an outage for every tenant.
	//
	// The context passed to fn is a FRESH root — it carries NO request values (a
	// detached copy of the request context would retain chi's pooled route
	// context, which is recycled once the handler returns) — with an INDEPENDENT
	// bounded deadline. fn MUST honor that deadline (return promptly once ctx is
	// Done): a deadline cancels the context, it cannot forcibly stop a goroutine,
	// so an fn that ignores cancellation still leaks. fn MUST NOT use the
	// handler's transaction (Tx/UnsafeTx/Query/Insert/Update) — that transaction
	// commits or rolls back as the handler returns, and a goroutine touching it
	// races a closed tx. SafeGo is for post-response, non-transactional side
	// effects where at-most-once is acceptable (fire-and-forget: a metric ping, a
	// best-effort cache warm). For DURABLE, retryable work use Enqueue (the
	// transactional outbox + worker); for parallel work whose result the response
	// needs, use SafeParallel and wait for it.
	SafeGo(fn func(context.Context))

	// CreateUser creates an identity in THIS tenant's auth_users, inside the
	// handler's transaction — if the handler later fails, the user rolls back
	// with everything else (LIBRARY-EXTEND-S1). It applies the SAME rules as
	// the admin API: the email is normalized + format-checked, the role must
	// be declared in the schema RBAC (ErrUnknownRole — no privilege invention),
	// the password is argon2id-hashed and must meet the engine's configured
	// minimum length. An EMPTY password creates an invitation-style user that
	// cannot password-login until a reset sets one (the OTP/invite gate — same
	// contract as an OAuth-created user). Duplicate email in the tenant →
	// ErrEmailTaken. Always scoped to Tenant(): a handler cannot create users
	// in another tenant. Deliberately usable on a Public route — creating the
	// user IS the point of a custom registration endpoint; the caller's
	// anonymity is why the role comes from the handler's code, never from
	// request input, and why every input must be validated by the handler.
	CreateUser(email, password, role string) (CreatedUser, error)

	// ServeFile streams one of THIS tenant's stored files (the engine file
	// store, pkg/files — the same store /api/files/{id} serves) as the route's
	// response: stored Content-Type, strong content-hash ETag (If-None-Match →
	// 304), Range → 206, and on the local backend sendfile zero-copy
	// (FRONTEND-SPEC-S1). It is the seam for a PUBLIC or custom-authorized
	// download route — the handler decides WHO may fetch the file (e.g. "it is
	// the image of an active product"), the engine moves the bytes safely.
	//
	// Contract:
	//   - The route MUST declare ByteServing: true (rejected loudly otherwise):
	//     that flag is what routes the response around the response cache and
	//     the compression wrapper, which would otherwise buffer the whole blob
	//     in RAM, drop Content-Disposition/Range headers on a cache hit, and
	//     suppress sendfile.
	//   - Call it once, INSTEAD of JSON/Error, and return its error. The bytes
	//     are streamed after the transaction commits (same flush discipline as
	//     JSON). ctx.Error before it still works (the error response wins).
	//   - fileID must be one of this tenant's file ids. A malformed id, an
	//     unknown id, or ANOTHER tenant's id all yield the same uniform 404
	//     (ErrFileNotFound — isolation is structural: the metadata lives in the
	//     tenant's own schema).
	//   - The lookup reads committed state (the engine pool), not the handler's
	//     transaction: a file uploaded inside THIS tx is not yet servable.
	//   - Cache policy (FILES-2): by default no Cache-Control is set (browsers
	//     revalidate — cheap 304s via the strong ETag). Pass
	//     appximo.WithCacheControl(...) to declare one; the store is
	//     content-addressed, so a given file id's BYTES can never change —
	//     appximo.CacheControlImmutable ("public, max-age=31536000,
	//     immutable") is safe for any route whose URL embeds the file id (a
	//     product image, an avatar): a different image is a different id, so a
	//     stale cache is structurally impossible. Do NOT use it when the SAME
	//     URL can start serving a DIFFERENT file (e.g. /api/logo that follows a
	//     mutable pointer) — there, the default revalidation is the correct
	//     policy. The header is only sent on a successful stream, never on the
	//     404/error paths.
	ServeFile(fileID string, opts ...ServeFileOption) error

	// JSON buffers a success response flushed AFTER the transaction commits, so
	// a commit failure becomes a 500 rather than a false 200. Error buffers an
	// error response and returns a non-nil error so the Handler can
	// `return ctx.Error(...)`; the middleware rolls back and flushes it.
	JSON(status int, v any) error
	Error(status int, msg string, cause error) error

	Request() *http.Request
	Context() context.Context
}

// CreatedUser is the identity Ctx.CreateUser returns — the same public shape
// the auth endpoints expose (never the password hash).
type CreatedUser struct {
	ID            string
	Email         string
	Role          string
	EmailVerified bool
}

// Errors Ctx.CreateUser returns for the caller to branch on (map them to 409 /
// 422 / 400 with ctx.Error as fits the endpoint's contract).
var (
	// ErrEmailTaken: the email already has a user in this tenant.
	ErrEmailTaken = userauth.ErrEmailTaken
	// ErrInvalidEmail: the email failed the engine's format check.
	ErrInvalidEmail = errors.New("appximo: invalid email")
	// ErrWeakPassword: a non-empty password shorter than the engine's minimum.
	ErrWeakPassword = errors.New("appximo: password too short")
	// ErrUnknownRole: the role is not declared in the schema RBAC.
	ErrUnknownRole = errors.New("appximo: role not declared in the schema RBAC")
	// ErrBodyTooLarge: the request body exceeded MaxBodyBytes. Returned by
	// RawBody/Bind/BindResource; returning it from a Handler yields a 413.
	ErrBodyTooLarge = errors.New("appximo: request body too large")
	// ErrUpdateConflict: a Ctx.Update matched zero rows because the row changed
	// concurrently (its state fields already equal the requested values, so the
	// guard, not a bad transition, is what fired). Returning it from a Handler
	// yields a 409 — the caller should re-read and retry.
	ErrUpdateConflict = errors.New("appximo: the resource changed during the update; retry")
	// ErrFileNotFound: Ctx.ServeFile's uniform miss — a malformed id, an unknown
	// id and another tenant's id are deliberately indistinguishable (no
	// enumeration oracle on a download route). Returning it from a Handler
	// yields a 404 {"error":"not found"}.
	ErrFileNotFound = files.ErrNotFound
)

// ServeFileOption tunes one Ctx.ServeFile response (FILES-2). Options are
// applied at serve time (post-commit), and only on the success path.
type ServeFileOption func(*serveFileOpts)

type serveFileOpts struct {
	cacheControl string
}

// WithCacheControl sets the Cache-Control header on the streamed response.
// The engine deliberately does not default this: it cannot know whether the
// ROUTE's URL is stable-per-content (immutable-safe) or a mutable pointer
// (must revalidate) — that is the handler's knowledge. For the common
// content-addressed case use CacheControlImmutable.
func WithCacheControl(value string) ServeFileOption {
	return func(o *serveFileOpts) { o.cacheControl = value }
}

// CacheControlImmutable is the aggressive-but-safe policy for a URL that
// embeds the file id: the store is content-addressed (an id's bytes never
// change), so a browser may cache it for a year and never revalidate. A
// changed image arrives under a NEW id — and therefore a new URL.
const CacheControlImmutable = "public, max-age=31536000, immutable"

// InvalidTransitionError is returned by Ctx.Update when a schema-declared
// state machine refused the move (LIBRARY-GAPS-S2, ENG-7) — the same verdict,
// message and race-safety the generated PATCH produces. Returning it from a
// Handler yields the identical 422 response; branch on it with errors.As to
// customize.
type InvalidTransitionError struct{ Message string }

func (e *InvalidTransitionError) Error() string { return e.Message }

// engineRefs is the read-only engine state shared by every requestCtx: the
// loaded schema, the validators compiled once at boot, the RBAC policy, and
// the per-tenant identity store (for Ctx.CreateUser). It is never mutated
// after New returns.
type engineRefs struct {
	schema     *schema.APISchema
	validators map[string]*schema.ResourceValidator
	policy     *rbac.Policy
	users      *userauth.Store
	// files is the engine's content-addressable file store (the SAME instance
	// /api/files is served from — backend, upload policy and metadata identical
	// by construction), for Ctx.ServeFile. Never nil in a New()-built App.
	files *files.Store
	// minPassword is the engine's configured minimum password length
	// (Config.AuthMinPasswordLength / APPXIMO_AUTH_MIN_PASSWORD, default 8) —
	// Ctx.CreateUser enforces the same bar as signup and the admin API.
	minPassword int

	// safeGoTimeout bounds a Ctx.SafeGo goroutine's CONTEXT (LIBRARY-HARDEN-S1):
	// it is cancelled after this. fn must honor cancellation to actually stop — a
	// deadline cannot forcibly kill a goroutine. Config.SafeGoTimeoutSeconds /
	// APPXIMO_SAFEGO_TIMEOUT, default 30s.
	safeGoTimeout time.Duration
	// onGoroutinePanic increments the goroutine_panics_total metric when a
	// SafeGo goroutine recovers a panic (set to app.metrics.IncGoroutinePanic).
	// nil in a bare test build — the recover + log still run.
	onGoroutinePanic func()
}

// requestCtx is the per-request Ctx implementation. One is built by the custom
// route middleware after the standard chain (tenant → JWT → RBAC) has run.
type requestCtx struct {
	w http.ResponseWriter
	r *http.Request
	// ctx is the handler's EFFECTIVE context: r.Context() wrapped with the
	// route's deadline (Route.Timeout, default 5s) by customHandler. Every DB /
	// outbox / user operation the Ctx runs uses it, so the deadline actually
	// bounds the handler's work (LIBRARY-HARDEN-S1) — pgx cancels a query when it
	// fires. It carries all of r.Context()'s values (tenant, claims, request id).
	ctx context.Context
	tx  pgx.Tx
	eng *engineRefs
	tc  *tenant.TenantCtx
	cl  *auth.Claims

	// Buffered request body (LIBRARY-GAPS-S1): read once, served to RawBody AND
	// Bind/BindResource so a webhook handler can verify a signature over the exact
	// bytes and still decode them. bodyRead is the "already consumed" flag; a
	// cap violation is memoized as ErrBodyTooLarge in bodyErr.
	bodyBuf  []byte
	bodyErr  error
	bodyRead bool

	// Buffered response (flushed by the middleware around commit).
	status int
	body   []byte
	set    bool
	// retryAfter marks a response reclassified to 503 (DB unavailable) so flush
	// adds the Retry-After header the generated routes already send (ENG-10).
	retryAfter bool

	// byteServing mirrors Route.ByteServing so Ctx.ServeFile can refuse, loudly
	// and at first use, a route that did not opt out of the cache/compression
	// wrappers (a stream through them is buffered, header-stripped and
	// sendfile-dead — a silent correctness bug, so it is an error instead).
	byteServing bool
	// serveFileID is the file registered by Ctx.ServeFile; flush streams it
	// AFTER the commit (same discipline as the JSON buffer — the response never
	// races the transaction). Empty = no file response. serveFileOpts carries
	// the per-response options (FILES-2: cache policy).
	serveFileID   string
	serveFileOpts serveFileOpts
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
		return nil, fmt.Errorf("appximo: unknown resource %q", resource)
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

	// The role's field allowlist bounds Filters/OrderBy too (SEC-5 closed
	// generally): a custom handler acting AS a restricted role cannot use a
	// hidden column as an oracle any more than the HTTP surface can.
	qb, err := query.BuildQuery(resource, &res, params, eval.Condition, eval.AllowedFields)
	if err != nil {
		return nil, err
	}
	selectQ, _, selectArgs, _ := qb.SQL()

	rows, err := c.tx.Query(c.ctx, selectQ, selectArgs...)
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

// Get implements Ctx.Get: SELECT one row by id, under the role's row-level
// condition, projected to the role's allowlist.
//
// It reuses the same building blocks the generated GET-by-id uses — the policy
// evaluation, query.AppendRowCondition and the field projection — so a custom route
// cannot see a row (or a column) the REST API would hide from the same caller.
func (c *requestCtx) Get(resource, id string) (map[string]any, error) {
	if _, ok := c.eng.schema.Resources[resource]; !ok {
		return nil, fmt.Errorf("appximo: unknown resource %q", resource)
	}
	eval := c.eng.policy.Evaluate(c.evalCtx(), resource, "read")
	if !eval.Allowed {
		return nil, &forbiddenError{"forbidden"}
	}
	q := fmt.Sprintf("SELECT * FROM %s WHERE id = $1", pgx.Identifier{resource}.Sanitize())
	args := []any{id}
	q, args, err := query.AppendRowCondition(q, args, eval.Condition)
	if err != nil {
		return nil, err
	}
	row, err := c.queryOne(q, args)
	if err != nil || row == nil {
		return nil, err // (nil, nil) = absent, or hidden by the row rule
	}
	out := []map[string]any{row}
	projectAll(out, eval.AllowedFields)
	return out[0], nil
}

func (c *requestCtx) Insert(resource string, data map[string]any) (map[string]any, error) {
	if _, ok := c.eng.schema.Resources[resource]; !ok {
		return nil, fmt.Errorf("appximo: unknown resource %q", resource)
	}
	eval := c.eng.policy.Evaluate(c.evalCtx(), resource, "create")
	if !eval.Allowed {
		return nil, &forbiddenError{"forbidden"}
	}
	// The SAME preparation the generated POST runs (CTX-PARITY-S1):
	// defaults → declarative rules + value types → state-machine initial
	// states. This used to be a bare ValidateWrite, so a custom handler skipped
	// all three: a schema `default` was not applied (rows landed with a NULL
	// status and the next transition failed with `invalid transition from ""`),
	// values were never type-checked, and a row could be created outside its
	// declared initial state — while backend-spec promised parity with the POST.
	if res, ok := c.eng.schema.Resources[resource]; ok {
		if verrs := codegen.PrepareCreate(&res, c.eng.validators[resource], data); len(verrs) > 0 {
			return nil, &ValidationError{Fields: verrs}
		}
	}
	// The SAME create-time RBAC the generated POST and the GraphQL create run
	// (EnforceCreateRBAC), at the same point in the sequence: it drops fields
	// outside the role's allowlist, FORCES the row-condition column to the
	// caller's resolved identity, and REJECTS a body that supplies a different
	// value for it.
	//
	// This used to be a local allowlist filter only, so a custom route was a way
	// AROUND a rule the REST API enforces: an owner-scoped role could create a
	// row with no owner at all (the condition was never applied) or attributed
	// to another principal (the mass-assignment block was absent) — 201 through
	// a handler, 403 through /api. Found by auditing the whole parity class
	// rather than only the two divergences the field report named.
	if status, msg := codegen.EnforceCreateRBAC(data, &eval); status != 0 {
		return nil, &forbiddenError{msg}
	}
	if len(data) == 0 {
		return nil, &ValidationError{Fields: []schema.FieldRuleError{{Rule: "empty", Message: "no writable fields"}}}
	}
	// Per-field file attach policy (FILES-1) — same check as REST/GraphQL/batch,
	// on the handler's tx.
	if res, ok := c.eng.schema.Resources[resource]; ok {
		if fpErrs, fpErr := codegen.CheckFilePoliciesTx(c.ctx, c.tx, &res, data); fpErr != nil {
			return nil, fpErr
		} else if len(fpErrs) > 0 {
			return nil, &ValidationError{Fields: fpErrs}
		}
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
	res, ok := c.eng.schema.Resources[resource]
	if !ok {
		return nil, fmt.Errorf("appximo: unknown resource %q", resource)
	}
	eval := c.eng.policy.Evaluate(c.evalCtx(), resource, "update")
	if !eval.Allowed {
		return nil, &forbiddenError{"forbidden"}
	}
	// The SAME preparation the generated PATCH runs (CTX-PARITY-S1): partial
	// semantics plus the value type check the library path never had.
	if verrs := codegen.PrepareUpdate(&res, c.eng.validators[resource], data); len(verrs) > 0 {
		return nil, &ValidationError{Fields: verrs}
	}
	data = applyWriteAllowlist(data, eval.AllowedFields)
	if len(data) == 0 {
		return nil, &ValidationError{Fields: []schema.FieldRuleError{{Rule: "empty", Message: "no writable fields"}}}
	}
	// Per-field file attach policy (FILES-1) — same check as REST/GraphQL/batch.
	if fpErrs, fpErr := codegen.CheckFilePoliciesTx(c.ctx, c.tx, &res, data); fpErr != nil {
		return nil, fpErr
	} else if len(fpErrs) > 0 {
		return nil, &ValidationError{Fields: fpErrs}
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
	// State-machine transition guard (LIBRARY-GAPS-S2, ENG-7) — the SAME
	// function the generated PATCH/PUT, GraphQL update and batch transaction
	// use, so a custom route can no longer bypass a declared lifecycle (and
	// never needs to re-state the transition table). Race-safe: the move is
	// allowed only if the row's CURRENT state permits it, inside the UPDATE's
	// WHERE. No-op for updates that touch no state-machine field.
	q, args, err = codegen.AppendStateTransitionGuard(q, args, &res, data)
	if err != nil {
		return nil, err
	}
	q += " RETURNING *"
	row, err := c.queryOne(q, args)
	if err != nil {
		return nil, err
	}
	if row == nil {
		// Zero rows: not found / row-condition excluded (→ nil, nil — the
		// historical contract) — or the transition guard refused the move.
		// The explain read runs the row condition too, so a hidden row stays
		// indistinguishable from a missing one.
		status, msg := codegen.ExplainTransitionFailureTx(c.ctx, c.tx,
			pgx.Identifier{resource}.Sanitize(), id, &res, data, eval.Condition)
		switch status {
		case http.StatusUnprocessableEntity:
			return nil, &InvalidTransitionError{Message: msg}
		case http.StatusConflict:
			return nil, ErrUpdateConflict
		}
		return nil, nil // id not found, or excluded by the row condition
	}
	return pkghandlers.FilterFields(row, eval.AllowedFields), nil
}

// queryOne runs a RETURNING * write on the tenant transaction and returns the
// first row (nil if none). UUID columns are normalised to strings by RowsToMaps.
func (c *requestCtx) queryOne(q string, args []any) (map[string]any, error) {
	rows, err := c.tx.Query(c.ctx, q, args...)
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

// readBody reads the request body ONCE, under the engine's cap, and memoizes it
// (bytes and error). Every binder goes through here, which is what makes
// RawBody + Bind composable in any order: an http.Request body is a
// single-use stream, so the pre-RawBody idiom (io.ReadAll on Request().Body)
// silently left Bind with nothing to decode.
func (c *requestCtx) readBody() ([]byte, error) {
	if c.bodyRead {
		return c.bodyBuf, c.bodyErr
	}
	c.bodyRead = true
	// MaxBytesReader with the ResponseWriter so an over-cap body also stops the
	// connection from being reused — the same protection the generated handlers get.
	c.r.Body = http.MaxBytesReader(c.w, c.r.Body, maxBodyBytes)
	c.bodyBuf, c.bodyErr = io.ReadAll(c.r.Body)
	if c.bodyErr != nil {
		var mbe *http.MaxBytesError
		if errors.As(c.bodyErr, &mbe) {
			// A typed, matchable error instead of net/http's internal one, so a
			// handler can `errors.Is(err, appximo.ErrBodyTooLarge)` and the
			// middleware can map a returned one to 413.
			c.bodyErr = ErrBodyTooLarge
		}
	}
	return c.bodyBuf, c.bodyErr
}

func (c *requestCtx) RawBody() ([]byte, error) { return c.readBody() }

func (c *requestCtx) Bind(dst any) error {
	b, err := c.readBody()
	if err != nil {
		return err
	}
	// A Decoder (not json.Unmarshal) over the buffered bytes keeps Bind's decoding
	// semantics byte-identical to the pre-buffer implementation.
	return json.NewDecoder(bytes.NewReader(b)).Decode(dst)
}

func (c *requestCtx) BindResource(resource string, dst any) error {
	rv := c.eng.validators[resource]
	if rv == nil {
		return fmt.Errorf("appximo: unknown resource %q", resource)
	}
	b, err := c.readBody()
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
	return outbox.Enqueue(c.ctx, c.tx, c.tc.ID, topic, payload)
}

// --- safe goroutines (LIBRARY-HARDEN-S1) -------------------------------------

func (c *requestCtx) SafeGo(fn func(context.Context)) {
	// The goroutine outlives the request, so its context must NOT retain any
	// request-scoped state. A detached COPY of c.ctx (context.WithoutCancel) would
	// keep ALL its values — including chi's POOLED *RouteContext, which chi
	// recycles the instant the handler returns — a use-after-recycle race for the
	// whole life of the goroutine. So the base is a FRESH root carrying only the
	// deadline; the tenant id and request id are extracted EAGERLY here (before
	// `go`) and passed by value for the log. fn captures anything else it needs by
	// value (it must never touch c.tx/c.w/c.r).
	go runSafe(context.Background(), c.eng.safeGoTimeout, c.tc.ID, requestID(c.ctx), c.eng.onGoroutinePanic, fn)
}

// --- identity creation (LIBRARY-EXTEND-S1) -----------------------------------

func (c *requestCtx) CreateUser(email, password, role string) (CreatedUser, error) {
	// Role must be declared in the schema RBAC — the same gate the admin API
	// applies (checkRole) and boot applies to the signup role. The role comes
	// from HANDLER CODE, so this catches typos at first use, not a caller
	// escalating (a caller never chooses it).
	if _, ok := c.eng.policy.Roles[role]; !ok {
		return CreatedUser{}, ErrUnknownRole
	}
	normalized, ok := userauth.ValidateEmail(email)
	if !ok {
		return CreatedUser{}, ErrInvalidEmail
	}
	hash := ""
	if password != "" {
		if len([]rune(password)) < c.eng.minPassword {
			return CreatedUser{}, ErrWeakPassword
		}
		var err error
		if hash, err = userauth.HashPassword(password); err != nil {
			return CreatedUser{}, err
		}
	}
	// On the handler's tx and the Ctx's tenant — atomic with the handler's
	// other writes, structurally incapable of touching another tenant.
	u, err := c.eng.users.InsertUserTx(c.ctx, c.tx, c.tc.ID, normalized, hash, role)
	if err != nil {
		return CreatedUser{}, err
	}
	return CreatedUser{ID: u.ID, Email: u.Email, Role: u.Role, EmailVerified: u.EmailVerified}, nil
}

// --- response ---------------------------------------------------------------

// ServeFile registers this tenant's file fileID as the route's response; the
// stream itself runs in flush, AFTER the transaction commits (the same
// discipline as the JSON buffer — a response never races its transaction, and
// a not-found discovered at serve time can still answer a clean 404 because
// nothing has been written yet).
func (c *requestCtx) ServeFile(fileID string, opts ...ServeFileOption) error {
	// Misuse is a loud error, not a degraded stream: without ByteServing the
	// response cache buffers the whole blob in RAM (and strips
	// Content-Disposition/Range headers on a hit) and the compression wrapper
	// suppresses sendfile — a silent correctness bug measured in FILES-BENCH.
	if !c.byteServing {
		return fmt.Errorf("appximo: Ctx.ServeFile requires the route to declare ByteServing: true (the response must bypass the response cache and compression)")
	}
	if c.set {
		return fmt.Errorf("appximo: Ctx.ServeFile called after JSON/Error already buffered a response")
	}
	if c.serveFileID != "" {
		return fmt.Errorf("appximo: Ctx.ServeFile called twice (one response per request)")
	}
	// Malformed ids get the SAME uniform miss as unknown/foreign ids — a
	// download route must not be a format oracle. (Store.Serve would surface a
	// raw Postgres cast error for a non-uuid, which is why the shape is checked
	// here, exactly like the engine's own /api/files/{id} handler does.)
	if _, err := uuid.Parse(strings.TrimSpace(fileID)); err != nil {
		return ErrFileNotFound
	}
	c.serveFileID = strings.TrimSpace(fileID)
	for _, opt := range opts {
		opt(&c.serveFileOpts)
	}
	return nil
}

// serveRegisteredFile streams the file ServeFile registered. Called by flush
// after commit; nothing has been written yet, so every failure can still
// produce a clean status: unknown/foreign id → the uniform 404, anything else
// → a masked 500 (logged), mirroring the engine's own download handlers.
func (c *requestCtx) serveRegisteredFile(w http.ResponseWriter) {
	if c.eng == nil || c.eng.files == nil {
		log.Printf("appximo: ServeFile: no file store wired (test-built ctx?)")
		writeErr(w, http.StatusInternalServerError, "internal error")
		return
	}
	// Handler-declared cache policy (FILES-2). Set BEFORE Serve (headers must
	// precede the first body write) and REMOVED on every failure path below —
	// a cached 404 with a year-long immutable policy would pin the miss in
	// every browser that saw it.
	if cc := c.serveFileOpts.cacheControl; cc != "" {
		w.Header().Set("Cache-Control", cc)
	}
	// The route's deadline (Route.Timeout) bounds the metadata lookup and an
	// S3 proxy read; http.ServeContent on the local backend is not
	// context-aware, but its work is a disk sendfile bounded by the server's
	// own write deadlines.
	err := c.eng.files.Serve(w, c.r.WithContext(c.ctx), c.tc.ID, c.serveFileID)
	if err == nil {
		return
	}
	w.Header().Del("Cache-Control")
	if errors.Is(err, files.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	log.Printf("appximo: ServeFile %s (tenant %s): %v", c.serveFileID, c.tc.ID, err)
	writeErr(w, http.StatusInternalServerError, "download failed")
}

func (c *requestCtx) JSON(status int, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	c.status, c.body, c.set = status, b, true
	return nil
}

func (c *requestCtx) Error(status int, msg string, cause error) error {
	// ENG-10 (CONSUMER-PATH-S1): a handler that wraps a DB-unavailable cause in a
	// 5xx gets the HONEST 503 + Retry-After automatically — the same
	// classification the generated routes apply. Measured live: with PostgreSQL
	// stopped, custom routes answered 151×500 ("looks like a bug") while the
	// generated surface said 503 ("down, retry later"). The handler's message is
	// kept; only the status/semantics are corrected. A 4xx choice is deliberate
	// (the handler classified a CALLER problem) and is never overridden.
	if status >= 500 && cause != nil && db.IsUnavailable(cause) {
		status = http.StatusServiceUnavailable
		c.retryAfter = true
	}
	b, _ := json.Marshal(map[string]string{"error": msg})
	c.status, c.body, c.set = status, b, true
	return &handledError{cause: cause, msg: msg}
}

func (c *requestCtx) Request() *http.Request   { return c.r }
func (c *requestCtx) Context() context.Context { return c.ctx }

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
