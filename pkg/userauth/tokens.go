package userauth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// tokensTable is the per-tenant single-use token table (password reset + email
// verification). Like auth_users it carries an underscore, so it never collides
// with a schema resource, and it lives INSIDE the tenant schema (tenant_<id>) —
// a reset/verify token of one tenant is physically unreachable from another.
const tokensTable = "auth_tokens"

// Token kinds. The DB stores the literal string in the `type` column.
const (
	tokenVerify = "verify"
	tokenReset  = "reset"
)

// Token lifetimes. A verify link is long-lived (a user may confirm hours later);
// a reset link is short-lived (it grants a password change, so the blast radius
// of a leaked link must be small). Documented in AGENTS/README.
const (
	verifyTTL = 24 * time.Hour
	resetTTL  = 1 * time.Hour
)

// ErrInvalidToken is returned when a reset/verify token does not match an unused,
// unexpired row (wrong/expired/already-used all collapse to this one error —
// the client never learns which).
var errTokenInvalid = errors.New("userauth: invalid or expired token")

// newPlainToken returns a 256-bit URL-safe random token. This is the value that
// rides the email link; only its SHA-256 hash is ever stored.
func newPlainToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("userauth: read token randomness: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// hashToken is the at-rest form of a token. A plain SHA-256 is correct here (NOT
// argon2): the token is already a uniformly-random 256-bit secret, so there is
// nothing to brute-force — the slow KDF argon2 exists for LOW-entropy passwords.
func hashToken(plain string) string {
	sum := sha256.Sum256([]byte(plain))
	return hex.EncodeToString(sum[:])
}

func (s *Store) tokensTbl(tenantID string) (string, error) {
	if !tenantSchemaRe.MatchString(tenantID) {
		return "", fmt.Errorf("userauth: invalid tenant id %q", tenantID)
	}
	return pgx.Identifier{"tenant_" + tenantID, tokensTable}.Sanitize(), nil
}

// ensureTokens creates the per-tenant auth_tokens table + a token_hash index,
// idempotently and once per process per tenant.
func (s *Store) ensureTokens(ctx context.Context, tenantID string) error {
	if _, done := s.ensuredTokens.Load(tenantID); done {
		return nil
	}
	tbl, err := s.tokensTbl(tenantID)
	if err != nil {
		return err
	}
	ddl := fmt.Sprintf(`
CREATE TABLE IF NOT EXISTS %s (
    id           UUID        DEFAULT gen_random_uuid() PRIMARY KEY,
    user_id      UUID        NOT NULL,
    type         TEXT        NOT NULL,
    token_hash   TEXT        NOT NULL,
    expires_at   TIMESTAMPTZ NOT NULL,
    used_at      TIMESTAMPTZ,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_%s_hash_%s ON %s (token_hash);`,
		tbl, tokensTable, tenantID, tbl)
	if _, err := s.pool.Exec(ctx, ddl); err != nil {
		return fmt.Errorf("userauth: ensure tokens table: %w", err)
	}
	s.ensuredTokens.Store(tenantID, struct{}{})
	return nil
}

// insertTokenTx inserts a token row on the GIVEN transaction (so the caller can
// enqueue the email event in the SAME tx — token and email are atomic).
func (s *Store) insertTokenTx(ctx context.Context, tx pgx.Tx, tenantID, userID, ttype, tokenHash string, expiresAt time.Time) error {
	tbl, err := s.tokensTbl(tenantID)
	if err != nil {
		return err
	}
	q := fmt.Sprintf(`INSERT INTO %s (user_id, type, token_hash, expires_at) VALUES ($1,$2,$3,$4)`, tbl)
	if _, err := tx.Exec(ctx, q, userID, ttype, tokenHash, expiresAt); err != nil {
		return fmt.Errorf("userauth: insert token: %w", err)
	}
	return nil
}

// consumeToken atomically marks the matching token used and returns its user_id.
// The single conditional UPDATE … RETURNING is the consume step: it succeeds only
// for a token that is the right type, not yet used, and not expired — closing any
// check-then-act race (a token can be redeemed at most once even under concurrent
// requests). A non-matching token yields errTokenInvalid. Runs on the given tx so
// the caller's follow-up write (set verified / set password) is atomic with it.
func (s *Store) consumeToken(ctx context.Context, tx pgx.Tx, tenantID, ttype, tokenHash string) (userID string, err error) {
	tbl, err := s.tokensTbl(tenantID)
	if err != nil {
		return "", err
	}
	q := fmt.Sprintf(`UPDATE %s SET used_at = now()
		WHERE token_hash = $1 AND type = $2 AND used_at IS NULL AND expires_at > now()
		RETURNING user_id::text`, tbl)
	err = tx.QueryRow(ctx, q, tokenHash, ttype).Scan(&userID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", errTokenInvalid
	}
	if err != nil {
		return "", fmt.Errorf("userauth: consume token: %w", err)
	}
	return userID, nil
}

// invalidateUserTokensTx marks every still-pending token of (user, type) used, so
// a completed reset invalidates any other outstanding reset links for that user.
func (s *Store) invalidateUserTokensTx(ctx context.Context, tx pgx.Tx, tenantID, userID, ttype string) error {
	tbl, err := s.tokensTbl(tenantID)
	if err != nil {
		return err
	}
	q := fmt.Sprintf(`UPDATE %s SET used_at = now() WHERE user_id = $1 AND type = $2 AND used_at IS NULL`, tbl)
	if _, err := tx.Exec(ctx, q, userID, ttype); err != nil {
		return fmt.Errorf("userauth: invalidate tokens: %w", err)
	}
	return nil
}

// setEmailVerifiedTx flips email_verified on the user (verify confirm).
func (s *Store) setEmailVerifiedTx(ctx context.Context, tx pgx.Tx, tenantID, userID string) error {
	tbl, err := s.table(tenantID)
	if err != nil {
		return err
	}
	q := fmt.Sprintf(`UPDATE %s SET email_verified = true, updated_at = now() WHERE id = $1`, tbl)
	if _, err := tx.Exec(ctx, q, userID); err != nil {
		return fmt.Errorf("userauth: set email_verified: %w", err)
	}
	return nil
}

// setPasswordTx replaces the user's password hash (reset confirm).
func (s *Store) setPasswordTx(ctx context.Context, tx pgx.Tx, tenantID, userID, passwordHash string) error {
	tbl, err := s.table(tenantID)
	if err != nil {
		return err
	}
	q := fmt.Sprintf(`UPDATE %s SET password_hash = $2, updated_at = now() WHERE id = $1`, tbl)
	if _, err := tx.Exec(ctx, q, userID, passwordHash); err != nil {
		return fmt.Errorf("userauth: set password: %w", err)
	}
	return nil
}
