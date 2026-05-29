package rbac

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/miguelangel/appitools/pkg/auth"
)

type evalResultKey struct{}

// RBACMiddleware returns a chi-compatible middleware that enforces the given
// policy JSON. It reads X-User-Role and X-User-ID from request headers, maps
// the HTTP method to a CRUD action, and injects the EvalResult into context.
// Requests to paths outside /api/ are passed through without enforcement.
func RBACMiddleware(policyJSON []byte) func(http.Handler) http.Handler {
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
			resource := resourceFromPath(r.URL.Path)
			if resource == "" {
				// Not an /api/ route — pass through.
				next.ServeHTTP(w, r)
				return
			}

			action := actionFromMethod(r.Method)

			// Prefer JWT claims injected by JWTMiddleware; fall back to
			// explicit headers so integration tests can run without a full JWT stack.
			evalCtx := EvalContext{
				Role:             r.Header.Get("X-User-Role"),
				UserID:           r.Header.Get("X-User-ID"),
				ExternalClientID: r.Header.Get("X-External-Client-ID"),
			}
			if claims := auth.ClaimsFromCtx(r.Context()); claims != nil {
				evalCtx = EvalContext{
					Role:             claims.Role,
					UserID:           claims.UserID,
					ExternalClientID: claims.ExternalClientID,
				}
			}

			result := policy.Evaluate(evalCtx, resource, action)
			if !result.Allowed {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusForbidden)
				json.NewEncoder(w).Encode(map[string]string{"error": "forbidden"})
				return
			}

			ctx := context.WithValue(r.Context(), evalResultKey{}, result)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
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

// resourceFromPath extracts the resource name from paths like /api/guides or /api/guides/{id}.
func resourceFromPath(path string) string {
	parts := strings.Split(strings.TrimPrefix(path, "/"), "/")
	if len(parts) >= 2 && parts[0] == "api" && parts[1] != "" {
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
