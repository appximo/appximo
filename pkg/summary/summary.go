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
	"time"

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
	// RangeName / RangeStart / RangeEnd (APP-AGENDA-S2, VOZ-16): the resource's
	// first declared time range — what makes "today's rows" a question the
	// digest can ask (the rows whose range touches the day). Empty = none.
	RangeName, RangeStart, RangeEnd string
	// TitleField is what a row is CALLED in the agenda line: the single
	// required, default-less, non-auto text field, else a text field with a
	// title-like name; empty = "(sin título)".
	TitleField string
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
	if len(res.Ranges) > 0 {
		rnames := make([]string, 0, len(res.Ranges))
		for rn := range res.Ranges {
			rnames = append(rnames, rn)
		}
		sort.Strings(rnames)
		rg := res.Ranges[rnames[0]]
		p.RangeName, p.RangeStart, p.RangeEnd = rnames[0], rg.Start, rg.End
		p.TitleField = titleFieldOf(res, names)
	}
	return p
}

// titleFieldOf picks the field a row is called by: exactly one required,
// default-less, non-auto, enum-less text field; else the first text field
// with a title-like name; else "".
func titleFieldOf(res *schema.ResourceSchema, sorted []string) string {
	var req []string
	for _, f := range sorted {
		fd := res.Fields[f]
		if fd.Required && fd.Default == nil && !fd.Auto.Enabled() && len(fd.Enum) == 0 && (fd.Type == "string" || fd.Type == "text") {
			req = append(req, f)
		}
	}
	if len(req) == 1 {
		return req[0]
	}
	for _, want := range []string{"titulo", "title", "nombre", "name", "asunto", "subject", "texto", "descripcion", "description"} {
		if fd, ok := res.Fields[want]; ok && (fd.Type == "string" || fd.Type == "text") {
			return want
		}
	}
	return ""
}

// Slot is one row of a range resource that touches the report's day — the
// agenda line "10:00–11:00 dentista" (VOZ-16). Instants are UTC; the text
// formats them in the report's zone.
type Slot struct {
	Start, End time.Time
	Title      string
}

// SlotLine words a slot in the report's zone: "10:00–11:00 dentista"; a slot
// that started before the day or ends after it shows the day's edge as "…".
func SlotLine(sl Slot, dayStart time.Time) string {
	loc := dayStart.Location()
	st, en := sl.Start.In(loc), sl.End.In(loc)
	dayEnd := dayStart.Add(24 * time.Hour)
	a := st.Format("15:04")
	if st.Before(dayStart) {
		a = "…"
	}
	b := en.Format("15:04")
	if !en.Before(dayEnd) {
		b = "…"
	}
	title := strings.TrimSpace(sl.Title)
	if title == "" {
		title = "(sin título)"
	}
	return fmt.Sprintf("%s–%s %s", a, b, title)
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
	// TotalText, when set, is what the census row PRINTS instead of Total
	// (a grouped question answer reuses the census card for a money sum).
	TotalText string
	// The delta (VOZ-DELTA-S1, ADR-034): HasPrev = a baseline snapshot exists;
	// Prev = that day's attention by state (empty map = the resource had no
	// attention then); PrevTotal its total. NewToday = attention rows CREATED
	// today (the "3 llegaron hoy" that weigh differently from 16 that have
	// been waiting for months); HasNewToday = the resource could measure it.
	HasPrev     bool
	Prev        map[string]int64
	PrevTotal   int64
	NewToday    int64
	HasNewToday bool
	// Today (VOZ-16) lists the rows of a RANGE resource whose block touches
	// the report's day, ordered by start — "hoy: 10:00 dentista, 15:00
	// reunión". HasToday = the resource declares a range (an empty list then
	// means a free day, not an unknown one). DayStart is the day's first
	// instant in the report's zone (what the lines are formatted against).
	Today    []Slot
	HasToday bool
	DayStart time.Time
}

// Delta is the attention change since the baseline (meaningful only with HasPrev).
func (f Facts) Delta() int64 { return f.AttentionTotal - f.PrevTotal }

// novel reports whether this resource carries NEWS in its attention: it grew
// since the baseline, or rows arrived today. Without a baseline, only rows
// created today count as novelty.
func (f Facts) novel() bool {
	if f.AttentionTotal == 0 {
		return false
	}
	if f.HasNewToday && f.NewToday > 0 {
		return true
	}
	return f.HasPrev && f.Delta() > 0
}

