// Package summary composes an owner-language daily digest of a tenant from
// facts the engine already knows how to compute (VOZ-ESCALON1-S1, step 1 of
// the voice plan A-70). It is GENERIC: the engine does not know what a "sale"
// is — it knows which resources the schema declares and what happened to them
// today. It is DETERMINISTIC: a template over real numbers, no language model
// (a model, if ever, would compose over numbers this already produced).
//
// This package is pure — no SQL, no HTTP. The caller (the /api/summary handler)
// gathers the Facts scoped by RBAC and hands them here to be told, in Spanish,
// for a phone.
package summary

import (
	"fmt"
	"sort"
	"strings"

	"github.com/appximo/appximo/pkg/schema"
)

// Plan describes, for one resource, WHAT to ask about it — derived from the
// schema alone. A nil field means "the schema can't express this, so the digest
// stays silent about it" (honesty over invented signals).
type Plan struct {
	Resource string
	// CreatedTsField is a create timestamp column (auto:"create" or legacy),
	// empty when the resource has none (then "nuevo hoy" cannot be known).
	CreatedTsField string
	// UpdatedTsField is a modification timestamp column (auto:"update" / the
	// legacy updated_at), empty when none.
	UpdatedTsField string
	// StateField is a string field with a state_machine, empty when none.
	StateField string
	// PendingStates are the NON-terminal states of StateField (a row there is
	// "pending of someone"), schema order preserved. Empty when no state field.
	PendingStates []string
}

// PlanFor derives the per-resource plan from the schema. It never guesses from
// English names: the create/update timestamps come from the declared auto ROLE
// (schema.AutoValue), and "pending" from the state machine's non-terminal
// states — both structural facts, not heuristics.
func PlanFor(name string, res *schema.ResourceSchema) Plan {
	p := Plan{Resource: name}
	// Deterministic field iteration.
	names := make([]string, 0, len(res.Fields))
	for f := range res.Fields {
		names = append(names, f)
	}
	sort.Strings(names)
	for _, f := range names {
		fd := res.Fields[f]
		if !fd.Auto.Enabled() {
			continue
		}
		if fd.Auto.RefreshesOnUpdate(f) {
			if p.UpdatedTsField == "" {
				p.UpdatedTsField = f
			}
		} else if p.CreatedTsField == "" {
			p.CreatedTsField = f
		}
	}
	for _, f := range names {
		fd := res.Fields[f]
		if fd.StateMachine == nil {
			continue
		}
		p.StateField = f
		p.PendingStates = nonTerminalStates(fd.StateMachine)
		break
	}
	return p
}

// nonTerminalStates returns the states with at least one outgoing transition
// (a terminal state — no outgoing moves — is done, not pending), in a stable
// order: enum order if the field declares one is not available here, so we sort
// for determinism.
func nonTerminalStates(sm *schema.StateMachine) []string {
	var out []string
	for s := range sm.KnownStates() {
		if tos, ok := sm.Transitions[s]; ok && len(tos) > 0 {
			out = append(out, s)
		}
	}
	sort.Strings(out)
	return out
}

// Facts is what the handler measured for one resource, scoped by the caller's
// RBAC (only resources the role may read reach here; the counts already honor
// the role's row condition).
type Facts struct {
	Resource     string
	CreatedToday int64
	HasCreated   bool // the resource has a create-ts column (else CreatedToday is unknown, not zero)
	UpdatedToday int64
	HasUpdated   bool
	// Pending maps a non-terminal state → current row count (only states with
	// count > 0 need be present). Nil when the resource has no state machine.
	Pending      map[string]int64
	PendingTotal int64
	HasState     bool
}

// Report is the whole digest.
type Report struct {
	AppName   string  `json:"app"`
	Tenant    string  `json:"tenant"`
	Day       string  `json:"day"` // YYYY-MM-DD in the report's timezone
	Facts     []Facts `json:"-"`
	Text      string  `json:"text"`
	HasMotion bool    `json:"has_motion"`
}

// Compose fills Text (Spanish, phone-first) and HasMotion from the gathered
// facts. Empty-with-dignity: a day with nothing to say is "sin movimiento hoy",
// never a wall of zeros. Only resources with something to report appear.
func Compose(appName, tenant, day string, facts []Facts) Report {
	r := Report{AppName: appName, Tenant: tenant, Day: day, Facts: facts}

	app := esc(appName)
	if app == "" {
		app = "tu app"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "📋 <b>Resumen de %s</b> · %s\n", app, esc(day))

	// Stable resource order.
	sort.Slice(facts, func(i, j int) bool { return facts[i].Resource < facts[j].Resource })

	lines := 0
	for _, f := range facts {
		var parts []string
		if f.HasCreated && f.CreatedToday > 0 {
			parts = append(parts, fmt.Sprintf("🆕 %d %s", f.CreatedToday, plural(f.Resource, f.CreatedToday, "nuevo", "nuevos")))
		}
		if f.HasUpdated && f.UpdatedToday > 0 {
			parts = append(parts, fmt.Sprintf("✏️ %d actualizad%s hoy", f.UpdatedToday, oS(f.UpdatedToday)))
		}
		if f.HasState && f.PendingTotal > 0 {
			parts = append(parts, "⏳ "+pendingPhrase(f))
		}
		if len(parts) == 0 {
			continue
		}
		fmt.Fprintf(&b, "\n<b>%s</b>\n%s\n", esc(f.Resource), strings.Join(parts, "\n"))
		lines++
	}

	if lines == 0 {
		r.Text = fmt.Sprintf("📋 <b>Resumen de %s</b> · %s\n\nSin movimiento hoy.", app, esc(day))
		r.HasMotion = false
		return r
	}
	r.HasMotion = true
	r.Text = strings.TrimRight(b.String(), "\n")
	return r
}

// pendingPhrase renders "pendientes: 5 (pendiente: 3, pagado: 2)" — the state
// vocabulary is the schema's own words, never translated (DEMO-SHOWCASE rule).
func pendingPhrase(f Facts) string {
	states := make([]string, 0, len(f.Pending))
	for s := range f.Pending {
		states = append(states, s)
	}
	sort.Strings(states)
	var detail []string
	for _, s := range states {
		if f.Pending[s] > 0 {
			detail = append(detail, fmt.Sprintf("%s: %d", esc(s), f.Pending[s]))
		}
	}
	noun := "pendientes"
	if f.PendingTotal == 1 {
		noun = "pendiente"
	}
	msg := fmt.Sprintf("%d %s de alguien", f.PendingTotal, noun)
	if len(detail) > 0 {
		msg += " (" + strings.Join(detail, ", ") + ")"
	}
	return msg
}

func plural(resource string, n int64, one, many string) string {
	// The resource name is the noun; the adjective agrees in number.
	adj := one
	if n != 1 {
		adj = many
	}
	return fmt.Sprintf("%s %s", esc(resource), adj)
}

func oS(n int64) string {
	if n == 1 {
		return "o"
	}
	return "os"
}

func esc(s string) string {
	// Minimal HTML escaping for Telegram's HTML parse mode; the composer emits
	// <b> deliberately, so only the dynamic values are escaped by callers of esc.
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;")
	return r.Replace(s)
}
