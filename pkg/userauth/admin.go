package userauth

import (
	"context"
	"fmt"
)

// This file adds ADMIN management operations over a tenant's auth_users — list,
// change role, suspend/unsuspend, delete. They are consumed by the admin API
// (pkg/platformadmin): a tenant admin manages the users of its OWN tenant, a
// platform super-admin manages any tenant's. Every method is scoped to
// tenant_<id> via the same sanitized identifier the rest of the Store uses, so an
// operation can never reach another tenant's users — the schema-per-tenant
// isolation is the authorization boundary, not new code.

// ListUsers returns every user in the tenant (newest first), without password
// hashes. Intended for the admin API's user list.
func (s *Store) ListUsers(ctx context.Context, tenantID string) ([]User, error) {
	if err := s.ensure(ctx, tenantID); err != nil {
		return nil, err
	}
	tbl, err := s.table(tenantID)
	if err != nil {
		return nil, err
	}
	q := fmt.Sprintf(`SELECT id::text, email, role, email_verified, suspended, created_at, updated_at
		FROM %s ORDER BY created_at DESC`, tbl)
	rows, err := s.pool.Query(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("userauth: list users: %w", err)
	}
	defer rows.Close()
	out := []User{}
	for rows.Next() {
		var u User
		if err := rows.Scan(&u.ID, &u.Email, &u.Role, &u.EmailVerified, &u.Suspended, &u.CreatedAt, &u.UpdatedAt); err != nil {
			return nil, fmt.Errorf("userauth: scan user: %w", err)
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// CountUsers returns the number of users in the tenant. Used for cheap tenant
// metadata in the admin tenant list.
func (s *Store) CountUsers(ctx context.Context, tenantID string) (int, error) {
	if err := s.ensure(ctx, tenantID); err != nil {
		return 0, err
	}
	tbl, err := s.table(tenantID)
	if err != nil {
		return 0, err
	}
	var n int
	if err := s.pool.QueryRow(ctx, fmt.Sprintf("SELECT count(*) FROM %s", tbl)).Scan(&n); err != nil {
		return 0, fmt.Errorf("userauth: count users: %w", err)
	}
	return n, nil
}

// UpdateUserRole sets a user's role. The new role is NOT validated against the
// schema RBAC here (the admin API does that before calling, so the error message
// can list the valid roles); the Store only persists. Returns ErrUserNotFound
// when no row matches in the tenant.
func (s *Store) UpdateUserRole(ctx context.Context, tenantID, id, role string) error {
	return s.execUpdate(ctx, tenantID, id, "role = $2, updated_at = now()", role)
}

// SetUserSuspended toggles a user's suspended flag (an administrative lockout
// enforced at login). Returns ErrUserNotFound when no row matches.
func (s *Store) SetUserSuspended(ctx context.Context, tenantID, id string, suspended bool) error {
	return s.execUpdate(ctx, tenantID, id, "suspended = $2, updated_at = now()", suspended)
}

// execUpdate runs an UPDATE ... WHERE id = $1 with one extra bound value ($2) and
// maps a zero-row result to ErrUserNotFound.
func (s *Store) execUpdate(ctx context.Context, tenantID, id, setClause string, val any) error {
	if err := s.ensure(ctx, tenantID); err != nil {
		return err
	}
	tbl, err := s.table(tenantID)
	if err != nil {
		return err
	}
	tag, err := s.pool.Exec(ctx, fmt.Sprintf("UPDATE %s SET %s WHERE id = $1", tbl, setClause), id, val)
	if err != nil {
		return fmt.Errorf("userauth: update user: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrUserNotFound
	}
	return nil
}

// DeleteUser removes a user from the tenant. Returns ErrUserNotFound when no row
// matches. (The user's stateless JWTs remain valid until exp — the documented
// stateless-JWT trade-off, same as a deleted user during Refresh.)
func (s *Store) DeleteUser(ctx context.Context, tenantID, id string) error {
	if err := s.ensure(ctx, tenantID); err != nil {
		return err
	}
	tbl, err := s.table(tenantID)
	if err != nil {
		return err
	}
	tag, err := s.pool.Exec(ctx, fmt.Sprintf("DELETE FROM %s WHERE id = $1", tbl), id)
	if err != nil {
		return fmt.Errorf("userauth: delete user: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrUserNotFound
	}
	return nil
}

// CreateUserWithRole creates a user with an explicit role and a pre-hashed
// password (the admin chooses the role, unlike public signup). It returns
// ErrEmailTaken on a duplicate within the tenant. A thin wrapper over Create so
// the admin API expresses intent clearly.
func (s *Store) CreateUserWithRole(ctx context.Context, tenantID, email, passwordHash, role string) (User, error) {
	return s.Create(ctx, tenantID, email, passwordHash, role)
}
