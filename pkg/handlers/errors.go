package handlers

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/sony/gobreaker"

	"github.com/appximo/appximo/pkg/db"
)

// WriteErrorKind is the engine's client-facing vocabulary for a write's
// database error. There is exactly ONE ladder that decides which SQLSTATE means
// what — ClassifyWriteError below — and four renderers compiled from it: the
// REST single-op response (WriteDBError), the batch-transaction response
// (codegen.dbTxError), the GraphQL errors array (graphql.safeDBErr) and the
// library path's typed errors (Ctx.Insert/Update, ENG-42). A kind added here
// reaches all four by construction; a kind classified in only one of them is
// the divergence class CTX_PARITY_AUDIT.md exists to prevent.
type WriteErrorKind int

const (
	// WriteErrNone: not classified — an unexpected server error. Handlers mask
	// it (500), never leaking the raw driver message.
	WriteErrNone WriteErrorKind = iota
	// WriteErrUnique: 23505 unique_violation → 409 `field "x": value already
	// exists` (Field carries the offending column).
	WriteErrUnique
	// WriteErrUnknownColumn: 42703 undefined_column on a write → the S44 422
	// {rule:"unknown_field"} (Field carries the column). The DB is the source
	// of truth for the writable column set (see the no-whitelist NOTE in
	// codegen.BuildRouter), so this classification happens on the error.
	WriteErrUnknownColumn
	// WriteErrFileRef: 23503 on the tenant's files FK (FILES-LINK-S1) → the
	// S44 422 {rule:"file_not_found"} (Field carries the file field's column).
	WriteErrFileRef
	// WriteErrForeignKey: any other 23503 → 409 with the safe message naming
	// the related resource (Message; never raw SQL).
	WriteErrForeignKey
	// WriteErrMissingTenant: 42P01/3F000 → 400 "invalid tenant".
	WriteErrMissingTenant
	// WriteErrBadInput: the observed class-22 codes (ADR-024) → 400 "invalid
	// request" — a caller-supplied value Postgres could not parse for the column.
	WriteErrBadInput
	// WriteErrUnavailable: the database cannot serve this right now → 503 +
	// Retry-After.
	WriteErrUnavailable
	// WriteErrRangeConflict: 23P01 exclusion_violation on an engine-generated
	// no-overlap constraint (MOTOR-AGENDA-S1) → 409 {"error":
	// "time_range_conflict", range, existing, conflicts[]} naming the row(s)
	// the write collided with (Conflict carries them).
	WriteErrRangeConflict
	// WriteErrRangeOrder: 23514 check_violation on an engine-generated
	// range-order CHECK → the S44 422 {rule:"range_order"} on the range's end
	// field (Field carries the constraint symbol; the describer maps it to the
	// field when the schema is at hand).
	WriteErrRangeOrder
)

// WriteErrorVerdict is ClassifyWriteError's result: the kind plus the safe,
// client-visible pieces each renderer needs (a column name, a referential
// message) — never the raw driver error.
type WriteErrorVerdict struct {
	Kind    WriteErrorKind
	Field   string // WriteErrUnique / WriteErrUnknownColumn / WriteErrFileRef
	Message string // WriteErrForeignKey's safe, human-readable message
	// Conflict is set for WriteErrRangeConflict: the colliding row's key and,
	// when a describer resolved it, the row(s) as the role may see them.
	Conflict *RangeConflict
}

// ClassifyWriteError is the single classification of a write's database error
// into the engine's error vocabulary. Every branch matches a predicate in
// pkg/db whose SQLSTATE was OBSERVED being produced by client input (the
// ADR-024 discipline: a code classified from theory turns a genuine engine
// fault into a lying 4xx — so an unobserved code stays WriteErrNone and is
// masked as the 500 it is).
//
// Order matters only once: the files-FK check must precede the generic FK
// check (both are 23503).
func ClassifyWriteError(err error) WriteErrorVerdict {
	if err == nil {
		return WriteErrorVerdict{}
	}
	// Time ranges (MOTOR-AGENDA-S1): a described conflict first, then the raw
	// driver forms of the two engine-generated constraints.
	if v, ok := classifyRangeErrors(err); ok {
		return v
	}
	if constraint, keyCols, keyVals, ok := db.ExclusionViolation(err); ok {
		c := RangeConflict{Constraint: constraint}
		if scope, start, end, pok := parseExclusionKey(keyCols, keyVals); pok {
			c.ExistingScope, c.ExistingStart, c.ExistingEnd = scope, start, end
		}
		return WriteErrorVerdict{Kind: WriteErrRangeConflict, Field: constraint, Message: RangeConflictMessage(c), Conflict: &c}
	}
	if constraint, ok := db.RangeOrderViolation(err); ok {
		return WriteErrorVerdict{Kind: WriteErrRangeOrder, Field: rangeOrderField(constraint), Message: RangeOrderMessage}
	}
	if field, ok := db.UniqueViolationField(err); ok {
		return WriteErrorVerdict{Kind: WriteErrUnique, Field: field}
	}
	if field, ok := db.UndefinedColumnField(err); ok {
		return WriteErrorVerdict{Kind: WriteErrUnknownColumn, Field: field}
	}
	if column, ok := db.FileReferenceViolation(err); ok {
		if column == "" {
			column = "file"
		}
		return WriteErrorVerdict{Kind: WriteErrFileRef, Field: column}
	}
	if msg, ok := db.ForeignKeyViolation(err); ok {
		return WriteErrorVerdict{Kind: WriteErrForeignKey, Message: msg}
	}
	switch {
	case db.IsMissingTenant(err):
		return WriteErrorVerdict{Kind: WriteErrMissingTenant}
	case db.IsBadInput(err):
		return WriteErrorVerdict{Kind: WriteErrBadInput}
	case db.IsUnavailable(err):
		// A deliberate breaker rejection must SAY it is the breaker (AUTO-4):
		// an unnamed 503 gets diagnosed as the outage it is protecting against.
		// The message rides Verdict.Message so all renderers say the same thing.
		if errors.Is(err, gobreaker.ErrOpenState) || errors.Is(err, gobreaker.ErrTooManyRequests) {
			return WriteErrorVerdict{Kind: WriteErrUnavailable, Message: BreakerOpenMessage}
		}
		return WriteErrorVerdict{Kind: WriteErrUnavailable}
	}
	return WriteErrorVerdict{}
}

