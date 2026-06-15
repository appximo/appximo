package userauth

import (
	"testing"
	"time"
)

// TestHOTP_RFC6238Vectors checks the TOTP core against the official RFC 6238
// Appendix B test vectors (SHA1, secret "12345678901234567890", 8 digits). If
// these pass, the algorithm is correct and authenticator-app compatible.
func TestHOTP_RFC6238Vectors(t *testing.T) {
	t.Parallel()
	key := []byte("12345678901234567890")
	cases := []struct {
		unix int64
		want string
	}{
		{59, "94287082"},
		{1111111109, "07081804"},
		{1111111111, "14050471"},
		{1234567890, "89005924"},
		{2000000000, "69279037"},
		{20000000000, "65353130"},
	}
	for _, c := range cases {
		counter := uint64(c.unix) / totpPeriod
		if got := hotp(key, counter, 8); got != c.want {
			t.Errorf("T=%d: hotp=%s, want %s", c.unix, got, c.want)
		}
	}
}

func TestValidateTOTP_WindowAndDrift(t *testing.T) {
	t.Parallel()
	secret, err := newTOTPSecret()
	if err != nil {
		t.Fatalf("secret: %v", err)
	}
	key, _ := base32NoPad.DecodeString(secret)
	now := time.Unix(1700000000, 0)

	// Current step verifies.
	if !validateTOTP(secret, totpCodeAt(key, now), now) {
		t.Fatal("current code did not validate")
	}
	// ±1 step (clock drift) verifies.
	prev := totpCodeAt(key, now.Add(-totpPeriod*time.Second))
	next := totpCodeAt(key, now.Add(totpPeriod*time.Second))
	if !validateTOTP(secret, prev, now) {
		t.Fatal("-1 step code did not validate (drift tolerance)")
	}
	if !validateTOTP(secret, next, now) {
		t.Fatal("+1 step code did not validate (drift tolerance)")
	}
	// ±2 steps must NOT verify (window is exactly ±1).
	far := totpCodeAt(key, now.Add(2*totpPeriod*time.Second))
	if validateTOTP(secret, far, now) {
		t.Fatal("+2 step code validated — window too wide")
	}
	// Wrong code rejected; malformed rejected.
	if validateTOTP(secret, "000000", now) && totpCodeAt(key, now) != "000000" {
		t.Fatal("a wrong code validated")
	}
	if validateTOTP(secret, "12345", now) || validateTOTP(secret, "abcdef", now) {
		t.Fatal("malformed code validated")
	}
}

func TestOtpauthURI(t *testing.T) {
	t.Parallel()
	uri := otpauthURI("Appitools (acme)", "user@example.com", "ABC234")
	for _, want := range []string{"otpauth://totp/", "secret=ABC234", "algorithm=SHA1", "digits=6", "period=30"} {
		if !contains(uri, want) {
			t.Errorf("otpauth uri missing %q: %s", want, uri)
		}
	}
}

func contains(s, sub string) bool { return len(s) >= len(sub) && (indexOf(s, sub) >= 0) }
func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
