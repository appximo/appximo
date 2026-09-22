package ask

// The agenda vocabulary of the voice channel (MOTOR-AGENDA-S1, ADR-039):
// what a declared time RANGE adds to questions and to writes.
//
//   - A question about a range resource with a period asks what is SCHEDULED
//     then («qué tengo mañana» → overlaps [tomorrow)), and «a las 4» asks
//     what CONTAINS that instant. «cuándo estoy libre el jueves» lists the
//     gaps between the day's blocks (kind free).
//   - A write «de 4 a 5» fills BOTH bounds; «a las 10» fills the start and the
//     engine adds the range's default duration; the confirmation runs the
//     conflict check the API exposes and SAYS the collision before the owner
//     confirms — the constraint in the database remains the net.
//   - When the owner confirms anyway, the rule's `when` decides: an
//     invertible condition (eq on a bool: ocupa = true) is flipped so the
//     row is saved as NOT blocking and the confirmation says so in words;
//     a rule that cannot be inverted (estado ≠ cancelada) cannot be
//     satisfied by the write, so the owner is asked for another time.
//
// Clock reading: a bare hour 1–6 is the afternoon («a las 4» = 16:00), 7–12
// the morning/noon, «de la mañana / am» keeps it, «de la tarde / pm / de la
// noche» adds twelve — the reading a Spanish speaker means by default, and
// the confirmation always prints the resolved hour so a wrong guess is
// caught before anything is written.

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Range is a declared time range as the voice sees it.
type Range struct {
	Name       string
	Start, End string
	NoOverlap  bool
	Default    time.Duration
	// When is the rule's partial condition (nil = every scheduled row blocks).
	WhenField string
	WhenOp    string
	WhenVal   any
	// Scope columns of the rule (compared with =).
	Scope []string
}

// Range returns the resource's first declared range (the agenda one), nil
// when it declares none.
func (r *Resource) Range() *Range {
	if len(r.Ranges) == 0 {
		return nil
	}
	return &r.Ranges[0]
}

// RangeNamed looks a range up by name.
func (r *Resource) RangeNamed(name string) *Range {
	for i := range r.Ranges {
		if r.Ranges[i].Name == name {
			return &r.Ranges[i]
		}
	}
	return nil
}

// PeriodTarget is what a bare period applies to: the range (what is
// scheduled then) when the resource declares one, else the creation stamp.
func (r *Resource) PeriodTarget() (field string, rg *Range) {
	if rg := r.Range(); rg != nil {
		return rg.Name, rg
	}
	return r.DefaultTimeField(), nil
}

// ConflictChecker answers the conflicts a window would have on a range —
// the engine's GET /api/{resource}/conflicts, scoped by the asking role.
type ConflictChecker interface {
	Conflicts(ctx context.Context, resource, rangeName, start, end string, scope map[string]string, excludeID string) ([]map[string]any, error)
}

// ── clocks ────────────────────────────────────────────────────────────────

var hourRe = regexp.MustCompile(`^(\d{1,2})(?::(\d{2}))?$`)

// readClock turns an hour said in Spanish into minutes since midnight:
// "4" → 16:00, "10" → 10:00, "4 y media"/"4:30" → 16:30, "16" → 16:00;
// qualifier: "" | "am" | "pm" (de la tarde/noche). ok=false on nonsense.
func readClock(h string, half bool, qualifier string) (int, bool) {
	m := hourRe.FindStringSubmatch(strings.TrimSpace(h))
	if m == nil {
		return 0, false
	}
	hour, _ := strconv.Atoi(m[1])
	minute := 0
	if m[2] != "" {
		minute, _ = strconv.Atoi(m[2])
	}
	if half {
		minute = 30
	}
	if hour > 23 || minute > 59 {
		return 0, false
	}
	switch qualifier {
	case "pm":
		if hour >= 1 && hour <= 11 {
			hour += 12
		}
	case "am":
	default:
		if hour >= 1 && hour <= 6 {
			hour += 12 // «a las 4» is the afternoon
		}
	}
	return hour*60 + minute, true
}

func clockString(minutes int) string {
	return fmt.Sprintf("%02d:%02d", minutes/60, minutes%60)
}

// ── the deterministic schedule parser ─────────────────────────────────────

