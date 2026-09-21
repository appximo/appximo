package handlers

import (
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
)

// TestClassify_ExclusionAndRangeOrder (MOTOR-AGENDA-S1): the two engine
// constraints enter the one ladder with the colliding key parsed from the
// Detail exactly as PG16 prints it; a consumer's own EXCLUDE stays unclassified.
func TestClassify_ExclusionAndRangeOrder(t *testing.T) {
	err := &pgconn.PgError{Code: "23P01", ConstraintName: "excl_eventos_horario_6069c31b",
		Message: `conflicting key value violates exclusion constraint "excl_eventos_horario_6069c31b"`,
		Detail:  `Key (dueno_id, tstzrange(inicio, fin, '[)'::text))=(11111111-1111-1111-1111-111111111111, ["2026-09-22 21:30:00+00","2026-09-22 22:30:00+00")) conflicts with existing key (dueno_id, tstzrange(inicio, fin, '[)'::text))=(11111111-1111-1111-1111-111111111111, ["2026-09-22 21:00:00+00","2026-09-22 22:00:00+00")).`}
	v := ClassifyWriteError(err)
	if v.Kind != WriteErrRangeConflict || v.Conflict == nil {
		t.Fatalf("kind=%v conflict=%v", v.Kind, v.Conflict)
	}
	c := v.Conflict
	if c.ExistingStart != "2026-09-22 21:00:00+00" || c.ExistingEnd != "2026-09-22 22:00:00+00" || c.ExistingScope["dueno_id"] != "11111111-1111-1111-1111-111111111111" {
		t.Fatalf("existing key parsed wrong: %+v", c)
	}
	body := RangeConflictBody(*c)
	if body["error"] != "time_range_conflict" || body["existing"].(map[string]any)["start"] != "2026-09-22T21:00:00+00:00" {
		t.Fatalf("body: %v", body)
	}
	// No scope column: only the range in the key.
	err2 := &pgconn.PgError{Code: "23P01", ConstraintName: "excl_eventos_horario_aaaa0000",
		Detail: `Key (tstzrange(inicio, fin, '[)'::text))=(["2026-09-22 21:30:00+00","2026-09-22 22:30:00+00")) conflicts with existing key (tstzrange(inicio, fin, '[)'::text))=(["2026-09-22 21:00:00+00","2026-09-22 22:00:00+00")).`}
	if v := ClassifyWriteError(err2); v.Kind != WriteErrRangeConflict || v.Conflict.ExistingStart != "2026-09-22 21:00:00+00" || len(v.Conflict.ExistingScope) != 0 {
		t.Fatalf("scope-less key: %+v", v.Conflict)
	}
	// A consumer's own EXCLUDE (not excl_-prefixed) stays unclassified → masked 500.
	if v := ClassifyWriteError(&pgconn.PgError{Code: "23P01", ConstraintName: "my_rooms_no_double"}); v.Kind != WriteErrNone {
		t.Fatalf("foreign exclusion must stay unclassified, got %v", v.Kind)
	}
	// The order CHECK.
	if v := ClassifyWriteError(&pgconn.PgError{Code: "23514", ConstraintName: "chk_eventos_horario_order"}); v.Kind != WriteErrRangeOrder || v.Field != "horario" {
		t.Fatalf("range order: %+v", v)
	}
	if v := ClassifyWriteError(&pgconn.PgError{Code: "23514", ConstraintName: "positive_amount"}); v.Kind != WriteErrNone {
		t.Fatalf("a foreign CHECK must stay unclassified, got %v", v.Kind)
	}
	// A described error wins over the raw one and carries the rows.
	described := &RangeConflictError{Conflict: RangeConflict{Constraint: "excl_x", Range: "horario", Conflicts: []map[string]any{{"id": "a"}}}, Cause: err}
	if v := ClassifyWriteError(described); v.Kind != WriteErrRangeConflict || v.Conflict.Range != "horario" || len(v.Conflict.Conflicts) != 1 {
		t.Fatalf("described: %+v", v)
	}
	if !errors.Is(described, err) {
		t.Fatalf("the described error must unwrap to its cause")
	}
}
