package appitools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/miguelangel/appitools/pkg/auth"
	"github.com/miguelangel/appitools/pkg/db"
	"github.com/miguelangel/appitools/pkg/resilience"
	"github.com/miguelangel/appitools/pkg/schema"
	"github.com/miguelangel/appitools/pkg/tenant"
)

// Handler is a Class-1 custom endpoint (ADR-016 Decision 2). It receives a Ctx
// with identity, tenant, and a tenant-scoped transaction already resolved.
// Returning nil COMMITS the transaction (and flushes any Ctx.JSON response);
// returning an error ROLLS IT BACK. Use `return ctx.Error(...)` to send a
// specific error response, or return any error for a masked 500.
type Handler func(ctx Ctx) error

// Route is a custom endpoint registered with (*App).Register before Start.
//
// Path must begin with "/api/" so it flows through the SAME middleware chain as
// generated routes (tenant → rate limit → JWT → RBAC). The first path segment
// after "/api/" must NOT be a schema resource name — that space is owned by the
// generated CRUD routes, and registering under it is rejected at boot as a
// collision (deterministic, before chi can shadow it).
type Route struct {
	Method  string // GET | POST | PUT | PATCH | DELETE
	Path    string // e.g. "/api/declarations/submit"
	Handler Handler

	// RequireRole, when non-empty, demands the caller's JWT role equal it
	// (else 403). This is in ADDITION to the path-based RBAC the middleware
	// already applied; a Route with no RequireRole still gets deny-by-default
	// from the policy when its path segment matches a policy rule.
	RequireRole string

	// Timeout bounds this endpoint's execution (LIBRARY-HARDEN-S1). When > 0 the
	// handler's context — and the tenant transaction opened for it — is cancelled
	// after Timeout: a slow query or a hung outbound call is aborted (the deadline
	// propagates to pgx and to any downstream that honours the context), the
	// transaction rolls back, and the caller gets a 500 instead of the request
	// pinning a connection indefinitely. 0 uses the engine default (5s). It bounds
	// the REQUEST goroutine only; a Ctx.SafeGo goroutine outlives the request and
	// carries its own independent deadline.
	Timeout time.Duration

	// RateLimit overrides this endpoint's dedicated throttle, per (tenant, client
	// IP) — the same bucket shape the public-route limiter uses, sized for THIS
	// route (LIBRARY-GAPS-S1).
	//
	// The engine's default for a Public route (5 rps / burst 10) is calibrated for
	// a public WRITE endpoint — a registration or a webhook, where 5 rps is
	// generous and abuse is the real risk. A public READ endpoint (a storefront
	// catalogue) has the opposite profile: legitimate traffic is bursty and much
	// higher. That mismatch is what this field fixes, without touching the
	// conservative default everyone else inherits.
	//
	//	{Method: "GET", Path: "/api/catalogue", Public: true,
	//	 RateLimit: &appitools.RateLimit{RPS: 200, Burst: 400}}
	//
	// Nil (the default) keeps today's behavior EXACTLY: a Public route uses the
	// shared public-route limiter, a non-public route has no dedicated limit (only
	// the per-tenant one). Set on a NON-public route, it adds a per-(tenant, IP)
	// limit on top of the per-tenant limiter — useful for an expensive
	// authenticated endpoint (a report, an export).
	RateLimit *RateLimit

	// Public marks this route as PRE-AUTHENTICATION (LIBRARY-EXTEND-S1): the
	// JWT and path-RBAC middlewares skip it by EXACT method+path match, so a
	// caller needs no Bearer token — the seam for a custom registration/webhook
	// endpoint that must run before an identity exists.
	//
	// ⚠ A public route is ATTACK SURFACE. The engine keeps what it can by
	// default — the tenant still resolves from the Host (per-tenant isolation
	// holds), the shared per-tenant rate limit still applies, and a DEDICATED,
	// far more aggressive public-route rate limit (per tenant+client IP,
	// APPITOOLS_PUBLIC_ROUTE_RPS/BURST, default 5 rps / burst 10 → 429) is
	// enforced before the handler runs. Everything else is the handler's
	// responsibility: validate EVERY input, and treat the caller as hostile.
	//
	// Authentication is OPTIONAL, not ignored (LIBRARY-GAPS-S2, ENG-6): with no
	// Authorization header the handler sees Claims() zero (anonymous — the
	// RBAC-aware helpers fail closed with forbidden; anonymous writes go
	// through CreateUser or a deliberate, greppable UnsafeTx). With a VALID
	// Bearer, Claims() is populated — one endpoint can serve guests and
	// recognized users (a checkout that links the order to a logged-in
	// customer). A present-but-invalid/expired/foreign-tenant Bearer is a 401:
	// sent credentials never silently degrade to anonymous, so the client
	// knows to re-authenticate or retry deliberately without the token. The
	// path-RBAC still skips a Public route entirely — a populated Claims is
	// input for the handler, never a new gate.
	//
	// Public routes must use literal paths (no chi {params} — the match is
	// exact) and cannot combine with RequireRole (use Claims().Role in the
	// handler if a public route wants to branch on identity). Only routes
	// explicitly marked Public relax auth; every other route keeps
	// deny-by-default.
	Public bool
}