// scheduleVerbs start a create on the agenda resource.
var scheduleVerbs = set("agenda", "agendar", "agendame", "agendá", "anota", "anotar", "anotame", "anotá", "programa", "programar", "programame", "programá", "reserva", "reservar", "reservame", "reservá", "pone", "poneme", "pon", "agrega", "agregame", "crea", "creame")

// dayPhrases map a day said in Spanish to a write time token (day part).
var dayPhrases = []struct{ phrase, token string }{
	{"pasado manana", "day_after_tomorrow"}, {"manana", "tomorrow"}, {"hoy", "today"},
	{"el lunes", "next_monday"}, {"el martes", "next_tuesday"}, {"el miercoles", "next_wednesday"}, {"el jueves", "next_thursday"}, {"el viernes", "next_friday"}, {"el sabado", "next_saturday"}, {"el domingo", "next_sunday"},
	{"lunes", "next_monday"}, {"martes", "next_tuesday"}, {"miercoles", "next_wednesday"}, {"jueves", "next_thursday"}, {"viernes", "next_friday"}, {"sabado", "next_saturday"}, {"domingo", "next_sunday"},
}

// timeSpan is a parsed «de 4 a 5» / «a las 10 [por dos horas]».
type timeSpan struct {
	start, end int // minutes since midnight; end < 0 = unsaid
}

// consumeTimeSpan finds and consumes the clock phrase of the sentence. It
// recognizes, in order: «de H[:MM] a H[:MM]», «desde las H hasta las H»,
// «a las H[:MM] [y media] [de la tarde|de la mañana|am|pm] [por N hora(s)|por media hora|hasta las H]».
func consumeTimeSpan(toks []token) (timeSpan, bool) {
	ts := timeSpan{end: -1}
	n := len(toks)
	isHour := func(i int) bool { return i < n && !toks[i].used && hourRe.MatchString(toks[i].norm) }
	// The tokenizer splits «15:30» into «15» «30»: an hour followed by a
	// two-digit minute token is read as one clock.
	hourText := func(i int) (string, int) {
		if i+1 < n && !toks[i+1].used && len(toks[i+1].norm) == 2 && toks[i+1].norm[0] >= '0' && toks[i+1].norm[0] <= '5' && toks[i+1].norm[1] >= '0' && toks[i+1].norm[1] <= '9' && !strings.Contains(toks[i].norm, ":") {
			return toks[i].norm + ":" + toks[i+1].norm, 2
		}
		return toks[i].norm, 1
	}
	qualifierAt := func(i int) (string, int) { // returns qualifier and tokens consumed
		if i < n && !toks[i].used {
			switch toks[i].norm {
			case "am", "pm":
				return toks[i].norm, 1
			}
			if i+2 < n && toks[i].norm == "de" && toks[i+1].norm == "la" {
				switch toks[i+2].norm {
				case "tarde", "noche":
					return "pm", 3
				case "manana":
					return "am", 3
				}
			}
		}
		return "", 0
	}
	halfAt := func(i int) int { // «y media», «y cuarto» → tokens consumed (only «y media» changes minutes)
		if i+1 < n && toks[i].norm == "y" && toks[i+1].norm == "media" {
			return 2
		}
		return 0
	}
	// «de H a H» / «desde las H hasta las H»
	for i := 0; i+2 < n; i++ {
		if toks[i].used || (toks[i].norm != "de" && toks[i].norm != "desde") {
			continue
		}
		j := i + 1
		if toks[j].norm == "las" || toks[j].norm == "la" {
			j++
		}
		if !isHour(j) {
			continue
		}
		jText, jn := hourText(j)
		k := j + jn
		hs := halfAt(k)
		k += hs
		q1, qn := qualifierAt(k)
		k += qn
		if k >= n || (toks[k].norm != "a" && toks[k].norm != "hasta") {
			continue
		}
		l := k + 1
		if l < n && (toks[l].norm == "las" || toks[l].norm == "la") {
			l++
		}
		if !isHour(l) {
			continue
		}
		lText, ln := hourText(l)
		m := l + ln
		he := halfAt(m)
		m += he
		q2, qn2 := qualifierAt(m)
		m += qn2
		if q1 == "" {
			q1 = q2
		}
		st, ok1 := readClock(jText, hs > 0, q1)
		en, ok2 := readClock(lText, he > 0, q2)
		if !ok1 || !ok2 {
			continue
		}
		if en <= st { // «de 11 a 1» → the 1 is the afternoon
			if en2, ok := readClock(lText, he > 0, "pm"); ok && en2 > st {
				en = en2
			}
		}
		if en <= st {
			return ts, false
		}
		for x := i; x < m; x++ {
			toks[x].used = true
		}
		ts.start, ts.end = st, en
		return ts, true
	}
	// «a las H …»
	for i := 0; i+1 < n; i++ {
		if toks[i].used || toks[i].norm != "a" {
			continue
		}
		j := i + 1
		if toks[j].norm == "las" || toks[j].norm == "la" {
			j++
		}
		if !isHour(j) {
			continue
		}
		jText, jn := hourText(j)
		k := j + jn
		hs := halfAt(k)
		k += hs
		q, qn := qualifierAt(k)
		k += qn
		st, ok := readClock(jText, hs > 0, q)
		if !ok {
			return ts, false
		}
		for x := i; x < k; x++ {
			toks[x].used = true
		}
		ts.start = st
		// «por N hora(s)» / «por media hora» / «hasta las H»
		if k+1 < n && toks[k].norm == "por" {
			switch {
			case k+2 < n && toks[k+1].norm == "media" && toks[k+2].norm == "hora":
				ts.end = st + 30
				toks[k].used, toks[k+1].used, toks[k+2].used = true, true, true
			case k+2 < n && (toks[k+2].norm == "hora" || toks[k+2].norm == "horas"):
				if d, ok := spanishNumber(toks[k+1].norm); ok {
					ts.end = st + d*60
					toks[k].used, toks[k+1].used, toks[k+2].used = true, true, true
				}
			}
		} else if k+1 < n && toks[k].norm == "hasta" {
			l := k + 1
			if l < n && (toks[l].norm == "las" || toks[l].norm == "la") {
				l++
			}
			if isHour(l) {
				lText, ln := hourText(l)
				m := l + ln
				he := halfAt(m)
				m += he
				q2, qn2 := qualifierAt(m)
				m += qn2
				if en, ok := readClock(lText, he > 0, q2); ok && en > st {
					ts.end = en
					for x := k; x < m; x++ {
						toks[x].used = true
					}
				}
			}
		}
		return ts, true
	}
	return ts, false
}

