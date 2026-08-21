package schema

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// AutoValue is the declared engine-management role of a timestamp field —
// the value of the `auto` key (SILENT-CORRUPTION-S1).
//
// JSON forms accepted:
//
//	"auto": true      → AutoLegacy — the historical contract, byte-compatible:
//	                    the column is provisioned TIMESTAMPTZ DEFAULT now()
//	                    (set once at insert) and, ONLY when the field is
//	                    literally named `updated_at`, additionally refreshed to
//	                    now() on every update. Any other name behaves as a
//	                    creation timestamp.
//	"auto": "create"  → AutoCreate — an explicit creation timestamp: set once
//	                    at insert, never refreshed, regardless of the field's
//	                    name. `creado_en`, `placed_at`, `fecha` all work.
//	"auto": "update"  → AutoUpdate — an explicit modification timestamp: set at
//	                    insert AND refreshed to now() on every engine update
//	                    (REST PUT/PATCH, GraphQL update, batch transaction,
//	                    Ctx.Update), regardless of the field's name.
//	                    `modificado_en` finally means what it says.
//	"auto": false     → off (same as omitting the key).
//
// The string forms exist because binding the refresh to the literal English
// name `updated_at` silently froze every non-English modification timestamp at
// its creation value forever — a validator-clean schema whose data was wrong
// (ENG-45). The declared role, not the field's name, is now the source of
// truth; the legacy boolean keeps its name-magic for backward compatibility
// and raises a load warning when its name suggests update intent.
//
// All three enabled forms share the auto contract: exempt from `required`,
// no `default`, excluded from create/update inputs (writes answer 422
// read_only on update), type must be `time` (validated at load — the column
// is TIMESTAMPTZ regardless of the declaration, so any other declared type
// would silently diverge from the database).
type AutoValue string

const (
	// AutoOff — the field is not engine-managed (the zero value).
	AutoOff AutoValue = ""
	// AutoLegacy — JSON `true`: creation-timestamp semantics plus the
	// documented literal-`updated_at` refresh magic.
	AutoLegacy AutoValue = "legacy"
	// AutoCreate — JSON `"create"`: explicit creation timestamp, any name.
	AutoCreate AutoValue = "create"
	// AutoUpdate — JSON `"update"`: explicit modification timestamp, any name.
	AutoUpdate AutoValue = "update"
)

// Enabled reports whether the field is engine-managed at all (any of the
// three enabled forms). Every consumer that used the old boolean semantics
// (`fd.Auto` as bool) asks this.
func (a AutoValue) Enabled() bool { return a != AutoOff }

// UnmarshalJSON accepts the boolean legacy form and the explicit string roles.
// An unrecognized string is preserved verbatim so the validator can reject it
// with a named, actionable error (a raw json error here would lose the path).
func (a *AutoValue) UnmarshalJSON(data []byte) error {
	var b bool
	if err := json.Unmarshal(data, &b); err == nil {
		if b {
			*a = AutoLegacy
		} else {
			*a = AutoOff
		}
		return nil
	}
	var s string
	if err := json.Unmarshal(data, &s); err == nil {
		*a = AutoValue(s)
		return nil
	}
	return fmt.Errorf(`auto must be true, false, "create" or "update"`)
}

// MarshalJSON round-trips faithfully: the legacy form marshals back to `true`
// (the editor's round-trip contract — a schema authored with booleans is
// re-exported with booleans), the explicit roles as their strings.
func (a AutoValue) MarshalJSON() ([]byte, error) {
	switch a {
	case AutoLegacy:
		return []byte("true"), nil
	case AutoOff:
		return []byte("false"), nil
	default:
		return json.Marshal(string(a))
	}
}

// autoUpdateName is the one field name whose LEGACY (`auto: true`) form
// carries refresh-on-update magic — the documented historical contract.
const autoUpdateName = "updated_at"

// RefreshesOnUpdate reports whether a field declared with this AutoValue and
// this name is refreshed to now() by every engine update. The single decision
// point: an explicit "update" role always refreshes; the legacy boolean
// refreshes only the literal `updated_at` (its documented magic); "create"
// never refreshes, even on a field named updated_at (the author explicitly
// opted out).
func (a AutoValue) RefreshesOnUpdate(fieldName string) bool {
	switch a {
	case AutoUpdate:
		return true
	case AutoLegacy:
		return fieldName == autoUpdateName
	default:
		return false
	}
}

// AutoRefreshColumns returns, sorted, the columns of this resource the engine
// forces to now() on every update — the SINGLE source the REST update core,
// the batch transaction, and Ctx.Update all consume, so no update path can
// diverge on which timestamps refresh (SILENT-CORRUPTION-S1: two of the three
// used to hardcode the literal `updated_at` and the third refreshed nothing).
func (r *ResourceSchema) AutoRefreshColumns() []string {
	var cols []string
	for name, fd := range r.Fields {
		if fd.Auto.RefreshesOnUpdate(name) {
			cols = append(cols, name)
		}
	}
	sort.Strings(cols) // deterministic SQL
	return cols
}

// EffectiveAutoRole names the role an enabled auto field actually plays —
// "update" when the engine refreshes it, "create" otherwise, "" when not
// auto. Consumed by the OpenAPI generator (`x-appximo-auto`) so generic
// tools read the truth instead of guessing from English field names.
func (fd FieldDef) EffectiveAutoRole(fieldName string) string {
	if !fd.Auto.Enabled() {
		return ""
	}
	if fd.Auto.RefreshesOnUpdate(fieldName) {
		return "update"
	}
	return "create"
}

// autoUpdateIntentMarkers are the case-insensitive substrings that make a
// field name read as a MODIFICATION timestamp — the names an author (or an
// AI generating a Spanish schema) naturally picks when they mean "when was
// this row last changed": modificado_en, fecha_actualizacion, last_modified,
// changed_at, editado_en… The list is deliberately small and documented; it
// gates a WARNING only (a miss costs a warning, never behavior), the same
// name-heuristic license the identity-column suppression list already takes.
var autoUpdateIntentMarkers = []string{
	"updated", "update", "modif", "actualiz", "changed", "edited", "editado",
}

func nameSuggestsUpdateIntent(name string) bool {
	n := strings.ToLower(name)
	for _, m := range autoUpdateIntentMarkers {
		if strings.Contains(n, m) {
			return true
		}
	}
	return false
}
