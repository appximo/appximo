package userauth

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// identitiesTable is the per-tenant OAuth identity-link table. Like auth_users /
// auth_tokens its name carries an underscore (never collides with a resource) and
// it lives inside tenant_<id>, so a social identity in one tenant is physically
// unreachable from another — the same email logging in via Google to tenant A and
// tenant B yields two DISTINCT users (the per-schema-unique-email advantage holds
// for social login too).
const identitiesTable = "auth_identities"

func (s *Store) identitiesTbl(tenantID string) (string, error) {
	if !tenantSchemaRe.MatchString(tenantID) {
		return "", fmt.Errorf("userauth: invalid tenant id %q", tenantID)
	}
	return pgx.Identifier{"tenant_" + tenantID, identitiesTable}.Sanitize(), nil
}

// ensureIdentities creates the per-tenant auth_identities table idempotently. The
// UNIQUE (provider, provider_user_id) is the stable identity key — provider_user_id
// (NOT the email, which can change) is what binds a returning social login to its
// user.
func (s *Store) ensureIdentities(ctx context.Context, tenantID string) error {
	if _, done := s.ensuredIdentities.Load(tenantID); done {
		return nil
	}
	tbl, err := s.identitiesTbl(tenantID)
	if err != nil {
		return err
	}
	ddl := fmt.Sprintf(`
CREATE TABLE IF NOT EXISTS %s (
    id               UUID        DEFAULT gen_random_uuid() PRIMARY KEY,
    user_id          UUID        NOT NULL,
    provider         TEXT        NOT NULL,
    provider_user_id TEXT        NOT NULL,
    email            TEXT,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX IF NOT EXISTS uniq_%s_provider_%s ON %s (provider, provider_user_id);`,
		tbl, identitiesTable, tenantID, tbl)
	if _, err := s.pool.Exec(ctx, ddl); err != nil {
		return fmt.Errorf("userauth: ensure identities table: %w", err)
	}
	s.ensuredIdentities.Store(tenantID, struct{}{})
	return nil
}

// GetIdentityUserID resolves (provider, providerUserID) to the linked user id
// within the tenant. found is false when no identity is linked yet.
func (s *Store) GetIdentityUserID(ctx context.Context, tenantID, provider, providerUserID string) (userID string, found bool, err error) {
	if err := s.ensureIdentities(ctx, tenantID); err != nil {
		return "", false, err
	}
	tbl, err := s.identitiesTbl(tenantID)
	if err != nil {
		return "", false, err
	}
	q := fmt.Sprintf(`SELECT user_id::text FROM %s WHERE provider = $1 AND provider_user_id = $2`, tbl)
	err = s.pool.QueryRow(ctx, q, provider, providerUserID).Scan(&userID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("userauth: get identity: %w", err)
	}
	return userID, true, nil
}

// GetByID loads a user by id within the tenant (used after an identity match).
func (s *Store) GetByID(ctx context.Context, tenantID, id string) (User, error) {
	if err := s.ensure(ctx, tenantID); err != nil {
		return User{}, err
	}
	tbl, err := s.table(tenantID)
	if err != nil {
		return User{}, err
	}
	q := fmt.Sprintf(`SELECT id::text, email, password_hash, role, email_verified, created_at, updated_at
		FROM %s WHERE id = $1`, tbl)
	var u User
	err = s.pool.QueryRow(ctx, q, id).
		Scan(&u.ID, &u.Email, &u.PasswordHash, &u.Role, &u.EmailVerified, &u.CreatedAt, &u.UpdatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return User{}, ErrUserNotFound
		}
		return User{}, fmt.Errorf("userauth: get user by id: %w", err)
	}
	return u, nil
}

// createExternalUserTx inserts a user with NO local password (password_hash = ”)
// and email_verified = true (the provider already verified the email). An empty
// hash can never satisfy VerifyPassword, so an OAuth-only user simply cannot
// password-login (any password → invalid credentials) until they set one via
// reset. Runs on the given tx so the user + its identity link commit atomically.
func (s *Store) createExternalUserTx(ctx context.Context, tx pgx.Tx, tenantID, email, role string) (User, error) {
	tbl, err := s.table(tenantID)
	if err != nil {
		return User{}, err
	}
	q := fmt.Sprintf(`INSERT INTO %s (email, password_hash, role, email_verified)
		VALUES ($1, '', $2, true)
		RETURNING id::text, email, role, email_verified, created_at, updated_at`, tbl)
	var u User
	err = tx.QueryRow(ctx, q, email, role).
		Scan(&u.ID, &u.Email, &u.Role, &u.EmailVerified, &u.CreatedAt, &u.UpdatedAt)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return User{}, ErrEmailTaken // a concurrent create won the email race
		}
		return User{}, fmt.Errorf("userauth: insert external user: %w", err)
	}
	return u, nil
}

// linkIdentityTx records the (provider, provider_user_id) → user link on the given
// tx. A unique violation means the identity was linked concurrently (race) — the
// caller treats it as "already linked" and re-resolves.
func (s *Store) linkIdentityTx(ctx context.Context, tx pgx.Tx, tenantID, userID, provider, providerUserID, email string) error {
	tbl, err := s.identitiesTbl(tenantID)
	if err != nil {
		return err
	}
	q := fmt.Sprintf(`INSERT INTO %s (user_id, provider, provider_user_id, email) VALUES ($1,$2,$3,$4)`, tbl)
	if _, err := tx.Exec(ctx, q, userID, provider, providerUserID, email); err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return ErrIdentityExists
		}
		return fmt.Errorf("userauth: link identity: %w", err)
	}
	return nil
}

// ErrIdentityExists signals a concurrent identity link (unique violation).
var ErrIdentityExists = errors.New("userauth: identity already linked")
