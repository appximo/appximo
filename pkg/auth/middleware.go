package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
)

type claimsKey struct{}

// ClaimsFromCtx retrieves the Claims injected by JWTMiddleware.
// Returns nil when the middleware was not applied or the path was not enforced.
func ClaimsFromCtx(ctx context.Context) *Claims {
	v, _ := ctx.Value(claimsKey{}).(*Claims)
	return v
}

// JWTMiddleware validates Bearer tokens on /api/* routes.
// Paths outside /api/ (e.g. /health) pass through without validation.
// 401 is returned for missing or invalid tokens on enforced routes.
func JWTMiddleware(secret string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !strings.HasPrefix(r.URL.Path, "/api/") {
				next.ServeHTTP(w, r)
				return
			}

			header := r.Header.Get("Authorization")
			if header == "" {
				writeJSON401(w, "missing token")
				return
			}

			const prefix = "Bearer "
			if !strings.HasPrefix(header, prefix) {
				writeJSON401(w, "invalid token")
				return
			}

			claims, err := ValidateToken(strings.TrimPrefix(header, prefix), secret)
			if err != nil {
				writeJSON401(w, "invalid token")
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
