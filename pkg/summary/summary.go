// Package summary composes an owner-language daily digest of a tenant from
// facts the engine already knows how to compute (VOZ-ESCALON1-S1, step 1 of
// the voice plan A-70; VOZ-VISUAL-S1 gave it a hierarchy and an image). It is
// GENERIC: the engine does not know what a "sale" is — it knows which
// resources the schema declares and what happened to them today. It is
// DETERMINISTIC: a template over real numbers, no language model (a model, if
// ever, would compose over numbers this already produced).
//
// The vocabulary (VOZ-2, decided in VOZ-VISUAL-S1 / ADR-032): a row in a
// state_machine state is NOT automatically "pending of someone". Three tiers:
//
//   - ATTENTION — the states the schema DECLARES as `pending` (a row there
//     waits for someone to act): "esperan acción", on top, big, red.
//   - INFERRED — when nothing is declared, the non-terminal INITIAL states:
//     the row was created and nobody moved it yet. Reported as "sin avanzar"
//     with that humble wording, amber — the engine says what it knows
//     (structure) and not what it doesn't (intent).
//   - FLOW — every other non-terminal state: a neutral count in the schema's
//     own words ("activo 9"), never called pending. Terminal states are done
//     and never counted.
//
// This package is pure — no SQL, no HTTP. The caller (the /api/summary
// handler) gathers the Facts scoped by RBAC and hands them here to be told, in
// Spanish, for a phone — as text (Telegram HTML) and as an image (render.go).
package summary

import (
	"fmt"
	"sort"
	"strings"

	"github.com/appximo/appximo/pkg/schema"
)

// Plan describes, for one resource, WHAT to ask about it — derived from the
// schema alone. An empty field means "the schema can't express this, so the
// digest stays silent about it" (honesty over invented signals).
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
	// Attention are the states whose rows WAIT for someone: the declared
	// `pending` list (declaration order), or — when nothing is declared — the
	// non-terminal INITIAL states (inferred, sorted). Never a terminal state.
	Attention []string
	// AttentionInferred is true when Attention came from structure, not from a
	// declaration; the digest words those counts humbly ("sin avanzar").
	AttentionInferred bool
	// Flow are the remaining non-terminal states (neutral counts), sorted.
	Flow []string
}

// PlanFor derives the per-resource plan from the schema. It never guesses from
// English names: the create/update timestamps come from the declared auto ROLE
// (schema.AutoValue), and the state tiers from the state machine's structure
// and its declared `pending` — facts, not heuristics over words.
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
		p.Attention, p.AttentionInferred, p.Flow = stateTiers(fd.StateMachine)
		break
	}
	return p
}

// stateTiers splits a machine's NON-terminal states into (attention, flow).
// Declared `pending` wins verbatim (validated at load: known, non-terminal,
// unique). Undeclared: the initial states with an outgoing move are the
// structural best guess for "waiting" — a row is born there and MUST be moved
// by someone — and the guess is labelled as such (inferred=true). A declared
// empty list means "nothing here waits": everything non-terminal is flow.
func stateTiers(sm *schema.StateMachine) (attention []string, inferred bool, flow []string) {
	nonTerminal := map[string]bool{}
	for s := range sm.KnownStates() {
		if !sm.IsTerminal(s) {
			nonTerminal[s] = true
		}
	}
	inAttention := map[string]bool{}
	if sm.PendingDeclared() {
		for _, s := range sm.Pending {
			if nonTerminal[s] && !inAttention[s] {
				attention = append(attention, s)
				inAttention[s] = true
			}
		}
	} else {
		inferred = true
		for _, s := range sm.Initial {
			if nonTerminal[s] && !inAttention[s] {
				attention = append(attention, s)
				inAttention[s] = true
			}
		}
		sort.Strings(attention)
		if len(attention) == 0 {
			inferred = false // nothing was guessed
		}
	}
	for s := range nonTerminal {
		if !inAttention[s] {
			flow = append(flow, s)
		}
	}
	sort.Strings(flow)
	return attention, inferred, flow
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
	// HasState is true when the resource has a state machine whose counts
	// could be measured.
	HasState bool
	// Attention maps an attention state → current row count (only states with
	// count > 0 need be present); AttentionTotal is their sum.
	Attention         map[string]int64
	AttentionTotal    int64
	AttentionInferred bool
	// Flow maps a neutral non-terminal state → count; FlowTotal is their sum.
	Flow      map[string]int64
	FlowTotal int64
	// Total carries the census view's row count (the `estado` command).
	Total    int64
	HasTotal bool
}