func spanishNumber(w string) (int, bool) {
	switch w {
	case "una", "un", "1":
		return 1, true
	case "dos", "2":
		return 2, true
	case "tres", "3":
		return 3, true
	case "cuatro", "4":
		return 4, true
	}
	if n, err := strconv.Atoi(w); err == nil && n > 0 && n < 24 {
		return n, true
	}
	return 0, false
}

// consumeDay consumes a day phrase («mañana», «el viernes», «pasado mañana»).
func consumeDay(toks []token) string {
	joined := joinedNorms(toks)
	for _, dp := range dayPhrases {
		if strings.Contains(joined, " "+dp.phrase+" ") && consumePhrase(toks, dp.phrase) {
			return dp.token
		}
	}
	return ""
}

// parseSchedule settles «agendá reunión con Fabián mañana de 4 a 5» without
// the model: a schedule verb, ONE creatable resource with a range (named or
// implied), a clock span, a day (default today), the title = the words left,
// and a «con/para Name» → the resource's single relation to a people-like
// resource. Anything else stays the model's.
func parseSchedule(question string, v *Vocabulary) ParseResult {
	toks := tokenize(question)
	verbIdx := -1
	for i, t := range toks {
		if scheduleVerbs[t.norm] {
			verbIdx = i
			break
		}
	}
	if verbIdx < 0 {
		return ParseResult{Reason: "no schedule verb"}
	}
	toks[verbIdx].used = true
	// The resource: named, else the only creatable resource with a range.
	var res *Resource
	namedBy := ""
	for i, t := range toks {
		if t.used {
			continue
		}
		for _, name := range v.order {
			r := v.resources[name]
			if !r.CanCreate || r.Range() == nil {
				continue
			}
			if v.namesResource(t.norm, r) {
				res = r
				toks[i].used = true
				namedBy = t.raw
				break
			}
		}
		if res != nil {
			break
		}
	}
	if res == nil {
		// The agenda among the creatable range resources (no_overlap first).
		res = pickAgenda(v, func(r *Resource) bool { return r.CanCreate })
		if res == nil {
			return ParseResult{Reason: "schedule: no single agenda resource"}
		}
	}
	rg := res.Range()
	span, ok := consumeTimeSpan(toks)
	if !ok {
		return ParseResult{Reason: "schedule: no clock"}
	}
	day := consumeDay(toks)
	if day == "" {
		day = "today"
	}
	// «con Fabián» / «para Marta»: the single relation field of the resource.
	data := map[string]any{}
	var relField *Field
	for _, f := range res.Fields {
		if f.Relation != "" {
			if relField != nil {
				relField = nil // two relations: the model decides
				break
			}
			relField = f
		}
	}
	if relField != nil {
		for i := 0; i+1 < len(toks); i++ {
			if toks[i].used || (toks[i].norm != "con" && toks[i].norm != "para") || !startsUpper(toks[i+1].raw) {
				continue
			}
			parts := nameRun(toks, i+1, nil)
			if len(parts) > 0 {
				toks[i].used = true
				data[relField.Name] = map[string]any{"match": strings.Join(parts, " ")}
				break
			}
		}
	}
	// The title: every word left that is not a stopword — kept as said. It
	// goes to the resource's REQUIRED label field (exactly one), else the
	// model decides.
	var title []string
	for _, t := range toks {
		if t.used {
			continue
		}
		title = append(title, t.raw)
	}
	for len(title) > 0 && stopwords[normalize(title[0])] {
		title = title[1:]
	}
	labels := res.LabelFields()
	var titleField *Field
	for _, f := range res.Fields {
		if f.Required && !f.HasDefault && !f.Auto && f.IsText() && len(f.Enum) == 0 {
			if titleField != nil {
				return ParseResult{Reason: "schedule: two required text fields"}
			}
			titleField = f
		}
	}
	if titleField == nil && len(labels) > 0 {
		titleField = res.Field(labels[0])
	}
	if titleField == nil {
		return ParseResult{Reason: "schedule: no title field"}
	}
	if len(title) == 0 && namedBy != "" {
		// «agendá reunión de 4 a 5»: the word that named the resource is
		// also what the block is called.
		title = []string{namedBy}
	}
	if len(title) == 0 {
		return ParseResult{Reason: "schedule: no title"}
	}
	data[titleField.Name] = strings.Join(title, " ")
	data[rg.Start] = day + " " + clockString(span.start)
	if span.end >= 0 {
		data[rg.End] = day + " " + clockString(span.end)
	}
	p := Plan{Kind: "create", Resource: res.Name, Data: data}
	if err := p.Validate(v); err != nil {
		return ParseResult{Reason: "schedule invalid: " + err.Error()}
	}
	return ParseResult{Plan: p, Sure: true}
}

