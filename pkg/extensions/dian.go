package extensions

import (
	"crypto/sha512"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
)

// nitWeights are the DIAN check-digit factors, applied to the NIT's digits from
// least-significant (rightmost) to most-significant.
var nitWeights = []int{3, 7, 13, 17, 19, 23, 29, 37, 41, 43, 47, 53, 59, 67, 71}

// nitDigits extracts the decimal digits from s, dropping separators like '-' and
// '.' that appear in printed NITs ("900.123.456-7").
func nitDigits(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// computeNITCheckDigit returns the DIAN verification digit (0–9) for a base NIT
// (the identification number WITHOUT its check digit). It implements the official
// algorithm: Σ(digit_i × weight_i) over digits right-to-left, mod 11; the digit is
// the remainder when it is 0 or 1, else 11 − remainder. Returns -1 if base is
// empty or longer than the weight table (15 digits).
func computeNITCheckDigit(base string) int {
	if base == "" || len(base) > len(nitWeights) {
		return -1
	}
	sum := 0
	for i := 0; i < len(base); i++ {
		c := base[len(base)-1-i]
		if c < '0' || c > '9' {
			return -1
		}
		sum += int(c-'0') * nitWeights[i]
	}
	r := sum % 11
	if r >= 2 {
		return 11 - r
	}
	return r
}

// validateNIT reports whether nit is a valid Colombian NIT including its
// verification digit as the LAST digit. Separators are ignored, so
// "800197268-4", "8001972684" and "800.197.268-4" all validate. This is the real
// DIAN algorithm, not a regex — the check digit must match the computed one.
func validateNIT(nit string) bool {
	digits := nitDigits(nit)
	if len(digits) < 2 {
		return false
	}
	base := digits[:len(digits)-1]
	want := int(digits[len(digits)-1] - '0')
	got := computeNITCheckDigit(base)
	return got >= 0 && got == want
}

// cufeFields is the ordered list of fields concatenated to form the CUFE input,
// per the DIAN electronic-invoice specification.
var cufeFields = []string{
	"NumFac", "FecFac", "HorFac", "ValFac",
	"CodImp1", "ValImp1", "CodImp2", "ValImp2", "CodImp3", "ValImp3",
	"ValTot", "NitOFE", "NumAdq", "ClaveT", "TipAmb",
}

// calculateCUFE returns the hex-encoded SHA-384 of the DIAN CUFE field
// concatenation. The fields are read in the fixed cufeFields order, so the same
// input always yields the same digest (reproducible). Missing fields are treated
// as empty strings. This is a correct-shape placeholder: the hash and field order
// match the spec; production may need locale-specific value formatting.
func calculateCUFE(fields map[string]any) string {
	var sb strings.Builder
	for _, k := range cufeFields {
		sb.WriteString(cufeValue(fields[k]))
	}
	sum := sha512.Sum384([]byte(sb.String()))
	return hex.EncodeToString(sum[:])
}

// cufeValue renders a field value as a string. goja passes JS numbers as float64,
// so format them without scientific notation and without a trailing ".0".
func cufeValue(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	case int64:
		return strconv.FormatInt(t, 10)
	case bool:
		return strconv.FormatBool(t)
	default:
		return fmt.Sprint(t)
	}
}
