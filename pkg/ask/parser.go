package ask

import (
	"fmt"
	"sort"
	"strings"
)

// The deterministic parser (VOZ-SIN-IA-S1): most owner questions have a fixed
// shape — a counting/listing verb, a resource the schema declares, maybe a
// declared state, a period, a proper name. The schema is finite and the plan
// grammar is closed, so those are solved with rules, at zero cost and in
// microseconds. The model is for the rare question, not the common one.
//
// THE GOLDEN RULE: the parser answers only when it is SURE. "Sure" means, and
// is checked in code, all of the following:
//
//   1. exactly ONE resource of the role's vocabulary is named (by its schema
//      name, singular/plural, underscores as spaces — never a synonym the
//      schema does not carry);
//   2. exactly ONE operation is recognized (count / list / sum / avg / group
//      by), or none with a bare "the <resource> …" which reads as a list;
//   3. EVERY word of the question is consumed by a recognized piece — a
//      stopword, the operation, the resource, a declared enum/state value of
//      that resource (matched whole, accent-insensitive, plural tolerated),
//      a period phrase, a group-by field, or a proper name introduced by a
//      preposition. ONE leftover word ("vendimos", "vigentes", "ignora") and
//      the question goes to the model;
//   4. each enum value maps to exactly one field of the resource;
//   5. a proper name maps to exactly one place: the single relation of the
//      resource whose target has a name-like label, else the resource's own
//      name-like field; two candidates → the model;
//   6. a sum/average names a numeric field, or the resource has ONE obvious
//      amount (MoneyField);
//   7. the resulting plan passes the same validation as a model plan.
//
// Anything else is NOT SURE and falls through. The parser never guesses to
// save a call: a wrong plan costs more than three tenths of a cent.
//
// It is generic: nothing here belongs to one app. Every word it understands
// beyond Spanish function words and the closed operation/period vocabulary
// comes from the tenant's schema. A write verb is recognized so a "borrá las
// órdenes" is refused without a model call either.

// ParseResult is what the parser says about a question.
type ParseResult struct {
	Plan Plan
	// Sure is true when the plan may run without the model.
	Sure bool
	// Reason says why not (for the log; never for the owner).
	Reason string
}

var (
	stopwords = set("de", "del", "la", "las", "el", "los", "un", "una", "unos", "unas", "en", "a", "al", "y", "e", "o", "u",
		"que", "me", "mi", "mis", "nos", "nuestro", "nuestra", "nuestros", "nuestras", "tengo", "tenemos", "tenes", "tienes", "tiene",
		"hay", "existen", "existe", "estan", "esta", "son", "es", "actualmente", "ahora", "ya", "todas", "todos", "toda", "todo",
		"por", "favor", "decime", "dime", "digame", "quiero", "quisiera", "necesito", "saber", "ver", "podes", "puedes", "podrias",
		"cargados", "cargadas", "hechas", "hechos", "actuales", "actual", "con", "estado", "tipo", "en", "total",
		"para", "sobre", "cual", "cuales", "hubo", "hubieron", "llegaron", "entraron", "vinieron", "quedan", "queda", "hoy")
	countWords = set("cuantos", "cuantas", "cuanto", "cuanta", "numero", "cantidad", "conta", "contame", "cuenta", "cuentame", "total")
	listWords  = set("lista", "listame", "listado", "mostrame", "muestrame", "mostra", "muestra", "dame", "traeme", "pasame", "cuales", "que", "ver")
	sumWords   = set("suma", "suman", "sumatoria", "sumame", "sumar")
	avgWords   = set("promedio", "media")
	writeVerbs = set("borra", "borrar", "elimina", "eliminar", "cancela", "cancelar", "crea", "crear", "agenda", "agendar", "cambia", "cambiar",
		"modifica", "modificar", "edita", "editar", "actualiza", "actualizar", "manda", "mandar", "envia", "enviar", "marca", "marcar",
		"pone", "pon", "poner", "agrega", "agregar", "registra", "registrar", "anota", "anotar", "programa", "programar", "sube", "subir", "baja", "bajar")
	prepositions = set("de", "del", "para", "con", "a")
)

