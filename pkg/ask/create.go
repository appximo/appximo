package ask

import (
	"regexp"
	"strconv"
	"strings"
)

// The FIXED FORM for creating (AGENDA-ASISTENTE-S1, Part C):
//
//	crear tarea: arreglar las puertas del auto, área personal, urgente
//	tarea: lavar el carro el sábado
//	anotá que hablé con Fabián del techo
//	anotá pagar la luz mañana
//
// A verb (crear / nueva / anotá / agregá / registrá…) or the resource word
// itself, then WHAT it is, then the data — in ANY order, with or without the
// field word («área personal» or a bare «personal», resolved against the
// rows that exist), separated by commas, «y» or the pause a dictation
// leaves. Every datum is recognized by its FORM, never by a domain word: a
// field's own name, a declared value or alias, a bool by its name, a day or
// a clock, a number with its unit, a name after «con» / «para», a bare name
// tried against every target (VOZ-20). Whatever is not a datum is the title,
// kept as said. The confirmation is the same as for any write; anything the
// form cannot settle stays the model's — never a «no entendí» where the
// model used to answer.
//
// Chosen over the alternatives: keyword=value pairs («título: X, área: Y»)
// are exhausting to dictate; a fixed positional order («crear tarea X Y Z»)
// has to be memorized and breaks the moment one datum is skipped; the
// colon-then-anything form is one word to remember («crear tarea:»),
// tolerates order and separators, and reads like a note to a person.

var createVerbs = set("crear", "crea", "creá", "creame", "nueva", "nuevo", "anota", "anotá", "anotame", "agrega", "agregá", "agregame", "registra", "registrá", "registrame", "apunta", "apuntá", "apuntame", "guarda", "guardá", "guardame", "carga", "cargá", "cargame")

// noteishVerbs may take a bare infinitive («anotá pagar la luz») as a to-do
// or «que …» as a note; «crear» needs the resource named.
var noteishVerbs = set("anota", "anotá", "anotame", "agrega", "agregá", "agregame", "registra", "registrá", "registrame", "apunta", "apuntá", "apuntame", "guarda", "guardá", "guardame")

var createArticles = set("una", "un", "el", "la", "otra", "otro", "de", "nueva", "nuevo", "esta", "este")

// sepRe finds the separators the fixed form respects.
var sepRe = regexp.MustCompile(`[,;:]`)

type ctok struct {
	raw, norm string
	sep       bool // "," or ":"
}

// tokenizeKeep is tokenize() with the separators kept as their own tokens.
func tokenizeKeep(q string) []ctok {
	q = strings.NewReplacer("¿", " ", "?", " ", "¡", " ", "!", " ", "«", " ", "»", " ", "\"", " ").Replace(q)
	// «7:30» is a clock, not the colon of «tarea: …»: split into «7» «30»
	// exactly as the question tokenizer does (the clock reader glues them)
	q = clockGlueRe.ReplaceAllString(q, "$1 $2")
	q = sepRe.ReplaceAllString(q, " $0 ")
	var out []ctok
	for _, r := range strings.Fields(q) {
		if r == "," || r == ";" || r == ":" {
			out = append(out, ctok{raw: r, norm: ",", sep: true})
			if r == ":" {
				out[len(out)-1].norm = ":"
			}
			continue
		}
		n := normalize(r)
		if n == "" {
			continue
		}
		out = append(out, ctok{raw: r, norm: n})
	}
	return out
}

var clockGlueRe = regexp.MustCompile(`(\d{1,2}):(\d{2})`)

