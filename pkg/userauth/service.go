package userauth

import (
	"context"
	"errors"
	"net/http"
	"net/mail"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/miguelangel/appitools/pkg/auth"
	"github.com/miguelangel/appitools/pkg/outbox"
)

// Sentinel errors the handlers map to HTTP status codes. Login NEVER distinguishes
// "unknown email" from "wrong password": both surface as ErrInvalidCredentials
// (anti-enumeration). Signup duplicate is the one case that reports existence
// (ErrEmailTaken → 409), the documented, conventional signup behaviour.
var (
	ErrSignupDisabled     = errors.New("userauth: public signup is disabled")
	ErrInvalidEmail       = errors.New("userauth: invalid email")
	ErrWeakPassword       = errors.New("userauth: password too short")
	ErrInvalidCredentials = errors.New("userauth: invalid credentials")
	ErrTooManyAttempts    = errors.New("userauth: too many login attempts")
	ErrInvalidToken       = errors.New("userauth: invalid or expired token")
	ErrTenantMismatch     = errors.New("userauth: token tenant mismatch")
	ErrEmailNotVerified   = errors.New("userauth: email not verified")
	ErrUserSuspended      = errors.New("userauth: account suspended")
	ErrTenantSuspended    = errors.New("userauth: tenant suspended")
)

// Config configures a Service. JWTSecret and SignupRole are the levers a deployer
// sets; the rest have sane defaults.
type Config struct {
	// JWTSecret signs login/refresh tokens — the SAME secret the engine's JWT
	// middleware validates with, so a login token is indistinguishable from an
	// externally-minted one (one claims contract, no second token path).
	JWTSecret string
	// SignupRole is the role assigned to every PUBLIC signup. An empty SignupRole
	// DISABLES public signup (POST /auth/signup → 403): safe by default, no
	// accidental self-service account creation. A client-supplied role is always
	// ignored (a public endpoint must never let a caller pick its own role —
	// privilege-escalation guard).
	SignupRole string
	// MinPasswordLength is the minimum accepted password length (default 8).
	MinPasswordLength int
	// TokenTTL is the lifetime of issued tokens (default 24h, matching the
	// engine's GenerateToken default).
	TokenTTL time.Duration
	// LoginAttemptsPerMinute / LoginBurst bound login attempts per (tenant,email)
	// (defaults 5 / 5) — online brute-force defence on top of the tenant limiter.
	LoginAttemptsPerMinute int
	LoginBurst             int

	// EmailTopic is the outbox topic the reset/verify flows enqueue email events
	// to (default "email.send"). It MUST match the email worker's
	// APPITOOLS_EMAIL_TOPIC so the consumer picks the events up.
	EmailTopic string
	// BaseURL optionally overrides the origin used to build email links
	// (e.g. "https://acme.example.com"). Empty ⇒ the link origin is derived from
	// the request Host, which is the multi-tenant-correct default (the link points
	// back at the tenant subdomain the request arrived on).
	BaseURL string
	// RequireVerified, when true, blocks login for a user whose email_verified is
	// false (→ 403). Default false: AUTH-CORE's login flow is unchanged unless an
	// app opts in to mandatory verification.
	RequireVerified bool

	// TenantActive, when set, is consulted on LOGIN to block a suspended tenant
	// (the admin API suspends a tenant by flipping a control-plane flag; this
	// predicate reads it). It runs ONLY on the login path — never the CRUD/JWT hot
	// path — so a suspended tenant can mint no NEW sessions while the measured p50
	// is untouched. nil ⇒ every tenant is active (zero overhead, current behaviour).
	TenantActive func(ctx context.Context, tenantID string) bool

	// --- AUTH-OAUTH-V1: social login ---
	// OAuthProviders maps a provider name ("google"/"github"/"microsoft") to its
	// client credentials. A provider with an empty ClientID is NOT offered (an
	// unconfigured provider never fails the boot). Empty map ⇒ OAuth fully off.
	OAuthProviders map[string]OAuthProviderConfig
	// OAuthCallbackURL is the FIXED public origin the provider redirects back to
	// (e.g. "https://auth.example.com"). It must be the redirect URI registered
	// with each provider. Empty ⇒ derived from the request (dev/single-domain).
	OAuthCallbackURL string
	// OAuthDefaultRole is the role assigned to a user auto-created on first social
	// login. Empty falls back to SignupRole; if BOTH are empty, a brand-new social
	// email is rejected (existing users still link/login) — auto-provision is
	// opt-in, like signup.
	OAuthDefaultRole string
	// OAuthSuccessRedirect, when set, makes the callback 302 to "<url>#token=<jwt>"
	// instead of returning JSON (convenient for a browser SPA). Empty ⇒ JSON.
	OAuthSuccessRedirect string

	// test hooks (same-package only): override provider endpoints + HTTP client so
	// tests never reach a real provider.
	oauthEndpoints  map[string]oauthEndpoints
	oauthHTTPClient *http.Client

	// --- AUTH-MFA-V1: TOTP multi-factor ---
	// MFAKey is the key material that ENCRYPTS the TOTP secret at rest (AES-256-GCM
	// over SHA-256(MFAKey)). Empty falls back to JWTSecret. A TOTP secret must be
	// recoverable (the server re-derives codes), so it is encrypted, not hashed.
	MFAKey string
	// MFAIssuer is the issuer label shown in the authenticator app (otpauth URI).
	// Empty ⇒ "Appitools".
	MFAIssuer string
}

