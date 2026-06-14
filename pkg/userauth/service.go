package userauth

import (
	"context"
	"errors"
	"net/mail"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/miguelangel/appitools/pkg/auth"
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
}

// Service implements the password identity core: signup, login, refresh.
type Service struct {
	store   *Store
	cfg     Config
	limiter *loginLimiter
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
	dummy, _ := HashPassword("appitools-anti-enumeration-timing-equalizer")
	return &Service{
		store:     store,
		cfg:       cfg,
		limiter:   newLoginLimiter(cfg.LoginAttemptsPerMinute, cfg.LoginBurst),
		dummyHash: dummy,
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
// minted, engine-valid JWT (signup auto-logs-in).
type AuthResult struct {
	User  PublicUser `json:"user"`
	Token string     `json:"token"`
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

func normalizeEmail(email string) string { return strings.ToLower(strings.TrimSpace(email)) }

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