// period phrases, normalized, longest first.
var periodPhrases = []struct {
	phrase, token string
}{
	{"la semana pasada", "last_week"}, {"semana pasada", "last_week"},
	{"el mes pasado", "last_month"}, {"mes pasado", "last_month"},
	{"ultimos 7 dias", "last_7_days"}, {"ultimos siete dias", "last_7_days"},
	{"ultimos 30 dias", "last_30_days"}, {"ultimos treinta dias", "last_30_days"},
	{"esta semana", "this_week"}, {"este mes", "this_month"}, {"este ano", "this_year"}, {"este año", "this_year"},
	{"del dia de hoy", "today"}, {"del dia", "today"}, {"de la semana", "this_week"}, {"del mes", "this_month"}, {"del ano", "this_year"}, {"del año", "this_year"},
	{"de hoy", "today"}, {"hoy", "today"}, {"de ayer", "yesterday"}, {"ayer", "yesterday"},
}

// periodOnly are words that mean nothing WITHOUT a period ("nuevos" = created
// in the period; alone it is a business word the schema does not declare):
// consumed only when a period phrase was found, else they stay leftover.
var periodOnly = set("nuevos", "nuevas", "nuevo", "nueva", "recientes", "reciente", "creados", "creadas", "creado", "creada", "registrados", "registradas")

func set(words ...string) map[string]bool {
	m := make(map[string]bool, len(words))
	for _, w := range words {
		m[w] = true
	}
	return m
}

// token keeps the original casing (a proper name is echoed as said) beside
// its normalized form.
type token struct {
	raw, norm string
	used      bool
}

func tokenize(q string) []token {
	raws := strings.Fields(strings.NewReplacer("¿", " ", "?", " ", "¡", " ", "!", " ", ",", " ", ";", " ", ":", " ", ".", " ", "«", " ", "»", " ", "\"", " ").Replace(q))
	out := make([]token, 0, len(raws))
	for _, r := range raws {
		n := normalize(r)
		if n == "" {
			continue
		}
		out = append(out, token{raw: r, norm: n})
	}
	return out
}