// Service implements the password identity core: signup, login, refresh, plus
// the AUTH-EMAIL-V1 reset + verification flows.
type Service struct {
	store        *Store
	cfg          Config
	limiter      *loginLimiter // login throttle, per (tenant,email)
	emailLimiter *loginLimiter // reset/verify REQUEST throttle (anti email-spam)
	mfaLimiter   *loginLimiter // mfa/verify throttle, per (tenant,user) — anti 6-digit brute-force
	oauth        *oauthManager // social login (AUTH-OAUTH-V1); nil when no provider configured
	mfaCipher    *secretCipher // encrypts the TOTP secret at rest (AUTH-MFA-V1)
	mfaIssuer    string        // otpauth issuer label
	// dummyHash equalizes login timing for an unknown email: we run a verify
	// against it so "no such user" costs the same as "wrong password", denying a
	// timing oracle for email enumeration.
	dummyHash string
}

// NewService builds a Service. dummyHash is computed once (an argon2id hash of a
// random-ish constant); its only purpose is constant-ish login timing.
func NewService(store *Store, cfg Config) *Service {
	if cfg.MinPasswordLength <= 0 {
		cfg.MinPasswordLength = 8
	}
	if cfg.TokenTTL <= 0 {
		cfg.TokenTTL = 24 * time.Hour
	}
	if cfg.EmailTopic == "" {
		cfg.EmailTopic = "email.send"
	}
	dummy, _ := HashPassword("appitools-anti-enumeration-timing-equalizer")
	// MFA secret cipher: encrypts the TOTP secret at rest. Key material is MFAKey,
	// falling back to the (always-present) JWTSecret, so MFA works out of the box.
	mfaKey := cfg.MFAKey
	if mfaKey == "" {
		mfaKey = cfg.JWTSecret
	}
	cipher, _ := newSecretCipher(mfaKey) // nil only if key material empty (JWTSecret is required)
	return &Service{
		store:   store,
		cfg:     cfg,
		limiter: newLoginLimiter(cfg.LoginAttemptsPerMinute, cfg.LoginBurst),
		// Reset/verify email requests are far rarer than logins and each one sends
		// an email — throttle tighter (3/min) to blunt email-spam / amplification.
		emailLimiter: newLoginLimiter(3, 3),
		mfaLimiter:   newLoginLimiter(cfg.LoginAttemptsPerMinute, cfg.LoginBurst),
		oauth:        newOAuthManager(cfg),
		mfaCipher:    cipher,
		mfaIssuer:    cfg.MFAIssuer,
		dummyHash:    dummy,
	}
}

// SignupEnabled reports whether public signup is configured.
func (s *Service) SignupEnabled() bool { return s.cfg.SignupRole != "" }

// PublicUser is the user shape returned to clients — NEVER the password hash.
type PublicUser struct {
	ID            string    `json:"id"`
	Email         string    `json:"email"`
	Role          string    `json:"role"`
	EmailVerified bool      `json:"email_verified"`
	CreatedAt     time.Time `json:"created_at"`
}

func toPublic(u User) PublicUser {
	return PublicUser{ID: u.ID, Email: u.Email, Role: u.Role, EmailVerified: u.EmailVerified, CreatedAt: u.CreatedAt}
}