// parseCreate settles the fixed form and its tolerant variants.
func parseCreate(question string, v *Vocabulary) ParseResult {
	if v == nil || !v.Writable() {
		return ParseResult{Reason: "create: read-only vocabulary"}
	}
	toks := tokenizeKeep(question)
	if len(toks) == 0 {
		return ParseResult{Reason: "empty"}
	}
	i := 0
	if i+1 < len(toks) && toks[i].norm == "por" && (toks[i+1].norm == "favor" || toks[i+1].norm == "fa") {
		i += 2
	}
	if i < len(toks) && toks[i].norm == "porfa" {
		i++
	}
	if i >= len(toks) {
		return ParseResult{Reason: "create: nothing said"}
	}
	verb := ""
	if createVerbs[toks[i].norm] || scheduleVerbs[toks[i].norm] {
		verb = toks[i].norm
		i++
		if i < len(toks) && toks[i].norm == ":" && noteishVerbs[verb] {
			// «registrá: la plataforma estuvo caída …» — the colon after a
			// note verb is the «que»
			toks = append(toks[:i], append([]ctok{{raw: "que", norm: "que"}}, toks[i+1:]...)...)
		}
	}
	// the resource: named right after the verb (articles skipped), or first
	// («tarea: …», «Tarea organizar suscripciones»).
	var res *Resource
	j := i
	for j < len(toks) && createArticles[toks[j].norm] && !toks[j].sep {
		j++
	}
	if j < len(toks) && !toks[j].sep {
		for _, name := range v.order {
			r := v.resources[name]
			if r.CanCreate && v.namesResource(toks[j].norm, r) {
				res = r
				break
			}
		}
	}
	note := ""
	switch {
	case res != nil:
		i = j + 1
		if verb == "" {
			// resource-first: with a colon or an infinitive next («tarea:
			// lavar el carro», «Tarea organizar suscripciones») — or, on the
			// agenda, a clock span with no question word («compromiso de 4 a
			// 5 con Fabián hoy», «reunión con Fabián mañana a las 3»).
			ok := i < len(toks) && (toks[i].norm == ":" || (looksInfinitive(toks[i].norm) && !isOpWord(toks[i].norm) && !isTimeWord(toks[i].norm) && !v.knownWord(toks[i].norm)))
			// «compromiso de 4 a 5» creates; «compromisos de 4 a 5» (the
			// plural) asks — only the singular word or an alias is a thing
			// being created
			if !ok && res.Range() != nil && res == agendaResource(v) && isSingularForm(res, toks[j].norm) && verblessSchedule(toks[i:]) {
				ok = true
			}
			if !ok {
				return ParseResult{Reason: "create: resource first without a colon"}
			}
		}
		if !res.OwnName(toks[j].norm) && (i >= len(toks) || toks[i].norm != ":") {
			// an alias («reunión», «cita», «nota») is what the thing is
			// called: it stays as the first word of the title
			i = j
		}
	case verb != "" && i < len(toks) && toks[i].norm == "que" && (noteishVerbs[verb] || scheduleVerbs[verb]):
		// «anotá que tengo que ir al médico» — an obligation inside: a to-do.
		if i+1 < len(toks) {
			var rest []string
			for _, t := range toks[i+1:] {
				rest = append(rest, t.raw)
			}
			if pr := parseObligation(strings.Join(rest, " "), v); pr.Sure {
				return pr
			}
		}
		// «anotá que hablé con Fabián» — something that happened: the note.
		res = noteResource(v)
		if res == nil {
			return ParseResult{Reason: "create: no single note resource"}
		}
		i++
	case verb != "" && scheduleVerbs[verb] && !createVerbs[verb]:
		// «agendá con Fabián», «agendá dentista»: no clock (the schedule
		// parser needs one) — the agenda resource, the engine asks the time.
		res = agendaResource(v)
		if res == nil || !res.CanCreate {
			return ParseResult{Reason: "create: no agenda resource"}
		}
	case verb != "" && noteishVerbs[verb] && i < len(toks) && looksInfinitive(toks[i].norm):
		// «anotá pagar la luz mañana» — an infinitive is a to-do.
		res = taskResource(v)
		if res == nil {
			return ParseResult{Reason: "create: no single to-do resource"}
		}
	default:
		return ParseResult{Reason: "create: no resource named"}
	}
	if i < len(toks) && toks[i].norm == ":" {
		i++
	}
	rest := toks[i:]
	if verb != "" && transitionVerbs[verb] && mentionsState(toks, v) {
		// «poné en curso la tarea …» is a state change; the transition
		// parser (or the model) owns it — never a create with a nonsense title
		return ParseResult{Reason: "create: a transition verb with a state value"}
	}
	if len(rest) == 0 && verb != "" {
		// «crear», «agendá», «anotá» alone: nothing to write — the guide's
		// business (preDiscard already routed it there)
		return ParseResult{Reason: "create: nothing after the verb"}
	}
	// Two intentions in one sentence («anotá X y agendá Y»): the first is
	// taken, the second is said back to be asked apart.
	rest, note = cutSecondIntent(rest, v)
	return buildCreate(v, res, rest, note)
}

