package platformadmin

import (
	"context"
	"encoding/json"
	"errors"
	"net/mail"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/miguelangel/appitools/pkg/controlplane"
	"github.com/miguelangel/appitools/pkg/db"
	"github.com/miguelangel/appitools/pkg/files"
	"github.com/miguelangel/appitools/pkg/userauth"
)

// Sentinel errors mapped to HTTP status by the handlers.
var (
	ErrInvalidCredentials = errors.New("platformadmin: invalid credentials")
	ErrTooManyAttempts    = errors.New("platformadmin: too many attempts")
	ErrWeakPassword       = errors.New("platformadmin: password too short")
	ErrInvalidEmail       = errors.New("platformadmin: invalid email")
	ErrMFANotEnrolled     = errors.New("platformadmin: mfa not enrolled")
	ErrMFAInvalidCode     = errors.New("platformadmin: invalid mfa code")
	ErrMFAConfig          = errors.New("platformadmin: mfa not configured")
	ErrUnknownRole        = errors.New("platformadmin: role not declared in the schema RBAC")
	ErrTenantNotFound     = errors.New("platformadmin: tenant not found")
	ErrConfirmRequired    = errors.New("platformadmin: explicit confirmation required")
	// ErrSchemaRejected marks a PersistBootSchema failure caused by an INVALID
	// schema — nothing was persisted and no restart happens (→ 422). Any other
	// persist failure (e.g. an unwritable schema file) is an infrastructure 500.
	ErrSchemaRejected = errors.New("schema rejected")
)

// Config configures the platform admin Service.
type Config struct {
	// JWTSecret signs platform tokens — the SAME engine secret, one signing key.
	JWTSecret string
	// MFAKey encrypts the TOTP secret at rest (falls back to JWTSecret).
	MFAKey string
	// MFAIssuer is the authenticator-app label (default "Appitools Platform").
	MFAIssuer string
	// SuperAdminRole is the role stamped on platform admins (default
	// platform_super_admin). It is a platform marker, not a tenant RBAC role.
	SuperAdminRole string
	// MinPasswordLength for a new admin (default 12 — a privileged credential).
	MinPasswordLength int

	// RoleExists reports whether a role name is declared in the (boot) schema RBAC.
	// Used to validate a role before assigning it to a tenant user (so the admin API
	// returns a clear error instead of creating a user who can never be authorized).
	// nil ⇒ no validation (any role accepted).
	RoleExists func(role string) bool
	// TenantAdminRole reports whether a tenant RBAC role is "admin-grade" — broad
	// enough that a holder may view their OWN tenant's observability. nil ⇒ no tenant
	// user is treated as an observability admin (only the platform super-admin sees
	// observability). Implemented over the boot RBAC policy (inherits RBAC).
	TenantAdminRole func(role string) bool

	// ServedResources is the set of resource names the engine serves LIVE — the REST
	// routes, GraphQL types and RBAC policy are all compiled from the BOOT --schema
	// (codegen.BuildRouter / gqlhandler.BuildHandler / rbac.RBACMiddleware), globally
	// and at boot. A resource deployed to a tenant but ABSENT here gets its tables
	// provisioned by the migration, but its API is unavailable (403 from RBAC deny-by-
	// default, or 404 when a wildcard role passes RBAC but no route exists) until the
	// engine restarts with a schema that includes it. Exposed
	// read-only at GET /admin/served-resources so the editor can honestly warn before
	// a new-resource deploy. Empty ⇒ the route reports an empty set.
	ServedResources []string

	// ServedResourcesFn, when non-nil, supplies the LIVE served-resource list and
	// takes precedence over the static ServedResources slice. It exists for the
	// in-process fleet hot-swap (MT-STRUCT-S4): after a per-app hot-swap the served
	// surface changes WITHOUT a process restart, so the editor's post-deploy verify
	// must read the current surface's resources, not the boot list. Single-engine
	// leaves it nil (a deploy re-execs, so the boot slice is authoritative).
	ServedResourcesFn func() []string

	// PersistBootSchema validates a schema and ATOMICALLY persists it as the new
	// BOOT schema (the file the engine loads at start), backing up the previous
	// one for rollback (UI-F4-S2). An invalid schema must be reported wrapped in
	// ErrSchemaRejected (→ 422) with NOTHING written. nil ⇒ the engine cannot
	// self-restart (POST /admin/engine/schema answers 503).
	PersistBootSchema func(raw json.RawMessage) error
	// TriggerRestart initiates the engine's graceful self-restart (drain via
	// readyz→503 + http.Server.Shutdown, then relaunch with the persisted
	// schema). Called only AFTER PersistBootSchema succeeded — never with an
	// invalid schema.
	TriggerRestart func()
}

