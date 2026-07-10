package platformadmin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/miguelangel/appitools/pkg/controlplane"
	"github.com/miguelangel/appitools/pkg/schema"
	"github.com/miguelangel/appitools/pkg/userauth"
)

// tenantIDRe is the LOOKUP/DELETE-side id guard (defence in depth before
// interpolating a schema name — every use is Sanitize()-quoted anyway). It is
// deliberately LOOSER than the creation rule (controlplane's tenantIDRe,
// ^[a-z][a-z0-9_]{1,29}$): it still accepts hyphens so that legacy/broken
// tenants created under the old permissive rule (e.g. "punto-gafas-v1", the
// zombie bug) remain DELETABLE through the admin API and CLI. Do not tighten
// it to the creation rule — that would strand exactly the tenants that most
// need deleting.
var tenantIDRe = regexp.MustCompile(`^[a-z0-9][a-z0-9\-_]{1,29}$`)

// TenantInfo is the per-tenant summary returned by the tenant list. resource_count
// is derived from the stored schema (free); data_rows and user_count are pg_stat
// n_live_tup ESTIMATES (the same inventory `appitools tenant list` prints) — free
// at list time, never a per-tenant exact COUNT.
type TenantInfo struct {
	ID            string    `json:"id"`
	DisplayName   string    `json:"display_name"`
	Email         string    `json:"email"`
	Plan          string    `json:"plan"`
	Suspended     bool      `json:"suspended"`
	CreatedAt     time.Time `json:"created_at"`
	ResourceCount int       `json:"resource_count"`
	DataRows      int64     `json:"data_rows"`
	UserCount     int64     `json:"user_count"`
}

// TenantDetail is a single tenant; its UserCount is exact (one per-tenant query),
// unlike the list's estimate.
type TenantDetail struct {
	TenantInfo
}

// ListTenants returns every registered tenant (newest first) with cheap metadata.
// Row/user counts come from pg_stat_user_tables estimates (auth_ tables split out
// of data_rows), exactly like the `appitools tenant list` CLI inventory.
func (s *Service) ListTenants(ctx context.Context) ([]TenantInfo, error) {
	if err := s.ensureSuspendCol(ctx); err != nil {
		return nil, fmt.Errorf("platformadmin: ensure suspended column: %w", err)
	}
	rows, err := s.pool.Query(ctx, `
		SELECT t.id, t.display_name, t.email, t.plan, t.suspended, t.created_at, t.json_schema,
		       COALESCE((SELECT SUM(s.n_live_tup) FROM pg_stat_user_tables s
		           WHERE s.schemaname = 'tenant_'||t.id
		             AND s.relname NOT LIKE 'auth\_%' AND s.relname <> 'files'), 0) AS data_rows,
		       COALESCE((SELECT SUM(s.n_live_tup) FROM pg_stat_user_tables s
		           WHERE s.schemaname = 'tenant_'||t.id
		             AND s.relname = 'auth_users'), 0)                              AS user_count
		FROM public.tenants t ORDER BY t.created_at DESC`)
	if err != nil {
		return nil, fmt.Errorf("platformadmin: list tenants: %w", err)
	}
	defer rows.Close()
	out := []TenantInfo{}
	for rows.Next() {
		var ti TenantInfo
		var raw []byte
		if err := rows.Scan(&ti.ID, &ti.DisplayName, &ti.Email, &ti.Plan, &ti.Suspended, &ti.CreatedAt, &raw, &ti.DataRows, &ti.UserCount); err != nil {
			return nil, fmt.Errorf("platformadmin: scan tenant: %w", err)
		}
		ti.ResourceCount = countResources(raw)
		out = append(out, ti)
	}
	return out, rows.Err()
}

// GetTenant returns one tenant's detail, including a live user count.
func (s *Service) GetTenant(ctx context.Context, id string) (TenantDetail, error) {
	if err := s.ensureSuspendCol(ctx); err != nil {
		return TenantDetail{}, fmt.Errorf("platformadmin: ensure suspended column: %w", err)
	}
	var ti TenantInfo
	var raw []byte
	err := s.pool.QueryRow(ctx, `
		SELECT id, display_name, email, plan, suspended, created_at, json_schema
		FROM public.tenants WHERE id = $1`, id).
		Scan(&ti.ID, &ti.DisplayName, &ti.Email, &ti.Plan, &ti.Suspended, &ti.CreatedAt, &raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return TenantDetail{}, ErrTenantNotFound
	}
	if err != nil {
		return TenantDetail{}, fmt.Errorf("platformadmin: get tenant: %w", err)
	}
	ti.ResourceCount = countResources(raw)
	detail := TenantDetail{TenantInfo: ti}
	if n, cerr := s.users.CountUsers(ctx, id); cerr == nil {
		detail.UserCount = int64(n) // exact, replacing the list's estimate
	}
	return detail, nil
}

