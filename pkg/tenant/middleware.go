package tenant

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"
)

// tenantRe: starts and ends with alnum, allows hyphens in the middle, 2–30 chars total.
// This rejects single-char subdomains and trailing hyphens. It is applied to the
// LOWERCASED host, so an upper/mixed-case label is normalised rather than
// rejected (RFC 9110 §4.2.3) — see the fold in MiddlewareWithBareHosts.
var tenantRe = regexp.MustCompile(`^[a-z0-9][a-z0-9\-]{0,28}[a-z0-9]$`)

// tenantRuleHint states tenantRe in words for the 400 body. It is kept next to
// the regex so the two cannot drift: an error that describes a rule the code no
// longer enforces is worse than no error at all.
const tenantRuleHint = "2-30 characters of lowercase letters, digits or hyphens, starting and ending with a letter or digit"

// isValidSubdomain reports whether s is a syntactically valid tenant subdomain.
// The Host header is fully client-controlled, so this MUST NOT memoize results in
// an unbounded map keyed by s — an attacker rotating the subdomain would grow it
// without limit (pre-auth memory exhaustion). The anchored regex is linear and
// cheap (sub-microsecond), so it is evaluated directly on every request.
func isValidSubdomain(s string) bool {
	return tenantRe.MatchString(s)
}

// TenantMiddleware extracts the tenant from the request's Host subdomain.
//
//   - "acme.localhost:8080" → injects TenantCtx{ID:"acme", PGSchema:"tenant_acme"}
//   - "ACME.localhost:8080" → the same tenant (the host is case-insensitive)
//   - "localhost:8080"      → passes through with no TenantCtx (health / control plane)
//   - anything invalid      → 400 naming the offending label and the rule
func TenantMiddleware(next http.Handler) http.Handler {
	return MiddlewareWithBareHosts(nil)(next)
}

// MiddlewareWithBareHosts is TenantMiddleware for a server that KNOWS its own
// hostnames (the fleet runtimes: an app's manifest `domains`). A request whose
// Host is EXACTLY one of bare (no tenant label in front) passes through with
// no TenantCtx — like a bare "localhost" — instead of mis-reading the domain's
// first label as a tenant. Before this, `GET erp.example.com/admin` recorded a
// phantom tenant "erp" in observability (the S1 finding); the domain's OWN
// first label is not a tenant.
//
// With bare empty this is byte-identical to the historical middleware — the
// single-engine chain (which has no domain knowledge) is unchanged, and the
// fleet chain adds one map lookup on a pre-parsed string (~ns, pre-auth).
func MiddlewareWithBareHosts(bare []string) func(http.Handler) http.Handler {
	var bareSet map[string]struct{}
	if len(bare) > 0 {
		bareSet = make(map[string]struct{}, len(bare))
		for _, h := range bare {
			// Store hosts port-less, exactly how the request Host is compared.
			if idx := strings.LastIndex(h, ":"); idx != -1 {
				h = h[:idx]
			}
			bareSet[strings.ToLower(h)] = struct{}{}
		}
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// RFC 9110 §4.2.3 makes the host case-INSENSITIVE, and DNS resolves
			// ACME.example.com and acme.example.com to the same place. This used
			// to compare the raw label, so `Host: ACME.example.com` — a legal
			// request for a real tenant — was answered 400 "invalid tenant"
			// (verified live before the fix, ADR-024).
			//
			// Normalising is safe rather than merely lenient: a tenant id is
			// registered through the control plane, which enforces
			// ^[a-z][a-z0-9]{1,29}$, so the lowercase form is the ONLY form that
			// can name a real tenant. Folding therefore maps an upper/mixed-case
			// host onto the same tenant_<id> schema the lowercase host resolves
			// to — never a different one. ToLower does not allocate when the
			// string is already lowercase, which is every real request.
			host := strings.ToLower(r.Host)

			// Strip port suffix.
			if idx := strings.LastIndex(host, ":"); idx != -1 {
				host = host[:idx]
			}

			// The app's own bare domain → app-level traffic (console, admin,
			// docs, probes), NOT a tenant. Only exact matches: a subdomain of
			// the app domain still resolves its first label as the tenant.
			if _, ok := bareSet[host]; ok {
				next.ServeHTTP(w, r)
				return
			}

			// No dot → root host (e.g. "localhost"), treat as control-plane traffic.
			dotIdx := strings.Index(host, ".")
			if dotIdx == -1 {
				next.ServeHTTP(w, r)
				return
			}

			subdomain := host[:dotIdx]

			if !isValidSubdomain(subdomain) {
				// Name the offending label AND the rule (ADR-024). "invalid
				// tenant" alone left the caller to guess which of the host, the
				// token and the tenant was wrong — and the rule is not
				// guessable: an underscore is legal in a Postgres schema name
				// and illegal in a DNS label, which is exactly the trap ENG-11
				// documented.
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusBadRequest)
				json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
					"error": fmt.Sprintf("invalid tenant %q in host %q: a tenant subdomain must match %s",
						subdomain, host, tenantRuleHint),
				})
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
}