// BreakerOpenMessage is the one 503 body every renderer uses when the rejection
// came from the OPEN circuit breaker rather than a live database error — so the
// operator can tell "the breaker is shedding" from "the database just failed".
const BreakerOpenMessage = "service unavailable: circuit breaker open (the database was not serving; the breaker re-probes within 8s — see appximo_breaker_state on /metrics)"

// FileRefMessage is the one message every surface uses for a `file` field whose
// value references no file of the tenant (REST 422, batch 422, GraphQL,
// Ctx.Insert/Update) — declared once so the four renderers cannot drift.
const FileRefMessage = "does not reference an existing file of this tenant"

// UnknownFieldMessage is the S44 message for a write naming a column no table
// backs — same single-declaration rationale as FileRefMessage.
const UnknownFieldMessage = "is not a field of this resource"

// IsServerError reports whether err would be answered as HTTP 500 by WriteDBError
// (i.e. the classifier has no verdict for it). Used to decide whether to capture a
// stack trace — client/conflict/availability errors are not bugs.
func IsServerError(err error) bool {
	if err == nil {
		return false
	}
	return ClassifyWriteError(err).Kind == WriteErrNone
}

// WriteDBError maps a database error to an HTTP response without leaking internal
// details (raw SQL, schema names) to the client. It is the REST single-op
// rendering of ClassifyWriteError:
//   - unique violation (23505)                     → 409 field "x": value already exists
//   - unknown column on a write (42703)            → 422 validation_failed/unknown_field
//   - bad `file` reference (23503 on the files FK) → 422 validation_failed/file_not_found
//   - other foreign-key violation (23503)          → 409 with the safe message
//   - missing tenant schema/relation (42P01/3F000) → 400 "invalid tenant"
//   - caller-supplied bad input (class 22)         → 400 "invalid request"
//   - unreachable database                         → 503 "service unavailable" + Retry-After
//   - anything else                                → 500 "internal error"
func WriteDBError(w http.ResponseWriter, err error) {
	switch v := ClassifyWriteError(err); v.Kind {
	case WriteErrUnique:
		writeJSONError(w, http.StatusConflict, fmt.Sprintf("field %q: value already exists", v.Field))
	case WriteErrUnknownColumn:
		// The same 422 shape as the declarative validator (S44), so clients
		// handle one error contract for every rejected write.
		writeFieldError(w, v.Field, "unknown_field", UnknownFieldMessage)
	case WriteErrFileRef:
		// To the client a bad file reference is input validation on that field
		// (FILES-LINK-S1), even though the guard is the real FK.
		writeFieldError(w, v.Field, "file_not_found", FileRefMessage)
	case WriteErrForeignKey:
		// A RESTRICT delete of a still-referenced row, or a write referencing a
		// non-existent row (MIG-F1-S1) — a clear, safe message, never a masked 500.
		writeJSONError(w, http.StatusConflict, v.Message)
	case WriteErrRangeConflict:
		// A declared no-overlap rule (MOTOR-AGENDA-S1): 409 naming the range and
		// the row(s) it collides with — the constraint is the race-safe net, the
		// body is what a UI shows.
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		json.NewEncoder(w).Encode(RangeConflictBody(*v.Conflict)) //nolint:errcheck
	case WriteErrRangeOrder:
		writeFieldError(w, v.Field, RangeOrderRule, RangeOrderMessage)
	case WriteErrMissingTenant:
		writeJSONError(w, http.StatusBadRequest, "invalid tenant")
	case WriteErrBadInput:
		writeJSONError(w, http.StatusBadRequest, "invalid request")
	case WriteErrUnavailable:
		// Tell the client this is transient and roughly when to come back. A 503
		// WITHOUT Retry-After leaves an SDK guessing (most default to an immediate
		// retry, which is exactly what a saturated database does not need). When
		// the rejection is the OPEN breaker, the body and Retry-After say so.
		if v.Message != "" {
			w.Header().Set("Retry-After", "8")
			writeJSONError(w, http.StatusServiceUnavailable, v.Message)
			return
		}
		w.Header().Set("Retry-After", "1")
		writeJSONError(w, http.StatusServiceUnavailable, "service unavailable")
	default:
		writeJSONError(w, http.StatusInternalServerError, "internal error")
	}
}

func writeJSONError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]string{"error": msg}) //nolint:errcheck
}

// writeFieldError writes the single-field S44 422 body. The fields entry is a
// map (not schema.FieldRuleError) to preserve this response's historical key
// order — encoding/json sorts map keys — byte-for-byte.
func writeFieldError(w http.ResponseWriter, field, rule, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnprocessableEntity)
	json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
		"error": "validation_failed",
		"fields": []map[string]string{
			{"field": field, "rule": rule, "message": message},
		},
	})
}
