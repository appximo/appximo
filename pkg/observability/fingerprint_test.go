package observability

import "testing"

func TestNormalizeMessageCollapsesOccurrenceData(t *testing.T) {
	a := NormalizeMessage(`ERROR: invalid input syntax for type timestamp with time zone: "notadate" (SQLSTATE 22007)`)
	b := NormalizeMessage(`ERROR: invalid input syntax for type timestamp with time zone: "2026-13-45T00" (SQLSTATE 22007)`)
	if a != b {
		t.Fatalf("same defect, different groups:\n%s\n%s", a, b)
	}
	if got := NormalizeMessage(`row 3f2a9c10-1111-4222-8333-444455556666 not found in tenant_acme after 12 retries`); got != "row <uuid> not found in tenant_acme after <n> retries" {
		t.Fatalf("normalize: %q", got)
	}
	// A different SQLSTATE is a different defect.
	c := NormalizeMessage(`ERROR: value "x" is out of range for type bigint (SQLSTATE 22003)`)
	if c == a {
		t.Fatal("distinct SQLSTATEs must not collapse")
	}
}

func TestFingerprintStableAndSiteSensitive(t *testing.T) {
	f1 := Fingerprint("/api/notes", `cannot scan NULL into *bool`, "main.attend")
	f2 := Fingerprint("/api/notes", `cannot scan NULL into *bool`, "main.attend")
	f3 := Fingerprint("/api/notes", `cannot scan NULL into *bool`, "main.other")
	if f1 != f2 || f1 == f3 {
		t.Fatalf("fingerprint: %d %d %d", f1, f2, f3)
	}
}