// Service is the platform admin backend: super-admin auth (login + MFA) and the
// consolidated tenant / user / observability admin operations.
type Service struct {
	store     *Store
	users     *userauth.Store // tenant users (auth_users), reused — not reimplemented
	cp        controlplane.Service
	pool      *pgxpool.Pool
	tdb       *db.TenantDB // tenant-scoped DB for read-only data browsing (set via SetTenantDB)
	cfg       Config
	cipher    *userauth.SecretCipher
	limiter   *loginThrottle
	mfaLimit  *loginThrottle
	dummyHash string
	adminKey  string // machine credential accepted on management routes (set by Register)

	// Files manager wiring (UI-F5-S1; set via SetFileStore, like tdb).
	files         *files.Store
	filesMaxBytes int64
	filesTokenTTL time.Duration
	filesSecret   []byte

	ensureSuspendCol func(ctx context.Context) error
}

// NewService builds a platform admin Service. cp wraps the existing control plane
// (tenant create/lookup); users is the existing per-tenant identity store.
func NewService(store *Store, users *userauth.Store, cp controlplane.Service, pool *pgxpool.Pool, cfg Config) *Service {
	if cfg.SuperAdminRole == "" {
		cfg.SuperAdminRole = DefaultSuperAdminRole
	}
	if cfg.MinPasswordLength <= 0 {
		cfg.MinPasswordLength = 12
	}
	if cfg.MFAIssuer == "" {
		cfg.MFAIssuer = "Appitools Platform"
	}
	mfaKey := cfg.MFAKey
	if mfaKey == "" {
		mfaKey = cfg.JWTSecret
	}
	cipher, _ := userauth.NewSecretCipher(mfaKey)
	dummy, _ := userauth.HashPassword("appitools-platform-anti-enumeration-timing-equalizer")
	s := &Service{
		store:     store,
		users:     users,
		cp:        cp,
		pool:      pool,
		cfg:       cfg,
		cipher:    cipher,
		limiter:   newLoginThrottle(5, 5),
		mfaLimit:  newLoginThrottle(5, 5),
		dummyHash: dummy,
	}
	// Lazily add public.tenants.suspended once (control-plane state for suspension).
	var done bool
	s.ensureSuspendCol = func(ctx context.Context) error {
		if done {
			return nil
		}
		_, err := pool.Exec(ctx, `ALTER TABLE public.tenants ADD COLUMN IF NOT EXISTS suspended BOOLEAN NOT NULL DEFAULT false`)
		if err == nil {
			done = true
		}
		return err
	}
	return s
}

// PublicAdmin is the admin shape returned to clients — never the password hash.
type PublicAdmin struct {
	ID        string    `json:"id"`
	Email     string    `json:"email"`
	Role      string    `json:"role"`
	CreatedAt time.Time `json:"created_at"`
}

func toPublicAdmin(a Admin) PublicAdmin {
	return PublicAdmin{ID: a.ID, Email: a.Email, Role: a.Role, CreatedAt: a.CreatedAt}
}

// PlatformAuthResult is returned by Login. On a password-only success Token is set;
// when the admin has MFA, Token is withheld and MFARequired+MFAToken are set.
type PlatformAuthResult struct {
	Admin       PublicAdmin `json:"admin,omitempty"`
	Token       string      `json:"token,omitempty"`
	MFARequired bool        `json:"mfa_required,omitempty"`
	MFAToken    string      `json:"mfa_token,omitempty"`
}

