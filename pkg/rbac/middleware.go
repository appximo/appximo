package rbac

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/miguelangel/appitools/pkg/auth"
	"github.com/miguelangel/appitools/pkg/observability"
)

type evalResultKey struct{}

// RBACMiddleware returns a chi-compatible middleware that enforces the given
// policy JSON. It reads X-User-Role and X-User-ID from request headers, maps
// the HTTP method to a CRUD action, and injects the EvalResult into context.
// Requests to paths outside /api/ are passed through without enforcement.
func RBACMiddleware(policyJSON []byte) func(http.Handler) http.Handler {
	return RBACMiddlewareWithPublic(policyJSON, nil)
}

// RBACMiddlewareWithPublic is RBACMiddleware plus an exact-match pass-through
// for the custom routes explicitly registered as Public (appitools.Route
// {Public: true}): an anonymous request has no role, so path-based enforcement
// would deny-by-default every public route. The pass-through injects NO
// EvalResult — inside the handler, the Ctx's RBAC-aware helpers (Query/Insert/
// Update) still evaluate the (empty) role themselves and fail closed; only
// deliberate anonymous logic (UnsafeTx, CreateUser) proceeds. Everything not
// matched by isPublic keeps full enforcement.
func RBACMiddlewareWithPublic(policyJSON []byte, isPublic func(method, path string) bool) func(http.Handler) http.Handler {
	var policy Policy
	if err := json.Unmarshal(policyJSON, &policy); err != nil {
		return func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusInternalServerError)
				json.NewEncoder(w).Encode(map[string]string{"error": "invalid policy configuration"})
			})
		}
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if isPublic != nil && isPublic(r.Method, r.URL.Path) {
				// Explicit public custom route — anonymous, no EvalResult.
				next.ServeHTTP(w, r)
				return
			}
			resource := resourceFromPath(r.URL.Path)
			if resource == "" {
				// Not an /api/ route — pass through.
				next.ServeHTTP(w, r)
				return
			}

			action := actionFromMethod(r.Method)

			result := policy.Evaluate(EvalContextFromRequest(r), resource, action)
			if !result.Allowed {
				// Mark the rbac stage so a persisted 403 trace shows it reached
				// (and was stopped at) RBAC (no stack — 403 is a client error).
				if t := observability.SpanTrackerFromCtx(r.Context()); t != nil {
					t.Mark("rbac")
					t.RecordError("rbac: forbidden")
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusForbidden)
				json.NewEncoder(w).Encode(map[string]string{"error": "forbidden"})
				return
			}

			if t := observability.SpanTrackerFromCtx(r.Context()); t != nil {
				t.Mark("rbac")
			}
			ctx := context.WithValue(r.Context(), evalResultKey{}, result)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// EvalContextFromRequest resolves the caller identity for a request: the JWT claims
// injected by JWTMiddleware when present, else the X-User-* headers (so integration
// tests can run without a full JWT stack). This is the SINGLE identity-resolution
// used by both the route RBAC middleware AND the relation/subresource read checks
// (codegen.makeRelationRBAC), so every authorization path scopes by the same
// principal.
func EvalContextFromRequest(r *http.Request) EvalContext {
	if claims := auth.ClaimsFromCtx(r.Context()); claims != nil {
		return EvalContext{
			Role:             claims.Role,
			UserID:           claims.UserID,
			ExternalClientID: claims.ExternalClientID,
		}
	}
	return EvalContext{
		Role:             r.Header.Get("X-User-Role"),
		UserID:           r.Header.Get("X-User-ID"),
		ExternalClientID: r.Header.Get("X-External-Client-ID"),
	}
}

// EvalResultFromCtx retrieves the EvalResult injected by RBACMiddleware.
// Returns nil if the middleware was not applied or the path was not enforced.
func EvalResultFromCtx(ctx context.Context) *EvalResult {
	v, ok := ctx.Value(evalResultKey{}).(EvalResult)
	if !ok {
		return nil
	}
	return &v
}

// TransactionRoute is the reserved single segment of the atomic multi-resource
// transaction endpoint (G4): POST /api/transaction. It is NOT a resource — the
// handler authorizes EACH operation in the batch against its own resource (so a
// restricted role may transact over the resources it is allowed). The middleware
// therefore passes this path through (no single-resource RBAC check here); per-op
// RBAC is enforced in the handler. A schema resource may not be named "transaction"
// (reserved at load), so this never shadows a real resource's policy.
const TransactionRoute = "transaction"

// resourceFromPath extracts the resource name from paths like /api/guides or
// /api/guides/{id}. Returns "" for non-/api paths AND for the reserved
// /api/transaction batch endpoint (which does its own per-operation RBAC), so the
// middleware enforces nothing there and the handler authorizes each op itself.
func resourceFromPath(path string) string {
	parts := strings.Split(strings.TrimPrefix(path, "/"), "/")
	if len(parts) >= 2 && parts[0] == "api" && parts[1] != "" && parts[1] != TransactionRoute {
		return parts[1]
	}
	return ""
}

// actionFromMethod maps HTTP verbs to the CRUD action names used in policies.
func actionFromMethod(method string) string {
	switch method {
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
