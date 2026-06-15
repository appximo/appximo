// Package platformadmin implements the BACKEND of the admin panel (ADMIN-API-V1):
// a platform super-admin that lives ABOVE the tenants, plus a consolidated admin
// API for managing tenants, their users, and their observability.
//
// The guiding principle is that the admin panel is NOT a second permission system.
// It INHERITS what already exists:
//
//   - auth-as-product (pkg/userauth): the super-admin authenticates with the same
//     argon2id password + TOTP MFA mechanism a tenant user does — but against a
//     SYSTEM schema (appitools_system.platform_admins), not a tenant. The reusable
//     primitives are exported from pkg/userauth (see primitives.go).
//   - schema-per-tenant isolation (search_path / tenant_<id> identifiers): a tenant
//     admin physically cannot reach another tenant; this is structural, not coded.
//   - the schema RBAC: "tenant admin" is just a tenant user whose role grants broad
//     access — not a new concept.
//   - the control plane (pkg/controlplane): tenant create/lookup is WRAPPED, never
//     reimplemented.
//   - the observability store (pkg/observability): per-tenant traces/metrics are
//     EXPOSED with the right authorization, never duplicated.
//
// This package adds only what did not exist: the platform-level super-admin (today
// the bare X-Admin-Key machine credential has no human identity) and the HTTP API
// that consolidates the above behind one authenticated, audited surface.
package platformadmin

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// SystemSchema is the Postgres schema that holds platform-level identity. It sits
// ABOVE every tenant_<id> schema: a platform admin is not a tenant user. The name
// is a fixed identifier (never tenant-derived), so it can never collide with a
// tenant schema (those are always tenant_<id>).
const SystemSchema = "appitools_system"

// DefaultSuperAdminRole is the role stamped on a platform admin. It is a marker
// inside the platform JWT (scope=platform), distinct from any tenant RBAC role.
const DefaultSuperAdminRole = "platform_super_admin"

// ErrAdminEmailTaken is returned by CreateAdmin when the email already exists in
// the system schema. ErrAdminNotFound when no admin matches a lookup.
var (
	ErrAdminEmailTaken = errors.New("platformadmin: admin email already registered")
	ErrAdminNotFound   = errors.New("platformadmin: admin not found")
)

// Admin is a stored platform super-admin. PasswordHash is never serialized to a
// client and never logged (handlers build their own response shape).
type Admin struct {
	ID            string
	Email         string
	PasswordHash  string
	Role          string
	EmailVerified bool
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// Store persists platform admins (and their MFA enrollment) in the system schema,
// using the engine's shared pool. DDL is run once per process (lazy, behind a
// sync.Once-ish guard). It mirrors pkg/userauth's Store shape so the two identity
// surfaces look and behave the same.
type Store struct {
	pool    *pgxpool.Pool
	ensure  sync.Once
	ensured error // result of the one-time DDL run
}

// NewStore builds a Store over the engine pool.
func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

func (s *Store) ensureSchema(ctx context.Context) error {
	s.ensure.Do(func() {
		ddl := fmt.Sprintf(`
CREATE SCHEMA IF NOT EXISTS %[1]s;
CREATE TABLE IF NOT EXISTS %[1]s.platform_admins (
    id             UUID        DEFAULT gen_random_uuid() PRIMARY KEY,
    email          TEXT        NOT NULL,
    password_hash  TEXT        NOT NULL,
    role           TEXT        NOT NULL DEFAULT '%[2]s',
    email_verified BOOLEAN     NOT NULL DEFAULT true,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX IF NOT EXISTS uniq_platform_admins_email ON %[1]s.platform_admins (lower(email));
CREATE TABLE IF NOT EXISTS %[1]s.platform_admin_mfa (
    admin_id               UUID        PRIMARY KEY,
    totp_secret_encrypted  TEXT        NOT NULL,
    enabled                BOOLEAN     NOT NULL DEFAULT false,
    created_at             TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at             TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE TABLE IF NOT EXISTS %[1]s.platform_admin_backup_codes (
    id         UUID        DEFAULT gen_random_uuid() PRIMARY KEY,
    admin_id   UUID        NOT NULL,
    code_hash  TEXT        NOT NULL,
    used_at    TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_platform_backup_admin ON %[1]s.platform_admin_backup_codes (admin_id);`,
			SystemSchema, DefaultSuperAdminRole)
		if _, err := s.pool.Exec(ctx, ddl); err != nil {
			s.ensured = fmt.Errorf("platformadmin: ensure system schema: %w", err)
		}
	})
	return s.ensured
}

// CreateAdmin inserts a platform admin. email is stored as given (the caller
// normalizes); a case-insensitive duplicate → ErrAdminEmailTaken. The returned
// Admin has no PasswordHash set.
func (s *Store) CreateAdmin(ctx context.Context, email, passwordHash, role string) (Admin, error) {
	if err := s.ensureSchema(ctx); err != nil {
		return Admin{}, err
	}
	q := fmt.Sprintf(`INSERT INTO %s.platform_admins (email, password_hash, role)
		VALUES ($1, $2, $3)
		RETURNING id::text, email, role, email_verified, created_at, updated_at`, SystemSchema)
	var a Admin
	err := s.pool.QueryRow(ctx, q, email, passwordHash, role).
		Scan(&a.ID, &a.Email, &a.Role, &a.EmailVerified, &a.CreatedAt, &a.UpdatedAt)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return Admin{}, ErrAdminEmailTaken
		}
		return Admin{}, fmt.Errorf("platformadmin: insert admin: %w", err)
	}
	return a, nil
}

