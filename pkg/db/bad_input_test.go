package db

import (
	"errors"
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
)

// Every code here was OBSERVED being returned to a client as a 500 during
// SILENT-FAILURE-S1, from a single mistyped query parameter or write value:
//
//	?filter[due][gt]=notadate       → 22007
//	?filter[due][gt]=2026-13-45     → 22008
//	?filter[amount][gt]=<24 nines>  → 22003
//	POST {"due":"not-a-date"}       → 22007
//
// Only 22P02 was classified as caller input, so integers took the 400 path and
// everything else took the 500 path. A 500 for a typo is worse than a bad
// message: it is logged as an engine fault, it burns the SLO error budget, and
// any authenticated caller could trigger it at will.
func TestIsBadInput_CoversTheDataExceptionsAClientCanTrigger(t *testing.T) {
	for _, code := range []string{"22P02", "22007", "22008", "22003"} {
		if !IsBadInput(&pgconn.PgError{Code: code}) {
			t.Errorf("SQLSTATE %s must be classified as caller input (400), not an engine fault (500)", code)
		}
	}
}

// The set is deliberately bounded. 22001 is NOT in it: it was in the first draft
// and removed because it was never observed — string/text columns are unbounded
// TEXT and length is enforced by the declarative maxLength rule long before any
// SQL. Classifying an unobserved code would make a genuine engine fault answer
// 400, which is the same guessing this fix exists to stop.
func TestIsBadInput_DoesNotSwallowServerFaults(t *testing.T) {
	for _, code := range []string{
		"22001", // string_data_right_truncation — unobserved from client input
		"23505", // unique_violation → 409, handled elsewhere
		"23503", // foreign_key_violation → 409, handled elsewhere
		"42703", // undefined_column → the 422 unknown_field shape
		"42P01", // undefined_table → IsMissingTenant
		"53300", // too_many_connections — a REAL engine fault; must stay a 500
		"57014", // query_canceled — must stay a 500
		"08006", // connection_failure — must stay a 500
	} {
		if IsBadInput(&pgconn.PgError{Code: code}) {
			t.Errorf("SQLSTATE %s must NOT be reported to the client as bad input", code)
		}
	}
}

func TestIsBadInput_NonPgErrors(t *testing.T) {
	if IsBadInput(nil) {
		t.Error("nil is not bad input")
	}
	if IsBadInput(errors.New("boom")) {
		t.Error("a plain error is not bad input")
	}
	// It must see through a wrapped error, which is how it arrives from pgx.
	if !IsBadInput(fmt.Errorf("query failed: %w", &pgconn.PgError{Code: "22007"})) {
		t.Error("a wrapped PgError must still be classified")
	}
}