// AuthResult is returned by Signup and Login: the user (no hash) plus a freshly
// minted, engine-valid JWT (signup auto-logs-in). When a password login hits a
// user with MFA enabled, Token is empty and MFARequired+MFAToken are set instead:
// the final JWT is withheld until /auth/mfa/verify succeeds.
type AuthResult struct {
	User        PublicUser `json:"user"`
	Token       string     `json:"token,omitempty"`
	MFARequired bool       `json:"mfa_required,omitempty"`
	MFAToken    string     `json:"mfa_token,omitempty"`
}

// Signup creates a user in the tenant's schema and returns it plus a token.
// The role is ALWAYS the configured SignupRole (client input ignored). Email is
// normalized (trim + lowercase). A duplicate within the tenant → ErrEmailTaken;
// the same email in another tenant is independent and succeeds (the advantage).
func (s *Service) Signup(ctx context.Context, tenantID, email, password string) (AuthResult, error) {
	if !s.SignupEnabled() {
		return AuthResult{}, ErrSignupDisabled
	}
	email = normalizeEmail(email)
	if !validEmail(email) {
		return AuthResult{}, ErrInvalidEmail
	}
	if len([]rune(password)) < s.cfg.MinPasswordLength {
		return AuthResult{}, ErrWeakPassword
	}
	hash, err := HashPassword(password)
	if err != nil {
		return AuthResult{}, err
	}
	user, err := s.store.Create(ctx, tenantID, email, hash, s.cfg.SignupRole)
	if err != nil {
		return AuthResult{}, err // ErrEmailTaken or internal
	}
	token, err := s.mint(user, tenantID)
	if err != nil {
		return AuthResult{}, err
	}
	return AuthResult{User: toPublic(user), Token: token}, nil
}

// Login verifies credentials and returns a token. It is uniform across "unknown
// email" and "wrong password" (same ErrInvalidCredentials, same ~timing) so a
// caller cannot enumerate which emails exist. Throttled per (tenant, email).
func (s *Service) Login(ctx context.Context, tenantID, email, password string) (AuthResult, error) {
	email = normalizeEmail(email)
	if !s.limiter.allow(tenantID, email) {
		return AuthResult{}, ErrTooManyAttempts
	}
	// A suspended tenant mints no new sessions (admin-API control-plane state).
	// Checked before the lookup — a suspended tenant is rejected regardless of
	// credentials (tenant existence is the public subdomain, not a secret).
	if s.cfg.TenantActive != nil && !s.cfg.TenantActive(ctx, tenantID) {
		return AuthResult{}, ErrTenantSuspended
	}
	user, err := s.store.GetByEmail(ctx, tenantID, email)
	if errors.Is(err, ErrUserNotFound) {
		// Equalize timing: do the same argon2 work we'd do for a real user, then
		// fail generically. No information leaks about whether the email exists.
		_, _ = VerifyPassword(password, s.dummyHash)
		return AuthResult{}, ErrInvalidCredentials
	}
	if err != nil {
		return AuthResult{}, err
	}
	ok, err := VerifyPassword(password, user.PasswordHash)
	if err != nil || !ok {
		return AuthResult{}, ErrInvalidCredentials
	}
	// Administrative lockout (managed by the admin API). Checked AFTER the password
	// verifies, so it only ever reveals "suspended" to someone holding the correct
	// password — never an enumeration vector. A suspended user cannot complete login
	// (no token, and no MFA challenge is even started).
	if user.Suspended {
		return AuthResult{}, ErrUserSuspended
	}
	// Optional mandatory verification (opt-in via RequireVerified). This is checked
	// AFTER the password verifies, so it only ever reveals "verified or not" to
	// someone who already holds the correct password — never an enumeration vector.
	if s.cfg.RequireVerified && !user.EmailVerified {
		return AuthResult{}, ErrEmailNotVerified
	}
	// Second factor (AUTH-MFA-V1): if the user has MFA enabled, DO NOT issue the
	// final JWT yet — issue a short-lived mfa_token and require /auth/mfa/verify.
	// One indexed lookup, only on the (occasional) login path, never on the CRUD
	// hot path. mfaCipher is always set (JWTSecret is required), so this is live.
	if s.mfaCipher != nil {
		enabled, merr := s.store.MFAEnabled(ctx, tenantID, user.ID)
		if merr != nil {
			return AuthResult{}, merr
		}
		if enabled {
			mfaToken, terr := s.signMFAPending(user.ID, tenantID)
			if terr != nil {
				return AuthResult{}, terr
			}
			return AuthResult{MFARequired: true, MFAToken: mfaToken}, nil
		}
	}
	token, err := s.mint(user, tenantID)
	if err != nil {
		return AuthResult{}, err
	}
	return AuthResult{User: toPublic(user), Token: token}, nil
}