// namesResource reports whether a normalized token names r (its own forms
// or a declared alias).
func (v *Vocabulary) namesResource(norm string, r *Resource) bool {
	for _, f := range r.NameForms() {
		if f == norm {
			return true
		}
	}
	return false
}

// ── writes: filling the end, checking the collision ──────────────────────

// fillRangeEnd completes a create whose start is given without an end: end
// = start + the range's default duration, labeled for the confirmation.
func fillRangeEnd(res *Resource, pend *Pending, loc *time.Location) {
	rg := res.Range()
	if rg == nil || pend.Kind != "create" {
		return
	}
	st, ok := pend.Data[rg.Start].(string)
	if !ok {
		return
	}
	if _, has := pend.Data[rg.End]; has {
		return
	}
	t, err := time.Parse(time.RFC3339, st)
	if err != nil {
		return
	}
	end := t.Add(rg.Default)
	pend.Data[rg.End] = end.UTC().Format(time.RFC3339)
	pend.Labels[rg.End] = end.In(loc).Format("15:04") + " (" + durationWords(rg.Default) + " por defecto)"
}

func durationWords(d time.Duration) string {
	switch {
	case d%time.Hour == 0:
		h := int(d / time.Hour)
		if h == 1 {
			return "una hora"
		}
		return fmt.Sprintf("%d horas", h)
	case d == 30*time.Minute:
		return "media hora"
	}
	return fmt.Sprintf("%d min", int(d/time.Minute))
}

// rangeBounds reads the pending's start/end as RFC 3339 (both present).
func rangeBounds(rg *Range, pend *Pending) (start, end string, ok bool) {
	s, ok1 := pend.Data[rg.Start].(string)
	e, ok2 := pend.Data[rg.End].(string)
	return s, e, ok1 && ok2
}

