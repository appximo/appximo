package ask

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"github.com/appximo/appximo/pkg/schema"
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
//   1. exactly ONE resource of the role's vocabulary is named — by its schema
//      name (singular/plural, underscores as spaces) or by an ALIAS the schema
//      declares for it (VOZ-AHORRO-S2: «pedidos» for ordenes, «mascotas» for
//      pets — declared, never wired; a word the schema does not declare is a
//      word the parser does not know);
//   2. exactly ONE operation is recognized (count / list / sum / avg / group
//      by), or none with a bare "the <resource> …" which reads as a list;
//   3. EVERY word of the question is consumed by a recognized piece — a
//      stopword, the operation, the resource, a declared enum/state value of
//      that resource or one of ITS declared aliases (matched whole,
//      accent-insensitive, plural tolerated), a period phrase, a group-by
//      field, or a proper name introduced by a preposition (or by «llamado»,
//      or by the resource a relation points at: «del cliente Ana Gómez»). ONE
//      leftover word ("vendimos", "vigentes", "ignora") and the question goes
//      to the model;
//   4. each enum value maps to exactly one field of the resource;
//   5. a proper name maps to exactly one place: the single relation of the
//      resource whose target has a name-like label, else the resource's own
//      name-like field; two candidates → the model — unless the sentence
//      NAMED the relation's target («las citas del paciente Ana»);
//   6. a sum/average names a numeric field, or the resource has ONE obvious
//      amount (MoneyField);
//   7. the resulting plan passes the same validation as a model plan.
//
// Anything else is NOT SURE and falls through. The parser never guesses to
// save a call: a wrong plan costs more than three tenths of a cent.
//
// The mirror rule (VOZ-AHORRO-S2 Part C): the parser may also be sure a
// sentence is NOT a data question — a stray answer to a confirmation that no
// longer exists («sí pero mejor el viernes»), a greeting, a request for help,
// a bare proper name — and answer it at zero cost. That verdict is given ONLY
// when the sentence contains NOTHING the grammar could execute: no operation
// word, no schema word (resource, alias, value, field), no period, no write
// verb. A sentence with any of those reaches the model even if the rest is
// noise, because the model's own knowledge may still map it («cuántos
// pedidos hay» on a schema that declares no alias for pedidos). Discarding a
// legitimate question is worse than three cents.
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
	// Discard is set (with Sure and an `unclear` plan) when the parser is sure
	// the sentence is NOT a data question: "stray_confirmation" | "greeting" |
	// "help" | "bare_name". The reply is composed without the model.
	Discard string
}

var (
	stopwords = set("de", "del", "la", "las", "el", "los", "un", "una", "unos", "unas", "en", "a", "al", "y", "e", "o", "u",
		"que", "me", "mi", "mis", "nos", "nuestro", "nuestra", "nuestros", "nuestras", "tengo", "tenemos", "tenes", "tienes", "tiene",
		"hay", "existen", "existe", "estan", "esta", "son", "es", "actualmente", "ahora", "ya", "todas", "todos", "toda", "todo",
		"por", "favor", "decime", "dime", "digame", "quiero", "quisiera", "necesito", "saber", "ver", "podes", "puedes", "podrias",
		"cargados", "cargadas", "hechas", "hechos", "actuales", "actual", "con", "estado", "tipo", "en", "total",
		"para", "sobre", "cual", "cuales", "hubo", "hubieron", "llegaron", "entraron", "vinieron", "quedan", "queda", "hoy",
		"alguna", "alguno", "algunas", "algunos", "algun", "se",
		// agenda function words (MOTOR-AGENDA-S1): «tengo algo», «cuándo estoy libre», «estoy ocupado»
		"algo", "cuando", "estoy", "estamos", "ocupado", "ocupada", "agendado", "agendada", "programado", "programada")
	countWords = set("cuantos", "cuantas", "cuanto", "cuanta", "numero", "cantidad", "conta", "contame", "cuenta", "cuentame", "total")
	listWords  = set("lista", "listar", "listame", "listado", "mostrame", "muestrame", "mostra", "mostrar", "muestra", "dame", "traeme", "pasame", "cuales", "que", "ver")
	// lastWords («los últimos 5 pedidos») list the most recent rows — the list
	// already sorts by the creation timestamp, newest first; a number right
	// after bounds it. Generic Spanish, no domain word.
	lastWords = set("ultimos", "ultimas", "ultimo", "ultima")
	sumWords  = set("suma", "suman", "sumatoria", "sumame", "sumar")
	avgWords  = set("promedio", "media")
	// deleteVerbs are refused deterministically on every channel: the voice
	// never deletes, sends or moves files (VOZ-ESCRITURAS-S1 keeps this).
	deleteVerbs = set("borra", "borrar", "borralo", "borrala", "elimina", "eliminar", "eliminalo", "eliminala", "quita", "quitar", "manda", "mandar", "envia", "enviar", "sube", "subir", "baja", "bajar")
	// writeVerbs are refused when the vocabulary is read-only; on a writable
	// one they go to the model, which may plan a create/update (confirmed
	// before executing).
	writeVerbs = set("cancela", "cancelar", "crea", "crear", "agenda", "agendar", "cambia", "cambiar",
		"modifica", "modificar", "edita", "editar", "actualiza", "actualizar", "marca", "marcar", "marcá",
		"pone", "pon", "poner", "agrega", "agregar", "registra", "registrar", "anota", "anotar", "programa", "programar", "anotame", "agregame", "cambiame", "ponele", "pasa", "pasar", "pasala", "pasalo")
	// prepositions introduce a proper name («de Ana», «para Marta»); so do the
	// participles of «llamar» («el cliente llamado Carlos», «que se llama
	// Carlos» — «se» is a stopword).
	prepositions = set("de", "del", "para", "con", "a", "llamado", "llamada", "llamados", "llamadas", "llama", "llame")
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
	{"de antier", "day_before_yesterday"}, {"antier", "day_before_yesterday"}, {"de anteayer", "day_before_yesterday"}, {"anteayer", "day_before_yesterday"},
	// The future (MOTOR-AGENDA-S1): what an agenda is asked about.
	{"pasado manana", "day_after_tomorrow"}, {"de manana", "tomorrow"}, {"manana", "tomorrow"},
	{"la semana que viene", "next_week"}, {"semana que viene", "next_week"}, {"la proxima semana", "next_week"}, {"proxima semana", "next_week"},
	{"el lunes", "next_monday"}, {"el martes", "next_tuesday"}, {"el miercoles", "next_wednesday"}, {"el jueves", "next_thursday"}, {"el viernes", "next_friday"}, {"el sabado", "next_saturday"}, {"el domingo", "next_sunday"},
}

