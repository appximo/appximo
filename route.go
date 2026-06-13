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