// RateLimit is one route's token-bucket budget (Route.RateLimit): RPS sustained
// requests per second with Burst instantaneous, counted per (tenant, client IP).
// Both must be > 0 — a zero value is rejected at Register, so a half-filled
// struct can never silently disable the throttle.
type RateLimit struct {
	RPS   float64
	Burst int
}

// defaultRouteTimeout bounds a custom handler that does not set Route.Timeout —
// the same 5s ceiling the library has always applied to a custom route's tenant
// transaction (LIBRARY-HARDEN-S1 made it per-route-overridable).
const defaultRouteTimeout = 5 * time.Second

var (
	validMethods   = map[string]bool{"GET": true, "POST": true, "PUT": true, "PATCH": true, "DELETE": true}
	pgSchemaNameRe = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)
)

// validateRoute checks a Route's shape and that it does not collide with any
// route the schema generates. Returns a descriptive error suitable for a boot
// failure. seen tracks already-registered custom (method,path) pairs so two
// custom routes cannot silently shadow each other either.
func validateRoute(rt Route, s *schema.APISchema, seen map[string]bool) error {
	method := strings.ToUpper(rt.Method)
	if !validMethods[method] {
		return fmt.Errorf("route %s %s: invalid method (want one of GET/POST/PUT/PATCH/DELETE)", rt.Method, rt.Path)
	}
	if rt.Handler == nil {
		return fmt.Errorf("route %s %s: nil handler", method, rt.Path)
	}
	if !strings.HasPrefix(rt.Path, "/api/") {
		return fmt.Errorf("route %s %s: Path must begin with \"/api/\" so it shares the engine middleware chain", method, rt.Path)
	}

	key := method + " " + rt.Path
	if seen[key] {
		return fmt.Errorf("route %s: registered twice", key)
	}

	// A partially-filled RateLimit would silently mean "unlimited" (rate.Limiter
	// with burst 0 rejects everything, RPS 0 the same) — reject it at boot instead.
	if rl := rt.RateLimit; rl != nil && (rl.RPS <= 0 || rl.Burst <= 0) {
		return fmt.Errorf("route %s %s: RateLimit requires RPS > 0 and Burst > 0 (got %g rps, burst %d)",
			method, rt.Path, rl.RPS, rl.Burst)
	}

	if rt.Public {
		// The JWT/RBAC skip is an EXACT method+path match — a chi pattern like
		// /api/x/{id} would route but never match the skip, yielding a public
		// route that 401s. Reject at boot instead of failing confusingly live.
		if strings.ContainsAny(rt.Path, "{*") {
			return fmt.Errorf("route %s %s: a Public route must use a literal path (no chi {params}/wildcards — the auth skip matches the exact path)", method, rt.Path)
		}
		// RequireRole implies an authenticated caller; combining it with Public
		// is a contradiction (an anonymous request has no role).
		if rt.RequireRole != "" {
			return fmt.Errorf("route %s %s: Public and RequireRole are mutually exclusive (a public route has no authenticated role)", method, rt.Path)
		}
	}

	// First segment after /api/ — collision iff it names a generated resource.
	segs := strings.Split(strings.TrimPrefix(rt.Path, "/api/"), "/")
	first := segs[0]
	if first == "" {
		return fmt.Errorf("route %s %s: empty resource segment", method, rt.Path)
	}
	if _, isResource := s.Resources[first]; isResource {
		return fmt.Errorf("route %s %s: collides with generated routes for resource %q (the /api/%s prefix is owned by the schema)",
			method, rt.Path, first, first)
	}
	seen[key] = true
	return nil
}

