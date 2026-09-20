package auth

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// Claims is the payload embedded in every Appximo JWT.
type Claims struct {
	UserID           string `json:"user_id"`
	Role             string `json:"role"`
	ExternalClientID string `json:"external_client_id,omitempty"`
	TenantID         string `json:"tenant_id"`
	// Paths (TOKEN-SCOPE, 2026-09-20) restricts the token to these request
	// paths: an exact path ("/api/ask") or a prefix ending in "/*"
	// ("/api/files/*"). Empty = every path the role may reach (the historical
	// behavior). A scoped token presented elsewhere is a 401 naming its scope
	// — a Siri shortcut's long-lived token can only ask and read the digest,
	// never touch /api/<resource> even though its role could.
	Paths []string `json:"paths,omitempty"`
	jwt.RegisteredClaims
}

// PathAllowed reports whether the token may be used on path (true when the
// token carries no scope).
func (c *Claims) PathAllowed(path string) bool {
	if len(c.Paths) == 0 {
		return true
	}
	for _, p := range c.Paths {
		if strings.HasSuffix(p, "/*") {
			if strings.HasPrefix(path, strings.TrimSuffix(p, "*")) {
				return true
			}
			continue
		}
		if path == p {
			return true
		}
	}
	return false
}

// ParseTTL reads a token lifetime: a Go duration ("24h", "90m") or whole days
// ("365d"). Zero or negative is an error — every token must expire.
func ParseTTL(s string) (time.Duration, error) {
	s = strings.TrimSpace(s)
	if strings.HasSuffix(s, "d") {
		n, err := strconv.Atoi(strings.TrimSuffix(s, "d"))
		if err != nil || n <= 0 {
			return 0, fmt.Errorf("ttl %q: use a whole number of days (365d) or a Go duration (24h)", s)
		}
		return time.Duration(n) * 24 * time.Hour, nil
	}
	d, err := time.ParseDuration(s)
	if err != nil || d <= 0 {
		return 0, fmt.Errorf("ttl %q: use a Go duration (24h, 90m) or whole days (365d); it must be positive", s)
	}
	return d, nil
}

// ── revocation without rotating the secret (TOKEN-SCOPE) ──────────────────
//
// A token minted with an id (`jti`) can be revoked by listing that id in
// APPXIMO_JWT_REVOKED (comma-separated) and restarting: the middleware checks
// the id on every request (one map lookup, only for tokens that carry an id)
// and answers 401 "token revoked". Every other token stays valid — no secret
// rotation, no session store. The set is process-wide: ids are random, so
// two apps in one process cannot collide on one.

var (
	revokedMu  sync.RWMutex
	revokedIDs = map[string]bool{}
)

// SetRevokedTokenIDs replaces the revocation set.
func SetRevokedTokenIDs(ids []string) {
	m := make(map[string]bool, len(ids))
	for _, id := range ids {
		if id = strings.TrimSpace(id); id != "" {
			m[id] = true
		}
	}
	revokedMu.Lock()
	revokedIDs = m
	revokedMu.Unlock()
}

// IsRevoked reports whether a token id is on the revocation list.
func IsRevoked(id string) bool {
	if id == "" {
		return false
	}
	revokedMu.RLock()
	defer revokedMu.RUnlock()
	return revokedIDs[id]
}

// RevokedFromEnv loads APPXIMO_JWT_REVOKED at boot. Fail-fast on a malformed
// value (an entry with spaces inside, or a stray separator): a revocation
// that silently does not apply is worse than none.
func RevokedFromEnv() (int, error) {
	raw := strings.TrimSpace(os.Getenv("APPXIMO_JWT_REVOKED"))
	if raw == "" {
		SetRevokedTokenIDs(nil)
		return 0, nil
	}
	var ids []string
	for _, part := range strings.Split(raw, ",") {
		id := strings.TrimSpace(part)
		if id == "" {
			return 0, fmt.Errorf("appximo: APPXIMO_JWT_REVOKED has an empty entry (stray comma) — list token ids separated by commas")
		}
		if strings.ContainsAny(id, " \t\"'") {
			return 0, fmt.Errorf("appximo: APPXIMO_JWT_REVOKED entry %q is not a token id (no spaces or quotes)", id)
		}
		ids = append(ids, id)
	}
	SetRevokedTokenIDs(ids)
	return len(ids), nil
}

// NewTokenID mints a random token id (16 hex chars) for `jti`.
func NewTokenID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("%x", time.Now().UnixNano())
	}
	return hex.EncodeToString(b[:])
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