// hasMotion reports whether anything happened to this resource TODAY.
func (f Facts) hasMotion() bool {
	return (f.HasCreated && f.CreatedToday > 0) || (f.HasUpdated && f.UpdatedToday > 0)
}

// tier ranks a resource for the default order: what needs a human first.
//
//	0 declared attention > 0
//	1 inferred attention > 0
//	2 motion today (created/updated)
//	3 only flow counts
//	4 nothing to say
func (f Facts) tier() int {
	switch {
	case f.AttentionTotal > 0 && !f.AttentionInferred:
		return 0
	case f.AttentionTotal > 0:
		return 1
	case f.hasMotion():
		return 2
	case f.FlowTotal > 0:
		return 3
	default:
		return 4
	}
}

// Order arranges the facts the way the digest reads them. A declared
// `summary.resources` block is authoritative: exactly those resources, in that
// order (a listed resource the caller could not read — absent from facts — is
// simply not there; RBAC wins). Without a declaration: attention first, then
// what moved today, then flow, then silence; within a tier, bigger attention
// first, then the name. This is the cure for "18 lines on a phone": the image
// gives individual rows to the top tiers and folds the rest.
func Order(cfg *schema.SummaryConfig, facts []Facts) []Facts {
	if cfg != nil && len(cfg.Resources) > 0 {
		byName := make(map[string]Facts, len(facts))
		for _, f := range facts {
			byName[f.Resource] = f
		}
		out := make([]Facts, 0, len(cfg.Resources))
		for _, name := range cfg.Resources {
			if f, ok := byName[name]; ok {
				out = append(out, f)
			}
		}
		return out
	}
	out := make([]Facts, len(facts))
	copy(out, facts)
	sort.SliceStable(out, func(i, j int) bool {
		ti, tj := out[i].tier(), out[j].tier()
		if ti != tj {
			return ti < tj
		}
		if out[i].AttentionTotal != out[j].AttentionTotal {
			return out[i].AttentionTotal > out[j].AttentionTotal
		}
		return out[i].Resource < out[j].Resource
	})
	return out
}

// Level is the digest's traffic light.
const (
	LevelRed   = "red"   // something DECLARED as waiting has rows
	LevelAmber = "amber" // only INFERRED waiting rows (created, not moved)
	LevelGreen = "green" // nothing to attend
)

// Report is the whole digest.
type Report struct {
	AppName string  `json:"app"`
	Tenant  string  `json:"tenant"`
	Day     string  `json:"day"` // YYYY-MM-DD in the report's timezone
	Facts   []Facts `json:"-"`   // already ORDERED (see Order)
	Text    string  `json:"text"`
	// HasMotion is true when something happened today or something waits.
	HasMotion bool `json:"has_motion"`
	// Level is red|amber|green; Headline is its one-line plain-text reading
	// ("3 esperan acción") — the same words the image paints.
	Level          string `json:"level"`
	Headline       string `json:"headline"`
	AttentionTotal int64  `json:"attention_total"`
	Census         bool   `json:"-"`
}

// Compose fills Text (Spanish, phone-first Telegram HTML), Level, Headline and
// HasMotion from ORDERED facts. Empty-with-dignity: a day with nothing to say
// is "Sin movimiento hoy", never a wall of zeros. Only resources with
// something to report get a block; resources with only flow counts are folded
// into one closing line so a wide app stays readable.
func Compose(appName, tenant, day string, facts []Facts) Report {
	r := Report{AppName: appName, Tenant: tenant, Day: day, Facts: facts}

	app := esc(appName)
	if app == "" {
		app = "tu app"
	}

	var declared, inferred int64
	for _, f := range facts {
		if f.AttentionInferred {
			inferred += f.AttentionTotal
		} else {
			declared += f.AttentionTotal
		}
	}
	r.AttentionTotal = declared + inferred
	switch {
	case declared > 0:
		r.Level = LevelRed
		r.Headline = fmt.Sprintf("%d %s acción", declared, esperan(declared))
		if inferred > 0 {
			r.Headline += fmt.Sprintf(" · %d sin avanzar", inferred)
		}
	case inferred > 0:
		r.Level = LevelAmber
		r.Headline = fmt.Sprintf("%d sin avanzar (recién %s, nadie %s movió)", inferred, creados(inferred), losLo(inferred))
	default:
		r.Level = LevelGreen
		r.Headline = "Nada que atender"
	}

	var b strings.Builder
	fmt.Fprintf(&b, "📋 <b>Resumen de %s</b> · %s\n", app, esc(day))
	fmt.Fprintf(&b, "%s <b>%s</b>\n", levelDot(r.Level), esc(r.Headline))

	blocks := 0
	var folded []string
	for _, f := range facts {
		var parts []string
		if f.AttentionTotal > 0 {
			parts = append(parts, attentionPhrase(f))
		}
		var motion []string
		if f.HasCreated && f.CreatedToday > 0 {
			motion = append(motion, fmt.Sprintf("🆕 %d %s", f.CreatedToday, nuevos(f.CreatedToday)))
		}
		if f.HasUpdated && f.UpdatedToday > 0 {
			motion = append(motion, fmt.Sprintf("✏️ %d actualizad%s", f.UpdatedToday, oS(f.UpdatedToday)))
		}
		if len(motion) > 0 {
			parts = append(parts, strings.Join(motion, " · "))
		}
		if len(parts) == 0 {
			if f.FlowTotal > 0 {
				folded = append(folded, fmt.Sprintf("%s (%s)", esc(f.Resource), flowDetail(f)))
			}
			continue
		}
		if f.FlowTotal > 0 {
			parts = append(parts, "▫️ en curso: "+flowDetail(f))
		}
		fmt.Fprintf(&b, "\n<b>%s</b>\n%s\n", esc(f.Resource), strings.Join(parts, "\n"))
		blocks++
	}

	if blocks == 0 {
		b.WriteString("\nSin movimiento hoy.\n")
	} else {
		r.HasMotion = true
	}
	if len(folded) > 0 {
		b.WriteString("\n▫️ Sin novedad hoy: " + strings.Join(folded, " · ") + "\n")
	}
	r.Text = strings.TrimRight(b.String(), "\n")
	return r
}

