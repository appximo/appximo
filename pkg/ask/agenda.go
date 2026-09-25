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

// gluedRe is an hour with its half of the day glued («7am», «2pm») — what a
// dictation writes for «siete de la mañana».
var gluedRe = regexp.MustCompile(`^(\d{1,2})(am|pm)$`)

// readClock turns an hour said in Spanish into minutes since midnight:
// "4" → 16:00, "10" → 10:00, "4 y media"/"4:30" → 16:30, "16" → 16:00;
// qualifier: "" | "am" | "pm" (de la tarde/noche). ok=false on nonsense.
// readClock reads «H», «H:MM» or «H» + extra minutes («y media» 30, «y
// cuarto» 15, «menos cuarto» −15 → the previous hour at 45) under a half-of-
// the-day qualifier ("am", "pm" or "" = the bare rule).
func readClock(h string, extra int, qualifier string) (int, bool) {
	m := hourRe.FindStringSubmatch(strings.TrimSpace(h))
	if m == nil {
		return 0, false
	}
	hour, _ := strconv.Atoi(m[1])
	minute := 0
	if m[2] != "" {
		minute, _ = strconv.Atoi(m[2])
	}
	switch {
	case extra > 0 && m[2] == "":
		minute = extra
	case extra < 0 && m[2] == "":
		if hour == 0 {
			hour = 24
		}
		hour--
		minute = 60 + extra
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
var scheduleVerbs = set("agenda", "agendar", "agendame", "agendá", "anota", "anotar", "anotame", "anotá", "programa", "programar", "programame", "programá", "reserva", "reservar", "reservame", "reservá", "pone", "poneme", "pon", "agrega", "agregame", "crea", "creame",
	"bloquea", "bloquear", "bloqueame", "aparta", "apartar", "apartame") // «bloqueá mañana de 2 a 4 para estudiar», «apartá el jueves de 10 a 12»

// dayPhrases map a day said in Spanish to a write time token (day part).
// dayPhrases are the days a person names (today, tomorrow, yesterday, a
// weekday) and the PARTS of a day («esta mañana», «en la tarde», «toda la
// mañana», «anoche») — a part sets the day AND lends its half («am»/«pm») to
// a clock said without one («esta mañana de 6 a 7» is 06:00–07:00, never
// the afternoon of the bare rule). «de la tarde» right after an hour is the
// clock's own qualifier and is not here. Longest phrase first (init sort),
// so «esta mañana» is read before «mañana» would make it tomorrow.
var dayPhrases = []struct{ phrase, token, hint string }{
	{"pasado manana", "day_after_tomorrow", ""}, {"el dia de hoy", "today", ""}, {"dia de hoy", "today", ""}, {"manana", "tomorrow", ""}, {"hoy", "today", ""},
	{"ayer", "yesterday", ""}, {"antier", "day_before_yesterday", ""}, {"anteayer", "day_before_yesterday", ""}, {"anoche", "yesterday", "pm"},
	{"esta manana", "today", "am"}, {"esta tarde", "today", "pm"}, {"esta noche", "today", "pm"}, {"hoy temprano", "today", "am"},
	{"hoy en la manana", "today", "am"}, {"hoy por la manana", "today", "am"}, {"hoy en la tarde", "today", "pm"}, {"hoy por la tarde", "today", "pm"}, {"hoy en la noche", "today", "pm"}, {"hoy por la noche", "today", "pm"},
	{"ayer en la manana", "yesterday", "am"}, {"ayer por la manana", "yesterday", "am"}, {"ayer en la tarde", "yesterday", "pm"}, {"ayer por la tarde", "yesterday", "pm"}, {"ayer en la noche", "yesterday", "pm"}, {"ayer por la noche", "yesterday", "pm"},
	{"en la manana", "today", "am"}, {"por la manana", "today", "am"}, {"toda la manana", "today", "am"}, {"en la tarde", "today", "pm"}, {"por la tarde", "today", "pm"}, {"toda la tarde", "today", "pm"}, {"en la noche", "today", "pm"}, {"por la noche", "today", "pm"}, {"toda la noche", "today", "pm"},
	{"el lunes", "next_monday", ""}, {"el martes", "next_tuesday", ""}, {"el miercoles", "next_wednesday", ""}, {"el jueves", "next_thursday", ""}, {"el viernes", "next_friday", ""}, {"el sabado", "next_saturday", ""}, {"el domingo", "next_sunday", ""},
	{"el lunes pasado", "last_monday", ""}, {"el martes pasado", "last_tuesday", ""}, {"el miercoles pasado", "last_wednesday", ""}, {"el jueves pasado", "last_thursday", ""}, {"el viernes pasado", "last_friday", ""}, {"el sabado pasado", "last_saturday", ""}, {"el domingo pasado", "last_sunday", ""},
	{"lunes", "next_monday", ""}, {"martes", "next_tuesday", ""}, {"miercoles", "next_wednesday", ""}, {"jueves", "next_thursday", ""}, {"viernes", "next_friday", ""}, {"sabado", "next_saturday", ""}, {"domingo", "next_sunday", ""},
}

func init() {
	sort.SliceStable(dayPhrases, func(i, j int) bool {
		return len(strings.Fields(dayPhrases[i].phrase)) > len(strings.Fields(dayPhrases[j].phrase))
	})
}

// timeSpan is a parsed «de 4 a 5» / «a las 10 [por dos horas]».
type timeSpan struct {
	start, end int // minutes since midnight; end < 0 = unsaid
}

// consumeTimeSpan finds and consumes the clock phrase of the sentence. It
// recognizes, in order: «de H[:MM] a H[:MM]», «desde las H hasta las H»,
// «a las H[:MM] [y media] [de la tarde|de la mañana|am|pm] [por N hora(s)|por media hora|hasta las H]».
func consumeTimeSpan(toks []token) (timeSpan, bool) { return consumeTimeSpanHint(toks, "") }

// consumeTimeSpanHint reads the clock(s) of a sentence; hint is the half of
// the day a part phrase lent («esta mañana») for a clock said without one.
func consumeTimeSpanHint(toks []token, hint string) (timeSpan, bool) {
	ts := timeSpan{end: -1}
	n := len(toks)
	// an hour is «4», «16», «4:30», «7am» — or a number word («cuatro», «una»)
	isHour := func(i int) bool {
		if i >= n || toks[i].used {
			return false
		}
		if hourRe.MatchString(toks[i].norm) || gluedRe.MatchString(toks[i].norm) {
			return true
		}
		_, ok := hourWord(toks[i].norm)
		return ok
	}
	// gluedQual is the qualifier an hour carries in its own token («2pm»).
	gluedQual := func(i int) string {
		if i < n {
			if m := gluedRe.FindStringSubmatch(toks[i].norm); m != nil {
				return m[2]
			}
		}
		return ""
	}
	// The tokenizer splits «15:30» into «15» «30»: an hour followed by a
	// two-digit minute token is read as one clock.
	hourText := func(i int) (string, int) {
		h := toks[i].norm
		if hw, ok := hourWord(h); ok {
			h = strconv.Itoa(hw)
		}
		if m := gluedRe.FindStringSubmatch(h); m != nil {
			h = m[1]
		}
		if i+1 < n && !toks[i+1].used && len(toks[i+1].norm) == 2 && toks[i+1].norm[0] >= '0' && toks[i+1].norm[0] <= '5' && toks[i+1].norm[1] >= '0' && toks[i+1].norm[1] <= '9' && !strings.Contains(h, ":") && hourRe.MatchString(toks[i].norm) {
			return h + ":" + toks[i+1].norm, 2
		}
		return h, 1
	}
	qualifierAt := func(i int) (string, int) { // returns qualifier and tokens consumed
		if i < n && !toks[i].used {
			switch toks[i].norm {
			case "am", "pm":
				return toks[i].norm, 1
			// «A.M.» / «P.M.» as a dictation writes them: one token whose
			// dots became a space («a m»), or two tokens («a», «m»)
			case "a m", "a.m", "am.":
				return "am", 1
			case "p m", "p.m", "pm.":
				return "pm", 1
			}
			if i+1 < n && !toks[i+1].used && toks[i+1].norm == "m" && (toks[i].norm == "a" || toks[i].norm == "p") {
				if toks[i].norm == "a" {
					return "am", 2
				}
				return "pm", 2
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
	// «y media» 30, «y cuarto» 15, «menos cuarto» −15 → (minutes, tokens)
	minutesAt := func(i int) (int, int) {
		if i+1 < n && !toks[i].used && !toks[i+1].used {
			switch toks[i].norm + " " + toks[i+1].norm {
			case "y media":
				return 30, 2
			case "y cuarto":
				return 15, 2
			case "menos cuarto":
				return -15, 2
			}
		}
		return 0, 0
	}
	art := func(j int) int { // «las 3», «la una»
		if j < n && !toks[j].used && (toks[j].norm == "las" || toks[j].norm == "la") {
			return j + 1
		}
		return j
	}
	// readAt reads one clock whose hour token is at j: minutes of the day,
	// the index after it, and the qualifier it carried (own or glued; "" =
	// none, the caller decides between the hint and the bare rule).
	type clock struct {
		text string
		mins int
		q    string
		end  int
	}
	scan := func(j int) (clock, bool) {
		if !isHour(j) {
			return clock{}, false
		}
		text, jn := hourText(j)
		k := j + jn
		mins, mn := minutesAt(k)
		k += mn
		q, qn := qualifierAt(k)
		k += qn
		if q == "" {
			q = gluedQual(j)
		}
		return clock{text: text, mins: mins, q: q, end: k}, true
	}
	readClockOf := func(c clock, def string) (int, bool) {
		q := c.q
		if q == "" {
			q = def
		}
		return readClock(c.text, c.mins, q)
	}
	mark := func(a, b int) {
		for x := a; x < b; x++ {
			toks[x].used = true
		}
	}
	// 1. «de H a H» / «desde las H hasta las H» / «entre las H y las H»
	for i := 0; i+2 < n; i++ {
		if toks[i].used || (toks[i].norm != "de" && toks[i].norm != "desde" && toks[i].norm != "entre") {
			continue
		}
		c1, ok := scan(art(i + 1))
		if !ok {
			continue
		}
		k := c1.end
		if k >= n || toks[k].used {
			continue
		}
		if toks[i].norm == "entre" {
			if toks[k].norm != "y" {
				continue
			}
		} else if toks[k].norm != "a" && toks[k].norm != "hasta" {
			continue
		}
		c2, ok := scan(art(k + 1))
		if !ok {
			continue
		}
		en, ok2 := readClockOf(c2, hint)
		st, ok1 := readClockOf(c1, hint)
		if !ok1 || !ok2 {
			continue
		}
		// The end's half of the day is lent to the start («de 7 a 8 de la
		// noche» is 19:00–20:00) UNLESS that runs the span backwards — then
		// the start keeps its own rule («de 7 a 2 de la tarde» is 07:00–14:00).
		if c1.q == "" && c2.q != "" {
			if lent, ok := readClock(c1.text, c1.mins, c2.q); ok && lent < en {
				st = lent
			}
		}
		if en <= st { // «de 11 a 1» → the 1 is the afternoon
			if en2, ok := readClock(c2.text, c2.mins, "pm"); ok && en2 > st {
				en = en2
			}
		}
		if en <= st {
			continue
		}
		mark(i, c2.end)
		ts.start, ts.end = st, en
		return ts, true
	}
	// 2. «a las H …», and the approximations a person says: «tipo 3»,
	// «como a las 3», «a eso de las 3», «como las 3»
	for i := 0; i+1 < n; i++ {
		if toks[i].used {
			continue
		}
		var start int
		switch toks[i].norm {
		case "a":
			start = i + 1
			if i+3 < n && toks[i+1].norm == "eso" && toks[i+2].norm == "de" {
				start = i + 3
			}
		case "tipo":
			start = i + 1
		case "como":
			start = i + 1
			if i+2 < n && toks[i+1].norm == "a" {
				start = i + 2
			}
		default:
			continue
		}
		c, ok := scan(art(start))
		if !ok {
			continue
		}
		st, ok := readClockOf(c, hint)
		if !ok {
			continue
		}
		k := c.end
		mark(i, k)
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
			if c2, ok := scan(art(k + 1)); ok {
				if en, ok := readClockOf(c2, hint); ok && en > st {
					ts.end = en
					mark(k, c2.end)
				}
			}
		}
		if ts.end < 0 {
			// a second clock later in the sentence is the END («se cayó a
			// las 7 y volvió a las 2»; «llegué a las 9 y salí a las 5»)
			for x := k; x+1 < n; x++ {
				if toks[x].used || toks[x].norm != "a" {
					continue
				}
				if c2, ok := scan(art(x + 1)); ok {
					if en, ok := readClockOf(c2, hint); ok && en > st {
						ts.end = en
						mark(x, c2.end)
					}
					break
				}
			}
		}
		return ts, true
	}
	// 3. a bare clock: «16:00» (two tokens after the tokenizer), «4 pm»,
	// «4:30 pm», «2pm»
	for i := 0; i < n; i++ {
		if toks[i].used || (!hourRe.MatchString(toks[i].norm) && !gluedRe.MatchString(toks[i].norm)) {
			continue
		}
		c, _ := scan(i)
		cnt := c.end - i
		if cnt == 1 && c.q == "" {
			continue // a bare number is not a clock («los últimos 3»)
		}
		if i > 0 && !toks[i-1].used && (toks[i-1].norm == "ultimos" || toks[i-1].norm == "ultimas") {
			continue
		}
		st, ok := readClockOf(c, hint)
		if !ok {
			continue
		}
		mark(i, c.end)
		ts.start = st
		return ts, true
	}
	return ts, false
}

// hourWord reads an hour said as a word («cuatro», «una», «doce»).
func hourWord(w string) (int, bool) {
	switch w {
	case "una", "uno":
		return 1, true
	case "dos":
		return 2, true
	case "tres":
		return 3, true
	case "cuatro":
		return 4, true
	case "cinco":
		return 5, true
	case "seis":
		return 6, true
	case "siete":
		return 7, true
	case "ocho":
		return 8, true
	case "nueve":
		return 9, true
	case "diez":
		return 10, true
	case "once":
		return 11, true
	case "doce":
		return 12, true
	}
	return 0, false
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
// consumeDayPart consumes the day and, when a part of the day was named, the
// half («am»/«pm») a clock without a qualifier should take. A part phrase
// («en la tarde») alone sets the day to today.
func consumeDayPart(toks []token) (day, hint string) {
	// a date said as a person says it («el 23 de septiembre», «23/09»)
	if tok, _ := scanDate(toks); tok != "" {
		day = tok
	}
	joined := joinedNorms(toks)
	dayFromPart := false
	for _, dp := range dayPhrases {
		if !strings.Contains(joined, " "+dp.phrase+" ") {
			continue
		}
		words := strings.Fields(dp.phrase)
		start, ok := findPhrase(toks, words)
		if !ok {
			continue
		}
		// «de la mañana» / «de la tarde» / «de la noche» after an hour is the
		// CLOCK's qualifier («de 9 a 11 de la mañana»), never the day «mañana»
		if len(words) == 1 && start >= 2 && toks[start-1].norm == "la" && toks[start-2].norm == "de" {
			continue
		}
		for k := range words {
			toks[start+k].used = true
		}
		// a part-only phrase («en la tarde», «toda la mañana») names no day:
		// it implies today only when nothing else names one («ayer … toda
		// la tarde» is yesterday)
		partOnly := dp.hint != "" && !strings.HasPrefix(dp.phrase, "esta ") && !strings.HasPrefix(dp.phrase, "hoy") && !strings.HasPrefix(dp.phrase, "ayer") && dp.phrase != "anoche"
		if partOnly {
			if hint == "" {
				hint = dp.hint
			}
			joined = joinedNorms(toks)
			continue
		}
		if day == "" || dayFromPart {
			day, dayFromPart = dp.token, false
		}
		if hint == "" {
			hint = dp.hint
		}
		joined = joinedNorms(toks)
	}
	if day == "" && hint != "" {
		day = "today"
	}
	return day, hint
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
	// «anotá que …» / «registrá que …» is something that HAPPENED — a note,
	// never a block on the agenda (AGENDA-ASISTENTE-S1: «anotá que la
	// plataforma se cayó de 2 a 4» used to become a compromiso).
	if verbIdx+1 < len(toks) && toks[verbIdx+1].norm == "que" {
		return ParseResult{Reason: "schedule: a note (verb + que)"}
	}
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
				namedBy = t.raw
				// the resource's own name is not part of the title; an
				// alias («reunión», «cita») is what the block is called
				if r.OwnName(t.norm) {
					toks[i].used = true
				}
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
	day, hint := consumeDayPart(toks)
	span, ok := consumeTimeSpanHint(toks, hint)
	if !ok {
		return ParseResult{Reason: "schedule: no clock"}
	}
	if day == "" {
		day = "today"
	}
	// «con Fabián» / «para Marta»: the relation the name belongs to — the one
	// people-like relation, or every candidate (VOZ-20) for the engine to try.
	data := map[string]any{}
	var refs []Ref
	for i := 0; i+1 < len(toks); i++ {
		if toks[i].used || (toks[i].norm != "con" && toks[i].norm != "para") || !startsUpper(toks[i+1].raw) {
			continue
		}
		parts := nameRun(toks, i+1, nil)
		if len(parts) == 0 {
			continue
		}
		name := strings.Join(parts, " ")
		mf, cands, _ := nameField(v, res)
		relOnly := func(names []string) []string {
			var out []string
			for _, n := range names {
				if f := res.Field(n); f != nil && f.Relation != "" {
					out = append(out, n)
				}
			}
			return out
		}
		switch {
		case mf != "" && res.Field(mf).Relation != "":
			toks[i].used = true
			data[mf] = map[string]any{"match": name}
		case len(relOnly(cands)) > 0:
			toks[i].used = true
			refs = append(refs, Ref{Match: name, Fields: relOnly(cands)})
		default:
			// no place for a name: the words stay in the title
			for k := i + 1; k < i+1+len(parts); k++ {
				toks[k].used = false
			}
		}
		break
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
		// «agendá compromiso de 4 a 5»: the word that named the resource is
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
	p := Plan{Kind: "create", Resource: res.Name, Data: data, Refs: refs}
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
	if lt := t.In(loc); lt.Hour() == 0 && lt.Minute() == 0 {
		// a day with no clock («ayer», «toda la tarde»): no default hour to
		// add one to — the note has a day, not a span
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
	warn := "⚠️ Ya tienes " + strings.Join(names, ", ") + "."
	pend.Labels["__conflict"] = warn
	// What a yes does: flip an invertible rule (ocupa: no) or ask for another time.
	if flipVal, can := rg.nonBlockingValue(); can {
		pend.Data[rg.WhenField] = flipVal
		pend.Labels[rg.WhenField] = fmt.Sprintf("%s (no bloquea el horario: ya había algo)", formatValue(res.Field(rg.WhenField), flipVal, loc))
		pend.Stage = "confirm"
		d.Pending.Put(pend)
		text := warn + "\n\n" + confirmationText(d, pend)
		r := pendingResult(pend, "confirm", "Ya tienes algo a esa hora. ¿Igual lo agendo?", text)
		r.Speech = Speech(warn) + " " + spokenConfirmation(d, pend)
		return r, true
	}
	// Not invertible: the agenda cannot take it. Ask for another time.
	pend.Stage, pend.Field, pend.Asked = "field", rg.Start, pend.Asked+1
	delete(pend.Data, rg.End)
	d.Pending.Put(pend)
	text := warn + " La agenda no permite encimar. ¿A qué hora lo paso? (hoy a las 3, mañana de 4 a 5… o <b>no</b> para cancelar)"
	return pendingResult(pend, "conflict", "Ya tienes algo a esa hora", text), true
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
		when := strings.Trim(strings.TrimPrefix(understood, res.Name), " ·")
		if rg.NoOverlap {
			out.Headline = "Nada agendado"
			fmt.Fprintf(&b, "📅 <b>Nada</b> agendado %s.", esc(understood))
			out.Speech = "Nada agendado"
		} else {
			// a log, not an agenda: «no hay ningún registro hoy»
			out.Headline = "Nada"
			fmt.Fprintf(&b, "📝 No hay %s %s.", esc(numberPhrase(0, res.Name)), esc(when))
			out.Speech = "No hay " + numberPhrase(0, res.Name)
		}
		if when != "" {
			out.Speech += " " + strings.NewReplacer(" (", ", ", "(", "", ")", "").Replace(when)
		}
		out.Speech = SpokenNumbers(out.Speech + ".")
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
	// The VOICE: «Tenés dos compromisos mañana: de diez a once, dentista. De
	// cuatro a cinco, almuerzo con Fabián.»
	var items []string
	for _, row := range rows {
		st, ok1 := asTime(row[rg.Start])
		en, ok2 := asTime(row[rg.End])
		label := labelOf(row, firstN(labels, 2))
		if label == "" {
			label = fmt.Sprint(row["id"])
		}
		if ok1 && ok2 {
			items = append(items, capFirst(ClockRangeWords(st.In(loc).Hour(), st.In(loc).Minute(), en.In(loc).Hour(), en.In(loc).Minute())+", "+label))
		} else {
			items = append(items, capFirst(label+", sin horario"))
		}
	}
	when := strings.TrimSpace(strings.TrimPrefix(understood, res.Name))
	when = strings.Trim(when, " ·")
	when = strings.NewReplacer(" (", ", ", "(", "", ")", "").Replace(when) // «mañana (jue 24 sep)» → «mañana, jue 24 sep»
	out.Speech = "Tienes " + numberPhrase(int(total), res.Name)
	if when != "" {
		out.Speech += " " + when
	}
	out.Speech += ": " + spokenList(items, int(total), "mira el panel")
	out.Speech = SpokenNumbers(out.Speech)
	return out
}

// capFirst upper-cases the first letter (a spoken list item starts a sentence).
func capFirst(s string) string {
	r := []rune(s)
	if len(r) == 0 {
		return s
	}
	r[0] = []rune(strings.ToUpper(string(r[0])))[0]
	return string(r)
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
		fmt.Fprintf(&b, "🟢 <b>Libre</b> %s: no tienes nada agendado.", esc(w.Words))
		out.Text = b.String()
		out.Speech = SpokenNumbers("Estás libre " + w.Words + ": no tienes nada agendado.")
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
	var items []string
	for _, f := range free {
		items = append(items, "de "+ClockWords(f.s.In(loc).Hour(), f.s.In(loc).Minute())+" a "+ClockWords(f.e.In(loc).Hour(), f.e.In(loc).Minute()))
	}
	out.Speech = SpokenNumbers("Libre " + w.Words + ": " + spokenList(items, len(items), "") + " Ocupado: " + numberPhrase(len(busy), "bloques") + ".")
	return out
}
