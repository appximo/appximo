package appitools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/miguelangel/appitools/pkg/auth"
	"github.com/miguelangel/appitools/pkg/db"
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
	// Inside the handler Claims() is zero (no identity), so the RBAC-aware
	// helpers (Query/Insert/Update) fail closed with forbidden — anonymous
	// writes go through the APIs that carry their own rules (CreateUser) or
	// through a deliberate, greppable UnsafeTx. Public routes must use literal
	// paths (no chi {params} — the skip is an exact match) and cannot combine
	// with RequireRole (a role implies authentication). Only routes explicitly
	// marked Public skip auth; every other route keeps deny-by-default.
	Public bool
}

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

// customHandler wraps a Route's Handler in the withTenantTx pattern (ADR-016
// Decision 4): it runs AFTER the shared middleware chain (so identity, tenant
// and RBAC are already resolved), opens a transaction scoped to the tenant
// search_path via set_config(...,true), builds the Ctx, invokes the Handler,
// and commits on nil / rolls back on error. It never re-implements JWT, rate
// limiting, or path-based RBAC — those are inherited from the chain.
func (a *App) customHandler(rt Route) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tc := tenant.FromCtx(r.Context())
		if tc == nil || !pgSchemaNameRe.MatchString(tc.PGSchema) {
			writeErr(w, http.StatusBadRequest, "invalid tenant")
			return
		}
		if rt.Public {
			// Dedicated public-route throttle, per (tenant, client IP), far
			// tighter than the per-tenant limiter that already ran — an
			// anonymous endpoint must not be a free abuse vector. Before the
			// transaction, so a throttled burst never touches the pool.
			if !a.publicLimiter.Allow(tc.ID+"|"+remoteIP(r), "") {
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

		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
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

		rc := &requestCtx{w: w, r: r, tx: tx, eng: a.eng, tc: tc, cl: cl}
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
	if db.IsUnavailable(err) {
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
