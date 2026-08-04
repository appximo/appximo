package files

import (
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// DefaultSignedURLTTL is how long an engine-minted download token (and an S3
// presigned URL) stays valid. Short by design (OWASP / the PocketBase pattern:
// a protected-file URL is a capability — it must expire fast); configurable
// via APPXIMO_FILES_TOKEN_TTL.
const DefaultSignedURLTTL = 180 * time.Second

// ErrBadToken covers every download-token failure — malformed, bad signature,
// expired, wrong claim shape. Handlers map it to 404 (not 403): an invalid
// token must be indistinguishable from a nonexistent file
// (anti-fingerprinting).
var ErrBadToken = errors.New("files: invalid download token")

// downloadClaims is the engine-minted signed-URL token for LOCAL files. Its
// claim keys (ften/ffid/frole) are deliberately DISJOINT from the access
// token's (user_id/role/tenant_id) — presented as a Bearer to /api it carries
// no role, so RBAC denies it; it can only ever fetch its one file (the same
// isolation argument as the MFA challenge token).
type downloadClaims struct {
	Tenant string `json:"ften"`
	FileID string `json:"ffid"`
	Role   string `json:"frole"`
	jwt.RegisteredClaims
}

// MintDownloadToken signs a short-lived, single-file download capability. role
// is the minting caller's RBAC role, re-checked against `read files` at serve
// time (a role whose grant was meanwhile revoked does not outlive the policy).
func MintDownloadToken(secret []byte, tenant, fileID, role string, ttl time.Duration) (string, error) {
	if ttl <= 0 {
		ttl = DefaultSignedURLTTL
	}
	now := time.Now()
	claims := downloadClaims{
		Tenant: tenant,
		FileID: fileID,
		Role:   role,
		RegisteredClaims: jwt.RegisteredClaims{
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(ttl)),
		},
	}
	tok, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(secret)
	if err != nil {
		return "", fmt.Errorf("files: sign download token: %w", err)
	}
	return tok, nil
}

// VerifyDownloadToken validates signature (HS256 pinned — alg confusion
// rejected), expiry, and claim shape. Any failure is ErrBadToken.
func VerifyDownloadToken(secret []byte, token string) (tenant, fileID, role string, err error) {
	var claims downloadClaims
	parsed, perr := jwt.ParseWithClaims(token, &claims, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method %v", t.Header["alg"])
		}
		return secret, nil
	}, jwt.WithValidMethods([]string{"HS256"}), jwt.WithExpirationRequired())
	if perr != nil || !parsed.Valid || claims.Tenant == "" || claims.FileID == "" {
		return "", "", "", ErrBadToken
	}
	return claims.Tenant, claims.FileID, claims.Role, nil
}