// mentionsState reports whether the sentence carries a value (or alias) of
// some resource's state field.
func mentionsState(toks []ctok, v *Vocabulary) bool {
	norms := make([]string, len(toks))
	for i, t := range toks {
		norms[i] = t.norm
	}
	joined := " " + strings.Join(norms, " ") + " "
	for _, name := range v.order {
		sf := v.resources[name].StateField()
		if sf == nil {
			continue
		}
		for _, vf := range sf.valueForms() {
			if strings.Contains(joined, " "+vf.form+" ") {
				return true
			}
		}
	}
	return false
}

// buildCreate reads the data of a create from the tokens after the verb and
// the resource: segments, each a datum or title text.
func buildCreate(v *Vocabulary, res *Resource, rest []ctok, note string) ParseResult {
	segs := segments(rest, v, res)
	data := map[string]any{}
	var refs []Ref
	var title []string
	titleF := titleField(res)
	if titleF == nil {
		if lf := res.LabelFields(); len(lf) > 0 {
			titleF = res.Field(lf[0])
		}
	}
	for _, sg := range segs {
		// the first FREE segment is the title: a bare word before any title
		// («almuerzo») is what the row is called, not a reference
		if d := classifySegment(sg, v, res, data, &refs, len(title) == 0); d {
			continue
		}
		// free text: the title (cleaned of the data said inside it)
		words := titleWords(sg, v, res, data, &refs)
		if len(words) > 0 {
			title = append(title, strings.Join(words, " "))
		}
	}
	if len(title) > 0 && titleF != nil {
		// the title stays as said — the owner's own rows are lowercase, the
		// way they were dictated; a list line capitalizes when it reads
		data[titleF.Name] = strings.Join(title, ", ")
	}
	if len(data) == 0 && len(refs) == 0 && titleF == nil {
		return ParseResult{Reason: "create: nothing to write"}
	}
	if res == noteResource(v) {
		// a note is about what HAPPENED: «el martes» is the past Tuesday
		for k, val := range data {
			sv, ok := val.(string)
			if !ok {
				continue
			}
			base, clock, _ := strings.Cut(sv, " ")
			if strings.HasPrefix(base, "next_") && base != "next_week" {
				base = "last_" + strings.TrimPrefix(base, "next_")
			} else if strings.HasPrefix(base, "date:") && len(base) == len("date:MM-DD") {
				base = "past_" + base // «el 30 de diciembre» with no year: the one that happened
			} else {
				continue
			}
			if clock != "" {
				base += " " + clock
			}
			data[k] = base
		}
	}
	p := Plan{Kind: "create", Resource: res.Name, Data: data, Refs: refs, Reason: note}
	if err := p.Validate(v); err != nil {
		return ParseResult{Reason: "create invalid: " + err.Error()}
	}
	return ParseResult{Plan: p, Sure: true}
}

// noteResource is the ONE resource that records something that HAPPENED:
// creatable, no lifecycle, exactly one required title text, not the to-do
// resource, and a moment — a range without a no-overlap rule (a lapse) or a
// time field that defaults to now.
func noteResource(v *Vocabulary) *Resource {
	task := taskResource(v)
	var found *Resource
	for _, name := range v.order {
		r := v.resources[name]
		if !r.CanCreate || r == task || titleField(r) == nil {
			continue
		}
		lifecycle := false
		for _, f := range r.Fields {
			if f.HasMachine {
				lifecycle = true
			}
		}
		if lifecycle {
			continue
		}
		moment := false
		if rg := r.Range(); rg != nil && !rg.NoOverlap {
			moment = true
		}
		for _, f := range r.Fields {
			if f.Type == "time" && f.HasDefault {
				moment = true
			}
		}
		if !moment {
			continue
		}
		if found != nil {
			return nil
		}
		found = r
	}
	return found
}