// GetAdminByEmail looks an admin up by email (case-insensitive). ErrAdminNotFound
// when none matches.
func (s *Store) GetAdminByEmail(ctx context.Context, email string) (Admin, error) {
	if err := s.ensureSchema(ctx); err != nil {
		return Admin{}, err
	}
	q := fmt.Sprintf(`SELECT id::text, email, password_hash, role, email_verified, created_at, updated_at
		FROM %s.platform_admins WHERE lower(email) = lower($1)`, SystemSchema)
	return s.scanAdmin(s.pool.QueryRow(ctx, q, email))
}

// GetAdminByID looks an admin up by id. ErrAdminNotFound when none matches.
func (s *Store) GetAdminByID(ctx context.Context, id string) (Admin, error) {
	if err := s.ensureSchema(ctx); err != nil {
		return Admin{}, err
	}
	q := fmt.Sprintf(`SELECT id::text, email, password_hash, role, email_verified, created_at, updated_at
		FROM %s.platform_admins WHERE id = $1`, SystemSchema)
	return s.scanAdmin(s.pool.QueryRow(ctx, q, id))
}

func (s *Store) scanAdmin(row pgx.Row) (Admin, error) {
	var a Admin
	err := row.Scan(&a.ID, &a.Email, &a.PasswordHash, &a.Role, &a.EmailVerified, &a.CreatedAt, &a.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Admin{}, ErrAdminNotFound
	}
	if err != nil {
		return Admin{}, fmt.Errorf("platformadmin: get admin: %w", err)
	}
	return a, nil
}

// CountAdmins returns the number of platform admins (used by bootstrap to refuse a
// public/duplicate first-admin path).
func (s *Store) CountAdmins(ctx context.Context) (int, error) {
	if err := s.ensureSchema(ctx); err != nil {
		return 0, err
	}
	var n int
	if err := s.pool.QueryRow(ctx, fmt.Sprintf("SELECT count(*) FROM %s.platform_admins", SystemSchema)).Scan(&n); err != nil {
		return 0, fmt.Errorf("platformadmin: count admins: %w", err)
	}
	return n, nil
}

// --- MFA (mirrors pkg/userauth's per-tenant MFA, but in the system schema) ----

// UpsertMFASecret stores (or replaces) an admin's encrypted TOTP secret with
// enabled=false — enrollment begins but is not active until ConfirmMFA.
func (s *Store) UpsertMFASecret(ctx context.Context, adminID, encSecret string) error {
	if err := s.ensureSchema(ctx); err != nil {
		return err
	}
	q := fmt.Sprintf(`INSERT INTO %s.platform_admin_mfa (admin_id, totp_secret_encrypted, enabled, updated_at)
		VALUES ($1, $2, false, now())
		ON CONFLICT (admin_id) DO UPDATE SET totp_secret_encrypted = EXCLUDED.totp_secret_encrypted,
			enabled = false, updated_at = now()`, SystemSchema)
	if _, err := s.pool.Exec(ctx, q, adminID, encSecret); err != nil {
		return fmt.Errorf("platformadmin: upsert mfa: %w", err)
	}
	return nil
}

