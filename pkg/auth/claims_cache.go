package auth

import (
	"context"
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

// GetCachedClaims looks up previously validated claims for a Bearer token,
// SCOPED to the validating secret (MT-STRUCT-S3). The secret is part of the
// cache key because with N apps in one process — each validating with its OWN
// JWT secret — a token-only key would let a token validated by app X be served
// from cache to app Y, silently bypassing the signature check with secret_Y (a
// cross-app auth hole). Scoping the key makes a cache hit mean exactly "this
// secret already validated this token". Returns nil, false on miss or expiry.
func GetCachedClaims(secret, token string) (*Claims, bool) {
	key := tokenCacheKey(secret, token)
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

// SetCachedClaims stores validated claims for a (secret, token). Exported for testing.
func SetCachedClaims(secret, token string, c *Claims) { setCachedClaims(secret, token, c) }

func setCachedClaims(secret, token string, c *Claims) {
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
	claimsCache.Store(tokenCacheKey(secret, token), &cachedClaims{
		claims:    c,
		expiresAt: time.Now().Add(ttl),
	})
}

// StartClaimsCacheGC periodically evicts expired entries. Lookups already delete
// entries lazily on access, but a token that is never presented again would
// otherwise linger until process exit; over a long uptime with token rotation that
// is an unbounded (if slow) leak. Only validated tokens are ever cached, so this is
// not attacker-floodable — the sweep just keeps the footprint tidy. Runs until ctx
// is cancelled.
func StartClaimsCacheGC(ctx context.Context) {
	go func() {
		t := time.NewTicker(5 * time.Minute)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				now := time.Now()
				claimsCache.Range(func(k, v any) bool {
					if cc, ok := v.(*cachedClaims); ok && now.After(cc.expiresAt) {
						claimsCache.Delete(k)
					}
					return true
				})
			}
		}
	}()
}

// tokenCacheKey returns the first 16 hex chars of SHA-256(secret || 0x00 ||
// token) — 64 bits, sufficient for collision resistance across the small set
// of live tokens, and DISTINCT per validating secret (the cross-app guard; the
// separator prevents ambiguous secret/token boundaries).
func tokenCacheKey(secret, token string) string {
	h := sha256.New()
	h.Write([]byte(secret)) //nolint:errcheck
	h.Write([]byte{0})      //nolint:errcheck
	h.Write([]byte(token))  //nolint:errcheck
	var sum [sha256.Size]byte
	return hex.EncodeToString(h.Sum(sum[:0])[:8])
}
