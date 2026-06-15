package platformadmin

import (
	"errors"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// platformScope is the marker claim that makes a platform token a PLATFORM token.
// Its presence is required by requirePlatformAdmin; its absence is what makes a
// normal tenant token (auth.Claims, which has no "scope") fail the admin routes.
const platformScope = "platform"

// platformTokenTTL is the lifetime of an issued platform session token.
const platformTokenTTL = 24 * time.Hour

// mfaPendingScope marks the intermediate token issued after a super-admin's
// password verifies but before the second factor.
const mfaPendingScope = "platform_mfa"

// mfaPendingTTL bounds the intermediate platform mfa_token.
const mfaPendingTTL = 5 * time.Minute

// ErrPlatformToken is returned when a platform token is missing/invalid/expired or
// is not actually a platform-scoped token (e.g. a tenant token presented to an
// admin route).
var ErrPlatformToken = errors.New("platformadmin: invalid or missing platform token")

// PlatformClaims is the payload of a platform super-admin JWT. Its claim KEYS
// (aid/prole/scope) deliberately DIFFER from auth.Claims (user_id/role/tenant_id):
//
//   - A platform token presented to a tenant /api/ route parses (same HS256 secret)
//     but yields an empty auth.Claims (no user_id/role/tenant_id) → the engine RBAC
//     denies it (deny by default). So a platform JWT can NEVER act as a tenant
//     without the super-admin EXPLICITLY selecting a tenant through the admin API.
//   - A tenant token presented to an admin route has no "scope" claim → it fails
//     requirePlatformAdmin (403). The two token worlds cannot be confused.
//
// It is signed with the engine's JWT_SECRET (one signing key, one validator family
// — HS256, exp required), exactly like every other token the engine issues.
type PlatformClaims struct {
	AdminID string `json:"aid"`
	Role    string `json:"prole"`
	Scope   string `json:"scope"`
	jwt.RegisteredClaims
}

// signPlatformToken mints a platform session token for an admin.
func signPlatformToken(adminID, role, secret string) (string, error) {
	now := time.Now()
	c := PlatformClaims{
		AdminID: adminID,
		Role:    role,
		Scope:   platformScope,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   "platform:" + adminID,
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(platformTokenTTL)),
		},
	}
	return jwt.NewWithClaims(jwt.SigningMethodHS256, c).SignedString([]byte(secret))
}

// parsePlatformToken validates a platform session token. It enforces HS256, a
// required exp, the platform scope, and a non-empty admin id.
func parsePlatformToken(tokenStr, secret string) (*PlatformClaims, error) {
	tok, err := jwt.ParseWithClaims(tokenStr, &PlatformClaims{}, func(t *jwt.Token) (any, error) {
		if t.Method != jwt.SigningMethodHS256 {
			return nil, ErrPlatformToken
		}
		return []byte(secret), nil
	}, jwt.WithExpirationRequired())
	if err != nil {
		return nil, ErrPlatformToken
	}
	c, ok := tok.Claims.(*PlatformClaims)
	if !ok || !tok.Valid || c.Scope != platformScope || c.AdminID == "" {
		return nil, ErrPlatformToken
	}
	return c, nil
}

// platformMFAPendingClaims is the intermediate token after a super-admin password
// verifies. Like the tenant MFA pending token, its scope differs from a usable
// session token, so it cannot authorize anything but the MFA completion step.
type platformMFAPendingClaims struct {
	AdminID string `json:"aid"`
	Scope   string `json:"scope"`
	jwt.RegisteredClaims
}

func signPlatformMFAPending(adminID, secret string) (string, error) {
	now := time.Now()
	c := platformMFAPendingClaims{
		AdminID: adminID,
		Scope:   mfaPendingScope,
		RegisteredClaims: jwt.RegisteredClaims{
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(mfaPendingTTL)),
		},
	}
	return jwt.NewWithClaims(jwt.SigningMethodHS256, c).SignedString([]byte(secret))
}

func parsePlatformMFAPending(tokenStr, secret string) (*platformMFAPendingClaims, error) {
	tok, err := jwt.ParseWithClaims(tokenStr, &platformMFAPendingClaims{}, func(t *jwt.Token) (any, error) {
		if t.Method != jwt.SigningMethodHS256 {
			return nil, ErrPlatformToken
		}
		return []byte(secret), nil
	}, jwt.WithExpirationRequired())
	if err != nil {
		return nil, ErrPlatformToken
	}
	c, ok := tok.Claims.(*platformMFAPendingClaims)
	if !ok || !tok.Valid || c.Scope != mfaPendingScope || c.AdminID == "" {
		return nil, ErrPlatformToken
	}
	return c, nil
}
