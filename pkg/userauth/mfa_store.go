package userauth

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// Per-tenant MFA tables. Like the rest of the auth_* tables they carry an
// underscore (never collide with a resource) and live inside tenant_<id>, so a
// user's MFA enrollment in one tenant is physically unreachable from another.
const (
	mfaTable         = "auth_mfa"
	backupCodesTable = "auth_backup_codes"
)

func (s *Store) mfaTbl(tenantID string) (string, error) {
	if !tenantSchemaRe.MatchString(tenantID) {
		return "", fmt.Errorf("userauth: invalid tenant id %q", tenantID)
	}
	return pgx.Identifier{"tenant_" + tenantID, mfaTable}.Sanitize(), nil
}

func (s *Store) backupTbl(tenantID string) (string, error) {
	if !tenantSchemaRe.MatchString(tenantID) {
		return "", fmt.Errorf("userauth: invalid tenant id %q", tenantID)
	}
	return pgx.Identifier{"tenant_" + tenantID, backupCodesTable}.Sanitize(), nil
}

// ensureMFA creates the per-tenant auth_mfa + auth_backup_codes tables idempotently.
func (s *Store) ensureMFA(ctx context.Context, tenantID string) error {
	if _, done := s.ensuredMFA.Load(tenantID); done {
		return nil
	}
	mfa, err := s.mfaTbl(tenantID)
	if err != nil {
		return err
	}
	backup, err := s.backupTbl(tenantID)
	if err != nil {
		return err
	}
	ddl := fmt.Sprintf(`
CREATE TABLE IF NOT EXISTS %s (
    user_id                UUID        PRIMARY KEY,
    totp_secret_encrypted  TEXT        NOT NULL,
    enabled                BOOLEAN     NOT NULL DEFAULT false,
    created_at             TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at             TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE TABLE IF NOT EXISTS %s (
    id         UUID        DEFAULT gen_random_uuid() PRIMARY KEY,
    user_id    UUID        NOT NULL,
    code_hash  TEXT        NOT NULL,
    used_at    TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_%s_user_%s ON %s (user_id);`,
		mfa, backup, backupCodesTable, tenantID, backup)
	if _, err := s.pool.Exec(ctx, ddl); err != nil {
		return fmt.Errorf("userauth: ensure mfa tables: %w", err)
	}
	s.ensuredMFA.Store(tenantID, struct{}{})
	return nil
}

// upsertMFASecret stores (or replaces) the user's encrypted TOTP secret with
// enabled=false — enrolling always starts UNCONFIRMED so a mis-scanned secret never
// locks the user out (they must prove one valid code to flip enabled=true).
func (s *Store) upsertMFASecret(ctx context.Context, tenantID, userID, encSecret string) error {
	if err := s.ensureMFA(ctx, tenantID); err != nil {
		return err
	}
	tbl, err := s.mfaTbl(tenantID)
	if err != nil {
		return err
	}
	q := fmt.Sprintf(`INSERT INTO %s (user_id, totp_secret_encrypted, enabled)
		VALUES ($1, $2, false)
		ON CONFLICT (user_id) DO UPDATE SET
			totp_secret_encrypted = EXCLUDED.totp_secret_encrypted,
			enabled = false,
			updated_at = now()`, tbl)
	if _, err := s.pool.Exec(ctx, q, userID, encSecret); err != nil {
		return fmt.Errorf("userauth: upsert mfa secret: %w", err)
	}
	return nil
}

// getMFA returns the user's encrypted secret and enabled flag. found is false when
// the user has no MFA row.
func (s *Store) getMFA(ctx context.Context, tenantID, userID string) (encSecret string, enabled, found bool, err error) {
	if err := s.ensureMFA(ctx, tenantID); err != nil {
		return "", false, false, err
	}
	tbl, err := s.mfaTbl(tenantID)
	if err != nil {
		return "", false, false, err
	}
	q := fmt.Sprintf(`SELECT totp_secret_encrypted, enabled FROM %s WHERE user_id = $1`, tbl)
	err = s.pool.QueryRow(ctx, q, userID).Scan(&encSecret, &enabled)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, false, nil
	}
	if err != nil {
		return "", false, false, fmt.Errorf("userauth: get mfa: %w", err)
	}
	return encSecret, enabled, true, nil
}