// checkConflicts runs the collision check for a pending write on a range
// with a no-overlap rule and, when something collides, rewrites the
// confirmation: names the row(s), says what a yes will do. Returns the
// Result to send and true when the normal confirmation must NOT be shown.
func checkConflicts(ctx context.Context, d Deps, pend *Pending) (Result, bool) {
	res := d.Vocab.Resource(pend.Resource)
	rg := res.Range()
	if rg == nil || !rg.NoOverlap || d.Conflicts == nil {
		return Result{}, false
	}
	start, end, ok := rangeBounds(rg, pend)
	if !ok {
		return Result{}, false
	}
	// A row the owner marks as not blocking never collides.
	if rg.WhenField != "" {
		if v, has := pend.Data[rg.WhenField]; has && !whenHolds(rg, v) {
			return Result{}, false
		}
	}
	scope := map[string]string{}
	for _, c := range rg.Scope {
		if v, has := pend.Data[c]; has && v != nil {
			scope[c] = fmt.Sprint(v)
		}
	}
	rows, err := d.Conflicts.Conflicts(ctx, pend.Resource, rg.Name, start, end, scope, pend.RowID)
	if err != nil {
		return Result{}, false // the constraint still decides at write time
	}
	if len(rows) == 0 {
		delete(pend.Labels, "__conflict")
		return Result{}, false
	}
	loc := d.Now.Location()
	var names []string
	for i, row := range rows {
		if i == 3 {
			names = append(names, fmt.Sprintf("y %d más", len(rows)-3))
			break
		}
		label := labelOf(row, res.LabelFields())
		if label == "" {
			label = singular(pend.Resource)
		}
		names = append(names, fmt.Sprintf("«%s» %s", esc(label), esc(spanWords(row[rg.Start], row[rg.End], loc))))
	}
	warn := "⚠️ Ya tenés " + strings.Join(names, ", ") + "."
	pend.Labels["__conflict"] = warn
	// What a yes does: flip an invertible rule (ocupa: no) or ask for another time.
	if flipVal, can := rg.nonBlockingValue(); can {
		pend.Data[rg.WhenField] = flipVal
		pend.Labels[rg.WhenField] = fmt.Sprintf("%s (no bloquea el horario: ya había algo)", formatValue(res.Field(rg.WhenField), flipVal, loc))
		pend.Stage = "confirm"
		d.Pending.Put(pend)
		text := warn + "\n\n" + confirmationText(d, pend)
		r := pendingResult(pend, "confirm", "Ya tenés algo a esa hora. ¿Igual lo agendo?", text)
		r.Speech = Speech(text)
		return r, true
	}
	// Not invertible: the agenda cannot take it. Ask for another time.
	pend.Stage, pend.Field, pend.Asked = "field", rg.Start, pend.Asked+1
	delete(pend.Data, rg.End)
	d.Pending.Put(pend)
	text := warn + " La agenda no permite encimar. ¿A qué hora lo paso? (hoy a las 3, mañana de 4 a 5… o <b>no</b> para cancelar)"
	return pendingResult(pend, "conflict", "Ya tenés algo a esa hora", text), true
}

// nonBlockingValue is the value that takes a row OUT of the rule: only an
// `eq` on a bool field is invertible (ocupa = true → false).
func (rg *Range) nonBlockingValue() (any, bool) {
	if rg.WhenField == "" || (rg.WhenOp != "" && rg.WhenOp != "eq") {
		return nil, false
	}
	if b, ok := rg.WhenVal.(bool); ok {
		return !b, true
	}
	return nil, false
}

func whenHolds(rg *Range, v any) bool {
	eq := fmt.Sprint(v) == fmt.Sprint(rg.WhenVal)
	if rg.WhenOp == "ne" {
		return !eq
	}
	return eq
}

// spanWords prints "de 16:00 a 17:00" (adding the day when it is not today).
func spanWords(start, end any, loc *time.Location) string {
	st, ok1 := asTime(start)
	en, ok2 := asTime(end)
	if !ok1 || !ok2 {
		return ""
	}
	st, en = st.In(loc), en.In(loc)
	return "de " + st.Format("15:04") + " a " + en.Format("15:04")
}

