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

// ValidateToken parses and validates a signed JWT string.
// Returns an error if the token is expired, malformed, or signed with a different secret.
func ValidateToken(tokenStr, secret string) (*Claims, error) {
	token, err := jwt.ParseWithClaims(tokenStr, &Claims{}, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
		}
		return []byte(secret), nil
	})
	if err != nil {
		return nil, err
	}
	claims, ok := token.Claims.(*Claims)
	if !ok || !token.Valid {
		return nil, fmt.Errorf("invalid token claims")
	}
	return claims, nil
}
