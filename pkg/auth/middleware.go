package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/miguelangel/appitools/pkg/tenant"
)

type claimsKey struct{}

// ClaimsFromCtx retrieves the Claims injected by JWTMiddleware.
// Returns nil when the middleware was not applied or the path was not enforced.
func ClaimsFromCtx(ctx context.Context) *Claims {
	v, _ := ctx.Value(claimsKey{}).(*Claims)
	return v
}

// skipJWT lists path prefixes that bypass JWT enforcement.
var skipJWT = []string{"/health", "/graphiql"}

// JWTMiddleware validates Bearer tokens on all routes except those in skipJWT.
// 401 is returned for missing or invalid tokens on enforced routes.
// Optional onError callback (tenantID, reason) is called on every 401 so callers
// can forward auth failures to an error store without importing auth from observability.
func JWTMiddleware(secret string, onError ...func(tenantID, reason string)) func(http.Handler) http.Handler {
	var recordErr func(tenantID, reason string)
	if len(onError) > 0 {
		recordErr = onError[0]
	}
	reject := func(w http.ResponseWriter, r *http.Request, reason string) {
		if recordErr != nil {
			tenantID := ""
			if tc := tenant.FromCtx(r.Context()); tc != nil {
				tenantID = tc.ID
			}
			recordErr(tenantID, reason)
		}
		writeJSON401(w, reason)
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			for _, p := range skipJWT {
				if strings.HasPrefix(r.URL.Path, p) {
					next.ServeHTTP(w, r)
					return
				}
			}

			header := r.Header.Get("Authorization")
			if header == "" {
				reject(w, r, "missing token")
				return
			}

			const prefix = "Bearer "
			if !strings.HasPrefix(header, prefix) {
				reject(w, r, "invalid token")
				return
			}

			tokenStr := strings.TrimPrefix(header, prefix)

			claims, ok := GetCachedClaims(tokenStr)
			if !ok {
				var err error
				claims, err = ValidateToken(tokenStr, secret)
				if err != nil {
					reject(w, r, "invalid token: "+err.Error())
					return
				}
				setCachedClaims(tokenStr, claims)
			}

			// Reject tokens whose TenantID does not match the request tenant.
			// TenantMiddleware must run before JWTMiddleware for this check to fire.
			if tc := tenant.FromCtx(r.Context()); tc != nil && claims.TenantID != "" && claims.TenantID != tc.ID {
				reject(w, r, "token tenant mismatch")
				return
			}

			ctx := context.WithValue(r.Context(), claimsKey{}, claims)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func writeJSON401(w http.ResponseWriter, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnauthorized)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}