// Refresh re-mints a token from a still-valid one, extending exp. The JWT stays
// stateless (no server session): the token is validated, its tenant is checked
// against the request tenant (no cross-tenant refresh), and a fresh token with
// the same identity/role is issued. A token whose user was later deleted keeps
// working until exp — the standard stateless-JWT trade-off, documented.
func (s *Service) Refresh(ctx context.Context, tenantID, tokenStr string) (string, error) {
	claims, err := auth.ValidateToken(tokenStr, s.cfg.JWTSecret)
	if err != nil {
		return "", ErrInvalidToken
	}
	if claims.TenantID != "" && claims.TenantID != tenantID {
		return "", ErrTenantMismatch
	}
	user := User{ID: claims.UserID, Role: claims.Role}
	return s.mint(user, tenantID)
}

// mint issues an engine-valid HS256 JWT carrying user_id, role and tenant_id —
// the exact claims shape auth.ValidateToken accepts. It reuses the engine's
// GenerateTokenWithTTL (one mint path), preserving Subject for audit.
func (s *Service) mint(user User, tenantID string) (string, error) {
	c := auth.Claims{
		UserID:   user.ID,
		Role:     user.Role,
		TenantID: tenantID,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject: user.ID,
		},
	}
	return auth.GenerateTokenWithTTL(c, s.cfg.JWTSecret, s.cfg.TokenTTL)
}

// --- AUTH-EMAIL-V1: password reset + email verification ---------------------
//
// Both flows enqueue an "email.send" event to the transactional outbox IN THE
// SAME TX as the token row, so the email and the token are atomic. The email is
// delivered ASYNC by the email consumer (cmd/appitools-worker, mode=email) — the
// request returns immediately. If no email worker is running, the event waits
// durably in the outbox and goes out when one starts (the user already saw "if
// that email exists, we sent a link"). The plain token rides the email link;
// only its SHA-256 hash is stored.

// RequestVerify issues an email-verification link for email, IF a user with that
// email exists and is not already verified. It is uniform regardless of existence
// (anti-enumeration): it ALWAYS returns nil to the caller (a real send happens
// only when the user exists), so a client cannot probe which emails are
// registered. Throttled per (tenant, email) to blunt email-spam.
func (s *Service) RequestVerify(ctx context.Context, tenantID, email, linkBase string) error {
	email = normalizeEmail(email)
	if !s.emailLimiter.allow(tenantID, email) {
		return ErrTooManyAttempts
	}
	user, err := s.store.GetByEmail(ctx, tenantID, email)
	if errors.Is(err, ErrUserNotFound) {
		return nil // uniform response; no email, no leak
	}
	if err != nil {
		return err
	}
	if user.EmailVerified {
		return nil // already verified — nothing to send, same uniform response
	}
	return s.issueEmailToken(ctx, tenantID, user, tokenVerify, verifyTTL, linkBase)
}