// attentionPhrase renders the attention line in the schema's own words:
// declared → "⏳ 3 esperan acción (pagada: 2, preparando: 1)";
// inferred → "🕐 2 sin avanzar (creada: 2)".
func attentionPhrase(f Facts) string {
	detail := stateDetail(f.Attention)
	if f.AttentionInferred {
		return fmt.Sprintf("🕐 %d sin avanzar (%s)", f.AttentionTotal, detail)
	}
	return fmt.Sprintf("⏳ %d %s acción (%s)", f.AttentionTotal, esperan(f.AttentionTotal), detail)
}

func flowDetail(f Facts) string { return stateDetail(f.Flow) }

// stateDetail renders "estado: n" pairs, sorted by count desc then name — the
// vocabulary is the schema's, never translated (DEMO-SHOWCASE rule).
func stateDetail(m map[string]int64) string {
	type kv struct {
		k string
		v int64
	}
	kvs := make([]kv, 0, len(m))
	for k, v := range m {
		if v > 0 {
			kvs = append(kvs, kv{k, v})
		}
	}
	sort.Slice(kvs, func(i, j int) bool {
		if kvs[i].v != kvs[j].v {
			return kvs[i].v > kvs[j].v
		}
		return kvs[i].k < kvs[j].k
	})
	parts := make([]string, 0, len(kvs))
	for _, e := range kvs {
		parts = append(parts, fmt.Sprintf("%s: %d", esc(e.k), e.v))
	}
	return strings.Join(parts, ", ")
}

// ComposeCensus renders the `estado` view: how big the business is right now
// (total rows per resource the role may read) — an owner census, not system
// metrics. Facts must be ORDERED; only facts with HasTotal are listed.
func ComposeCensus(appName, tenant string, facts []Facts) Report {
	rep := Report{AppName: appName, Tenant: tenant, Facts: facts, Census: true, Level: LevelGreen}
	app := esc(appName)
	if app == "" {
		app = "tu app"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "📊 <b>Estado de %s</b>\n", app)
	any := false
	for _, f := range facts {
		if !f.HasTotal {
			continue
		}
		any = true
		fmt.Fprintf(&b, "• <b>%s</b>: %d\n", esc(f.Resource), f.Total)
	}
	if !any {
		rep.Text = "📊 <b>Estado de " + app + "</b>\n\nNo hay datos todavía."
		rep.Headline = "No hay datos todavía"
		return rep
	}
	rep.HasMotion = true
	rep.Headline = "Cuántos hay de cada cosa"
	rep.Text = strings.TrimRight(b.String(), "\n")
	return rep
}

func levelDot(level string) string {
	switch level {
	case LevelRed:
		return "🔴"
	case LevelAmber:
		return "🟡"
	default:
		return "🟢"
	}
}

func esperan(n int64) string {
	if n == 1 {
		return "espera"
	}
	return "esperan"
}

func creados(n int64) string {
	if n == 1 {
		return "creado"
	}
	return "creados"
}

func losLo(n int64) string {
	if n == 1 {
		return "lo"
	}
	return "los"
}

func nuevos(n int64) string {
	if n == 1 {
		return "nuevo"
	}
	return "nuevos"
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