// cutSecondIntent splits «… y agendá …» / «… y marcá …» / «… y anotá …»:
// returns the first part and the second, as said.
func cutSecondIntent(toks []ctok, v *Vocabulary) ([]ctok, string) {
	for i := 1; i+1 < len(toks); i++ {
		if toks[i].sep || toks[i].norm != "y" {
			continue
		}
		n := toks[i+1].norm
		if createVerbs[n] || scheduleVerbs[n] || transitionVerbs[n] || writeVerbs[n] {
			var rest []string
			for _, t := range toks[i+1:] {
				rest = append(rest, t.raw)
			}
			return toks[:i], strings.Join(rest, " ")
		}
	}
	return toks, ""
}

// segments splits the remainder on commas/colons, and on «y» when what
// follows is a datum on its own.
func segments(toks []ctok, v *Vocabulary, res *Resource) [][]ctok {
	var out [][]ctok
	var cur []ctok
	flush := func() {
		if len(cur) > 0 {
			out = append(out, cur)
		}
		cur = nil
	}
	for i := 0; i < len(toks); i++ {
		t := toks[i]
		if t.sep {
			flush()
			continue
		}
		if t.norm == "y" && len(cur) > 0 && i+1 < len(toks) && (toks[i+1].norm == "media" || toks[i+1].norm == "cuarto") && isHourTok(cur[len(cur)-1].norm) {
			cur = append(cur, t) // «9 y media»: the clock goes on
			continue
		}
		if t.norm == "y" && len(cur) > 0 && hasEntre(cur) {
			cur = append(cur, t) // «entre las 7 y las 2»: one span
			continue
		}
		if t.norm == "y" && len(cur) > 0 && i+1 < len(toks) {
			// look ahead to the next separator
			j := i + 1
			for j < len(toks) && !toks[j].sep && toks[j].norm != "y" {
				j++
			}
			if isDatum(toks[i+1:j], v, res) {
				flush()
				continue
			}
		}
		cur = append(cur, t)
	}
	flush()
	return out
}

// isSingularForm reports whether norm is the resource's singular name or a
// declared alias as declared (in the singular): «compromiso» / «cita» /
// «reunión» name ONE thing being created; «compromisos» / «citas» ask.
func isSingularForm(res *Resource, norm string) bool {
	if norm == normalize(singular(res.Name)) {
		return true
	}
	for _, a := range res.Aliases {
		if norm == normalize(a) {
			return true
		}
	}
	return false
}

// hasEntre reports an «entre» in the run not yet closed by its «y».
func hasEntre(run []ctok) bool {
	open := false
	for _, t := range run {
		switch t.norm {
		case "entre":
			open = true
		case "y":
			open = false
		}
	}
	return open
}

// isHourTok reports whether a token reads as an hour («9», «9:30», «nueve»).
func isHourTok(n string) bool {
	if hourRe.MatchString(n) {
		return true
	}
	_, ok := hourWord(n)
	return ok
}

// isDatum reports whether a run of tokens is a datum by itself.
func isDatum(seg []ctok, v *Vocabulary, res *Resource) bool {
	if len(seg) == 0 {
		return false
	}
	data := map[string]any{}
	var refs []Ref
	return classifySegment(seg, v, res, data, &refs, false)
}

// fieldByWord finds the field a spoken word names: its own name forms («vence
// en»), a relation's name without _id («área»), or the relation's target
// («persona» for persona_id → personas).
func fieldByWord(v *Vocabulary, res *Resource, word string) *Field {
	for _, f := range res.Fields {
		if f.Auto {
			continue
		}
		base := strings.ReplaceAll(normalize(f.Name), "_", " ")
		forms := []string{base, singularES(base), strings.TrimSuffix(base, " id")}
		if f.Relation != "" {
			forms = append(forms, normalize(singular(f.Relation)), normalize(f.Relation))
		}
		for _, form := range forms {
			if form != "" && (word == form || word == form+"s") {
				return f
			}
		}
	}
	return nil
}

var unitRe = regexp.MustCompile(`^(\d+|media|una|un|dos|tres|cuatro|cinco|seis|siete|ocho|nueve|diez|quince|veinte|treinta|cuarenta|cincuenta|sesenta|noventa)\s+(minutos?|min|horas?|h)$`)