// CreateAdmin provisions a platform super-admin. It is the BOOTSTRAP path (no
// public signup): the first admin is created via the CLI (gated by the admin key /
// direct DB access), because a super-admin cannot authenticate with a super-admin
// that does not yet exist. role defaults to the configured super-admin role.
func (s *Service) CreateAdmin(ctx context.Context, email, password, role string) (PublicAdmin, error) {
	email = normalizeEmail(email)
	if !validEmail(email) {
		return PublicAdmin{}, ErrInvalidEmail
	}
	if len([]rune(password)) < s.cfg.MinPasswordLength {
		return PublicAdmin{}, ErrWeakPassword
	}
	if role == "" {
		role = s.cfg.SuperAdminRole
	}
	hash, err := userauth.HashPassword(password)
	if err != nil {
		return PublicAdmin{}, err
	}
	a, err := s.store.CreateAdmin(ctx, email, hash, role)
	if err != nil {
		return PublicAdmin{}, err
	}
	return toPublicAdmin(a), nil
}

// Login verifies a platform admin's credentials and returns a platform token (or
// an MFA challenge). Uniform on unknown-email vs wrong-password (anti-enumeration,
// equalized timing). Throttled per email.
func (s *Service) Login(ctx context.Context, email, password string) (PlatformAuthResult, error) {
	email = normalizeEmail(email)
	if !s.limiter.allow(email) {
		return PlatformAuthResult{}, ErrTooManyAttempts
	}
	a, err := s.store.GetAdminByEmail(ctx, email)
	if errors.Is(err, ErrAdminNotFound) {
		_, _ = userauth.VerifyPassword(password, s.dummyHash) // equalize timing
		return PlatformAuthResult{}, ErrInvalidCredentials
	}
	if err != nil {
		return PlatformAuthResult{}, err
	}
	ok, err := userauth.VerifyPassword(password, a.PasswordHash)
	if err != nil || !ok {
		return PlatformAuthResult{}, ErrInvalidCredentials
	}
	enabled, err := s.store.MFAEnabled(ctx, a.ID)
	if err != nil {
		return PlatformAuthResult{}, err
	}
	if enabled {
		tok, terr := signPlatformMFAPending(a.ID, s.cfg.JWTSecret)
		if terr != nil {
			return PlatformAuthResult{}, terr
		}
		return PlatformAuthResult{MFARequired: true, MFAToken: tok}, nil
	}
	tok, err := signPlatformToken(a.ID, a.Role, s.cfg.JWTSecret)
	if err != nil {
		return PlatformAuthResult{}, err
	}
	return PlatformAuthResult{Admin: toPublicAdmin(a), Token: tok}, nil
}

// Refresh re-mints a platform token from a still-valid one.
func (s *Service) Refresh(ctx context.Context, tokenStr string) (string, error) {
	c, err := parsePlatformToken(tokenStr, s.cfg.JWTSecret)
	if err != nil {
		return "", ErrPlatformToken
	}
	// Confirm the admin still exists (a deleted admin's token stops refreshing).
	a, err := s.store.GetAdminByID(ctx, c.AdminID)
	if err != nil {
		return "", ErrPlatformToken
	}
	return signPlatformToken(a.ID, a.Role, s.cfg.JWTSecret)
}

// --- MFA --------------------------------------------------------------------

// EnableMFA begins enrollment for an authenticated admin: generates a TOTP secret,
// stores it encrypted (enabled=false), returns the secret + otpauth URI once.
func (s *Service) EnableMFA(ctx context.Context, adminID string) (secret, uri string, err error) {
	if s.cipher == nil {
		return "", "", ErrMFAConfig
	}
	a, err := s.store.GetAdminByID(ctx, adminID)
	if err != nil {
		return "", "", err
	}
	secret, err = userauth.NewTOTPSecret()
	if err != nil {
		return "", "", err
	}
	enc, err := s.cipher.Encrypt(secret)
	if err != nil {
		return "", "", err
	}
	if err := s.store.UpsertMFASecret(ctx, adminID, enc); err != nil {
		return "", "", err
	}
	return secret, userauth.OTPAuthURI(s.cfg.MFAIssuer, a.Email, secret), nil
}

