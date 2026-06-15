package userauth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1" //nolint:gosec // HMAC-SHA1 is the RFC 6238 TOTP standard (authenticator-app compatible), not used for collision resistance
	"crypto/subtle"
	"encoding/base32"
	"encoding/binary"
	"fmt"
	"net/url"
	"strings"
	"time"
)

// TOTP parameters. SHA1 + 6 digits + 30 s is the de-facto standard every
// authenticator app (Google Authenticator, Authy, 1Password, …) supports — RFC
// 6238. We accept the code at the current step and ±1 step (±30 s) to tolerate
// modest clock drift; NOT wider, because every extra step a code stays valid
// linearly weakens the factor.
const (
	totpDigits   = 6
	totpPeriod   = 30 // seconds
	totpSecretND = 20 // random secret bytes (160 bits, RFC 6238 recommended for SHA1)
	totpSkew     = 1  // accept ±1 step
)

// base32NoPad is the encoding authenticator apps expect for the shared secret
// (uppercase base32, no '=' padding).
var base32NoPad = base32.StdEncoding.WithPadding(base32.NoPadding)

// newTOTPSecret returns a fresh random TOTP secret, base32-encoded (the form a
// user types or scans into their authenticator app).
func newTOTPSecret() (string, error) {
	b := make([]byte, totpSecretND)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("userauth: read totp secret: %w", err)
	}
	return base32NoPad.EncodeToString(b), nil
}

// otpauthURI builds the otpauth://totp/… URI an authenticator app reads from a QR
// code. issuer/account are shown to the user; the engine returns this URI (the
// client renders the QR — the engine ships no image encoder).
func otpauthURI(issuer, account, secretB32 string) string {
	label := url.PathEscape(issuer + ":" + account)
	q := url.Values{}
	q.Set("secret", secretB32)
	q.Set("issuer", issuer)
	q.Set("algorithm", "SHA1")
	q.Set("digits", fmt.Sprintf("%d", totpDigits))
	q.Set("period", fmt.Sprintf("%d", totpPeriod))
	return "otpauth://totp/" + label + "?" + q.Encode()
}

// hotp is the RFC 4226 HMAC-based one-time password for a counter. TOTP is HOTP
// with counter = unix_time / period.
func hotp(key []byte, counter uint64, digits int) string {
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], counter)
	mac := hmac.New(sha1.New, key)
	mac.Write(buf[:])
	sum := mac.Sum(nil)

	// Dynamic truncation (RFC 4226 §5.3).
	offset := sum[len(sum)-1] & 0x0f
	code := (uint32(sum[offset]&0x7f) << 24) |
		(uint32(sum[offset+1]) << 16) |
		(uint32(sum[offset+2]) << 8) |
		uint32(sum[offset+3])

	mod := uint32(1)
	for i := 0; i < digits; i++ {
		mod *= 10
	}
	return fmt.Sprintf("%0*d", digits, code%mod)
}

// totpCodeAt returns the TOTP code for the given key (raw secret bytes) at time t.
func totpCodeAt(key []byte, t time.Time) string {
	counter := uint64(t.Unix()) / totpPeriod
	return hotp(key, counter, totpDigits)
}

// validateTOTP reports whether code is valid for the base32 secret at time t,
// within ±totpSkew steps. The comparison is constant-time per candidate step.
func validateTOTP(secretB32, code string, t time.Time) bool {
	code = strings.TrimSpace(code)
	if len(code) != totpDigits {
		return false
	}
	key, err := base32NoPad.DecodeString(strings.ToUpper(strings.TrimSpace(secretB32)))
	if err != nil || len(key) == 0 {
		return false
	}
	for skew := -totpSkew; skew <= totpSkew; skew++ {
		candidate := totpCodeAt(key, t.Add(time.Duration(skew)*totpPeriod*time.Second))
		if subtle.ConstantTimeCompare([]byte(candidate), []byte(code)) == 1 {
			return true
		}
	}
	return false
}