// MFAEnabled reports whether the user has CONFIRMED MFA — the one cheap query the
// login path runs (only after a password verifies) to decide on a second factor.
func (s *Store) MFAEnabled(ctx context.Context, tenantID, userID string) (bool, error) {
	if err := s.ensureMFA(ctx, tenantID); err != nil {
		return false, err
	}
	tbl, err := s.mfaTbl(tenantID)
	if err != nil {
		return false, err
	}
	var enabled bool
	q := fmt.Sprintf(`SELECT enabled FROM %s WHERE user_id = $1`, tbl)
	err = s.pool.QueryRow(ctx, q, userID).Scan(&enabled)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("userauth: mfa enabled: %w", err)
	}
	return enabled, nil
}

// confirmMFA flips enabled=true and replaces the user's backup codes (delete old,
// insert the new hashes) in ONE transaction.
func (s *Store) confirmMFA(ctx context.Context, tenantID, userID string, codeHashes []string) error {
	mfa, err := s.mfaTbl(tenantID)
	if err != nil {
		return err
	}
	backup, err := s.backupTbl(tenantID)
	if err != nil {
		return err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(context.Background()) //nolint:errcheck

	if _, err := tx.Exec(ctx, fmt.Sprintf(`UPDATE %s SET enabled = true, updated_at = now() WHERE user_id = $1`, mfa), userID); err != nil {
		return fmt.Errorf("userauth: enable mfa: %w", err)
	}
	if _, err := tx.Exec(ctx, fmt.Sprintf(`DELETE FROM %s WHERE user_id = $1`, backup), userID); err != nil {
		return fmt.Errorf("userauth: clear old backup codes: %w", err)
	}
	ins := fmt.Sprintf(`INSERT INTO %s (user_id, code_hash) VALUES ($1, $2)`, backup)
	for _, h := range codeHashes {
		if _, err := tx.Exec(ctx, ins, userID, h); err != nil {
			return fmt.Errorf("userauth: insert backup code: %w", err)
		}
	}
	return tx.Commit(ctx)
}

// consumeBackupCode atomically marks a matching unused backup code used and reports
// whether one was consumed (single conditional UPDATE … RETURNING — a code redeems
// at most once even under concurrent verifies).
func (s *Store) consumeBackupCode(ctx context.Context, tenantID, userID, codeHash string) (bool, error) {
	if err := s.ensureMFA(ctx, tenantID); err != nil {
		return false, err
	}
	tbl, err := s.backupTbl(tenantID)
	if err != nil {
		return false, err
	}
	q := fmt.Sprintf(`UPDATE %s SET used_at = now()
		WHERE user_id = $1 AND code_hash = $2 AND used_at IS NULL
		RETURNING id`, tbl)
	var id string
	err = s.pool.QueryRow(ctx, q, userID, codeHash).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("userauth: consume backup code: %w", err)
	}
	return true, nil
}

// disableMFA removes the user's MFA enrollment and all backup codes (one tx).
func (s *Store) disableMFA(ctx context.Context, tenantID, userID string) error {
	if err := s.ensureMFA(ctx, tenantID); err != nil {
		return err
	}
	mfa, err := s.mfaTbl(tenantID)
	if err != nil {
		return err
	}
	backup, err := s.backupTbl(tenantID)
	if err != nil {
		return err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(context.Background()) //nolint:errcheck
	if _, err := tx.Exec(ctx, fmt.Sprintf(`DELETE FROM %s WHERE user_id = $1`, backup), userID); err != nil {
		return fmt.Errorf("userauth: delete backup codes: %w", err)
	}
	if _, err := tx.Exec(ctx, fmt.Sprintf(`DELETE FROM %s WHERE user_id = $1`, mfa), userID); err != nil {
		return fmt.Errorf("userauth: delete mfa: %w", err)
	}
	return tx.Commit(ctx)
}