// Parse tries to turn the question into a plan without a model.
func Parse(question string, v *Vocabulary) ParseResult {
	toks := tokenize(question)
	if len(toks) == 0 {
		return ParseResult{Reason: "empty"}
	}
	// A write verb anywhere → refused deterministically.
	for _, t := range toks {
		if writeVerbs[t.norm] {
			return ParseResult{Plan: Plan{Kind: "write", Reason: "verbo de escritura: " + t.raw}, Sure: true}
		}
	}

	// 1. period phrases (multi-word first) — consume tokens.
	var period *Period
	norms := make([]string, len(toks))
	for i, t := range toks {
		norms[i] = t.norm
	}
	joined := " " + strings.Join(norms, " ") + " "
	for _, pp := range periodPhrases {
		if !strings.Contains(joined, " "+pp.phrase+" ") {
			continue
		}
		if !consumePhrase(toks, pp.phrase) {
			continue // already consumed as part of a longer phrase
		}
		if period != nil {
			return ParseResult{Reason: "two periods"}
		}
		period = &Period{Range: pp.token}
	}

	// 1b. MULTI-WORD enum values first, across every readable resource: an
	// "orden pendiente de pago" must not read "pago" as the resource pagos.
	// Each consumed value remembers its resource; it must be the one named.
	type hinted struct {
		res    *Resource
		filter Filter
	}
	var hints []hinted
	for _, name := range v.ResourceNames() {
		r := v.Resource(name)
		for _, f := range r.Fields {
			for _, val := range f.Enum {
				form := strings.ReplaceAll(normalize(val), "_", " ")
				if !strings.Contains(form, " ") {
					continue
				}
				for _, fm := range []string{form, form + "s", strings.ReplaceAll(form, " ", " de "), strings.ReplaceAll(form, " ", " de ") + "s"} {
					if consumePhrase(toks, fm) {
						hints = append(hints, hinted{res: r, filter: Filter{Field: f.Name, Op: "eq", Value: val}})
						break
					}
				}
			}
		}
	}

	// 2. the resource: exactly one, by schema name (singular/plural, _ as space).
	var res *Resource
	for _, name := range v.ResourceNames() {
		r := v.Resource(name)
		for _, form := range nameForms(name) {
			if consumePhrase(toks, form) {
				if res != nil && res != r {
					return ParseResult{Reason: "two resources: " + res.Name + ", " + r.Name}
				}
				res = r
			}
		}
	}
	if res == nil {
		return ParseResult{Reason: "no resource named"}
	}
	var filters []Filter
	seenField := map[string]bool{}
	for _, h := range hints {
		if h.res != res {
			return ParseResult{Reason: "value " + fmt.Sprint(h.filter.Value) + " belongs to " + h.res.Name + ", not " + res.Name}
		}
		if seenField[h.filter.Field] {
			return ParseResult{Reason: "two values for " + h.filter.Field}
		}
		seenField[h.filter.Field] = true
		filters = append(filters, h.filter)
	}

	// 3. the operation.
	op := ""
	setOp := func(o string) bool {
		if op != "" && op != o {
			return false
		}
		op = o
		return true
	}
	for i := range toks {
		t := &toks[i]
		if t.used {
			continue
		}
		var o string
		switch {
		case countWords[t.norm]:
			o = "count"
		case listWords[t.norm]:
			o = "list"
		case sumWords[t.norm]:
			o = "sum"
		case avgWords[t.norm]:
			o = "avg"
		default:
			continue
		}
		// "cuánto suman" / "cuál es el promedio": the amount word wins over the
		// count word — both are consumed.
		if op == "count" && (o == "sum" || o == "avg") {
			op = ""
		}
		if (op == "sum" || op == "avg") && o == "count" {
			t.used = true
			continue
		}
		if !setOp(o) {
			return ParseResult{Reason: "two operations: " + op + ", " + o}
		}
		t.used = true
	}

	// 4. group_by: "por <field>" where field is a groupable field of the resource.
	groupBy := ""
	for i := 0; i+1 < len(toks); i++ {
		if toks[i].norm != "por" || toks[i].used {
			continue
		}
		next := toks[i+1]
		if f := res.fieldByForm(next.norm); f != nil && f.Groupable() {
			if groupBy != "" {
				return ParseResult{Reason: "two group_by"}
			}
			groupBy = f.Name
			toks[i].used, toks[i+1].used = true, true
		}
	}

	// 5. single-word enum / state values of the resource's fields, whole,
	// plural-tolerant (the multi-word ones were consumed in 1b).
	for _, f := range res.Fields {
		if len(f.Enum) == 0 {
			continue
		}
		for _, val := range f.Enum {
			form := strings.ReplaceAll(normalize(val), "_", " ")
			if form == "" || strings.Contains(form, " ") {
				continue
			}
			forms := []string{form, form + "s", form + "es"}
			for _, fm := range forms {
				if consumePhrase(toks, fm) {
					if seenField[f.Name] {
						return ParseResult{Reason: "two values for " + f.Name}
					}
					if fieldOfValue(res, val) == nil {
						return ParseResult{Reason: "value " + val + " belongs to two fields"}
					}
					seenField[f.Name] = true
					filters = append(filters, Filter{Field: f.Name, Op: "eq", Value: val})
					break
				}
			}
		}
	}

	// 6. the amount field for sum/avg: a numeric field named, else the one
	// obvious amount.
	field := ""
	if op == "sum" || op == "avg" {
		for i := range toks {
			if toks[i].used {
				continue
			}
			if f := res.fieldByForm(toks[i].norm); f != nil && f.IsNumeric() {
				if field != "" {
					return ParseResult{Reason: "two amount fields"}
				}
				field = f.Name
				toks[i].used = true
			}
		}
		if field == "" {
			field = res.MoneyField()
		}
		if field == "" {
			return ParseResult{Reason: "no amount field"}
		}
	}

	// 7. a proper name after a preposition: the run of unconsumed, unknown
	// tokens right after "de/del/para/con/a".
	match := ""
	matchField := ""
	for i := 0; i < len(toks); i++ {
		if toks[i].used || !prepositions[toks[i].norm] {
			continue
		}
		j := i + 1
		var parts []string
		for j < len(toks) && !toks[j].used && !stopwords[toks[j].norm] && !countWords[toks[j].norm] && !listWords[toks[j].norm] {
			parts = append(parts, toks[j].raw)
			j++
		}
		if len(parts) == 0 {
			continue
		}
		if match != "" {
			return ParseResult{Reason: "two names"}
		}
		mf, reason := nameField(v, res)
		if mf == "" {
			return ParseResult{Reason: reason}
		}
		match, matchField = strings.Join(parts, " "), mf
		toks[i].used = true
		for k := i + 1; k < j; k++ {
			toks[k].used = true
		}
	}
	if match != "" {
		filters = append(filters, Filter{Field: matchField, Op: "eq", Match: match})
	}

	// 8. every remaining token must be a stopword (or a period-only word
	// when a period was found: "nuevos hoy" = created today).
	for _, t := range toks {
		if t.used || stopwords[t.norm] || (period != nil && periodOnly[t.norm]) {
			continue
		}
		return ParseResult{Reason: "unknown word: " + t.raw}
	}
	if op == "" {
		// "las órdenes de hoy", "órdenes pendientes": a bare noun phrase lists.
		op = "list"
	}
	p := Plan{Kind: op, Resource: res.Name, Filters: filters, Period: period, GroupBy: groupBy}
	if op == "list" && groupBy != "" {
		// "cuáles … por estado" reads as a breakdown: count by the field.
		p.Kind = "count"
	}
	if op == "sum" || op == "avg" {
		p.Field = field
	}
	if err := p.Validate(v); err != nil {
		return ParseResult{Reason: "invalid: " + err.Error()}
	}
	return ParseResult{Plan: p, Sure: true}
}

