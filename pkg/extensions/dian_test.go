package extensions

import (
	"crypto/sha512"
	"encoding/hex"
	"testing"
)

// Known real Colombian NITs (number-check digit) verified against the official
// DIAN mod-11 algorithm: DIAN 800197268-4, Bancolombia 890903938-8.
func TestValidateNIT(t *testing.T) {
	valid := []string{
		"800197268-4",
		"8001972684",
		"800.197.268-4",
		"890903938-8",
		"8909039388",
	}
	for _, n := range valid {
		if !validateNIT(n) {
			t.Errorf("validateNIT(%q) = false, want true", n)
		}
	}

	invalid := []string{
		"800197268-5", // wrong check digit
		"800197268-0",
		"890903938-1", // wrong check digit
		"123456789-1", // arbitrary
		"",
		"4",
		"abc-1",
	}
	for _, n := range invalid {
		if validateNIT(n) {
			t.Errorf("validateNIT(%q) = true, want false", n)
		}
	}
}

func TestComputeNITCheckDigit(t *testing.T) {
	cases := map[string]int{
		"800197268": 4,
		"890903938": 8,
	}
	for base, want := range cases {
		if got := computeNITCheckDigit(base); got != want {
			t.Errorf("computeNITCheckDigit(%q) = %d, want %d", base, got, want)
		}
	}
	// Too long for the weight table → -1.
	if got := computeNITCheckDigit("1234567890123456"); got != -1 {
		t.Errorf("oversized base: got %d, want -1", got)
	}
}

func TestCalculateCUFE_IsSHA384(t *testing.T) {
	// Empty field set → concatenation is "" → SHA-384 of "".
	got := calculateCUFE(map[string]any{})
	want := func() string { s := sha512.Sum384([]byte("")); return hex.EncodeToString(s[:]) }()
	if got != want {
		t.Fatalf("CUFE of empty fields = %q, want SHA-384(\"\") = %q", got, want)
	}
	if len(got) != 96 { // SHA-384 = 48 bytes = 96 hex chars
		t.Fatalf("CUFE length = %d, want 96", len(got))
	}
}

func TestCalculateCUFE_Reproducible(t *testing.T) {
	fields := map[string]any{
		"NumFac": "FE001", "FecFac": "2026-06-04", "HorFac": "05:00:00-05:00",
		"ValFac": 1000.0, "CodImp1": "01", "ValImp1": 190.0,
		"ValTot": 1190.0, "NitOFE": "800197268", "NumAdq": "900123456",
		"ClaveT": "secret", "TipAmb": "1",
	}
	a := calculateCUFE(fields)
	b := calculateCUFE(fields)
	if a != b {
		t.Fatalf("CUFE not reproducible: %q != %q", a, b)
	}
	// Changing any field must change the digest.
	fields["ValTot"] = 1191.0
	if c := calculateCUFE(fields); c == a {
		t.Fatal("CUFE did not change when a field changed")
	}
}

func TestCalculateCUFE_OrderSensitive(t *testing.T) {
	// Same values in different fields must produce a different CUFE (field order
	// is part of the spec), proving the concatenation respects field identity.
	a := calculateCUFE(map[string]any{"NumFac": "A", "FecFac": "B"})
	b := calculateCUFE(map[string]any{"NumFac": "B", "FecFac": "A"})
	if a == b {
		t.Fatal("CUFE should depend on which field holds which value")
	}
}
