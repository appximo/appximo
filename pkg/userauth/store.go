package userauth

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// usersTable is the per-tenant identity table. The name carries an underscore,
// which a user-declared resource name CANNOT (resource names match
// ^[a-z][a-z0-9-]*$ — hyphens only), so this table can never collide with a
// schema resource's table. It lives INSIDE the tenant's own Postgres schema
// (tenant_<id>), so a user row is physically isolated per tenant exactly like
// the tenant's business data — the structural property that lets the SAME email
// be a distinct user in two different tenants (email is UNIQUE per schema, not
// global; this is the multi-tenancy Supabase's globally-unique email cannot do).
const usersTable = "auth_users"

// ErrEmailTaken is returned by Create when the email already exists IN THIS
// TENANT's schema. The same email in another tenant's schema is a different row
// and does not conflict.
var ErrEmailTaken = errors.New("userauth: email already registered")

// ErrUserNotFound is returned by GetByEmail when no user matches in the tenant.
var ErrUserNotFound = errors.New("userauth: user not found")

// User is a stored identity. PasswordHash is never serialized to a client (no
// json tag exposure — handlers build their own response shape) and never logged.
type User struct {
	ID            string
	Email         string
	PasswordHash  string
	Role          string
	EmailVerified bool
	// Suspended, when true, blocks login (an administrative lockout managed by the
	// admin API — pkg/platformadmin). Only GetByEmail reads it (login is the only
	// path that enforces it); Create/GetByID leave it zero (false) as they never
	// need it. The column is added idempotently in ensure.
	Suspended bool
	CreatedAt time.Time
	UpdatedAt time.Time
}

// Store persists per-tenant users. It uses the engine's shared pool directly and
// keys every statement to tenant_<id> via a sanitized identifier — the same
// schema-per-tenant isolation pkg/files uses for blob metadata. The table DDL is
// run once per tenant per process (lazy, cached in `ensured`).
type Store struct {
	pool              *pgxpool.Pool
	ensured           sync.Map // tenant id → struct{}: auth_users DDL run once
	ensuredTokens     sync.Map // tenant id → struct{}: auth_tokens DDL run once
	ensuredIdentities sync.Map // tenant id → struct{}: auth_identities DDL run once
	ensuredMFA        sync.Map // tenant id → struct{}: auth_mfa + auth_backup_codes DDL run once
}

// NewStore builds a Store over the engine pool.
func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

// tenantSchemaRe validates a tenant id before it is interpolated into a schema
// name. TenantMiddleware already validated the Host subdomain, but the Store is a
// library boundary, so it re-checks (defence in depth) before building DDL/DML.
var tenantSchemaRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

func (s *Store) table(tenantID string) (string, error) {
	if !tenantSchemaRe.MatchString(tenantID) {
		return "", fmt.Errorf("userauth: invalid tenant id %q", tenantID)
	}
	return pgx.Identifier{"tenant_" + tenantID, usersTable}.Sanitize(), nil
}

// ensure creates the per-tenant auth_users table and its case-insensitive unique
// email index, idempotently and once per process per tenant. The UNIQUE index is
// on lower(email): email uniqueness is per-schema and case-insensitive.
func (s *Store) ensure(ctx context.Context, tenantID string) error {
	if _, done := s.ensured.Load(tenantID); done {
		return nil
	}
	tbl, err := s.table(tenantID)
	if err != nil {
		return err
	}
	ddl := fmt.Sprintf(`
CREATE TABLE IF NOT EXISTS %s (
    id             UUID        DEFAULT gen_random_uuid() PRIMARY KEY,
    email          TEXT        NOT NULL,
    password_hash  TEXT        NOT NULL,
    role           TEXT        NOT NULL,
    email_verified BOOLEAN     NOT NULL DEFAULT false,
    suspended      BOOLEAN     NOT NULL DEFAULT false,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);
ALTER TABLE %s ADD COLUMN IF NOT EXISTS suspended BOOLEAN NOT NULL DEFAULT false;
CREATE UNIQUE INDEX IF NOT EXISTS uniq_%s_email_%s ON %s (lower(email));`,
		tbl, tbl, usersTable, tenantID, tbl)
	if _, err := s.pool.Exec(ctx, ddl); err != nil {
		return fmt.Errorf("userauth: ensure table: %w", err)
	}
	s.ensured.Store(tenantID, struct{}{})
	return nil
}

// Create inserts a new user in the tenant's schema. email is stored as given
// (trimmed/lowercased by the caller); a duplicate (case-insensitive) within the
// SAME tenant returns ErrEmailTaken. The returned User has no PasswordHash set
// (the caller already has it; it is never round-tripped out).
func (s *Store) Create(ctx context.Context, tenantID, email, passwordHash, role string) (User, error) {
	if err := s.ensure(ctx, tenantID); err != nil {
		return User{}, err
	}
	tbl, err := s.table(tenantID)
	if err != nil {
		return User{}, err
	}
	q := fmt.Sprintf(`INSERT INTO %s (email, password_hash, role)
		VALUES ($1, $2, $3)
		RETURNING id::text, email, role, email_verified, created_at, updated_at`, tbl)
	var u User
	err = s.pool.QueryRow(ctx, q, email, passwordHash, role).
		Scan(&u.ID, &u.Email, &u.Role, &u.EmailVerified, &u.CreatedAt, &u.UpdatedAt)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" { // unique_violation
			return User{}, ErrEmailTaken
		}
		return User{}, fmt.Errorf("userauth: insert user: %w", err)
	}
	return u, nil
}

// GetByEmail looks a user up by email (case-insensitive) within the tenant's
// schema. It returns ErrUserNotFound when no row matches. Because the query is
// scoped to tenant_<id>, a tenant can never resolve another tenant's user — the
// isolation that makes a cross-tenant login structurally impossible.
func (s *Store) GetByEmail(ctx context.Context, tenantID, email string) (User, error) {
	if err := s.ensure(ctx, tenantID); err != nil {
		return User{}, err
	}
	tbl, err := s.table(tenantID)
	if err != nil {
		return User{}, err
	}
	q := fmt.Sprintf(`SELECT id::text, email, password_hash, role, email_verified, suspended, created_at, updated_at
		FROM %s WHERE lower(email) = lower($1)`, tbl)
	var u User
	err = s.pool.QueryRow(ctx, q, email).
		Scan(&u.ID, &u.Email, &u.PasswordHash, &u.Role, &u.EmailVerified, &u.Suspended, &u.CreatedAt, &u.UpdatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return User{}, ErrUserNotFound
		}
		return User{}, fmt.Errorf("userauth: get user: %w", err)
	}
	return u, nil
}