var spanishSmall = map[string]int{"media": 0, "una": 1, "un": 1, "dos": 2, "tres": 3, "cuatro": 4, "cinco": 5, "seis": 6, "siete": 7, "ocho": 8, "nueve": 9, "diez": 10, "quince": 15, "veinte": 20, "treinta": 30, "cuarenta": 40, "cincuenta": 50, "sesenta": 60, "noventa": 90}

// durationField is the numeric field a «30 minutos» lands on: the one whose
// name says duration/minutes, else the only writable numeric field.
func durationField(res *Resource) *Field {
	var nums []*Field
	for _, f := range res.Fields {
		if f.IsNumeric() && !f.Auto && !f.Money {
			nums = append(nums, f)
		}
	}
	if len(nums) == 1 {
		return nums[0]
	}
	var hits []*Field
	for _, f := range nums {
		n := normalize(f.Name)
		if strings.Contains(n, "min") || strings.Contains(n, "dur") || strings.Contains(n, "estim") {
			hits = append(hits, f)
		}
	}
	if len(hits) >= 1 {
		return hits[0]
	}
	return nil
}

// classifySegment reads one segment as a datum and, when it is one, writes it
// into data/refs. first says whether this is the first segment (a bare name
// there is the title, not a reference).
func classifySegment(seg []ctok, v *Vocabulary, res *Resource, data map[string]any, refs *[]Ref, first bool) bool {
	if len(seg) == 0 {
		return false
	}
	norms := make([]string, len(seg))
	raws := make([]string, len(seg))
	for i, t := range seg {
		norms[i], raws[i] = t.norm, t.raw
	}
	joined := strings.Join(norms, " ")
	// keyword + value
	if f := fieldByWord(v, res, norms[0]); f != nil && len(seg) > 1 {
		rest := seg[1:]
		if rest[0].norm == "de" || rest[0].norm == "en" || rest[0].norm == "es" {
			rest = rest[1:]
		}
		if len(rest) > 0 {
			if setField(v, res, f, rest, data, refs) {
				return true
			}
		}
	}
	// «con Name» / «para Name» → the people-like relation(s); never a day
	// («para mañana») nor a function word. The name stops at a time word or
	// a stopword («con Fabián mañana a las 3»: the rest must be a datum).
	if (norms[0] == "con" || norms[0] == "para") && len(seg) > 1 && !stopwords[norms[1]] && !isTimePhrase(seg[1:], res) {
		k := 1
		for k < len(seg) && !stopwords[norms[k]] && !isTimeWord(norms[k]) && !v.knownWord(norms[k]) && !hourRe.MatchString(norms[k]) {
			k++
		}
		if k > 1 {
			name := strings.Join(raws[1:k], " ")
			restSeg := seg[k:]
			probe := map[string]any{}
			var pr []Ref
			for kk, val := range data {
				probe[kk] = val
			}
			if len(restSeg) == 0 || classifySegment(restSeg, v, res, probe, &pr, false) {
				if placeName(v, res, name, false, data, refs) {
					for kk, val := range probe {
						if _, taken := data[kk]; !taken {
							data[kk] = val
						}
					}
					*refs = append(*refs, pr...)
					return true
				}
			}
		}
	}
	// a bool by its name («urgente»), or its negation («no urgente», «sin urgencia»)
	neg := false
	bw := norms
	if len(bw) > 1 && (bw[0] == "no" || bw[0] == "sin") {
		neg, bw = true, bw[1:]
	}
	if len(bw) == 1 {
		for _, f := range res.Fields {
			if f.Type == "bool" && !f.Auto && boolNamed(f, bw[0]) {
				data[f.Name] = !neg
				return true
			}
		}
	}
	// a declared value or alias
	for _, f := range res.Fields {
		if len(f.Enum) == 0 {
			continue
		}
		for _, vf := range f.valueForms() {
			if vf.form == joined {
				data[f.Name] = vf.val
				return true
			}
		}
	}
	// a duration with its unit
	if m := unitRe.FindStringSubmatch(joined); m != nil {
		if f := durationField(res); f != nil {
			n, err := strconv.Atoi(m[1])
			if err != nil {
				n = spanishSmall[m[1]]
			}
			minutes := n
			if strings.HasPrefix(m[2], "h") {
				minutes = n * 60
				if m[1] == "media" {
					minutes = 30
				}
			}
			if strings.Contains(normalize(f.Name), "hora") && !strings.HasPrefix(m[2], "h") {
				data[f.Name] = float64(minutes) / 60
			} else {
				data[f.Name] = float64(minutes)
			}
			return true
		}
	}
	// a day and/or a clock, the whole segment
	{
		tt := make([]token, len(seg))
		for i, t := range seg {
			tt[i] = token{raw: t.raw, norm: t.norm}
		}
		if applyTime(tt, res, data) {
			all := true
			for _, t := range tt {
				if !t.used && !stopwords[t.norm] {
					all = false
				}
			}
			if all {
				return true
			}
			// partially a time: not a pure datum — the caller reads it as
			// title text (and titleWords strips the time again).
			for k := range data {
				if fd := res.Field(k); fd != nil && fd.Type == "time" {
					delete(data, k)
				}
			}
		}
	}
	// a bare name (not the first segment): tried against every target
	if !first && len(seg) <= 3 {
		known := false
		for _, n := range norms {
			if stopwords[n] || countWords[n] || listWords[n] {
				known = true
			}
		}
		if !known && placeName(v, res, strings.Join(raws, " "), true, data, refs) {
			return true
		}
	}
	return false
}

