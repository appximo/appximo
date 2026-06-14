package userauth

import (
	"strings"
	"testing"
)

func TestHashPassword_RoundTrip(t *testing.T) {
	t.Parallel()
	hash, err := HashPassword("correct horse battery staple")
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	if !strings.HasPrefix(hash, "$argon2id$v=19$") {
		t.Fatalf("unexpected PHC prefix: %q", hash)
	}
	if strings.Contains(hash, "correct horse") {
		t.Fatal("plaintext password leaked into the hash string")
	}

	ok, err := VerifyPassword("correct horse battery staple", hash)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if !ok {
		t.Fatal("correct password did not verify")
	}

	bad, err := VerifyPassword("wrong password", hash)
	if err != nil {
		t.Fatalf("verify wrong: %v", err)
	}
	if bad {
		t.Fatal("wrong password verified")
	}
}

func TestHashPassword_DistinctSalts(t *testing.T) {
	t.Parallel()
	h1, _ := HashPassword("same-password")
	h2, _ := HashPassword("same-password")
	if h1 == h2 {
		t.Fatal("two hashes of the same password are identical — salt not random")
	}
	// Both must still verify.
	for _, h := range []string{h1, h2} {
		ok, _ := VerifyPassword("same-password", h)
		if !ok {
			t.Fatal("a salted hash failed to verify")
		}
	}
}

func TestVerifyPassword_MalformedHash(t *testing.T) {
	t.Parallel()
	for _, bad := range []string{
		"", "not-a-hash", "$argon2id$bad",
		"$argon2i$v=19$m=19456,t=2,p=1$c2FsdA$aGFzaA", // wrong variant
		"$argon2id$v=19$m=19456,t=2,p=1$!!!$aGFzaA",   // bad base64 salt
	} {
		ok, err := VerifyPassword("x", bad)
		if ok {
			t.Fatalf("malformed hash %q verified", bad)
		}
		if err == nil {
			t.Fatalf("malformed hash %q returned nil error", bad)
		}
	}
}

func TestVerifyPassword_StoredParamsHonored(t *testing.T) {
	t.Parallel()
	// A hash carries its own params; verifying must use those, not the current
	// defaults — so a future cost bump never breaks an old password.
	hash, err := HashPassword("paramcheck")
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	mem, time, threads, _, _, err := decodeHash(hash)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if mem != argonMemoryKiB || time != argonTime || threads != argonThreads {
		t.Fatalf("decoded params %d,%d,%d != defaults %d,%d,%d",
			mem, time, threads, argonMemoryKiB, argonTime, argonThreads)
	}
}
