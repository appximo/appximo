package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/miguelangel/appitools/pkg/observability"
	"github.com/miguelangel/appitools/pkg/tenant"
)

type claimsKey struct{}

// ClaimsFromCtx retrieves the Claims injected by JWTMiddleware.
// Returns nil when the middleware was not applied or the path was not enforced.
func ClaimsFromCtx(ctx context.Context) *Claims {
	v, _ := ctx.Value(claimsKey{}).(*Claims)
	return v
}

// HookUserContext builds the `user` binding passed to a lifecycle hook from the
// request's JWT claims (SEC-AUDIT-V2 Hallazgo B): so a before_create/before_update
// js/wasm hook can see WHO performed the operation (`user.user_id`, `user.role`,
// `user.tenant_id`). Returns nil when there are no claims (e.g. an internal call
// without a JWT) — the hook's `user` is then nil, the pre-fix behavior. The SAME
// shape is used on REST and GraphQL so a hook is portable across both surfaces.
func HookUserContext(ctx context.Context) map[string]any {
	c := ClaimsFromCtx(ctx)
	if c == nil {
		return nil
	}
	uc := map[string]any{
		"user_id":   c.UserID,
		"role":      c.Role,
		"tenant_id": c.TenantID,
	}
	if c.ExternalClientID != "" {
		uc["external_client_id"] = c.ExternalClientID
	}
	return uc
}

// skipJWT lists path prefixes that bypass JWT enforcement.
// /metrics, /debug and /admin carry their own admin-key gate (observability.AdminAuth),
// so JWT is not enforced on them — they must never require a tenant Bearer token.
// /health (covers /healthz) and /readyz are infra liveness/readiness probes: they
// MUST be reachable without a token, otherwise the load balancer polling /readyz
// during graceful drain only ever sees 401 and the drain handshake never works.
// "/auth/" is the password identity core (AUTH-CORE-V1): signup/login/refresh
// happen BEFORE a token exists, so they are unauthenticated (but tenant-aware via
// Host). The trailing slash keeps the prefix exact — it never matches an /api/
// resource (those live under /api/) nor a hypothetical "/authors".
// "/favicon.ico" is the admin panel's icon (a static asset the browser probes at
// the origin root before any token exists) — it must not 401 (ADMIN-UI-V1.2).
// "/openapi" (the generated spec) and "/docs" (Swagger UI) are the PUBLIC API
// contract + its explorer (API-PRODUCTIVA-V1): the schema surface is the same for
// every tenant of this engine, so the spec is engine-global and unauthenticated —
// a consumer reads the contract before it has a token.
var skipJWT = []string{"/health", "/readyz", "/graphiql", "/metrics", "/debug", "/admin", "/auth/", "/favicon.ico", "/openapi", "/docs"}

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
		// Record that the request reached (and was stopped at) the jwt stage, so a
		// persisted 401 trace shows the pipeline cut here (with the reason — no stack,
		// a 401 is a client error, not a server bug).
		if t := observability.SpanTrackerFromCtx(r.Context()); t != nil {
			t.Mark("jwt")
			t.RecordError("jwt: " + reason)
		}
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

			if t := observability.SpanTrackerFromCtx(r.Context()); t != nil {
				t.Mark("jwt")
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