// stale reports "the same as yesterday and nothing moved": the rows the
// picture folds into one small line instead of repeating the same red.
func (f Facts) stale() bool {
	return f.HasPrev && f.AttentionTotal > 0 && f.Delta() == 0 && !(f.HasNewToday && f.NewToday > 0) && !f.hasMotion()
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

// Level is the digest's traffic light. Since VOZ-DELTA-S1 it answers "is
// there NEWS to attend?", not "is there stock?": the same red every morning
// is what kills the habit.
const (
	LevelRed   = "red"   // declared attention with NOVELTY (grew since yesterday, or rows arrived today) — or, with no baseline yet, any declared attention
	LevelAmber = "amber" // attention exists but nothing new (the old stock), or only inferred attention
	LevelGreen = "green" // nothing to attend
)

// Policy is the schema's `summary` send policy (schema.SummaryConfig, with
// defaults applied): Notify "changes" (default) sends the scheduled digest only
// when something changed; "always" sends the daily report regardless.
// QuietDays (default 7; 0 = never) is the heartbeat: after that many
// consecutive silent scheduled runs one short message goes out so a quiet
// channel is distinguishable from a dead one.
type Policy struct {
	Notify    string
	QuietDays int
}

// DefaultPolicy is what an undeclared block means.
var DefaultPolicy = Policy{Notify: "changes", QuietDays: 7}

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
	// Subtitle overrides the card's second line ("Resumen del día" / "Estado
	// ahora") — a grouped question answer reuses the census card with its own.
	Subtitle string `json:"-"`
	// The delta (VOZ-DELTA-S1): Baseline is the day compared against ("" on the
	// first digest ever); Changed says whether anything WORTH A MESSAGE changed
	// since it (level, any attention count, rows that arrived today); the
	// reasons are plain words for the log.
	Baseline      string   `json:"baseline,omitempty"`
	Changed       bool     `json:"changed"`
	ChangeReasons []string `json:"change_reasons,omitempty"`
	// The scheduled decision (only when computed with ?mode=scheduled):
	// ShouldSend + SendReason ("always" | "changes" | "heartbeat" | "silent"),
	// SilentStreak = consecutive silent scheduled runs after this one.
	ShouldSend   bool   `json:"should_send"`
	SendReason   string `json:"send_reason,omitempty"`
	SilentStreak int    `json:"silent_streak"`
	// LastScheduled is a one-line account of the last scheduled evaluation
	// (the census view prints it so a quiet channel proves it is alive).
	LastScheduled string `json:"last_scheduled,omitempty"`
}