// GetMFA returns the admin's encrypted secret and enabled flag. found=false when
// there is no enrollment row.
func (s *Store) GetMFA(ctx context.Context, adminID string) (encSecret string, enabled, found bool, err error) {
	if err := s.ensureSchema(ctx); err != nil {
		return "", false, false, err
	}
	q := fmt.Sprintf(`SELECT totp_secret_encrypted, enabled FROM %s.platform_admin_mfa WHERE admin_id = $1`, SystemSchema)
	err = s.pool.QueryRow(ctx, q, adminID).Scan(&encSecret, &enabled)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, false, nil
	}
	if err != nil {
		return "", false, false, fmt.Errorf("platformadmin: get mfa: %w", err)
	}
	return encSecret, enabled, true, nil
}

// MFAEnabled reports whether the admin has confirmed (active) MFA.
func (s *Store) MFAEnabled(ctx context.Context, adminID string) (bool, error) {
	_, enabled, found, err := s.GetMFA(ctx, adminID)
	return found && enabled, err
}

// ConfirmMFA flips enabled=true and stores the backup-code hashes atomically.
func (s *Store) ConfirmMFA(ctx context.Context, adminID string, codeHashes []string) error {
	if err := s.ensureSchema(ctx); err != nil {
		return err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(context.Background()) //nolint:errcheck
	if _, err := tx.Exec(ctx, fmt.Sprintf(
		`UPDATE %s.platform_admin_mfa SET enabled = true, updated_at = now() WHERE admin_id = $1`, SystemSchema), adminID); err != nil {
		return fmt.Errorf("platformadmin: enable mfa: %w", err)
	}
	// Replace any prior backup codes (a re-confirm reissues).
	if _, err := tx.Exec(ctx, fmt.Sprintf(
		`DELETE FROM %s.platform_admin_backup_codes WHERE admin_id = $1`, SystemSchema), adminID); err != nil {
		return fmt.Errorf("platformadmin: clear backup codes: %w", err)
	}
	for _, h := range codeHashes {
		if _, err := tx.Exec(ctx, fmt.Sprintf(
			`INSERT INTO %s.platform_admin_backup_codes (admin_id, code_hash) VALUES ($1, $2)`, SystemSchema), adminID, h); err != nil {
			return fmt.Errorf("platformadmin: insert backup code: %w", err)
		}
	}
	return tx.Commit(ctx)
}

// ConsumeBackupCode marks a matching unused backup code as used and returns true.
// false when no unused code matches the hash. Consumption is atomic (a code is
// one-time).
func (s *Store) ConsumeBackupCode(ctx context.Context, adminID, codeHash string) (bool, error) {
	if err := s.ensureSchema(ctx); err != nil {
		return false, err
	}
	tag, err := s.pool.Exec(ctx, fmt.Sprintf(
		`UPDATE %s.platform_admin_backup_codes SET used_at = now()
			WHERE admin_id = $1 AND code_hash = $2 AND used_at IS NULL`, SystemSchema), adminID, codeHash)
	if err != nil {
		return false, fmt.Errorf("platformadmin: consume backup code: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

// DisableMFA clears an admin's secret and backup codes.
func (s *Store) DisableMFA(ctx context.Context, adminID string) error {
	if err := s.ensureSchema(ctx); err != nil {
		return err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(context.Background()) //nolint:errcheck
	if _, err := tx.Exec(ctx, fmt.Sprintf(`DELETE FROM %s.platform_admin_mfa WHERE admin_id = $1`, SystemSchema), adminID); err != nil {
		return fmt.Errorf("platformadmin: disable mfa: %w", err)
	}
	if _, err := tx.Exec(ctx, fmt.Sprintf(`DELETE FROM %s.platform_admin_backup_codes WHERE admin_id = $1`, SystemSchema), adminID); err != nil {
		return fmt.Errorf("platformadmin: clear backup codes: %w", err)
	}
	return tx.Commit(ctx)
}