// validateRouteGrants is the BOOT half of custom-route authorization
// (LIBRARY-GAPS-S1, ADR-021): it cross-checks every `routes` grant in the schema's
// RBAC against the routes actually REGISTERED in this binary.
//
// The schema validator checks what a standalone schema can be checked for (segment
// shape, known actions, no collision with a real resource); only here does the set
// of registered endpoints exist. A grant for a segment nothing serves — or for an
// action no registered method provides — is DEAD CONFIG, and dead authorization
// config is exactly the thing that later reads as "the RBAC says they can, so why
// the 403?". It fails the boot with the registered segments listed, instead of
// surfacing as a confusing runtime denial.
//
// It is deliberately about EXISTENCE, never about widening: nothing here grants
// anything. A schema with no `routes` (every schema before this key) returns nil
// after one map length check.
func validateRouteGrants(s *schema.APISchema, routes []Route) error {
	if !schemaHasRouteGrants(s) {
		return nil
	}
	// segment → the set of actions its registered routes provide.
	served := make(map[string]map[string]bool, len(routes))
	for _, rt := range routes {
		seg := firstAPISegment(rt.Path)
		if seg == "" {
			continue
		}
		if served[seg] == nil {
			served[seg] = make(map[string]bool, 4)
		}
		if a := actionForMethod(rt.Method); a != "" {
			served[seg][a] = true
		}
	}

	var problems []string
	for _, roleName := range sortedMapKeys(s.RBAC.Roles) {
		role := s.RBAC.Roles[roleName]
		for _, seg := range sortedMapKeys(role.Routes) {
			actions, ok := served[seg]
			if !ok {
				problems = append(problems, fmt.Sprintf(
					"rbac.roles.%s.routes.%s: no custom route is registered under /api/%s (registered segments: %s)",
					roleName, seg, seg, listOrNone(sortedMapKeys(served))))
				continue
			}
			for _, a := range role.Routes[seg].Actions {
				if a == "*" {
					continue // grants whatever the segment serves, now and later
				}
				if !actions[a] {
					problems = append(problems, fmt.Sprintf(
						"rbac.roles.%s.routes.%s: action %q is granted but no registered route on /api/%s uses the matching method (%s); the segment serves: %s",
						roleName, seg, a, seg, methodsForAction(a), listOrNone(sortedSetKeys(actions))))
				}
			}
		}
	}
	if len(problems) > 0 {
		return fmt.Errorf("appitools: RBAC grants custom routes that are not registered:\n  %s",
			strings.Join(problems, "\n  "))
	}
	return nil
}

// schemaHasRouteGrants reports whether any role declares a `routes` block.
func schemaHasRouteGrants(s *schema.APISchema) bool {
	for _, role := range s.RBAC.Roles {
		if len(role.Routes) > 0 {
			return true
		}
	}
	return false
}

// firstAPISegment returns the first path element after "/api/" — the VIRTUAL
// resource the RBAC middleware authorizes a custom route by (rbac.resourceFromPath
// derives the same value from the live request path).
func firstAPISegment(path string) string {
	rest := strings.TrimPrefix(path, "/api/")
	if rest == path { // no /api/ prefix (rejected at Register anyway)
		return ""
	}
	if i := strings.IndexByte(rest, '/'); i >= 0 {
		return rest[:i]
	}
	return rest
}