// setField writes a value said for a named field.
func setField(v *Vocabulary, res *Resource, f *Field, rest []ctok, data map[string]any, refs *[]Ref) bool {
	raws := make([]string, len(rest))
	norms := make([]string, len(rest))
	for i, t := range rest {
		raws[i], norms[i] = t.raw, t.norm
	}
	said := strings.Join(raws, " ")
	joined := strings.Join(norms, " ")
	switch {
	case f.Relation != "":
		data[f.Name] = map[string]any{"match": said}
		return true
	case len(f.Enum) > 0:
		for _, vf := range f.valueForms() {
			if vf.form == joined {
				data[f.Name] = vf.val
				return true
			}
		}
		return false
	case f.Type == "bool":
		switch joined {
		case "si", "sí", "verdadero", "true":
			data[f.Name] = true
		case "no", "falso", "false":
			data[f.Name] = false
		default:
			return false
		}
		return true
	case f.Type == "time":
		tok := spanishTimeToken(said)
		if tok == "" {
			return false
		}
		data[f.Name] = tok
		return true
	case f.IsNumeric():
		if m := unitRe.FindStringSubmatch(joined); m != nil {
			n, err := strconv.Atoi(m[1])
			if err != nil {
				n = spanishSmall[m[1]]
			}
			if strings.HasPrefix(m[2], "h") {
				n *= 60
			}
			data[f.Name] = float64(n)
			return true
		}
		if n, err := strconv.ParseFloat(strings.ReplaceAll(joined, ",", "."), 64); err == nil {
			data[f.Name] = n
			return true
		}
		return false
	case f.IsText():
		data[f.Name] = said
		return true
	}
	return false
}

// placeName puts a name on the relation it belongs to, or on every candidate
// (VOZ-20). soft marks a bare word that may turn out to be title text.
func placeName(v *Vocabulary, res *Resource, name string, soft bool, data map[string]any, refs *[]Ref) bool {
	mf, cands, _ := nameField(v, res)
	var rel []string
	for _, c := range cands {
		if f := res.Field(c); f != nil && f.Relation != "" {
			rel = append(rel, c)
		}
	}
	switch {
	case mf != "" && res.Field(mf).Relation != "":
		if _, taken := data[mf]; taken {
			return false
		}
		data[mf] = map[string]any{"match": name}
		return true
	case len(rel) > 0:
		*refs = append(*refs, Ref{Match: name, Fields: rel, Soft: soft})
		return true
	}
	return false
}

