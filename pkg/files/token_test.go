package files

import (
	"errors"
	"testing"
	"time"
)

var tokSecret = []byte("a-test-secret-of-at-least-32-chars!!")

func TestDownloadToken_RoundTrip(t *testing.T) {
	tok, err := MintDownloadToken(tokSecret, "acme", "file-123", "viewer", time.Minute)
	if err != nil {
		t.Fatalf("mint: %v", err)
	}
	tenant, fileID, role, err := VerifyDownloadToken(tokSecret, tok)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if tenant != "acme" || fileID != "file-123" || role != "viewer" {
		t.Fatalf("claims = %s/%s/%s", tenant, fileID, role)
	}
}

func TestDownloadToken_Expired(t *testing.T) {
	tok, err := MintDownloadToken(tokSecret, "acme", "f", "r", time.Nanosecond)
	if err != nil {
		t.Fatalf("mint: %v", err)
	}
	time.Sleep(5 * time.Millisecond)
	if _, _, _, err := VerifyDownloadToken(tokSecret, tok); !errors.Is(err, ErrBadToken) {
		t.Fatalf("expired verify err = %v, want ErrBadToken", err)
	}
}

func TestDownloadToken_WrongSecretAndGarbage(t *testing.T) {
	tok, _ := MintDownloadToken(tokSecret, "acme", "f", "r", time.Minute)
	if _, _, _, err := VerifyDownloadToken([]byte("another-secret-entirely-not-same!!"), tok); !errors.Is(err, ErrBadToken) {
		t.Fatalf("wrong-secret err = %v, want ErrBadToken", err)
	}
	for _, garbage := range []string{"", "not.a.jwt", "eyJhbGciOiJub25lIn0.e30."} {
		if _, _, _, err := VerifyDownloadToken(tokSecret, garbage); !errors.Is(err, ErrBadToken) {
			t.Fatalf("garbage %q err = %v, want ErrBadToken", garbage, err)
		}
	}
}