// Compose fills Text (Spanish, phone-first Telegram HTML), Level, Headline,
// Changed and HasMotion from ORDERED facts, compared against base (nil = the
// first digest ever, said with dignity — never an invented zero). Empty-with-
// dignity: a day with nothing to say is "Sin movimiento hoy", never a wall of
// zeros. Resources that are exactly as yesterday and did not move are folded
// into one closing line — the picture and the text stop repeating the same
// red every morning.
func Compose(appName, tenant, day string, facts []Facts, base *Snapshot) Report {
	r := Report{AppName: appName, Tenant: tenant, Day: day, Facts: facts}
	if base != nil {
		r.Baseline = base.Day
	}

	app := esc(appName)
	if app == "" {
		app = "tu app"
	}

	var declared, inferred, prevTotal, newToday int64
	var declaredNovel, inferredNovel bool
	for _, f := range facts {
		if f.AttentionInferred {
			inferred += f.AttentionTotal
			inferredNovel = inferredNovel || f.novel()
		} else {
			declared += f.AttentionTotal
			declaredNovel = declaredNovel || f.novel()
		}
		if f.HasPrev {
			prevTotal += f.PrevTotal
		}
		if f.HasNewToday {
			newToday += f.NewToday
		}
	}
	r.AttentionTotal = declared + inferred
	total := declared + inferred
	delta := total - prevTotal

	switch {
	case declared > 0 && (base == nil || declaredNovel):
		r.Level = LevelRed
		r.Headline = fmt.Sprintf("%d %s acción", declared, esperan(declared))
	case declared > 0:
		r.Level = LevelAmber
		r.Headline = fmt.Sprintf("%d %s acción", declared, esperan(declared))
	case inferred > 0:
		r.Level = LevelAmber
		r.Headline = fmt.Sprintf("%d sin avanzar (recién %s, nadie %s movió)", inferred, creados(inferred), losLo(inferred))
	default:
		r.Level = LevelGreen
		r.Headline = "Nada que atender"
	}
	// The comparison, in the headline — the part that makes it worth opening.
	switch {
	case base == nil && total > 0:
		r.Headline += " · primer resumen, sin comparación todavía"
	case base != nil && total == 0 && prevTotal > 0:
		r.Headline += fmt.Sprintf(" — ayer %s %d", esperabanN(prevTotal), prevTotal)
	case base != nil && total > 0:
		r.Headline += " · " + deltaWords(delta)
		if newToday > 0 {
			r.Headline += fmt.Sprintf(" · %d %s hoy", newToday, llegaron(newToday))
		}
	}

	// Changed: what deserves a message (the silence rule, ADR-034).
	if base == nil {
		r.Changed = true
		r.ChangeReasons = []string{"primer resumen"}
	} else {
		// A traffic-light change is news when it goes UP (more to attend) or
		// lands on GREEN (good news). red → amber is not: it only means the
		// novelty aged — the same stock, one day older — and reporting it
		// would be the "same red every morning" wearing a different colour.
		if r.Level != base.Level && (levelRank(r.Level) > levelRank(base.Level) || r.Level == LevelGreen) {
			r.ChangeReasons = append(r.ChangeReasons, "semáforo "+base.Level+" → "+r.Level)
		}
		for _, f := range facts {
			if !f.HasState {
				continue
			}
			switch {
			case f.HasPrev && f.Delta() != 0:
				r.ChangeReasons = append(r.ChangeReasons, fmt.Sprintf("%s %s", f.Resource, deltaWords(f.Delta())))
			case f.HasPrev && f.AttentionTotal > 0 && !sameStates(f.Attention, f.Prev):
				r.ChangeReasons = append(r.ChangeReasons, f.Resource+" cambió de estado")
			case f.HasNewToday && f.NewToday > 0:
				r.ChangeReasons = append(r.ChangeReasons, fmt.Sprintf("%s: %d %s hoy", f.Resource, f.NewToday, llegaron(f.NewToday)))
			}
		}
		r.Changed = len(r.ChangeReasons) > 0
	}

	var b strings.Builder
	fmt.Fprintf(&b, "📋 <b>Resumen de %s</b> · %s\n", app, esc(day))
	fmt.Fprintf(&b, "%s <b>%s</b>\n", levelDot(r.Level), esc(r.Headline))

	// Today's agenda (VOZ-16): the rows whose block touches the day, with
	// their hour — the first thing an agenda's morning digest must say. It
	// is news every day it is non-empty (the send policy counts it).
	agendaResources := 0
	for _, f := range facts {
		if f.HasToday && len(f.Today) > 0 {
			agendaResources++
		}
	}
	for _, f := range facts {
		if !f.HasToday || len(f.Today) == 0 {
			continue
		}
		label := "Hoy en agenda"
		if agendaResources > 1 {
			label = "Hoy · " + f.Resource
		}
		fmt.Fprintf(&b, "\n📅 <b>%s</b> (%d)\n", esc(label), len(f.Today))
		for i, sl := range f.Today {
			if i == maxSlots {
				fmt.Fprintf(&b, "  +%d más\n", len(f.Today)-maxSlots)
				break
			}
			fmt.Fprintf(&b, "• %s\n", esc(SlotLine(sl, f.DayStart)))
		}
		r.HasMotion = true
		if base != nil {
			r.ChangeReasons = append(r.ChangeReasons, fmt.Sprintf("%s: %d hoy en agenda", f.Resource, len(f.Today)))
			r.Changed = true
		}
	}

	blocks := 0
	var folded, stale []string
	for _, f := range facts {
		if f.stale() {
			stale = append(stale, fmt.Sprintf("%s (%s)", esc(f.Resource), stateDetail(f.Attention)))
			continue
		}
		var parts []string
		if f.AttentionTotal > 0 {
			parts = append(parts, attentionPhrase(f, base != nil))
		} else if f.HasPrev && f.PrevTotal > 0 {
			parts = append(parts, fmt.Sprintf("✅ ya no espera nada (ayer %s %d)", esperabanN(f.PrevTotal), f.PrevTotal))
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

	if blocks == 0 && len(stale) == 0 && !r.HasMotion {
		b.WriteString("\nSin movimiento hoy.\n")
	}
	if blocks > 0 || len(stale) > 0 {
		r.HasMotion = true
	}
	if len(stale) > 0 {
		b.WriteString("\n⏸ Igual que ayer: " + strings.Join(stale, " · ") + "\n")
	}
	if len(folded) > 0 {
		b.WriteString("\n▫️ Sin novedad hoy: " + strings.Join(folded, " · ") + "\n")
	}
	r.Text = strings.TrimRight(b.String(), "\n")
	return r
}

// Decide applies the send policy to a scheduled run: always → send; changes →
// send iff Changed; otherwise stay silent, except the heartbeat after
// QuietDays consecutive silent runs (prevStreak is the baseline's streak).
// The heartbeat line is appended to Text so the reader knows the silence was
// a choice, not a crash.
func Decide(r *Report, p Policy, prevStreak int) {
	if p.Notify == "" {
		p.Notify = DefaultPolicy.Notify
	}
	switch {
	case p.Notify == "always":
		r.ShouldSend, r.SendReason, r.SilentStreak = true, "always", 0
	case r.Changed:
		r.ShouldSend, r.SendReason, r.SilentStreak = true, "changes", 0
	default:
		r.SilentStreak = prevStreak + 1
		if p.QuietDays > 0 && r.SilentStreak >= p.QuietDays {
			r.ShouldSend, r.SendReason = true, "heartbeat"
			r.Text += fmt.Sprintf("\n\n🔕 %d %s sin novedad. Sigo acá — todo igual que la última vez.", r.SilentStreak, dias(r.SilentStreak))
			r.SilentStreak = 0
		} else {
			r.ShouldSend, r.SendReason = false, "silent"
		}
	}
}

// ScheduledLine words the last scheduled evaluation for the census view.
func ScheduledLine(s *Snapshot) string {
	if s == nil || s.ScheduledAt == nil {
		return "⏰ Parte automático: nunca corrió todavía (¿corre el worker? ¿está el workflow?)."
	}
	when := s.ScheduledAt.Format("2006-01-02 15:04")
	switch {
	case strings.HasPrefix(s.Decision, "sent"):
		return fmt.Sprintf("⏰ Último parte automático: %s — enviado (%s).", when, strings.TrimPrefix(s.Decision, "sent:"))
	case s.SilentStreak > 0:
		return fmt.Sprintf("⏰ Último parte automático: %s — callado a propósito, %d %s sin novedad.", when, s.SilentStreak, dias(s.SilentStreak))
	default:
		return fmt.Sprintf("⏰ Último parte automático: %s — callado, nada cambió.", when)
	}
}

func levelRank(l string) int {
	switch l {
	case LevelRed:
		return 2
	case LevelAmber:
		return 1
	default:
		return 0
	}
}

func sameStates(a, b map[string]int64) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

func deltaWords(d int64) string {
	switch {
	case d > 0:
		return fmt.Sprintf("+%d desde ayer", d)
	case d < 0:
		return fmt.Sprintf("−%d desde ayer", -d)
	default:
		return "igual que ayer"
	}
}

func llegaron(n int64) string {
	if n == 1 {
		return "llegó"
	}
	return "llegaron"
}

func esperabanN(n int64) string {
	if n == 1 {
		return "esperaba"
	}
	return "esperaban"
}

func dias(n int) string {
	if n == 1 {
		return "día"
	}
	return "días"
}

// attentionPhrase renders the attention line in the schema's own words, with
// the delta when a baseline exists:
// declared → "⏳ 16 esperan acción (+3 desde ayer · 3 llegaron hoy) (pendiente: 16)";
// inferred → "🕐 2 sin avanzar (igual que ayer) (creada: 2)".
func attentionPhrase(f Facts, compared bool) string {
	detail := stateDetail(f.Attention)
	var cmp []string
	if compared {
		if f.HasPrev && f.PrevTotal == 0 && len(f.Prev) == 0 {
			cmp = append(cmp, "nuevo desde ayer")
		} else if f.HasPrev {
			cmp = append(cmp, deltaWords(f.Delta()))
		}
	}
	if f.HasNewToday && f.NewToday > 0 {
		cmp = append(cmp, fmt.Sprintf("%d %s hoy", f.NewToday, llegaron(f.NewToday)))
	}
	suffix := ""
	if len(cmp) > 0 {
		suffix = " (" + strings.Join(cmp, " · ") + ")"
	}
	if f.AttentionInferred {
		return fmt.Sprintf("🕐 %d sin avanzar%s (%s)", f.AttentionTotal, suffix, detail)
	}
	return fmt.Sprintf("⏳ %d %s acción%s (%s)", f.AttentionTotal, esperan(f.AttentionTotal), suffix, detail)
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
func ComposeCensus(appName, tenant string, facts []Facts, last *Snapshot) Report {
	rep := Report{AppName: appName, Tenant: tenant, Facts: facts, Census: true, Level: LevelGreen, LastScheduled: ScheduledLine(last)}
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
		rep.Text = "📊 <b>Estado de " + app + "</b>\n\nNo hay datos todavía.\n\n" + rep.LastScheduled
		rep.Headline = "No hay datos todavía"
		return rep
	}
	rep.HasMotion = true
	rep.Headline = "Cuántos hay de cada cosa"
	b.WriteString("\n" + rep.LastScheduled + "\n")
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
