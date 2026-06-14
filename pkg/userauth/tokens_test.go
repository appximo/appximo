package userauth

import "testing"

func TestNewPlainToken_UniqueAndUrlSafe(t *testing.T) {
	t.Parallel()
	seen := map[string]bool{}
	for i := 0; i < 100; i++ {
		tok, err := newPlainToken()
		if err != nil {
			t.Fatalf("newPlainToken: %v", err)
		}
		if len(tok) < 40 {
			t.Fatalf("token too short (%d): %q", len(tok), tok)
		}
		for _, c := range tok {
			ok := (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '-' || c == '_'
			if !ok {
				t.Fatalf("token has non-URL-safe char %q in %q", c, tok)
			}
		}
		if seen[tok] {
			t.Fatal("duplicate token generated — not random")
		}
		seen[tok] = true
	}
}

func TestHashToken_DeterministicAndNotPlain(t *testing.T) {
	t.Parallel()
	h1 := hashToken("abc")
	h2 := hashToken("abc")
	if h1 != h2 {
		t.Fatal("hashToken not deterministic")
	}
	if h1 == "abc" {
		t.Fatal("hashToken returned the plaintext")
	}
	if len(h1) != 64 { // sha256 hex
		t.Fatalf("expected 64 hex chars, got %d", len(h1))
	}
	if hashToken("abd") == h1 {
		t.Fatal("different inputs hashed to the same value")
	}
}

func TestEmailTemplateAndPath(t *testing.T) {
	t.Parallel()
	tmpl, path := emailTemplateAndPath(tokenReset)
	if tmpl != "reset" || path != "/auth/reset?token=" {
		t.Fatalf("reset mapping wrong: %q %q", tmpl, path)
	}
	tmpl, path = emailTemplateAndPath(tokenVerify)
	if tmpl != "verification" || path != "/auth/verify?token=" {
		t.Fatalf("verify mapping wrong: %q %q", tmpl, path)
	}
}
