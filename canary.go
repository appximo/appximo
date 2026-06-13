package appitools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/miguelangel/appitools/pkg/auth"
	"github.com/miguelangel/appitools/pkg/observability"
	"github.com/miguelangel/appitools/pkg/schema"
)

// canaryResolver builds the synthetic monitor's API canary from the LOADED
// schema instead of hardcoded values: the probed resource is the first resource
// (sorted) unless APPITOOLS_SYNTHETIC_RESOURCE overrides it, the role is the
// first schema role that can read it, and the tenant is APPITOOLS_SYNTHETIC_TENANT
// or — lazily — the first registered tenant in public.tenants. Until a tenant
// exists the canary reports "pending"; once found, the tenant is cached and the
// JWT re-minted at half its 24h life so the canary survives long-running processes.
func canaryResolver(pool *pgxpool.Pool, s *schema.APISchema, jwtSecret string, port int) func(context.Context) (*observability.Check, string) {
	resource := os.Getenv("APPITOOLS_SYNTHETIC_RESOURCE")
	if resource == "" {
		resource = firstResourceName(s)
	}
	role := canaryRole(s, resource)
	envTenant := os.Getenv("APPITOOLS_SYNTHETIC_TENANT")

	var mu sync.Mutex
	var tenantID, token string
	var tokenMintedAt time.Time

	return func(ctx context.Context) (*observability.Check, string) {
		if resource == "" {
			return nil, "schema defines no resources"
		}
		if role == "" {
			return nil, "no schema role has read access to " + resource
		}

		mu.Lock()
		defer mu.Unlock()
		if tenantID == "" {
			tenantID = envTenant
		}
		if tenantID == "" {
			qctx, cancel := context.WithTimeout(ctx, 3*time.Second)
			defer cancel()
			if err := pool.QueryRow(qctx,
				"SELECT id FROM public.tenants ORDER BY created_at LIMIT 1").Scan(&tenantID); err != nil {
				return nil, "no tenant registered yet"
			}
		}
		if token == "" || time.Since(tokenMintedAt) > 12*time.Hour {
			t, err := auth.GenerateToken(auth.Claims{
				UserID:   "synthetic-monitor",
				Role:     role,
				TenantID: tenantID,
			}, jwtSecret)
			if err != nil {
				return nil, "token mint failed: " + err.Error()
			}
			token = t
			tokenMintedAt = time.Now()
		}

		return &observability.Check{
			Method: "GET",
			URL:    fmt.Sprintf("http://localhost:%d/api/%s?per_page=1", port, resource),
			Headers: map[string]string{
				"Host":          tenantID + ".localhost",
				"Authorization": "Bearer " + token,
			},
			Expected: 200,
		}, ""
	}
}

// firstResourceName returns the alphabetically first resource of the schema.
func firstResourceName(s *schema.APISchema) string {
	names := make([]string, 0, len(s.Resources))
	for n := range s.Resources {
		names = append(names, n)
	}
	if len(names) == 0 {
		return ""
	}
	sort.Strings(names)
	return names[0]
}

// canaryRole returns the alphabetically first role whose policy can read resource.
func canaryRole(s *schema.APISchema, resource string) string {
	names := make([]string, 0, len(s.RBAC.Roles))
	for n := range s.RBAC.Roles {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		if roleGrantsRead(s.RBAC.Roles[n], resource) {
			return n
		}
	}
	return ""
}

// roleGrantsRead reports whether the policy allows read (or *) on resource.
func roleGrantsRead(rp schema.RolePolicy, resource string) bool {
	action := false
	for _, a := range rp.Actions {
		if a == "*" || a == "read" {
			action = true
			break
		}
	}
	if !action {
		return false
	}
	var star string
	if json.Unmarshal(rp.Resources, &star) == nil {
		return star == "*"
	}
	var list []string
	if json.Unmarshal(rp.Resources, &list) == nil {
		for _, r := range list {
			if r == resource {
				return true
			}
		}
	}
	return false
}