// applyTime reads a day and/or a clock anywhere in the tokens into the
// resource's time fields (a range's start/end, or its due field). Marks
// what it consumed. Returns whether anything was read.
func applyTime(tt []token, res *Resource, data map[string]any) bool {
	day, hint := consumeDayPart(tt)
	span, hasSpan := consumeTimeSpanHint(tt, hint)
	if !hasSpan && day == "" {
		return false
	}
	// «antes del viernes», «hasta el viernes», «para el viernes»: the words
	// that lead into the day are the deadline's, never the title's
	for i := range tt {
		if tt[i].used || (tt[i].norm != "antes" && tt[i].norm != "hasta" && tt[i].norm != "para") {
			continue
		}
		k := i + 1
		for k < len(tt) && !tt[k].used && (tt[k].norm == "de" || tt[k].norm == "del" || articles[tt[k].norm]) {
			k++
		}
		if k < len(tt) && tt[k].used && k > i && (tt[i].norm != "para" || k > i+1) {
			for x := i; x < k; x++ {
				tt[x].used = true
			}
		}
	}
	if day == "" {
		day = "today"
	}
	if rg := res.Range(); rg != nil {
		if _, taken := data[rg.Start]; taken {
			return false
		}
		if hasSpan {
			data[rg.Start] = day + " " + clockString(span.start)
			if span.end >= 0 {
				data[rg.End] = day + " " + clockString(span.end)
			}
		} else {
			data[rg.Start] = day
		}
		return true
	}
	f := res.DueTimeField()
	if f == nil {
		return false
	}
	if _, taken := data[f.Name]; taken {
		return false
	}
	if hasSpan {
		data[f.Name] = day + " " + clockString(span.start)
	} else {
		data[f.Name] = day
	}
	return true
}

// titleWords is a free-text segment cleaned of the data said INSIDE it: a
// day or a clock, a bool by its name, «con Name» (a capitalized run), a
// field word with its value at the end («… área casa»). What remains is the
// title, as said; a capitalized name kept in it («llamar a Fabián») is also
// offered as a soft reference so the relation is filled when it exists.
func titleWords(seg []ctok, v *Vocabulary, res *Resource, data map[string]any, refs *[]Ref) []string {
	tt := make([]token, len(seg))
	for i, t := range seg {
		tt[i] = token{raw: t.raw, norm: t.norm}
	}
	applyTime(tt, res, data)
	for i := range tt {
		if tt[i].used {
			continue
		}
		for _, f := range res.Fields {
			if f.Type == "bool" && !f.Auto && boolNamed(f, tt[i].norm) {
				if _, taken := data[f.Name]; !taken {
					val := true
					if i > 0 && !tt[i-1].used && (tt[i-1].norm == "no" || tt[i-1].norm == "sin") {
						tt[i-1].used, val = true, false
					}
					data[f.Name] = val
					tt[i].used = true
				}
			}
		}
	}
	// «con Name» / «para Name» (capitalized) out of the title — except on
	// the NOTE: «me reuní con Camilo de 4 a 5» is the text of a log, and
	// the person is a SOFT reference (linked when Camilo exists, the words
	// kept either way). A hard one refused the owner's real note when Camilo
	// was in no table (2026-09-25); the capitalized run below picks it up.
	note := res == noteResource(v)
	for i := 0; i+1 < len(tt) && !note; i++ {
		if tt[i].used || (tt[i].norm != "con" && tt[i].norm != "para") || !startsUpper(tt[i+1].raw) {
			continue
		}
		j := i + 1
		var parts []string
		for j < len(tt) && !tt[j].used && startsUpper(tt[j].raw) && !stopwords[tt[j].norm] {
			parts = append(parts, tt[j].raw)
			j++
		}
		if len(parts) > 0 && placeName(v, res, strings.Join(parts, " "), false, data, refs) {
			tt[i].used = true
			for k := i + 1; k < j; k++ {
				tt[k].used = true
			}
		}
	}
	// a trailing «<field word> <value>» («… área casa», «… persona Marta»)
	for i := 1; i < len(tt); i++ {
		if tt[i].used {
			continue
		}
		if f := fieldByWord(v, res, tt[i].norm); f != nil && i+1 < len(tt) {
			rest := seg[i+1:]
			probe := map[string]any{}
			var pr []Ref
			if setField(v, res, f, rest, probe, &pr) {
				for k, val := range probe {
					if _, taken := data[k]; !taken {
						data[k] = val
					}
				}
				for k := i; k < len(tt); k++ {
					tt[k].used = true
				}
				break
			}
		}
	}
	// a capitalized name INSIDE the title («llamar a Fabián»): a soft
	// reference, the words stay
	for i := 1; i < len(tt); i++ {
		if tt[i].used || !startsUpper(tt[i].raw) || stopwords[tt[i].norm] {
			continue
		}
		j := i
		var parts []string
		for j < len(tt) && !tt[j].used && startsUpper(tt[j].raw) && !stopwords[tt[j].norm] {
			parts = append(parts, tt[j].raw)
			j++
		}
		if len(parts) > 0 {
			mf, cands, _ := nameField(v, res)
			var rel []string
			for _, c := range cands {
				if f := res.Field(c); f != nil && f.Relation != "" {
					rel = append(rel, c)
				}
			}
			if mf != "" && res.Field(mf).Relation != "" {
				rel = []string{mf}
			}
			if len(rel) > 0 {
				*refs = append(*refs, Ref{Match: strings.Join(parts, " "), Fields: rel, Soft: true, InTitle: true})
			}
		}
		i = j
	}
	var words []string
	for _, t := range tt {
		if !t.used {
			words = append(words, t.raw)
		}
	}
	// a note keeps its text as said («se fue la luz», «me reuní con Camilo»
	// — the pronoun and the article are the sentence); any other resource
	// drops a leading function word («la tarea de …»)
	for len(words) > 0 && !note && (stopwords[normalize(words[0])] && normalize(words[0]) != "que") {
		words = words[1:]
	}
	for len(words) > 0 {
		last := normalize(words[len(words)-1])
		if last == "por" || last == "favor" || last == "porfa" || last == "y" || last == "para" || last == "con" {
			words = words[:len(words)-1]
			continue
		}
		break
	}
	return words
}

