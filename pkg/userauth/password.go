package userauth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

// argon2id parameters. argon2id is the current password-hashing standard
// (memory-hard, GPU-resistant), pure Go via golang.org/x/crypto — no CGO, in
// keeping with the engine's CGO-free constraint. The cost is INTENTIONALLY slow:
// it is paid only on signup and login (occasional operations), NEVER on the
// 2000 RPS request hot path (that path validates an already-minted JWT). The
// defaults below track the OWASP minimum (m=19 MiB, t=2, p=1) — a sensible point
// for a 1–2 vCPU VPS, measured in the session's COST benchmark. Stored hashes
// carry their own parameters (PHC string), so these can be raised later without
// invalidating existing passwords.
const (
	argonMemoryKiB  uint32 = 19456 // 19 MiB
	argonTime       uint32 = 2
	argonThreads    uint8  = 1
	argonSaltLength uint32 = 16
	argonKeyLength  uint32 = 32
)

// errInvalidHash is returned when a stored hash is not a well-formed argon2id
// PHC string. It is never surfaced to a client — a verify failure is always a
// generic 401, so a corrupt stored hash and a wrong password are indistinguishable.
var errInvalidHash = errors.New("userauth: malformed password hash")

// HashPassword derives an argon2id hash of password and returns it encoded as a
// PHC string ($argon2id$v=19$m=...,t=...,p=...$salt$hash). A fresh 16-byte random
// salt is generated per call, so two identical passwords hash to different
// strings.
func HashPassword(password string) (string, error) {
	salt := make([]byte, argonSaltLength)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("userauth: read salt: %w", err)
	}
	hash := argon2.IDKey([]byte(password), salt, argonTime, argonMemoryKiB, argonThreads, argonKeyLength)

	b64 := base64.RawStdEncoding
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, argonMemoryKiB, argonTime, argonThreads,
		b64.EncodeToString(salt), b64.EncodeToString(hash)), nil
}

// VerifyPassword reports whether password matches the argon2id PHC string in
// encodedHash. It re-derives the hash using the PARAMETERS STORED IN THE STRING
// (not the current defaults) so old hashes keep verifying after a cost bump, and
// compares in constant time. A malformed stored hash returns (false, errInvalidHash);
// a plain mismatch returns (false, nil).
func VerifyPassword(password, encodedHash string) (bool, error) {
	mem, time, threads, salt, want, err := decodeHash(encodedHash)
	if err != nil {
		return false, err
	}
	got := argon2.IDKey([]byte(password), salt, time, mem, threads, uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1, nil
}

// decodeHash parses an argon2id PHC string into its parameters, salt and digest.
func decodeHash(encoded string) (mem, time uint32, threads uint8, salt, hash []byte, err error) {
	parts := strings.Split(encoded, "$")
	// "" / "argon2id" / "v=19" / "m=..,t=..,p=.." / salt / hash
	if len(parts) != 6 || parts[1] != "argon2id" {
		return 0, 0, 0, nil, nil, errInvalidHash
	}
	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil || version != argon2.Version {
		return 0, 0, 0, nil, nil, errInvalidHash
	}
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &mem, &time, &threads); err != nil {
		return 0, 0, 0, nil, nil, errInvalidHash
	}
	b64 := base64.RawStdEncoding
	if salt, err = b64.DecodeString(parts[4]); err != nil {
		return 0, 0, 0, nil, nil, errInvalidHash
	}
	if hash, err = b64.DecodeString(parts[5]); err != nil {
		return 0, 0, 0, nil, nil, errInvalidHash
	}
	return mem, time, threads, salt, hash, nil
}
