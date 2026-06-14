package auth

import (
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// Claims is the payload embedded in every Appitools JWT.
type Claims struct {
	UserID           string `json:"user_id"`
	Role             string `json:"role"`
	ExternalClientID string `json:"external_client_id,omitempty"`
	TenantID         string `json:"tenant_id"`
	jwt.RegisteredClaims
}

// GenerateToken signs a HS256 JWT that expires in 24h.
// The caller fills UserID/Role/TenantID; expiry and issued-at are set here.
func GenerateToken(c Claims, secret string) (string, error) {
	c.RegisteredClaims = jwt.RegisteredClaims{
		ExpiresAt: jwt.NewNumericDate(time.Now().Add(24 * time.Hour)),
		IssuedAt:  jwt.NewNumericDate(time.Now()),
	}
	return jwt.NewWithClaims(jwt.SigningMethodHS256, c).SignedString([]byte(secret))
}

// GenerateTokenWithTTL signs a HS256 JWT that expires in ttl. Unlike
// GenerateToken (fixed 24h), it lets a caller mint a SHORT-LIVED token — e.g.
// the outbox worker minting a 60s service token per write-back operation, so no
// long-lived credential exists to leak (ADR-016 §Class 2 write-back). It sets
// iat/exp but PRESERVES any RegisteredClaims the caller pre-filled (e.g.
// Subject "service:worker" for audit), and emits the exact same Claims shape
// ValidateToken accepts — there is only ONE claims contract.
func GenerateTokenWithTTL(c Claims, secret string, ttl time.Duration) (string, error) {
	now := time.Now()
	c.RegisteredClaims.IssuedAt = jwt.NewNumericDate(now)
	c.RegisteredClaims.ExpiresAt = jwt.NewNumericDate(now.Add(ttl))
	return jwt.NewWithClaims(jwt.SigningMethodHS256, c).SignedString([]byte(secret))
}

// ValidateToken parses and validates a signed JWT string.
// Returns an error if the token is expired, malformed, or signed with a different secret.
func ValidateToken(tokenStr, secret string) (*Claims, error) {
	token, err := jwt.ParseWithClaims(tokenStr, &Claims{}, func(t *jwt.Token) (any, error) {
		// Pin to HS256 (the only method GenerateToken emits). This rejects alg=none,
		// RSA/ECDSA confusion, AND other HMAC families — no token should validate
		// under a method we never issue.
		if t.Method != jwt.SigningMethodHS256 {
			return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
		}
		return []byte(secret), nil
	}, jwt.WithExpirationRequired()) // every token MUST carry an exp; no immortal tokens
	if err != nil {
		return nil, err
	}
	claims, ok := token.Claims.(*Claims)
	if !ok || !token.Valid {
		return nil, fmt.Errorf("invalid token claims")
	}
	return claims, nil
}
