package userauth

import (
	"context"
	"crypto/rand"
	"errors"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// MFA sentinel errors mapped to HTTP status by the handlers.
var (
	ErrMFANotEnrolled = errors.New("userauth: mfa not enrolled")
	ErrMFAInvalidCode = errors.New("userauth: invalid mfa code")
	ErrMFAToken       = errors.New("userauth: invalid or expired mfa token")
	ErrMFAConfig      = errors.New("userauth: mfa not configured")
)

// backupCodeCount is how many one-time recovery codes are issued at MFA
// confirmation (shown once; only their hashes are stored).
const backupCodeCount = 10

// mfaPendingTTL bounds the intermediate mfa_token: a login's second factor must
// be completed promptly.
const mfaPendingTTL = 5 * time.Minute

// mfaPendingClaims is the intermediate token issued after a password verifies but
// BEFORE the second factor. Its claim KEYS (uid/t/purpose) deliberately differ from
// auth.Claims (user_id/role/tenant_id), so even though it is signed with the same
// secret it carries NO role and NO usable identity into the engine's JWT
// middleware — presented as a Bearer to /api/ it is inert (RBAC denies, deny by
// default). It only ever completes the MFA step.
type mfaPendingClaims struct {
	UserID  string `json:"uid"`
	Tenant  string `json:"t"`
	Purpose string `json:"purpose"`
	jwt.RegisteredClaims
}

func (s *Service) signMFAPending(userID, tenantID string) (string, error) {
	now := time.Now()
	c := mfaPendingClaims{
		UserID: userID, Tenant: tenantID, Purpose: "mfa",
		RegisteredClaims: jwt.RegisteredClaims{
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(mfaPendingTTL)),
		},
	}
	return jwt.NewWithClaims(jwt.SigningMethodHS256, c).SignedString([]byte(s.cfg.JWTSecret))
}

func (s *Service) parseMFAPending(tokenStr string) (*mfaPendingClaims, error) {
	tok, err := jwt.ParseWithClaims(tokenStr, &mfaPendingClaims{}, func(t *jwt.Token) (any, error) {
		if t.Method != jwt.SigningMethodHS256 {
			return nil, ErrMFAToken
		}
		return []byte(s.cfg.JWTSecret), nil
	}, jwt.WithExpirationRequired())
	if err != nil {
		return nil, ErrMFAToken
	}
	c, ok := tok.Claims.(*mfaPendingClaims)
	if !ok || !tok.Valid || c.Purpose != "mfa" || c.UserID == "" || c.Tenant == "" {
		return nil, ErrMFAToken
	}
	return c, nil
}

// EnableMFA begins enrollment for an (already authenticated) user: it generates a
// TOTP secret, stores it ENCRYPTED with enabled=false, and returns the secret +
// otpauth URI ONCE for the user to load into their authenticator app. MFA is not
// active until ConfirmMFA proves a working code. The account label in the URI is
// the user's email (cosmetic — shown in the app).
func (s *Service) EnableMFA(ctx context.Context, tenantID, userID string) (secret, uri string, err error) {
	if s.mfaCipher == nil {
		return "", "", ErrMFAConfig
	}
	user, err := s.store.GetByID(ctx, tenantID, userID)
	if err != nil {
		return "", "", err
	}
	secret, err = newTOTPSecret()
	if err != nil {
		return "", "", err
	}
	enc, err := s.mfaCipher.encrypt(secret)
	if err != nil {
		return "", "", err
	}
	if err := s.store.upsertMFASecret(ctx, tenantID, userID, enc); err != nil {
		return "", "", err
	}
	issuer := s.mfaIssuer
	if issuer == "" {
		issuer = "Appximo"
	}
	return secret, otpauthURI(issuer+" ("+tenantID+")", user.Email, secret), nil
}

// ConfirmMFA validates the first TOTP code against the pending secret and, on
// success, flips enabled=true and returns freshly generated one-time backup codes
// (only their hashes are stored). Requiring a valid code before enabling means a
// mis-scanned secret can never lock the user out.
func (s *Service) ConfirmMFA(ctx context.Context, tenantID, userID, code string) ([]string, error) {
	if s.mfaCipher == nil {
		return nil, ErrMFAConfig
	}
	enc, _, found, err := s.store.getMFA(ctx, tenantID, userID)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, ErrMFANotEnrolled
	}
	secret, err := s.mfaCipher.decrypt(enc)
	if err != nil {
		return nil, err
	}
	if !validateTOTP(secret, code, time.Now()) {
		return nil, ErrMFAInvalidCode
	}
	display, hashes, err := newBackupCodes(backupCodeCount)
	if err != nil {
		return nil, err
	}
	if err := s.store.confirmMFA(ctx, tenantID, userID, hashes); err != nil {
		return nil, err
	}
	return display, nil
}

