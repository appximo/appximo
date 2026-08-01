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

// tenantRuleHint describes the rule that can actually produce a WORKING tenant,
// which is NOT this file's regex.
//
// tenantRe is the DNS-label alphabet: it tolerates hyphens and a leading digit,
// because the middleware's job is only to reject something that cannot be a host
// label. But a tenant is registrable only under the control plane's stricter
// ^[a-z][a-z0-9]{1,29}$ — the INTERSECTION of the DNS label and the Postgres
// schema name. The first version of this hint recited tenantRe and so told the
// caller that `my-shop` and `7eleven` were fine; registration refuses both.
//
// That is exactly the two-alphabet trap ENG-11 documented, where the damage was
// that Studio recommended an id that could never work. An error message that
// suggests an invalid fix is worse than one that suggests nothing, so this states
// the registrable rule and the middleware stays deliberately more permissive than
// what it describes.
const tenantRuleHint = "2-30 characters, starting with a lowercase letter and continuing with lowercase letters or digits (no hyphens, underscores or uppercase)"

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
				// Name the offending label AND the rule (ADR-024). A bare
				// "invalid tenant" left the caller to guess which of the host,
				// the token and the tenant was wrong — and the rule is not
				// guessable: an underscore is legal in a Postgres schema name and
				// illegal in a DNS label, which is exactly the trap ENG-11
				// documented.
				//
				// But echo the LABEL only, truncated, never the whole host. This
				// path is PRE-AUTH and un-rate-limited, and the Host is entirely
				// client-controlled up to Go's 1 MiB header limit, so reflecting
				// it — twice, as the first version of this did — turns a 27-byte
				// constant response into a ~2 MiB amplifier any anonymous client
				// can drive in a loop. The diagnostic value is in the rule plus
				// the first characters of the label; the caller already knows
				// what they sent.
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusBadRequest)
				json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
					"error": fmt.Sprintf("invalid tenant %q: a tenant subdomain must match %s",
						truncateLabel(subdomain), tenantRuleHint),
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

// maxEchoedLabel bounds how much of a client-controlled host label is reflected
// in an error body. A valid tenant is at most 30 characters, so anything longer
// is already invalid and its tail carries no diagnostic value.
const maxEchoedLabel = 32

// truncateLabel returns s bounded to maxEchoedLabel runes, marking that it was
// cut. It is rune-safe so a multi-byte label cannot be sliced mid-character into
// invalid UTF-8 in the response body.
func truncateLabel(s string) string {
	r := []rune(s)
	if len(r) <= maxEchoedLabel {
		return s
	}
	return string(r[:maxEchoedLabel]) + "…"
}