// isOpWord reports whether a word is an operation of the read grammar (list,
// count, sum…) — never the start of a to-do («listar compromisos»).
func isOpWord(n string) bool {
	return listWords[n] || countWords[n] || sumWords[n] || avgWords[n] || lastWords[n] || summaryWords[n]
}

// isTimePhrase reports whether a run of tokens is entirely a day and/or a
// clock («mañana», «el viernes a las 3»).
func isTimePhrase(seg []ctok, res *Resource) bool {
	tt := make([]token, len(seg))
	for i, t := range seg {
		tt[i] = token{raw: t.raw, norm: t.norm}
	}
	probe := map[string]any{}
	if !applyTime(tt, res, probe) {
		// applyTime needs a time field; a day is still a day
		day, hint := consumeDayPart(tt)
		_, hasSpan := consumeTimeSpanHint(tt, hint)
		if day == "" && !hasSpan {
			return false
		}
	}
	for _, t := range tt {
		if !t.used && !stopwords[t.norm] {
			return false
		}
	}
	return true
}

// isTimeWord reports whether a word belongs to the period / day grammar
// («antier» ends in -er and is no infinitive).
func isTimeWord(n string) bool {
	for _, pp := range periodPhrases {
		for _, w := range strings.Fields(pp.phrase) {
			if w == n {
				return true
			}
		}
	}
	for _, dp := range dayPhrases {
		for _, w := range strings.Fields(dp.phrase) {
			if w == n {
				return true
			}
		}
	}
	return false
}

// verblessSchedule reports whether the tokens after the agenda word read as
// a block («de 4 a 5 con Fabián hoy», «con Fabián mañana a las 3 por una
// hora»): a clock span and no question or operation word.
func verblessSchedule(toks []ctok) bool {
	if len(toks) == 0 {
		return false
	}
	tt := make([]token, 0, len(toks))
	for _, t := range toks {
		if t.sep {
			continue
		}
		if isOpWord(t.norm) || t.norm == "tengo" || t.norm == "hay" || t.norm == "tenemos" || t.norm == "que" || t.norm == "cual" || t.norm == "cuales" || t.norm == "estoy" {
			return false
		}
		tt = append(tt, token{raw: t.raw, norm: t.norm})
	}
	_, hasSpan := consumeTimeSpan(tt)
	return hasSpan
}
