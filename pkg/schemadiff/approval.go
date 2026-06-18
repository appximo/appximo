package schemadiff

// This file defines the APPROVAL surface of the migration engine: which operations
// are DESTRUCTIVE (lose data) and the stable, human-readable KEY an operator must
// enumerate to approve one. It is the data the migration layer's approval gate uses
// to refuse a data-losing drop unless it was named explicitly — informed, enumerated
// consent, never a blind "yes to everything".
//
// Destructive = data-losing = DropTable / DropColumn (Risk() == RiskDestructive).
// A safe drop (an index or a constraint) loses no row data and is NOT approvable in
// the additive v1 policy — it stays as drift. The migration layer is the only place
// that decides apply-vs-gate; this file just classifies and names.

// DestructiveKey returns the stable approval token for a data-losing operation and
// true, or ("", false) for any non-destructive operation. The token is exactly what
// an operator lists to approve the drop, and is unambiguous because resource and
// field names match ^[a-z][a-z0-9_]*$ (no dots), so a table key never collides with
// a column key:
//
//	DropTable  → "<table>"            e.g. "proyectos"
//	DropColumn → "<table>.<column>"   e.g. "empleados.telefono"
func DestructiveKey(op Operation) (key string, destructive bool) {
	switch o := op.(type) {
	case DropTable:
		return o.Table.Name, true
	case DropColumn:
		return o.Table + "." + o.Column.Name, true
	default:
		return "", false
	}
}

// IsDestructive reports whether op loses data (DropTable / DropColumn). It is the
// boolean companion to DestructiveKey for callers that only need the predicate.
func IsDestructive(op Operation) bool {
	_, d := DestructiveKey(op)
	return d
}