// ConfirmMFA validates the first code and activates MFA, returning backup codes.
func (s *Service) ConfirmMFA(ctx context.Context, adminID, code string) ([]string, error) {
	if s.cipher == nil {
		return nil, ErrMFAConfig
	}
	enc, _, found, err := s.store.GetMFA(ctx, adminID)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, ErrMFANotEnrolled
	}
	secret, err := s.cipher.Decrypt(enc)
	if err != nil {
		return nil, err
	}
	if !userauth.ValidateTOTPNow(secret, code) {
		return nil, ErrMFAInvalidCode
	}
	display, hashes, err := userauth.NewBackupCodes(10)
	if err != nil {
		return nil, err
	}
	if err := s.store.ConfirmMFA(ctx, adminID, hashes); err != nil {
		return nil, err
	}
	return display, nil
}

// MFAVerify completes a login's second factor and mints the final platform token.
func (s *Service) MFAVerify(ctx context.Context, mfaToken, code string) (PlatformAuthResult, error) {
	if s.cipher == nil {
		return PlatformAuthResult{}, ErrMFAConfig
	}
	c, err := parsePlatformMFAPending(mfaToken, s.cfg.JWTSecret)
	if err != nil {
		return PlatformAuthResult{}, err
	}
	if !s.mfaLimit.allow(c.AdminID) {
		return PlatformAuthResult{}, ErrTooManyAttempts
	}
	enc, enabled, found, err := s.store.GetMFA(ctx, c.AdminID)
	if err != nil {
		return PlatformAuthResult{}, err
	}
	if !found || !enabled {
		return PlatformAuthResult{}, ErrMFANotEnrolled
	}
	ok, err := s.checkSecondFactor(ctx, c.AdminID, enc, code)
	if err != nil {
		return PlatformAuthResult{}, err
	}
	if !ok {
		return PlatformAuthResult{}, ErrMFAInvalidCode
	}
	a, err := s.store.GetAdminByID(ctx, c.AdminID)
	if err != nil {
		return PlatformAuthResult{}, err
	}
	tok, err := signPlatformToken(a.ID, a.Role, s.cfg.JWTSecret)
	if err != nil {
		return PlatformAuthResult{}, err
	}
	return PlatformAuthResult{Admin: toPublicAdmin(a), Token: tok}, nil
}

func (s *Service) checkSecondFactor(ctx context.Context, adminID, encSecret, code string) (bool, error) {
	secret, err := s.cipher.Decrypt(encSecret)
	if err != nil {
		return false, err
	}
	if userauth.ValidateTOTPNow(secret, code) {
		return true, nil
	}
	return s.store.ConsumeBackupCode(ctx, adminID, userauth.HashToken(userauth.NormalizeBackupCode(code)))
}

// DisableMFA turns MFA off, but only with a valid second factor (TOTP/backup) or
// the admin's password — never the session token alone.
func (s *Service) DisableMFA(ctx context.Context, adminID, code, password string) error {
	if s.cipher == nil {
		return ErrMFAConfig
	}
	enc, _, found, err := s.store.GetMFA(ctx, adminID)
	if err != nil {
		return err
	}
	if !found {
		return ErrMFANotEnrolled
	}
	verified := false
	if code != "" {
		if ok, cerr := s.checkSecondFactor(ctx, adminID, enc, code); cerr != nil {
			return cerr
		} else if ok {
			verified = true
		}
	}
	if !verified && password != "" {
		a, aerr := s.store.GetAdminByID(ctx, adminID)
		if aerr != nil {
			return aerr
		}
		if ok, _ := userauth.VerifyPassword(password, a.PasswordHash); ok {
			verified = true
		}
	}
	if !verified {
		return ErrMFAInvalidCode
	}
	return s.store.DisableMFA(ctx, adminID)
}

// --- helpers ----------------------------------------------------------------

func normalizeEmail(email string) string { return strings.ToLower(strings.TrimSpace(email)) }

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