// nameForms derives the forms a resource is named by: the schema name with
// underscores as spaces, its singular by stripping a plural suffix, and the
// last word alone for a multi-word name ("orden_lineas" → "lineas").
func nameForms(name string) []string {
	base := strings.ReplaceAll(normalize(name), "_", " ")
	forms := []string{base}
	add := func(s string) {
		if s != "" && !contains(forms, s) {
			forms = append(forms, s)
		}
	}
	add(singularES(base))
	add(base + "s")
	add(base + "es")
	return forms
}

func singularES(w string) string {
	switch {
	case strings.HasSuffix(w, "ones"), strings.HasSuffix(w, "enes"), strings.HasSuffix(w, "ores"), strings.HasSuffix(w, "ales"), strings.HasSuffix(w, "iles"), strings.HasSuffix(w, "ades"):
		return strings.TrimSuffix(w, "es")
	case strings.HasSuffix(w, "s") && !strings.HasSuffix(w, "ss") && len(w) > 3:
		return strings.TrimSuffix(w, "s")
	}
	return w
}

// fieldByForm finds a field by its normalized name (underscores as spaces),
// singular or plural.
func (r *Resource) fieldByForm(word string) *Field {
	for _, f := range r.Fields {
		base := strings.ReplaceAll(normalize(f.Name), "_", " ")
		if word == base || word == singularES(base) || word == base+"s" {
			return f
		}
	}
	return nil
}

// fieldOfValue returns the single field of res declaring val as an enum
// member, nil when none or more than one.
func fieldOfValue(res *Resource, val string) *Field {
	var found *Field
	for _, f := range res.Fields {
		if contains(f.Enum, val) {
			if found != nil {
				return nil
			}
			found = f
		}
	}
	return found
}

// nameField decides WHERE a proper name applies: the ONE relation of res whose
// target has a name-like label field; else res's own name-like field.
func nameField(v *Vocabulary, res *Resource) (string, string) {
	var cands []string
	for _, f := range res.Fields {
		if f.Relation == "" {
			continue
		}
		t := v.Resource(f.Relation)
		if t == nil {
			continue
		}
		if lf := t.LabelFields(); len(lf) > 0 && nameRank(lf[0]) < len(primaryParts) {
			cands = append(cands, f.Name)
		}
	}
	sort.Strings(cands)
	switch len(cands) {
	case 1:
		return cands[0], ""
	case 0:
		if lf := res.LabelFields(); len(lf) > 0 && nameRank(lf[0]) < len(primaryParts) {
			return lf[0], ""
		}
		return "", "name with no place to match"
	default:
		return "", "name could match " + strings.Join(cands, " or ")
	}
}

// consumePhrase marks the first unconsumed occurrence of phrase (normalized
// words) as used and reports whether it was found.
func consumePhrase(toks []token, phrase string) bool {
	words := strings.Fields(phrase)
	if len(words) == 0 {
		return false
	}
outer:
	for i := 0; i+len(words) <= len(toks); i++ {
		for k, w := range words {
			if toks[i+k].used || toks[i+k].norm != w {
				continue outer
			}
		}
		for k := range words {
			toks[i+k].used = true
		}
		return true
	}
	return false
}
