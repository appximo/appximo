package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/appximo/appximo/pkg/observability"
	"github.com/appximo/appximo/pkg/tenant"
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
// "/editor" is the visual schema editor SPA (UI-F0-S1): a static client-side app
// (HTML/JS/CSS) served from the binary; the shell loads before any token exists,
// exactly like /admin.
// "/files/signed/" is the signed-token download route (FILES-V2): the HMAC token
// in the path IS the credential (short-lived, tenant-bound, role-rechecked by the
// handler; any failure is a uniform 404). A Bearer requirement would defeat the
// point — the URL exists so <img>/video tags and share links can fetch without an
// Authorization header.
// "/app" is the generic back-office SPA (ENG-38): the static shell + module
// files load before any token exists, exactly like /admin and /editor; the API
// calls the SPA makes carry the Bearer and keep full enforcement. Like /admin,
// the entry is a plain prefix — /app is an engine-owned prefix (reserved
// against static mounts in reservedStaticPrefixes), so nothing else may live
// under it.
var skipJWT = []string{"/health", "/readyz", "/graphiql", "/metrics", "/debug", "/admin", "/app", "/editor", "/auth/", "/favicon.ico", "/openapi", "/docs", "/files/signed/"}

// PublicMatcher reports whether (method, path) is an EXPLICITLY-registered
// public custom route (LIBRARY-EXTEND-S1: Route.Public). Matching is exact —
// method + literal path, never a prefix — so marking one route public can never
// widen the skip to a sibling. nil means "no public routes" (the default), and
// the middlewares below pay only a nil check for it.
type PublicMatcher func(method, path string) bool

// JWTMiddleware validates Bearer tokens on all routes except those in skipJWT.
// 401 is returned for missing or invalid tokens on enforced routes.
// Optional onError callback (tenantID, reason) is called on every 401 so callers
// can forward auth failures to an error store without importing auth from observability.
func JWTMiddleware(secret string, onError ...func(tenantID, reason string)) func(http.Handler) http.Handler {
	return JWTMiddlewareWithPublic(secret, nil, onError...)
}

// JWTMiddlewareWithStatic is JWTMiddlewareWithPublic plus a PER-APP predicate for
// user-declared static mounts (appximo.Config.Static, LOOSE-ENDS-SWEEP-S1). A
// frontend's HTML/JS loads before any token exists — exactly like /editor and
// /admin, which are in the fixed skipJWT list — but a user mount is only known at
// boot, so it is passed in rather than hardcoded.
//
// It is a PREDICATE rather than a prefix because a root-mounted SPA cannot be
// expressed as a prefix (the prefix "/" would disable authentication for /api),
// and a PARAMETER rather than package state so two apps in one fleet process
// never inherit each other's mounts. nil behaves byte-identically to
// JWTMiddlewareWithPublic.
func JWTMiddlewareWithStatic(secret string, isPublic PublicMatcher, isStatic func(path string) bool, onError ...func(tenantID, reason string)) func(http.Handler) http.Handler {
	return jwtMiddleware(secret, isPublic, isStatic, "", onError...)
}

// JWTMiddlewareWithAnonymous is JWTMiddlewareWithStatic plus the DECLARATIVE
// anonymous surface (ADR-026, PUBLIC-SURFACE-S1): when anonRole is non-empty
// (the app's schema declares rbac.public), a request with NO Authorization
// header on an otherwise-enforced route proceeds carrying synthetic claims
// {Role: anonRole, TenantID: <request tenant>} — downstream RBAC then decides
// per resource with the one existing evaluator, deny-by-default. The ENG-6
// rule holds unchanged: a PRESENT-but-invalid/expired/foreign-tenant Bearer is
// a 401, never a silent downgrade to anonymous. Empty anonRole is byte-
// identical to JWTMiddlewareWithStatic.
func JWTMiddlewareWithAnonymous(secret string, isPublic PublicMatcher, isStatic func(path string) bool, anonRole string, onError ...func(tenantID, reason string)) func(http.Handler) http.Handler {
	return jwtMiddleware(secret, isPublic, isStatic, anonRole, onError...)
}

// JWTMiddlewareWithPublic is JWTMiddleware plus OPTIONAL authentication for the
// custom routes explicitly registered as Public (appximo.Route{Public: true}).
// Public means "no token required", not "identity ignored" (LIBRARY-GAPS-S2,
// ENG-6): with no Authorization header the request proceeds anonymous (Claims
// zero / ClaimsFromCtx nil); with a VALID Bearer the claims are populated so a
// handler can personalize (a checkout that recognizes a logged-in customer);
// with a present-but-invalid, expired or tenant-mismatched Bearer the request
// is 401ed — sent credentials never silently degrade to anonymous. Everything
// not matched by isPublic keeps the full Bearer enforcement: deny-by-default is
// unchanged for every other route.
func JWTMiddlewareWithPublic(secret string, isPublic PublicMatcher, onError ...func(tenantID, reason string)) func(http.Handler) http.Handler {
	return jwtMiddleware(secret, isPublic, nil, "", onError...)
}

