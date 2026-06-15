package userauth

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
)

// secretCipher encrypts/decrypts a TOTP secret AT REST. A TOTP secret MUST be
// recoverable (the server re-derives the current code on every verify), so it is
// ENCRYPTED, not hashed. AES-256-GCM gives confidentiality + integrity; the key is
// SHA-256(key material) where the material is APPITOOLS_MFA_KEY (or the JWT secret
// as a fallback). The DB alone is therefore useless to an attacker without the key.
type secretCipher struct {
	gcm cipher.AEAD
}

func newSecretCipher(keyMaterial string) (*secretCipher, error) {
	if keyMaterial == "" {
		return nil, errors.New("userauth: empty MFA key material")
	}
	sum := sha256.Sum256([]byte(keyMaterial)) // 32 bytes → AES-256
	block, err := aes.NewCipher(sum[:])
	if err != nil {
		return nil, fmt.Errorf("userauth: mfa cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("userauth: mfa gcm: %w", err)
	}
	return &secretCipher{gcm: gcm}, nil
}

// encrypt seals plaintext and returns base64(nonce || ciphertext+tag). A fresh
// random nonce per call means the same secret encrypts to a different string each
// time.
func (c *secretCipher) encrypt(plain string) (string, error) {
	nonce := make([]byte, c.gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", fmt.Errorf("userauth: mfa nonce: %w", err)
	}
	sealed := c.gcm.Seal(nonce, nonce, []byte(plain), nil)
	return base64.RawStdEncoding.EncodeToString(sealed), nil
}

// decrypt reverses encrypt. A tampered or wrong-key ciphertext fails the GCM tag
// and returns an error (never garbage plaintext).
func (c *secretCipher) decrypt(enc string) (string, error) {
	raw, err := base64.RawStdEncoding.DecodeString(enc)
	if err != nil {
		return "", fmt.Errorf("userauth: mfa b64: %w", err)
	}
	ns := c.gcm.NonceSize()
	if len(raw) < ns {
		return "", errors.New("userauth: mfa ciphertext too short")
	}
	nonce, ct := raw[:ns], raw[ns:]
	pt, err := c.gcm.Open(nil, nonce, ct, nil)
	if err != nil {
		return "", fmt.Errorf("userauth: mfa decrypt: %w", err)
	}
	return string(pt), nil
}
