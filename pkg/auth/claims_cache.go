package auth

import (
	"crypto/sha256"
	"encoding/hex"
	"sync"
	"time"
)

type cachedClaims struct {
	claims    *Claims
	expiresAt time.Time
}

var claimsCache sync.Map // map[string]*cachedClaims

// GetCachedClaims looks up previously validated claims by Bearer token string.
// Returns nil, false on miss or expiry.
func GetCachedClaims(token string) (*Claims, bool) {
	key := tokenCacheKey(token)
	v, ok := claimsCache.Load(key)
	if !ok {
		return nil, false
	}
	cc := v.(*cachedClaims)
	if time.Now().After(cc.expiresAt) {
		claimsCache.Delete(key)
		return nil, false
	}
	return cc.claims, true
}

// SetCachedClaims stores validated claims for a token. Exported for testing.
func SetCachedClaims(token string, c *Claims) { setCachedClaims(token, c) }

func setCachedClaims(token string, c *Claims) {
	ttl := 30 * time.Second
	// Never cache past the token's own expiry.
	if c.ExpiresAt != nil {
		if remaining := time.Until(c.ExpiresAt.Time); remaining < ttl {
			ttl = remaining
		}
	}
	if ttl <= 0 {
		return
	}
	claimsCache.Store(tokenCacheKey(token), &cachedClaims{
		claims:    c,
		expiresAt: time.Now().Add(ttl),
	})
}

// tokenCacheKey returns the first 16 hex chars of SHA-256(token) — 64 bits,
// sufficient for collision resistance across the small set of live tokens.
func tokenCacheKey(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:8])
}