func jwtMiddleware(secret string, isPublic PublicMatcher, isStatic func(path string) bool, anonRole string, onError ...func(tenantID, reason string)) func(http.Handler) http.Handler {
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
	// resolveClaims validates a Bearer token string and applies the tenant-match
	// check — ONE implementation for the enforced path and the public
	// optional-auth path, so the two can never drift.
	resolveClaims := func(r *http.Request, tokenStr string) (*Claims, string) {
		claims, ok := GetCachedClaims(secret, tokenStr)
		if !ok {
			var err error
			claims, err = ValidateToken(tokenStr, secret)
			if err != nil {
				return nil, "invalid token: " + err.Error()
			}
			setCachedClaims(secret, tokenStr, claims)
		}
		// Reject tokens whose TenantID does not match the request tenant.
		// TenantMiddleware must run before JWTMiddleware for this check to fire.
		// The message names BOTH sides and where each came from (ENG-11). The bare
		// "token tenant mismatch" was measured in the field as a dead end: it names
		// neither the cause nor the fix, and the usual cause is not a wrong token —
		// it is a tenant registered under a name that is not the first label of the
		// domain serving it, which nothing warns about at registration time.
		if tc := tenant.FromCtx(r.Context()); tc != nil && claims.TenantID != "" && claims.TenantID != tc.ID {
			return nil, fmt.Sprintf(
				"token tenant mismatch: this request arrived at host %q, so it is for tenant %q, but the token was issued for tenant %q. "+
					"The tenant is taken from the FIRST part of the address (%q → tenant %q), so a tenant only works at <tenant-id>.<your-domain>. "+
					"Either use a token for %q, or register/reach the app at %s.%s",
				r.Host, tc.ID, claims.TenantID, r.Host, tc.ID, tc.ID, claims.TenantID, domainOf(r.Host))
		}
		return claims, ""
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Explicit public custom routes (exact method+path) — OPTIONALLY
			// authenticated (LIBRARY-GAPS-S2, ENG-6):
			//   no Authorization header → anonymous, Claims zero (as always);
			//   a valid Bearer          → Claims populated (identity is INPUT —
			//                             the route stays public, RBAC still skips);
			//   a present-but-invalid/expired/foreign-tenant Bearer → 401.
			// Garbage must not silently degrade to anonymous: the caller sent
			// credentials and deserves to know they were refused, so the client
			// can re-authenticate or deliberately retry without the token.
			if isPublic != nil && isPublic(r.Method, r.URL.Path) {
				header := r.Header.Get("Authorization")
				if header == "" {
					next.ServeHTTP(w, r)
					return
				}
				tok, ok := BearerToken(header)
				if !ok {
					reject(w, r, errUnsupportedScheme)
					return
				}
				claims, reason := resolveClaims(r, tok)
				if claims == nil {
					reject(w, r, reason)
					return
				}
				if t := observability.SpanTrackerFromCtx(r.Context()); t != nil {
					t.Mark("jwt")
				}
				noteIdentity(r.Context(), claims)
				next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), claimsKey{}, claims)))
				return
			}
			for _, p := range skipJWT {
				if strings.HasPrefix(r.URL.Path, p) {
					next.ServeHTTP(w, r)
					return
				}
			}
			// Per-app static mounts (LOOSE-ENDS-SWEEP-S1). nil for every app that
			// declares none — one nil check on the hot path.
			if isStatic != nil && isStatic(r.URL.Path) {
				next.ServeHTTP(w, r)
				return
			}

			header := r.Header.Get("Authorization")
			if header == "" {
				// The anonymous surface (ADR-026): tokenless ≠ rejected when the
				// schema declares rbac.public. The synthetic claims carry ONLY the
				// public role and the request tenant — no user id — and RBAC
				// evaluates them exactly like a real token's. The claims being
				// non-nil also means the X-User-Role test fallback in
				// EvalContextFromRequest can never engage on this path.
				if anonRole != "" {
					claims := &Claims{Role: anonRole}
					if tc := tenant.FromCtx(r.Context()); tc != nil {
						claims.TenantID = tc.ID
					}
					if t := observability.SpanTrackerFromCtx(r.Context()); t != nil {
						t.Mark("jwt")
					}
					noteIdentity(r.Context(), claims)
					next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), claimsKey{}, claims)))
					return
				}
				reject(w, r, "missing token")
				return
			}

			tok, ok := BearerToken(header)
			if !ok {
				reject(w, r, errUnsupportedScheme)
				return
			}

			claims, reason := resolveClaims(r, tok)
			if claims == nil {
				reject(w, r, reason)
				return
			}

			if t := observability.SpanTrackerFromCtx(r.Context()); t != nil {
				t.Mark("jwt")
			}
			noteIdentity(r.Context(), claims)
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

// domainOf returns the host minus its first DNS label (and minus any port) — the
// part a tenant subdomain is prefixed to. It exists so a tenant-mismatch error can
// show the caller the address their token WOULD work at, instead of describing the
// rule in the abstract.
func domainOf(host string) string {
	if i := strings.IndexByte(host, ':'); i >= 0 {
		host = host[:i]
	}
	if i := strings.IndexByte(host, '.'); i >= 0 {
		return host[i+1:]
	}
	return host
}

// noteIdentity writes the validated identity onto the request's span tracker,
// which downstream AND upstream code share by pointer — the request logger
// (which runs before this middleware and never sees the derived context) reads
// it from there, so the request line and every persisted trace name WHO
// (OBSERVABILIDAD-ERRORES-S1, Parte B.3).
func noteIdentity(ctx context.Context, claims *Claims) {
	if claims == nil {
		return
	}
	if t := observability.SpanTrackerFromCtx(ctx); t != nil {
		t.UserID, t.Role = claims.UserID, claims.Role
	}
}