// freeWords ask for the gaps of an agenda («cuándo estoy libre», «qué huecos
// tengo el jueves») — generic Spanish, no domain word.
var freeWords = set("libre", "libres", "hueco", "huecos", "disponible", "disponibles", "desocupado", "desocupada")

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
	// 0a. An obligation in the first person — «tengo que comprar pintura»
	// (APP-AGENDA-S2, VOZ-17): no write verb, yet the most common sentence an
	// agenda hears. Settled before the discard rule (nothing in it is an
	// "executable word") and before the write-verb walk.
	if pr := parseObligation(question, v); pr.Sure {
		return pr
	}
	// 0. Sure it is NOT a data question (Part C): a stray confirmation, a
	// greeting, a help request, a bare name — only when nothing in the
	// sentence could be executed. Otherwise the rest of the parser decides.
	if d := preDiscard(toks, v); d != "" {
		return ParseResult{Plan: Plan{Kind: "unclear", Reason: discardReasonES(d)}, Sure: true, Discard: d, Reason: "discard: " + d}
	}
	// A delete/send verb anywhere → refused deterministically. A write verb
	// → refused too on a read-only vocabulary; on a writable one the parser
	// steps aside (not sure) and the model plans the create/update.
	for _, t := range toks {
		if deleteVerbs[t.norm] {
			return ParseResult{Plan: Plan{Kind: "write", Reason: "verbo de escritura: " + t.raw}, Sure: true}
		}
		if writeVerbs[t.norm] || scheduleVerbs[t.norm] {
			if v != nil && v.Writable() {
				// The ONE write shape the parser settles itself (VOZ-ESCRITURAS-S1):
				// a state transition of one row — "marcá como hecha la tarea de
				// Fabián", "cancelá el pedido 1003", "pasá a pagada la orden de
				// Marta". Anything else (a create with free text) is the model's.
				if pr := parseTransition(question, v); pr.Sure {
					return pr
				}
				// The second write shape (MOTOR-AGENDA-S1): a block on the agenda
				// — "agendá reunión con Fabián mañana de 4 a 5".
				if pr := parseSchedule(question, v); pr.Sure {
					return pr
				}
				return ParseResult{Reason: "write verb: " + t.norm}
			}
			return ParseResult{Plan: Plan{Kind: "write", Reason: "verbo de escritura: " + t.raw}, Sure: true}
		}
	}

	// 1. period phrases (multi-word first) — consume tokens.
	var period *Period
	joined := joinedNorms(toks)
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

	// 1a. a clock («a las 4») narrows a period to one instant on an agenda
	// resource; «libre»/«huecos» ask for the gaps (MOTOR-AGENDA-S1).
	at := ""
	if period != nil {
		if span, ok := consumeTimeSpan(toks); ok && span.end < 0 {
			at = clockString(span.start)
		}
	}
	free := false
	for i := range toks {
		if !toks[i].used && freeWords[toks[i].norm] {
			toks[i].used, free = true, true
		}
	}

	// 1b. MULTI-WORD values first, across every readable resource: an "orden
	// pendiente de pago" must not read "pago" as the resource pagos. The same
	// phrase may be a value of SEVERAL resources («sin pagar» as an alias on
	// orders and on invoices), so each consumed phrase remembers every
	// (resource, field, value) it could mean; the one of the resource the
	// sentence names is kept after step 2.
	groups := consumeMultiWordValues(toks, v)

	// 2. the resource: exactly one, by schema name or declared alias. Two
	// resources are still one question when the second is the target of the
	// first's relation and introduces a name («las órdenes del cliente Ana»).
	res, labelField, labelPos, reason := findResource(toks, v)
	var filters []Filter
	seenField := map[string]bool{}
	if reason == "no resource named" {
		// A value that exists in exactly ONE place names its resource:
		// «cuántos perros hay» → pets.species = dog (the alias «perro» is
		// declared once). Two places («pendientes» on orders and on
		// invoices) → not sure.
		if r, f, ok := impliedResource(toks, v, groups); ok {
			res, reason = r, ""
			if f.Field != "" {
				seenField[f.Field] = true
				filters = append(filters, f)
			}
		} else if r := agendaResource(v); r != nil && (period != nil || free) {
			// «qué tengo mañana», «cuándo estoy libre el jueves»: no resource
			// word, a day — the ONE agenda resource is what is meant.
			res, reason = r, ""
		}
	}
	if reason != "" {
		return ParseResult{Reason: reason}
	}
	for _, g := range groups {
		var mine []valueHint
		for _, h := range g {
			if h.res == res {
				mine = append(mine, h)
			}
		}
		switch {
		case len(mine) == 0:
			return ParseResult{Reason: "value " + fmt.Sprint(g[0].filter.Value) + " belongs to " + g[0].res.Name + ", not " + res.Name}
		case len(mine) > 1:
			return ParseResult{Reason: "value " + fmt.Sprint(mine[0].filter.Value) + " belongs to two fields"}
		}
		h := mine[0]
		if seenField[h.filter.Field] {
			return ParseResult{Reason: "two values for " + h.filter.Field}
		}
		seenField[h.filter.Field] = true
		filters = append(filters, h.filter)
	}

	// 3. the operation.
	op := ""
	limit := 0
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
		case lastWords[t.norm]:
			// «los últimos 5 pedidos»: a list, newest first, bounded by the
			// number that follows (1..MaxListLimit) when there is one.
			o = "list"
			if i+1 < len(toks) && !toks[i+1].used {
				if n, err := strconv.Atoi(toks[i+1].norm); err == nil && n >= 1 && n <= MaxListLimit {
					limit = n
					toks[i+1].used = true
				}
			}
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

	// 5. single-word enum / state values of the resource's fields — declared
	// values and their aliases — whole, plural-tolerant (the multi-word ones
	// were consumed in 1b).
	for _, f := range res.Fields {
		if len(f.Enum) == 0 {
			continue
		}
		for _, vf := range f.valueForms() {
			if vf.multi || !consumePhrase(toks, vf.form) {
				continue
			}
			if seenField[f.Name] {
				return ParseResult{Reason: "two values for " + f.Name}
			}
			if !vf.alias && fieldOfValue(res, vf.val) == nil {
				return ParseResult{Reason: "value " + vf.val + " belongs to two fields"}
			}
			seenField[f.Name] = true
			filters = append(filters, Filter{Field: f.Name, Op: "eq", Value: vf.val})
			break
		}
	}

	// 5b. a BOOL field named by its own word («tareas urgentes» on a bool
	// `urgente`): the field's name forms mean true; «no» / «sin» right before
	// it means false. Nothing domain-specific lives here — the schema named
	// the flag, and that name is the word (VOZ-18).
	for _, f := range res.Fields {
		if f.Type != "bool" || seenField[f.Name] {
			continue
		}
		for i := range toks {
			if toks[i].used || !boolNamed(f, toks[i].norm) {
				continue
			}
			val := true
			if i > 0 && !toks[i-1].used && (toks[i-1].norm == "no" || toks[i-1].norm == "sin") {
				toks[i-1].used = true
				val = false
			}
			toks[i].used = true
			seenField[f.Name] = true
			filters = append(filters, Filter{Field: f.Name, Op: "eq", Value: val})
			break
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

	// 7. a proper name: the run of unconsumed, unknown tokens right after a
	// preposition / «llamado» — or right after the relation's target the
	// sentence named («del cliente Ana Gómez», settled in step 2).
	match := ""
	matchField := ""
	if labelPos >= 0 {
		parts := nameRun(toks, labelPos, nil)
		if len(parts) == 0 {
			return ParseResult{Reason: "two resources: " + res.Name + ", " + v.Resource(res.Field(labelField).Relation).Name}
		}
		match, matchField = strings.Join(parts, " "), labelField
	}
	for i := 0; i < len(toks); i++ {
		if toks[i].used || !prepositions[toks[i].norm] {
			continue
		}
		parts := nameRun(toks, i+1, nil)
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
	}
	// 7b. a Capitalized run that is not the first word («cuántos pedidos
	// tiene Juan Peres»): dictation capitalizes proper names; the first word
	// is always capitalized, so it never counts.
	if match == "" {
		for i := 1; i < len(toks); i++ {
			if toks[i].used || stopwords[toks[i].norm] || !startsUpper(toks[i].raw) {
				continue
			}
			parts := nameRun(toks, i, nil)
			if len(parts) == 0 {
				continue
			}
			mf, reason := nameField(v, res)
			if mf == "" {
				return ParseResult{Reason: reason}
			}
			match, matchField = strings.Join(parts, " "), mf
			break
		}
	}
	// 7c. a CODE («el pedido ORD-1003», «la factura 4521»): a token with a
	// digit, matched against the resource's own identifier field (numero,
	// codigo, sku, placa, referencia) through the same matcher — exact
	// codes score 1.0, a near miss is asked about.
	if match == "" {
		if code, field := codeToken(toks, res); code != "" {
			match, matchField = code, field
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
	if at != "" || free {
		if res.Range() == nil {
			return ParseResult{Reason: "clock/free on a resource without a range"}
		}
		if period == nil {
			return ParseResult{Reason: "free without a period"}
		}
		period.At = at
	}
	if free {
		op = "free"
	}
	p := Plan{Kind: op, Resource: res.Name, Filters: filters, Period: period, GroupBy: groupBy, Limit: limit}
	if op == "list" && groupBy != "" {
		// "cuáles … por estado" reads as a breakdown: count by the field.
		p.Kind = "count"
	}
	if p.Kind != "list" && p.Kind != "free" {
		p.Limit = 0
	}
	if op == "sum" || op == "avg" {
		p.Field = field
	}
	if err := p.Validate(v); err != nil {
		return ParseResult{Reason: "invalid: " + err.Error()}
	}
	return ParseResult{Plan: p, Sure: true}
}

// nameRun collects the raw tokens of a proper name starting at i: unconsumed,
// not a stopword, not an operation word, not one of extra stops — and marks
// them used. Returns nil when there is none.
func nameRun(toks []token, i int, extraStop map[string]bool) []string {
	var parts []string
	j := i
	for j < len(toks) && !toks[j].used && !stopwords[toks[j].norm] && !countWords[toks[j].norm] && !listWords[toks[j].norm] && !extraStop[toks[j].norm] {
		parts = append(parts, toks[j].raw)
		j++
	}
	for k := i; k < j; k++ {
		toks[k].used = true
	}
	return parts
}

func joinedNorms(toks []token) string {
	norms := make([]string, len(toks))
	for i, t := range toks {
		norms[i] = t.norm
	}
	return " " + strings.Join(norms, " ") + " "
}

// valueHint is one meaning of a consumed multi-word value phrase.
type valueHint struct {
	res    *Resource
	filter Filter
}

// consumeMultiWordValues consumes every multi-word value phrase (declared
// values and aliases, longest first) across every readable resource; each
// consumed phrase yields the group of meanings it has.
func consumeMultiWordValues(toks []token, v *Vocabulary) [][]valueHint {
	phrases := map[string][]valueHint{}
	var order []string
	for _, name := range v.ResourceNames() {
		r := v.Resource(name)
		for _, f := range r.Fields {
			for _, vf := range f.valueForms() {
				if !vf.multi {
					continue
				}
				if _, ok := phrases[vf.form]; !ok {
					order = append(order, vf.form)
				}
				phrases[vf.form] = append(phrases[vf.form], valueHint{res: r, filter: Filter{Field: f.Name, Op: "eq", Value: vf.val}})
			}
		}
	}
	sort.SliceStable(order, func(i, j int) bool {
		wi, wj := len(strings.Fields(order[i])), len(strings.Fields(order[j]))
		if wi != wj {
			return wi > wj
		}
		if len(order[i]) != len(order[j]) {
			return len(order[i]) > len(order[j])
		}
		return order[i] < order[j]
	})
	var groups [][]valueHint
	for _, form := range order {
		if consumePhrase(toks, form) {
			groups = append(groups, dedupeHints(phrases[form]))
		}
	}
	return groups
}

// dedupeHints keeps one hint per (resource, field, value): the plural and the
// «de» variant of one value are the same meaning.
func dedupeHints(hs []valueHint) []valueHint {
	seen := map[string]bool{}
	var out []valueHint
	for _, h := range hs {
		k := h.res.Name + "\x00" + h.filter.Field + "\x00" + fmt.Sprint(h.filter.Value)
		if seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, h)
	}
	return out
}

// codeToken finds ONE unconsumed token that carries a digit («ORD-1003»,
// «4521», «SKU-9») and the resource's identifier field it can be matched
// against: the first secondary label (numero, codigo, sku, placa,
// referencia…) — a name-like field is not a code. Marks the token used.
func codeToken(toks []token, res *Resource) (string, string) {
	field := ""
	for _, f := range res.Fields {
		if !f.IsText() || len(f.Enum) > 0 {
			continue
		}
		if r := nameRank(f.Name); r >= len(primaryParts) && r < len(nameishParts) {
			field = f.Name
			break
		}
	}
	if field == "" {
		return "", ""
	}
	for i := range toks {
		t := &toks[i]
		if t.used || stopwords[t.norm] || !strings.ContainsAny(t.norm, "0123456789") {
			continue
		}
		t.used = true
		return t.raw, field
	}
	return "", ""
}

func startsUpper(raw string) bool {
	for _, r := range raw {
		return unicode.IsUpper(r)
	}
	return false
}

// impliedResource names the resource by a value that exists in exactly one
// (resource, field, value) of the vocabulary: a multi-word group already
// consumed in 1b with one meaning, or one single-word value form present in
// the sentence. Returns the resource and the filter it implies.
func impliedResource(toks []token, v *Vocabulary, groups [][]valueHint) (*Resource, Filter, bool) {
	var hits []valueHint
	for _, g := range groups {
		hits = append(hits, g...)
	}
	if len(groups) == 0 {
		for _, name := range v.ResourceNames() {
			r := v.Resource(name)
			for _, f := range r.Fields {
				for _, vf := range f.valueForms() {
					if vf.multi {
						continue
					}
					if _, ok := findPhrase(toks, []string{vf.form}); ok {
						hits = append(hits, valueHint{res: r, filter: Filter{Field: f.Name, Op: "eq", Value: vf.val}})
					}
				}
			}
		}
		hits = dedupeHints(hits)
	}
	if len(hits) != 1 {
		return nil, Filter{}, false
	}
	h := hits[0]
	if len(groups) == 0 {
		// consume the single-word form now (the multi-word one already was)
		for _, vf := range h.res.Field(h.filter.Field).valueForms() {
			if !vf.multi && vf.val == h.filter.Value && consumePhrase(toks, vf.form) {
				break
			}
		}
	}
	return h.res, h.filter, true
}

// resourceMention is one place a resource was named.
type resourceMention struct {
	res        *Resource
	start, end int // token range [start, end)
}

// findResource consumes every resource mention (schema names and aliases,
// every form) and settles the ONE resource of the sentence. Two distinct
// resources are one question only when one is the target of the other's
// single relation and its mention is immediately followed by a name
// («las órdenes del cliente Ana Gómez»): the relation's source is the
// resource, the target's word introduces the name. Returns the resource, the
// relation field + the token position the name starts at (labelPos = -1 when
// none), or a reason.
func findResource(toks []token, v *Vocabulary) (res *Resource, labelField string, labelPos int, reason string) {
	var mentions []resourceMention
	for _, name := range v.ResourceNames() {
		r := v.Resource(name)
		for _, form := range r.NameForms() {
			words := strings.Fields(form)
			if start, ok := findPhrase(toks, words); ok {
				for k := range words {
					toks[start+k].used = true
				}
				mentions = append(mentions, resourceMention{res: r, start: start, end: start + len(words)})
			}
		}
	}
	var distinct []*Resource
	for _, m := range mentions {
		dup := false
		for _, d := range distinct {
			if d == m.res {
				dup = true
			}
		}
		if !dup {
			distinct = append(distinct, m.res)
		}
	}
	switch len(distinct) {
	case 0:
		return nil, "", -1, "no resource named"
	case 1:
		return distinct[0], "", -1, ""
	case 2:
		a, b := distinct[0], distinct[1]
		if f, pos := labelIntro(toks, v, a, b, mentions); f != "" {
			return a, f, pos, ""
		}
		if f, pos := labelIntro(toks, v, b, a, mentions); f != "" {
			return b, f, pos, ""
		}
		return nil, "", -1, "two resources: " + a.Name + ", " + b.Name
	}
	names := make([]string, 0, len(distinct))
	for _, d := range distinct {
		names = append(names, d.Name)
	}
	return nil, "", -1, "two resources: " + strings.Join(names, ", ")
}

// labelIntro reports whether tgt's mention introduces a name for src's single
// relation to tgt («órdenes del CLIENTE Ana»): src has exactly one relation
// field pointing at tgt, tgt has a name-like label, and the token right after
// tgt's mention starts a name run.
func labelIntro(toks []token, v *Vocabulary, src, tgt *Resource, mentions []resourceMention) (string, int) {
	field := ""
	for _, f := range src.Fields {
		if f.Relation == tgt.Name {
			if field != "" {
				return "", -1 // two relations to the same target: not sure
			}
			field = f.Name
		}
	}
	if field == "" {
		return "", -1
	}
	if lf := tgt.LabelFields(); len(lf) == 0 || nameRank(lf[0]) >= len(primaryParts) {
		return "", -1
	}
	for _, m := range mentions {
		if m.res != tgt {
			continue
		}
		j := m.end
		if j < len(toks) && !toks[j].used && !stopwords[toks[j].norm] && !countWords[toks[j].norm] && !listWords[toks[j].norm] && !prepositions[toks[j].norm] {
			return field, j
		}
	}
	return "", -1
}

func singularES(w string) string { return schema.SingularES(w) }

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

// findPhrase locates the first unconsumed occurrence of words in toks.
func findPhrase(toks []token, words []string) (int, bool) {
	if len(words) == 0 {
		return 0, false
	}
outer:
	for i := 0; i+len(words) <= len(toks); i++ {
		for k, w := range words {
			if toks[i+k].used || toks[i+k].norm != w {
				continue outer
			}
		}
		return i, true
	}
	return 0, false
}

// consumePhrase marks the first unconsumed occurrence of phrase (normalized
// words) as used and reports whether it was found.
func consumePhrase(toks []token, phrase string) bool {
	words := strings.Fields(phrase)
	start, ok := findPhrase(toks, words)
	if !ok {
		return false
	}
	for k := range words {
		toks[start+k].used = true
	}
	return true
}

// ── sure it is NOT a question (Part C) ────────────────────────────────────

var (
	// yesNoLead are the words a stray answer to a confirmation starts with.
	yesNoLead = set("si", "sí", "no", "ok", "okey", "okay", "dale", "listo", "bueno", "vale", "claro", "mejor")
	// greetingCore are the words that make a sentence a greeting or a thanks;
	// greetingFill are the words allowed beside them.
	greetingCore    = set("hola", "holi", "holis", "buenas", "buenos", "gracias", "chau", "chao", "adios", "saludos", "hey", "ey", "genial", "perfecto", "excelente", "bien", "joya", "barbaro", "buenisimo")
	greetingPhrases = set("que tal", "como estas", "como esta", "como va", "como andas", "como anda", "todo bien", "que hay", "que mas", "que hubo")
	greetingFill    = set("dias", "dia", "tardes", "tarde", "noches", "noche", "muchas", "mil", "que", "tal", "como", "estas", "esta", "va", "todo", "ok", "dale", "listo", "muy", "bueno", "buena", "y", "vos", "usted", "hasta", "luego", "nos", "vemos")
	// helpPhrases are the exact (normalized) ways an owner asks what the bot
	// can do; helpPrefixes catch the same intent with a tail.
	helpPhrases  = set("ayuda", "help", "que puedo preguntar", "que puedo preguntarte", "que te puedo preguntar", "que puedo pedir", "que puedo pedirte", "que sabes hacer", "que sabes", "que podes hacer", "que puedes hacer", "que haces", "como funciona", "como funcionas", "como te uso", "que preguntas puedo hacer", "que preguntas respondes", "que me podes decir", "que me puedes decir", "que comandos hay", "cuales son los comandos", "instrucciones", "menu")
	helpPrefixes = []string{"que puedo preguntar", "que te puedo preguntar", "que puedo pedir", "que sabes hacer", "que podes hacer", "que puedes hacer", "como funciona", "que preguntas puedo", "que comandos"}
)

// preDiscard returns the discard code when the sentence contains NOTHING the
// grammar could execute AND matches a recognizable non-question shape. Any
// operation word, schema word (resource, alias, value, field), period or
// write verb means "let the parser and, if needed, the model decide".
func preDiscard(toks []token, v *Vocabulary) string {
	if v == nil {
		return ""
	}
	if hasExecutableWord(toks, v) {
		return ""
	}
	joined := strings.TrimSpace(joinedNorms(toks))
	if helpPhrases[joined] {
		return "help"
	}
	if greetingPhrases[joined] {
		return "greeting"
	}
	for _, p := range helpPrefixes {
		if strings.HasPrefix(joined, p) {
			return "help"
		}
	}
	core := false
	all := true
	for _, t := range toks {
		switch {
		case greetingCore[t.norm]:
			core = true
		case greetingFill[t.norm] || stopwords[t.norm]:
		default:
			all = false
		}
	}
	if core && all {
		return "greeting"
	}
	if len(toks) >= 2 && yesNoLead[toks[0].norm] {
		return "stray_confirmation"
	}
	if len(toks) >= 2 {
		capitalized := true
		for _, t := range toks {
			r := []rune(t.raw)[0]
			if !unicode.IsUpper(r) {
				capitalized = false
				break
			}
		}
		if capitalized {
			return "bare_name"
		}
	}
	return ""
}

// hasExecutableWord reports whether any token or phrase of the sentence is
// something the grammar could act on.
func hasExecutableWord(toks []token, v *Vocabulary) bool {
	words, phrases := v.lexicon()
	joined := joinedNorms(toks)
	agenda := agendaResource(v) != nil
	for _, t := range toks {
		n := t.norm
		// «que», «cuáles» and «ver» list only beside a resource; alone they
		// are the function words of «qué puedo preguntar» — not executable.
		if countWords[n] || (listWords[n] && n != "que" && n != "cuales" && n != "ver") || lastWords[n] || sumWords[n] || avgWords[n] || deleteVerbs[n] || writeVerbs[n] || periodOnly[n] || words[n] {
			return true
		}
		if n == "por" || n == "hoy" || n == "ayer" || n == "antier" || n == "anteayer" {
			return true
		}
		// A day to come («mañana», «el lunes») or «libre» is a question only
		// where there is an agenda to ask (MOTOR-AGENDA-S1); elsewhere «sí
		// pero mejor el lunes» stays the stray answer it is.
		if agenda && (n == "manana" || freeWords[n]) {
			return true
		}
	}
	for _, p := range phrases {
		if strings.Contains(joined, " "+p+" ") {
			return true
		}
	}
	for _, pp := range periodPhrases {
		if !strings.Contains(joined, " "+pp.phrase+" ") {
			continue
		}
		if futureRange(pp.token) && !agenda {
			continue
		}
		return true
	}
	return false
}

// discardReasonES words the discard for the log/JSON reason field.
func discardReasonES(code string) string {
	switch code {
	case "stray_confirmation":
		return "parece la respuesta a una confirmación, no una pregunta"
	case "greeting":
		return "es un saludo"
	case "help":
		return "pide ayuda"
	case "bare_name":
		return "es solo un nombre, sin qué preguntar"
	}
	return code
}

// transitionVerbs introduce a state change; the state itself follows
// ("como hecha", "a pagada", "en cancelada") or is the verb's own stem
// ("cancelá" → cancelada, "confirmá" → confirmada).
var transitionVerbs = set("marca", "marcá", "marcar", "marcame", "marcala", "marcalo", "pasa", "pasá", "pasar", "pasala", "pasalo", "pone", "poné", "poner", "ponele", "ponela", "ponelo",
	"cambia", "cambiá", "cambiar", "cambiale", "deja", "dejá", "dejar", "dejala", "dejalo", "actualiza", "actualizá", "actualizar", "da", "dá", "dar", "dale")
var transitionLinkers = set("como", "a", "en", "por", "estado", "el", "la", "lo", "le", "ya", "esta", "está")

// parseTransition settles "<verb> [como|a|en] <state> <resource> [de <name>]"
// and "<state-verb> <resource> [de <name>]" into an update of the resource's
// state field on the row the name (or an enum value) identifies. SURE only
// when every word is consumed, one resource, one target state, at most one
// name; the confirmation still gates the write. Resources and states are
// recognized by their schema names AND their declared aliases.
func parseTransition(question string, v *Vocabulary) ParseResult {
	toks := tokenize(question)
	// the resource, exactly one, updatable
	res, labelField, labelPos, reason := findResource(toks, v)
	if reason != "" {
		return ParseResult{Reason: reason}
	}
	if !res.CanUpdate {
		return ParseResult{Reason: "role may not update " + res.Name}
	}
	sf := res.StateField()
	if sf == nil {
		return ParseResult{Reason: res.Name + " has no state field"}
	}
	// the target state: an explicit value (or one of its aliases), else the
	// verb's stem
	target := ""
	for _, vf := range sf.valueForms() {
		if consumePhrase(toks, vf.form) {
			if target != "" && target != vf.val {
				return ParseResult{Reason: "two values for " + sf.Name}
			}
			target = vf.val
		}
	}
	verbSeen := false
	for i := range toks {
		t := &toks[i]
		if t.used {
			continue
		}
		if transitionVerbs[t.norm] {
			verbSeen, t.used = true, true
			continue
		}
		if !writeVerbs[t.norm] {
			continue
		}
		// a state-named verb ("cancelá" → cancelada, "confirmá" → confirmada):
		// its first five letters are a prefix of exactly one state
		stem := t.norm
		if len(stem) > 5 {
			stem = stem[:5]
		}
		if len(stem) < 4 {
			return ParseResult{Reason: "write verb: " + t.norm}
		}
		var hits []string
		for _, val := range sf.Enum {
			if strings.HasPrefix(normalize(val), stem) {
				hits = append(hits, val)
			}
		}
		if len(hits) != 1 || (target != "" && target != hits[0]) {
			return ParseResult{Reason: "write verb: " + t.norm}
		}
		target, verbSeen, t.used = hits[0], true, true
	}
	if !verbSeen || target == "" {
		return ParseResult{Reason: "no transition"}
	}
	// other enum values of the resource narrow the row ("la orden pendiente de Marta")
	var where []Filter
	seen := map[string]bool{}
	for _, f := range res.Fields {
		if len(f.Enum) == 0 || f == sf {
			continue
		}
		for _, vf := range f.valueForms() {
			if !consumePhrase(toks, vf.form) {
				continue
			}
			if seen[f.Name] {
				return ParseResult{Reason: "two values for " + f.Name}
			}
			seen[f.Name] = true
			where = append(where, Filter{Field: f.Name, Op: "eq", Value: vf.val})
			break
		}
	}
	// the name after a preposition (or after the relation's target the
	// sentence named)
	match, matchField := "", ""
	if labelPos >= 0 {
		parts := nameRun(toks, labelPos, transitionLinkers)
		if len(parts) > 0 {
			match, matchField = strings.Join(parts, " "), labelField
		}
	}
	for i := 0; i < len(toks); i++ {
		if toks[i].used || !prepositions[toks[i].norm] {
			continue
		}
		parts := nameRun(toks, i+1, transitionLinkers)
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
	}
	if match == "" {
		if code, field := codeToken(toks, res); code != "" {
			match, matchField = code, field
		}
	}
	if match != "" {
		where = append(where, Filter{Field: matchField, Op: "eq", Match: match})
	}
	if len(where) == 0 {
		return ParseResult{Reason: "transition without a row"}
	}
	for _, t := range toks {
		if t.used || stopwords[t.norm] || transitionLinkers[t.norm] {
			continue
		}
		return ParseResult{Reason: "unknown word: " + t.raw}
	}
	p := Plan{Kind: "update", Resource: res.Name, Where: where, Data: map[string]any{sf.Name: target}}
	if err := p.Validate(v); err != nil {
		return ParseResult{Reason: "invalid: " + err.Error()}
	}
	return ParseResult{Plan: p, Sure: true}
}

// agendaResource is THE agenda among the readable resources: the one whose
// range declares no_overlap when exactly one does (an agenda blocks; a log
// of what happened does not — APP-AGENDA-S1 found «qué tengo mañana» falling
// to the model because a `registros` resource also declared a range), else
// the only resource with a range; nil when none or still several (then a
// word must name it).
func agendaResource(v *Vocabulary) *Resource {
	return pickAgenda(v, func(*Resource) bool { return true })
}

// pickAgenda applies the preference over the resources that pass keep.
func pickAgenda(v *Vocabulary, keep func(*Resource) bool) *Resource {
	var blocking, any []*Resource
	for _, name := range v.order {
		r := v.resources[name]
		rg := r.Range()
		if rg == nil || !keep(r) {
			continue
		}
		any = append(any, r)
		if rg.NoOverlap {
			blocking = append(blocking, r)
		}
	}
	if len(blocking) == 1 {
		return blocking[0]
	}
	if len(any) == 1 {
		return any[0]
	}
	return nil
}

// futureRange reports whether a period token names a day to come.
func futureRange(tok string) bool {
	return tok == "tomorrow" || tok == "day_after_tomorrow" || tok == "next_week" || strings.HasPrefix(tok, "next_")
}

// boolNamed reports whether a single normalized token is one of the forms a
// bool field is named by (its schema name, singular or plural, underscores as
// spaces — a multi-word name never matches one token).
func boolNamed(f *Field, norm string) bool {
	for _, form := range schema.NameForms(f.Name) {
		if !strings.Contains(form, " ") && form == norm {
			return true
		}
	}
	return false
}
