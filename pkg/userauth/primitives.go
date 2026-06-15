package userauth

import (
	"fmt"
	"strings"
	"time"
)

// This file EXPORTS a small, audited set of identity primitives so a SIBLING
// identity surface (the platform super-admin in pkg/platformadmin) can reuse the
// exact same crypto — argon2id passwords, RFC 6238 TOTP, AES-256-GCM secret
// encryption, one-time backup codes — WITHOUT duplicating security-sensitive code
// or re-implementing it subtly differently. The platform admin lives in a system
// schema (above the tenants), not a tenant; it is a different STORE, but it must
// be the SAME identity mechanism. These wrappers are thin pass-throughs to the
// in-package implementations (password.go, totp.go, mfa_crypto.go, mfa.go,
// tokens.go) and add no new behaviour.

// NewTOTPSecret returns a fresh random base32 TOTP secret (RFC 6238).
func NewTOTPSecret() (string, error) { return newTOTPSecret() }

// OTPAuthURI builds the otpauth://totp/… URI an authenticator app scans.
func OTPAuthURI(issuer, account, secretB32 string) string {
	return otpauthURI(issuer, account, secretB32)
}

// ValidateTOTPNow reports whether code is a valid TOTP for secretB32 right now
// (±1 step / ±30 s), the same window the per-tenant MFA uses.
func ValidateTOTPNow(secretB32, code string) bool {
	return validateTOTP(secretB32, code, time.Now())
}

// TOTPCodeNow computes the current TOTP code for a base32 secret. It is the client
// side of TOTP (what an authenticator app shows) — exported mainly so an automated
// flow (a test, or a programmatic enrollment) can complete the enable/confirm
// handshake without a human typing a code.
func TOTPCodeNow(secretB32 string) (string, error) {
	key, err := base32NoPad.DecodeString(strings.ToUpper(strings.TrimSpace(secretB32)))
	if err != nil {
		return "", fmt.Errorf("userauth: decode totp secret: %w", err)
	}
	return totpCodeAt(key, time.Now()), nil
}

// NewBackupCodes returns n one-time recovery codes: the display forms (shown
// once) and their SHA-256 hashes (stored).
func NewBackupCodes(n int) (display, hashes []string, err error) { return newBackupCodes(n) }

// NormalizeBackupCode strips formatting from a backup code so it verifies whether
// typed "ABCD-EFGH", "abcdefgh" or "abcd efgh".
func NormalizeBackupCode(code string) string { return normalizeBackupCode(code) }

// HashToken returns the SHA-256 hex hash used to store reset/verify tokens and
// backup codes (only the hash is ever persisted; the plain value never is).
func HashToken(plain string) string { return hashToken(plain) }

// SecretCipher encrypts/decrypts a recoverable secret (e.g. a TOTP secret) at
// rest with AES-256-GCM. It is an alias of the in-package cipher so the platform
// admin store reuses the identical construction.
type SecretCipher = secretCipher

// NewSecretCipher builds a SecretCipher from key material (SHA-256(keyMaterial) →
// AES-256). Returns an error only when keyMaterial is empty.
func NewSecretCipher(keyMaterial string) (*SecretCipher, error) { return newSecretCipher(keyMaterial) }

// Encrypt seals plain → base64(nonce||ciphertext+tag).
func (c *secretCipher) Encrypt(plain string) (string, error) { return c.encrypt(plain) }

// Decrypt reverses Encrypt; a tampered/wrong-key ciphertext returns an error.
func (c *secretCipher) Decrypt(enc string) (string, error) { return c.decrypt(enc) }