// actionForMethod mirrors rbac.actionFromMethod — the SAME mapping the middleware
// applies at request time, so the boot check validates what will actually be
// evaluated.
func actionForMethod(method string) string {
	switch strings.ToUpper(method) {
	case http.MethodGet:
		return "read"
	case http.MethodPost:
		return "create"
	case http.MethodPut, http.MethodPatch:
		return "update"
	case http.MethodDelete:
		return "delete"
	default:
		return ""
	}
}

// methodsForAction is actionForMethod backwards, for the error message.
func methodsForAction(action string) string {
	switch action {
	case "read":
		return "GET"
	case "create":
		return "POST"
	case "update":
		return "PUT/PATCH"
	case "delete":
		return "DELETE"
	default:
		return "?"
	}
}

func sortedMapKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func sortedSetKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func listOrNone(ss []string) string {
	if len(ss) == 0 {
		return "none"
	}
	return strings.Join(ss, ", ")
}

// publicRoutePaths returns the "METHOD /path" set of routes registered Public,
// or nil when there are none (the common case — the middlewares then skip the
// lookup entirely).
func (a *App) publicRoutePaths() map[string]bool {
	var out map[string]bool
	for _, rt := range a.routes {
		if !rt.Public {
			continue
		}
		if out == nil {
			out = make(map[string]bool)
		}
		out[rt.Method+" "+rt.Path] = true
	}
	return out
}

// routeLimiter picks the throttle for rt (LIBRARY-GAPS-S1): an own limiter when
// the route declares RateLimit, the shared conservative public-route limiter for
// a Public route that does not, and nil for an authenticated route with no
// declaration (unchanged — only the per-tenant limiter applies). Each declaring
// route gets its OWN limiter instance, so two routes never share a bucket.
func (a *App) routeLimiter(rt Route) *resilience.TenantLimiter {
	if rl := rt.RateLimit; rl != nil {
		return resilience.NewConfiguredLimiter(resilience.RateLimitConfig{RPS: rl.RPS, Burst: rl.Burst})
	}
	if rt.Public {
		return a.publicLimiter
	}
	return nil
}

// customHandler wraps a Route's Handler in the withTenantTx pattern (ADR-016
// Decision 4): it runs AFTER the shared middleware chain (so identity, tenant
// and RBAC are already resolved), opens a transaction scoped to the tenant
// search_path via set_config(...,true), builds the Ctx, invokes the Handler,
// and commits on nil / rolls back on error. It never re-implements JWT, rate
// limiting, or path-based RBAC — those are inherited from the chain.
func (a *App) customHandler(rt Route) http.HandlerFunc {
	// Resolve this route's throttle ONCE, at boot: its own limiter when
	// Route.RateLimit is set, else the shared public-route limiter for a Public
	// route, else none (an authenticated route keeps only the per-tenant limit —
	// byte-identical to before). The request path just nil-checks a captured
	// pointer; no per-request configuration lookup.
	limiter := a.routeLimiter(rt)
	return func(w http.ResponseWriter, r *http.Request) {
		tc := tenant.FromCtx(r.Context())
		if tc == nil || !pgSchemaNameRe.MatchString(tc.PGSchema) {
			writeErr(w, http.StatusBadRequest, "invalid tenant")
			return
		}
		if limiter != nil {
			// Per (tenant, client IP), on top of the per-tenant limiter that
			// already ran — an anonymous endpoint must not be a free abuse
			// vector. Before the transaction, so a throttled burst never touches
			// the pool.
			if !limiter.Allow(tc.ID+"|"+remoteIP(r), "") {
				w.Header().Set("Retry-After", "1")
				writeErr(w, http.StatusTooManyRequests, "too many requests")
				return
			}
		}
		cl := auth.ClaimsFromCtx(r.Context())
		if rt.RequireRole != "" && (cl == nil || cl.Role != rt.RequireRole) {
			writeErr(w, http.StatusForbidden, "forbidden")
			return
		}

		timeout := rt.Timeout
		if timeout <= 0 {
			timeout = defaultRouteTimeout
		}
		ctx, cancel := context.WithTimeout(r.Context(), timeout)
		defer cancel()

		tx, err := a.pool.Begin(ctx)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "internal error")
			return
		}
		committed := false
		// context.Background so the rollback still runs if ctx was the canceller.
		defer func() {
			if !committed {
				_ = tx.Rollback(context.Background())
			}
		}()

		// Tenant isolation: bind search_path as DATA (not SQL) and transaction-
		// local (reverts on commit/rollback; the pooled conn returns clean).
		// NEVER "SET LOCAL search_path = $1" (syntax error) or string concat.
		if _, err := tx.Exec(ctx, "SELECT set_config('search_path', $1, true)", tc.PGSchema); err != nil {
			writeErr(w, http.StatusInternalServerError, "internal error")
			return
		}

		rc := &requestCtx{w: w, r: r, ctx: ctx, tx: tx, eng: a.eng, tc: tc, cl: cl}
		if herr := rt.Handler(rc); herr != nil {
			a.writeHandlerError(w, rc, rt, herr)
			return // rollback runs via defer
		}

		if err := tx.Commit(ctx); err != nil {
			writeErr(w, http.StatusInternalServerError, "internal error")
			return
		}
		committed = true
		rc.flush(w)
	}
}