// ConfirmVerify consumes a verification token and marks the user's email
// verified. The token is single-use and consumed atomically with the flag flip.
func (s *Service) ConfirmVerify(ctx context.Context, tenantID, plainToken string) error {
	if plainToken == "" {
		return ErrInvalidToken
	}
	if err := s.store.ensureTokens(ctx, tenantID); err != nil {
		return err
	}
	tx, err := s.store.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(context.Background()) //nolint:errcheck
	userID, err := s.store.consumeToken(ctx, tx, tenantID, tokenVerify, hashToken(plainToken))
	if errors.Is(err, errTokenInvalid) {
		return ErrInvalidToken
	}
	if err != nil {
		return err
	}
	if err := s.store.setEmailVerifiedTx(ctx, tx, tenantID, userID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// RequestReset issues a password-reset link for email IF a user exists. Uniform
// regardless of existence (anti-enumeration): always returns nil. Throttled.
func (s *Service) RequestReset(ctx context.Context, tenantID, email, linkBase string) error {
	email = normalizeEmail(email)
	if !s.emailLimiter.allow(tenantID, email) {
		return ErrTooManyAttempts
	}
	user, err := s.store.GetByEmail(ctx, tenantID, email)
	if errors.Is(err, ErrUserNotFound) {
		return nil // uniform response; no email, no leak
	}
	if err != nil {
		return err
	}
	return s.issueEmailToken(ctx, tenantID, user, tokenReset, resetTTL, linkBase)
}

// ConfirmReset consumes a reset token and sets the user's new password. The token
// is consumed atomically with the password update, and ALL other still-pending
// reset tokens for that user are invalidated in the same tx (a completed reset
// kills every outstanding reset link). The new password must meet the minimum
// length.
func (s *Service) ConfirmReset(ctx context.Context, tenantID, plainToken, newPassword string) error {
	if plainToken == "" {
		return ErrInvalidToken
	}
	if len([]rune(newPassword)) < s.cfg.MinPasswordLength {
		return ErrWeakPassword
	}
	hash, err := HashPassword(newPassword)
	if err != nil {
		return err
	}
	if err := s.store.ensureTokens(ctx, tenantID); err != nil {
		return err
	}
	tx, err := s.store.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(context.Background()) //nolint:errcheck
	userID, err := s.store.consumeToken(ctx, tx, tenantID, tokenReset, hashToken(plainToken))
	if errors.Is(err, errTokenInvalid) {
		return ErrInvalidToken
	}
	if err != nil {
		return err
	}
	if err := s.store.setPasswordTx(ctx, tx, tenantID, userID, hash); err != nil {
		return err
	}
	// Invalidate any OTHER outstanding reset tokens for this user.
	if err := s.store.invalidateUserTokensTx(ctx, tx, tenantID, userID, tokenReset); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// issueEmailToken creates a single-use token and enqueues its email — both in ONE
// transaction so the token and the email event commit together or not at all.
func (s *Service) issueEmailToken(ctx context.Context, tenantID string, user User, ttype string, ttl time.Duration, linkBase string) error {
	if err := s.store.ensureTokens(ctx, tenantID); err != nil {
		return err
	}
	plain, err := newPlainToken()
	if err != nil {
		return err
	}
	tmpl, path := emailTemplateAndPath(ttype)
	link := strings.TrimRight(linkBase, "/") + path + plain

	tx, err := s.store.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(context.Background()) //nolint:errcheck

	if err := s.store.insertTokenTx(ctx, tx, tenantID, user.ID, ttype, hashToken(plain), time.Now().Add(ttl)); err != nil {
		return err
	}
	ev := map[string]any{
		"to":       user.Email,
		"template": tmpl,
		"data":     map[string]any{"name": user.Email, "link": link},
	}
	if _, err := outbox.Enqueue(ctx, tx, tenantID, s.cfg.EmailTopic, ev); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// emailTemplateAndPath maps a token type to its email template name and the URL
// path (with trailing "?token=") the link points at. verify reuses the built-in
// "verification" template; reset uses the new "reset" template.
func emailTemplateAndPath(ttype string) (template, path string) {
	if ttype == tokenReset {
		return "reset", "/auth/reset?token="
	}
	return "verification", "/auth/verify?token="
}

func normalizeEmail(email string) string { return strings.ToLower(strings.TrimSpace(email)) }

// ValidateEmail normalizes (trim + lowercase) and format-checks an email for
// callers OUTSIDE this package that create users (the library's Ctx.CreateUser)
// — one email rule for signup, admin API and custom handlers. ok is false when
// the address is not acceptable; normalized is always the canonical form.
func ValidateEmail(email string) (normalized string, ok bool) {
	normalized = normalizeEmail(email)
	return normalized, validEmail(normalized)
}

// validEmail accepts an addr-spec parseable by net/mail with a single address
// and a domain part. It is a format check, not deliverability.
func validEmail(email string) bool {
	if email == "" || len(email) > 254 {
		return false
	}
	addr, err := mail.ParseAddress(email)
	if err != nil || addr.Address != email {
		return false
	}
	at := strings.LastIndex(email, "@")
	return at > 0 && strings.Contains(email[at+1:], ".")
}