func asTime(v any) (time.Time, bool) {
	switch x := v.(type) {
	case time.Time:
		return x, true
	case string:
		if t, err := time.Parse(time.RFC3339Nano, x); err == nil {
			return t, true
		}
		if t, err := time.Parse(time.RFC3339, x); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

// ── reads: the agenda list and the free gaps ─────────────────────────────

// composeAgenda words a list of a range resource as an agenda: one line per
// block, "16:00–17:00 reunión con Fabián", in start order.
func composeAgenda(d Deps, p Plan, res *Resource, rg *Range, rows []map[string]any, total int64, understood string) Result {
	loc := d.Now.Location()
	out := Result{Kind: "answer", Understood: understood}
	n := float64(total)
	out.Number = &n
	var b strings.Builder
	if total == 0 {
		out.Headline = "Nada agendado"
		fmt.Fprintf(&b, "📅 <b>Nada</b> agendado %s.", esc(understood))
		out.Text = b.String()
		return out
	}
	out.Headline = fmt.Sprintf("%d %s", total, phrase(res.Name, n))
	fmt.Fprintf(&b, "📅 <b>%d</b> %s:\n", total, esc(phrase(res.Name, n)))
	labels := res.LabelFields()
	lastDay := ""
	for _, row := range rows {
		st, ok1 := asTime(row[rg.Start])
		en, ok2 := asTime(row[rg.End])
		label := labelOf(row, firstN(labels, 2))
		if label == "" {
			label = fmt.Sprint(row["id"])
		}
		if ok1 && ok2 {
			day := dateWords(st.In(loc))
			if day != lastDay {
				fmt.Fprintf(&b, "<i>%s</i>\n", esc(day))
				lastDay = day
			}
			fmt.Fprintf(&b, "• %s–%s %s\n", st.In(loc).Format("15:04"), en.In(loc).Format("15:04"), esc(label))
		} else {
			fmt.Fprintf(&b, "• %s (sin horario)\n", esc(label))
		}
	}
	if int64(len(rows)) < total {
		fmt.Fprintf(&b, "… y %d más\n", total-int64(len(rows)))
	}
	fmt.Fprintf(&b, "\n<i>%s</i>", esc(understood))
	out.Text = strings.TrimRight(b.String(), "\n")
	return out
}

// composeFree words the gaps of a window: the complement of the blocks that
// overlap it, clipped to the window.
func composeFree(d Deps, res *Resource, rg *Range, rows []map[string]any, w Window, understood string) Result {
	loc := d.Now.Location()
	type span struct{ s, e time.Time }
	var busy []span
	for _, row := range rows {
		st, ok1 := asTime(row[rg.Start])
		en, ok2 := asTime(row[rg.End])
		if !ok1 || !ok2 {
			continue
		}
		if st.Before(w.From) {
			st = w.From
		}
		if en.After(w.To) {
			en = w.To
		}
		if en.After(st) {
			busy = append(busy, span{st, en})
		}
	}
	sort.Slice(busy, func(i, j int) bool { return busy[i].s.Before(busy[j].s) })
	var free []span
	cursor := w.From
	for _, bz := range busy {
		if bz.s.After(cursor) {
			free = append(free, span{cursor, bz.s})
		}
		if bz.e.After(cursor) {
			cursor = bz.e
		}
	}
	if w.To.After(cursor) {
		free = append(free, span{cursor, w.To})
	}
	out := Result{Kind: "answer", Understood: understood}
	var b strings.Builder
	if len(busy) == 0 {
		out.Headline = "Libre todo el día"
		fmt.Fprintf(&b, "🟢 <b>Libre</b> %s: no tenés nada agendado.", esc(w.Words))
		out.Text = b.String()
		return out
	}
	out.Headline = fmt.Sprintf("%d hueco(s) libre(s)", len(free))
	fmt.Fprintf(&b, "🟢 Libre %s:\n", esc(w.Words))
	for _, f := range free {
		fmt.Fprintf(&b, "• %s–%s\n", f.s.In(loc).Format("15:04"), f.e.In(loc).Format("15:04"))
	}
	fmt.Fprintf(&b, "Ocupado: %d bloque(s).", len(busy))
	fmt.Fprintf(&b, "\n\n<i>%s</i>", esc(understood))
	out.Text = strings.TrimRight(b.String(), "\n")
	return out
}