// MFAVerify completes a login's second factor: it validates the intermediate
// mfa_token, then accepts either a current TOTP code (±1 step) OR a one-time backup
// code (consumed). On success it mints the FINAL engine JWT. Throttled per
// (tenant, user) — a 6-digit code is brute-forceable without a limit.
func (s *Service) MFAVerify(ctx context.Context, tenantID, mfaToken, code string) (AuthResult, error) {
	if s.mfaCipher == nil {
		return AuthResult{}, ErrMFAConfig
	}
	claims, err := s.parseMFAPending(mfaToken)
	if err != nil {
		return AuthResult{}, err
	}
	if claims.Tenant != tenantID {
		return AuthResult{}, ErrMFAToken // mfa_token issued for a different tenant
	}
	if !s.mfaLimiter.allow(tenantID, claims.UserID) {
		return AuthResult{}, ErrTooManyAttempts
	}
	enc, enabled, found, err := s.store.getMFA(ctx, tenantID, claims.UserID)
	if err != nil {
		return AuthResult{}, err
	}
	if !found || !enabled {
		return AuthResult{}, ErrMFANotEnrolled
	}
	ok, err := s.checkSecondFactor(ctx, tenantID, claims.UserID, enc, code)
	if err != nil {
		return AuthResult{}, err
	}
	if !ok {
		return AuthResult{}, ErrMFAInvalidCode
	}
	user, err := s.store.GetByID(ctx, tenantID, claims.UserID)
	if err != nil {
		return AuthResult{}, err
	}
	token, err := s.mint(user, tenantID)
	if err != nil {
		return AuthResult{}, err
	}
	return AuthResult{User: toPublic(user), Token: token}, nil
}

// checkSecondFactor accepts a current TOTP code OR a one-time backup code (the
// latter is consumed atomically). TOTP is tried first (no DB write on the common
// path); a backup code is the fallback.
func (s *Service) checkSecondFactor(ctx context.Context, tenantID, userID, encSecret, code string) (bool, error) {
	secret, err := s.mfaCipher.decrypt(encSecret)
	if err != nil {
		return false, err
	}
	if validateTOTP(secret, code, time.Now()) {
		return true, nil
	}
	return s.store.consumeBackupCode(ctx, tenantID, userID, hashToken(normalizeBackupCode(code)))
}

// DisableMFA turns MFA off, but ONLY when the caller proves a second factor (a
// current TOTP code OR a backup code) or the account password — never the session
// JWT alone, so a stolen access token cannot strip the protection.
func (s *Service) DisableMFA(ctx context.Context, tenantID, userID, code, password string) error {
	if s.mfaCipher == nil {
		return ErrMFAConfig
	}
	enc, _, found, err := s.store.getMFA(ctx, tenantID, userID)
	if err != nil {
		return err
	}
	if !found {
		return ErrMFANotEnrolled
	}
	verified := false
	if code != "" {
		if ok, cerr := s.checkSecondFactor(ctx, tenantID, userID, enc, code); cerr != nil {
			return cerr
		} else if ok {
			verified = true
		}
	}
	if !verified && password != "" {
		user, uerr := s.store.GetByID(ctx, tenantID, userID)
		if uerr != nil {
			return uerr
		}
		if ok, _ := VerifyPassword(password, user.PasswordHash); ok {
			verified = true
		}
	}
	if !verified {
		return ErrMFAInvalidCode
	}
	return s.store.disableMFA(ctx, tenantID, userID)
}

// newBackupCodes returns n recovery codes: the display forms (shown once, e.g.
// "ABCD-EFGH") and their SHA-256 hashes (stored). They are high-entropy random
// secrets, so a fast hash is correct (same reasoning as the email tokens).
func newBackupCodes(n int) (display, hashes []string, err error) {
	display = make([]string, 0, n)
	hashes = make([]string, 0, n)
	for i := 0; i < n; i++ {
		b := make([]byte, 5) // 40 bits → 8 base32 chars
		if _, err := rand.Read(b); err != nil {
			return nil, nil, err
		}
		raw := base32NoPad.EncodeToString(b) // 8 chars
		code := raw[:4] + "-" + raw[4:]
		display = append(display, code)
		hashes = append(hashes, hashToken(normalizeBackupCode(code)))
	}
	return display, hashes, nil
}

// normalizeBackupCode strips formatting (dashes/spaces) and uppercases, so a code
// verifies whether the user types "ABCD-EFGH", "abcdefgh" or "abcd efgh".
func normalizeBackupCode(code string) string {
	var b strings.Builder
	for _, r := range strings.ToUpper(code) {
		if (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	return b.String()
}
