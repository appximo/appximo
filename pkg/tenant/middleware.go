package tenant

import (
	"context"
	"encoding/json"
	"net/http"
	"regexp"
	"strings"
)

// tenantRe: starts and ends with alnum, allows hyphens in the middle, 2–30 chars total.
// This rejects single-char subdomains, trailing hyphens, and uppercase.
var tenantRe = regexp.MustCompile(`^[a-z0-9][a-z0-9\-]{0,28}[a-z0-9]$`)

// TenantMiddleware extracts the tenant from the request's Host subdomain.
//
//   - "acme.localhost:8080" → injects TenantCtx{ID:"acme", PGSchema:"tenant_acme"}
//   - "localhost:8080"      → passes through with no TenantCtx (health / control plane)
//   - anything invalid      → 400 {"error":"invalid tenant"}
func TenantMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host := r.Host

		// Strip port suffix.
		if idx := strings.LastIndex(host, ":"); idx != -1 {
			host = host[:idx]
		}

		// No dot → root host (e.g. "localhost"), treat as control-plane traffic.
		dotIdx := strings.Index(host, ".")
		if dotIdx == -1 {
			next.ServeHTTP(w, r)
			return
		}

		subdomain := host[:dotIdx]

		if !tenantRe.MatchString(subdomain) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]string{"error": "invalid tenant"})
			return
		}

		tc := &TenantCtx{
			ID:       subdomain,
			PGSchema: "tenant_" + subdomain,
		}
		ctx := context.WithValue(r.Context(), contextKey{}, tc)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