// flush writes the response the Handler buffered via Ctx.JSON / Ctx.Error. A
// Handler that wrote nothing and returned nil yields 204 No Content.
func (c *requestCtx) flush(w http.ResponseWriter) {
	if !c.set {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if c.retryAfter {
		// The response was reclassified to 503 by Ctx.Error (DB unavailable,
		// ENG-10) — same retry signal the generated routes send.
		w.Header().Set("Retry-After", "1")
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(c.status)
	_, _ = w.Write(c.body)
}

// writeHandlerError maps a Handler's returned error to its HTTP response. The
// transaction has already been (or will be) rolled back by the caller's defer.
func (a *App) writeHandlerError(w http.ResponseWriter, rc *requestCtx, rt Route, err error) {
	// The Handler explicitly chose the response via ctx.Error — honour it.
	var he *handledError
	if errors.As(err, &he) {
		rc.flush(w)
		if he.cause != nil {
			a.logf("custom route %s %s: %v", rt.Method, rt.Path, he.cause)
		}
		return
	}
	// Field-level validation failure → 422 in the same shape as REST/GraphQL.
	if ve, ok := asValidationError(err); ok {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnprocessableEntity)
		_ = json.NewEncoder(w).Encode(map[string]any{"error": "validation_failed", "fields": ve.Fields})
		return
	}
	var fe *forbiddenError
	if errors.As(err, &fe) {
		writeErr(w, http.StatusForbidden, "forbidden")
		return
	}
	// An over-cap body is a client error, not a server fault: a handler that just
	// returns the error from RawBody/Bind gets the right status for free
	// (LIBRARY-GAPS-S1).
	if errors.Is(err, ErrBodyTooLarge) {
		writeErr(w, http.StatusRequestEntityTooLarge, "request body too large")
		return
	}
	// A state-machine refusal from Ctx.Update → the same 422 the generated
	// PATCH answers; a concurrent-change conflict → 409 (LIBRARY-GAPS-S2, ENG-7).
	var ite *InvalidTransitionError
	if errors.As(err, &ite) {
		writeErr(w, http.StatusUnprocessableEntity, ite.Message)
		return
	}
	if errors.Is(err, ErrUpdateConflict) {
		writeErr(w, http.StatusConflict, "the resource changed during the update; retry")
		return
	}
	if db.IsUnavailable(err) {
		// Transient: the database could not serve this, the request is not broken.
		w.Header().Set("Retry-After", "1")
		writeErr(w, http.StatusServiceUnavailable, "service unavailable")
		return
	}
	// Unknown error: mask it (Postgres errors never reach the client body) but
	// log the cause for the operator.
	a.logf("custom route %s %s: %v", rt.Method, rt.Path, err)
	writeErr(w, http.StatusInternalServerError, "internal error")
}

// writeErr writes {"error": msg} with the given status and JSON content type.
func writeErr(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}