// CreateTenant wraps the EXISTING control plane (controlplane.Register) — it does
// not reimplement tenant provisioning. The schema is validated identically to the
// :9090 path before provisioning.
func (s *Service) CreateTenant(ctx context.Context, req controlplane.RegisterRequest) (*controlplane.Tenant, error) {
	return s.cp.Register(ctx, req)
}

// SuspendTenant / ActivateTenant flip the control-plane suspended flag. Suspension
// blocks NEW logins for the tenant's users (enforced on the non-hot login path via
// the TenantActive predicate wired in app.go); already-issued JWTs remain valid
// until exp (the documented stateless-JWT trade-off). It deliberately adds NO
// per-request check to the CRUD/JWT hot path, preserving the measured p50.
func (s *Service) SuspendTenant(ctx context.Context, id string) error {
	return s.setSuspended(ctx, id, true)
}

// ActivateTenant clears the suspension flag.
func (s *Service) ActivateTenant(ctx context.Context, id string) error {
	return s.setSuspended(ctx, id, false)
}

func (s *Service) setSuspended(ctx context.Context, id string, suspended bool) error {
	if err := s.ensureSuspendCol(ctx); err != nil {
		return fmt.Errorf("platformadmin: ensure suspended column: %w", err)
	}
	tag, err := s.pool.Exec(ctx, `UPDATE public.tenants SET suspended = $2, updated_at = now() WHERE id = $1`, id, suspended)
	if err != nil {
		return fmt.Errorf("platformadmin: set suspended: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrTenantNotFound
	}
	return nil
}

// IsTenantActive reports whether a tenant exists and is not suspended. It is the
// predicate wired into userauth login (TenantActive): a suspended tenant cannot
// mint new sessions. One PK lookup, on the login path only (never the CRUD hot
// path). A tenant with no public.tenants row is treated as active (login then
// fails on the missing user anyway) to avoid coupling the engine's
// naming-convention tenant model to a hard registry check.
func (s *Service) IsTenantActive(ctx context.Context, id string) bool {
	if err := s.ensureSuspendCol(ctx); err != nil {
		return true // fail open: a column-ensure hiccup must not lock everyone out
	}
	var suspended bool
	err := s.pool.QueryRow(ctx, `SELECT suspended FROM public.tenants WHERE id = $1`, id).Scan(&suspended)
	if errors.Is(err, pgx.ErrNoRows) {
		return true
	}
	if err != nil {
		return true
	}
	return !suspended
}

// DeleteTenant DESTRUCTIVELY removes a tenant: it requires an explicit confirmation
// equal to the tenant id (so a stray DELETE can never wipe a tenant), then drops
// the tenant's Postgres schema (CASCADE — all its data) and deletes its control-
// plane rows. There is no undo.
func (s *Service) DeleteTenant(ctx context.Context, id, confirm string) error {
	if !tenantIDRe.MatchString(id) {
		return ErrTenantNotFound
	}
	if confirm != id {
		return ErrConfirmRequired
	}
	// Verify it exists first (a clear 404 instead of a silent no-op).
	var exists bool
	if err := s.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM public.tenants WHERE id = $1)`, id).Scan(&exists); err != nil {
		return fmt.Errorf("platformadmin: check tenant: %w", err)
	}
	if !exists {
		return ErrTenantNotFound
	}
	pgSchema := pgx.Identifier{"tenant_" + id}.Sanitize()
	if _, err := s.pool.Exec(ctx, fmt.Sprintf("DROP SCHEMA IF EXISTS %s CASCADE", pgSchema)); err != nil {
		return fmt.Errorf("platformadmin: drop schema: %w", err)
	}
	// Best-effort cleanup of EVERY control-plane table keyed by tenant_id (the
	// schema is already gone; some of these tables are created lazily and may
	// not exist in a given deployment — an error here must not abort the
	// delete). DEV-CLEANUP-S1 extended this list to schema_history, flow
	// tests/runs and outbox: a deleted tenant used to leave orphaned rows there.
	for _, q := range []string{
		`DELETE FROM public.tenant_policies WHERE tenant_id = $1`,
		`DELETE FROM public.migration_log WHERE tenant_id = $1`,
		`DELETE FROM public.schema_history WHERE tenant_id = $1`,
		`DELETE FROM public.flow_runs WHERE tenant_id = $1`,
		`DELETE FROM public.flow_tests WHERE tenant_id = $1`,
		`DELETE FROM public.outbox WHERE tenant_id = $1`,
	} {
		s.pool.Exec(ctx, q, id) //nolint:errcheck
	}
	if _, err := s.pool.Exec(ctx, `DELETE FROM public.tenants WHERE id = $1`, id); err != nil {
		return fmt.Errorf("platformadmin: delete tenant row: %w", err)
	}
	return nil
}

// --- tenant user management (delegates to the existing userauth.Store) -------

// PublicTenantUser is a tenant user as seen by the admin API (no hash).
type PublicTenantUser struct {
	ID            string    `json:"id"`
	Email         string    `json:"email"`
	Role          string    `json:"role"`
	EmailVerified bool      `json:"email_verified"`
	Suspended     bool      `json:"suspended"`
	CreatedAt     time.Time `json:"created_at"`
}

func toPublicTenantUser(u userauth.User) PublicTenantUser {
	return PublicTenantUser{
		ID: u.ID, Email: u.Email, Role: u.Role, EmailVerified: u.EmailVerified,
		Suspended: u.Suspended, CreatedAt: u.CreatedAt,
	}
}

// ListUsers returns a tenant's users.
func (s *Service) ListUsers(ctx context.Context, tenantID string) ([]PublicTenantUser, error) {
	us, err := s.users.ListUsers(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	out := make([]PublicTenantUser, 0, len(us))
	for _, u := range us {
		out = append(out, toPublicTenantUser(u))
	}
	return out, nil
}

// CreateUser creates a tenant user with an admin-chosen role (validated against the
// schema RBAC). Unlike public signup, the role is explicit.
func (s *Service) CreateUser(ctx context.Context, tenantID, email, password, role string) (PublicTenantUser, error) {
	email = normalizeEmail(email)
	if !validEmail(email) {
		return PublicTenantUser{}, ErrInvalidEmail
	}
	if len([]rune(password)) < 8 {
		return PublicTenantUser{}, ErrWeakPassword
	}
	if err := s.checkRole(role); err != nil {
		return PublicTenantUser{}, err
	}
	hash, err := userauth.HashPassword(password)
	if err != nil {
		return PublicTenantUser{}, err
	}
	u, err := s.users.CreateUserWithRole(ctx, tenantID, email, hash, role)
	if err != nil {
		return PublicTenantUser{}, err
	}
	u.Email = email
	return toPublicTenantUser(u), nil
}

// UpdateUserRole changes a tenant user's role (validated against the schema RBAC).
func (s *Service) UpdateUserRole(ctx context.Context, tenantID, userID, role string) error {
	if err := s.checkRole(role); err != nil {
		return err
	}
	return s.users.UpdateUserRole(ctx, tenantID, userID, role)
}

// SetUserSuspended toggles a tenant user's administrative lockout.
func (s *Service) SetUserSuspended(ctx context.Context, tenantID, userID string, suspended bool) error {
	return s.users.SetUserSuspended(ctx, tenantID, userID, suspended)
}

// DeleteUser removes a tenant user.
func (s *Service) DeleteUser(ctx context.Context, tenantID, userID string) error {
	return s.users.DeleteUser(ctx, tenantID, userID)
}

// checkRole validates a role against the boot RBAC (when a checker is configured),
// so the admin API never assigns a role the engine cannot authorize.
func (s *Service) checkRole(role string) error {
	if role == "" {
		return ErrUnknownRole
	}
	if s.cfg.RoleExists != nil && !s.cfg.RoleExists(role) {
		return ErrUnknownRole
	}
	return nil
}

// countResources counts the resources in a stored schema JSON (best-effort: a
// malformed/empty schema counts 0).
func countResources(raw []byte) int {
	if len(raw) == 0 {
		return 0
	}
	var s schema.APISchema
	if err := json.Unmarshal(raw, &s); err != nil {
		return 0
	}
	return len(s.Resources)
}
