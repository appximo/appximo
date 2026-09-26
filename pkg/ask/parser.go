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
	// summaryWords beside a resource ask for its breakdown by state («resumen
	// de tareas», «resumime los compromisos»); alone they are the day's digest.
	summaryWords = set("resumen", "resumir", "resumime", "resumeme", "resume", "resumi")
	// overdueWords name rows past their due date («tareas vencidas»).
	overdueWords = set("vencida", "vencidas", "vencido", "vencidos", "atrasada", "atrasadas", "atrasado", "atrasados")
	listWords    = set("lista", "listar", "listame", "listado", "mostrame", "muestrame", "mostra", "mostrar", "muestra", "dame", "traeme", "pasame", "cuales", "que", "ver")
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
		"modifica", "modificar", "edita", "editar", "actualiza", "actualizar", "marca", "marcar", "marca",
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
	{"del dia de hoy", "today"}, {"el dia de hoy", "today"}, {"dia de hoy", "today"},
	{"el dia de manana", "tomorrow"}, {"del dia de manana", "tomorrow"}, {"dia de manana", "tomorrow"},
	{"el dia de ayer", "yesterday"}, {"del dia de ayer", "yesterday"}, {"dia de ayer", "yesterday"}, {"del dia", "today"}, {"de la semana", "this_week"}, {"del mes", "this_month"}, {"del ano", "this_year"}, {"del año", "this_year"},
	{"de hoy", "today"}, {"hoy", "today"}, {"de ayer", "yesterday"}, {"ayer", "yesterday"},
	{"de antier", "day_before_yesterday"}, {"antier", "day_before_yesterday"}, {"de anteayer", "day_before_yesterday"}, {"anteayer", "day_before_yesterday"},
	// The future (MOTOR-AGENDA-S1): what an agenda is asked about.
	{"pasado manana", "day_after_tomorrow"}, {"de manana", "tomorrow"}, {"manana", "tomorrow"},
	{"la semana que viene", "next_week"}, {"semana que viene", "next_week"}, {"la proxima semana", "next_week"}, {"proxima semana", "next_week"},
	{"el lunes", "next_monday"}, {"el martes", "next_tuesday"}, {"el miercoles", "next_wednesday"}, {"el jueves", "next_thursday"}, {"el viernes", "next_friday"}, {"el sabado", "next_saturday"}, {"el domingo", "next_sunday"},
	// «del martes» = «el martes»; «el martes pasado» / «del martes pasado» look back; «el martes que viene» / «el próximo martes» look forward, said so.
	{"del lunes", "next_monday"}, {"del martes", "next_tuesday"}, {"del miercoles", "next_wednesday"}, {"del jueves", "next_thursday"}, {"del viernes", "next_friday"}, {"del sabado", "next_saturday"}, {"del domingo", "next_sunday"},
	{"el lunes pasado", "last_monday"}, {"del lunes pasado", "last_monday"}, {"lunes pasado", "last_monday"}, {"el martes pasado", "last_tuesday"}, {"del martes pasado", "last_tuesday"}, {"martes pasado", "last_tuesday"}, {"el miercoles pasado", "last_wednesday"}, {"del miercoles pasado", "last_wednesday"}, {"miercoles pasado", "last_wednesday"}, {"el jueves pasado", "last_thursday"}, {"del jueves pasado", "last_thursday"}, {"jueves pasado", "last_thursday"}, {"el viernes pasado", "last_friday"}, {"del viernes pasado", "last_friday"}, {"viernes pasado", "last_friday"}, {"el sabado pasado", "last_saturday"}, {"del sabado pasado", "last_saturday"}, {"sabado pasado", "last_saturday"}, {"el domingo pasado", "last_sunday"}, {"del domingo pasado", "last_sunday"}, {"domingo pasado", "last_sunday"},
	{"del lunes que viene", "next_monday"}, {"del lunes proximo", "next_monday"}, {"del martes que viene", "next_tuesday"}, {"del martes proximo", "next_tuesday"}, {"del miercoles que viene", "next_wednesday"}, {"del miercoles proximo", "next_wednesday"}, {"del jueves que viene", "next_thursday"}, {"del jueves proximo", "next_thursday"}, {"del viernes que viene", "next_friday"}, {"del viernes proximo", "next_friday"}, {"del sabado que viene", "next_saturday"}, {"del sabado proximo", "next_saturday"}, {"del domingo que viene", "next_sunday"}, {"del domingo proximo", "next_sunday"},
	{"el lunes que viene", "next_monday"}, {"el proximo lunes", "next_monday"}, {"del proximo lunes", "next_monday"}, {"el lunes proximo", "next_monday"}, {"el martes que viene", "next_tuesday"}, {"el proximo martes", "next_tuesday"}, {"del proximo martes", "next_tuesday"}, {"el martes proximo", "next_tuesday"}, {"el miercoles que viene", "next_wednesday"}, {"el proximo miercoles", "next_wednesday"}, {"del proximo miercoles", "next_wednesday"}, {"el miercoles proximo", "next_wednesday"}, {"el jueves que viene", "next_thursday"}, {"el proximo jueves", "next_thursday"}, {"del proximo jueves", "next_thursday"}, {"el jueves proximo", "next_thursday"}, {"el viernes que viene", "next_friday"}, {"el proximo viernes", "next_friday"}, {"del proximo viernes", "next_friday"}, {"el viernes proximo", "next_friday"}, {"el sabado que viene", "next_saturday"}, {"el proximo sabado", "next_saturday"}, {"del proximo sabado", "next_saturday"}, {"el sabado proximo", "next_saturday"}, {"el domingo que viene", "next_sunday"}, {"el proximo domingo", "next_sunday"}, {"del proximo domingo", "next_sunday"}, {"el domingo proximo", "next_sunday"},
}

func init() {
	// longest phrase first: «de la semana que viene» must win over «de la
	// semana» (this_week) — the table is written by meaning, not by length
	sort.SliceStable(periodPhrases, func(i, j int) bool {
		return len(strings.Fields(periodPhrases[i].phrase)) > len(strings.Fields(periodPhrases[j].phrase))
	})
}

// freeWords ask for the gaps of an agenda («cuándo estoy libre», «qué huecos
// tengo el jueves») — generic Spanish, no domain word.
var freeWords = set("libre", "libres", "hueco", "huecos", "disponible", "disponibles", "desocupado", "desocupada")

// articles a proper name may start with when it is a row's title.
var articles = set("el", "la", "los", "las")

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
// Parse settles a sentence when the schema alone decides its shape; else it
// steps aside (not sure) and the model plans. One last resort after every
// shape failed: the DICTATION TAIL (AGENDA-ASISTENTE-S1, seen on the owner's
// real phone) — a Siri shortcut named after the verb («Anota», «Registra»)
// swallows it, so «anotá que la plataforma estuvo caída de 7 a 2» arrives as
// «que la plataforma estuvo caída de 7 a 2». A sentence that starts with
// «que», names no resource, carries no question or operation word and DOES
// carry a clock span is what a log entry sounds like without its verb: it is
// read as «anotá que …» — the note resource, confirmed like any write.
func Parse(question string, v *Vocabulary) ParseResult {
	pr := parseInner(question, v)
	if pr.Sure {
		return pr
	}
	if tail := dictationTail(question, v); tail.Sure {
		return tail
	}
	return pr
}

// dictationTail reads a verb-less «que …» sentence as the note («anotá que
// …») when it has the shape of a log entry: see Parse.
func dictationTail(question string, v *Vocabulary) ParseResult {
	if v == nil || !v.Writable() || (noteResource(v) == nil && taskResource(v) == nil) {
		return ParseResult{Reason: "tail: no note or to-do resource"}
	}
	toks := tokenize(question)
	if len(toks) < 2 {
		return ParseResult{Reason: "tail: too short"}
	}
	// the tail starts with «que» (the swallowed verb's complement) — or has
	// no verb of order at all and tells what HAPPENED in the past («estudio
	// estuvo caído de 7:30 a 2:15», dictated without the «que»)
	if strings.ToLower(toks[0].raw) == "qué" {
		return ParseResult{Reason: "tail: a question («qué …»)"}
	}
	leadingQue := toks[0].norm == "que"
	// «pedir video de Máximo», «pagar la luz mañana»: a sentence that opens
	// with an infinitive is a to-do said without its verb of order
	infinitiveLead := looksInfinitive(toks[0].norm) && !listWords[toks[0].norm] && !countWords[toks[0].norm] && !deleteVerbs[toks[0].norm] && !writeVerbs[toks[0].norm] && taskResource(v) != nil
	body := toks
	if leadingQue {
		body = toks[1:]
	}
	// the shape of a log entry with no verb of order: «que …», a past tense
	// («trabajé»), or — when a dictation dropped the accent («trabaje») — a
	// full time span on a named day; the guards below still refuse any
	// question or operation word and any resource named as a subject
	probe := tokenize(question)
	day, hint := consumeDayPart(probe)
	span, hasSpan := consumeTimeSpanHint(probe, hint)
	// a verb-less span on a day to come («bloqueá mañana de 2 a 4 para
	// estudiar») is an order for the agenda, never a note: a log has no
	// tomorrow (the corpus caught it as a confirmed registro)
	futureDay := day == "tomorrow" || day == "day_after_tomorrow" || day == "next_week"
	if !leadingQue && !infinitiveLead && !hasPreterite(toks) && !(hasSpan && span.end >= 0 && day != "" && !futureDay) {
		return ParseResult{Reason: "tail: not a «que» sentence nor a past-tense one"}
	}
	nr := noteResource(v)
	tr := taskResource(v)
	for _, t := range body {
		n := t.norm
		if countWords[n] || listWords[n] || lastWords[n] || deleteVerbs[n] || writeVerbs[n] || createVerbs[n] || scheduleVerbs[n] || transitionVerbs[n] || freeWords[n] {
			return ParseResult{Reason: "tail: an operation word (" + n + ")"}
		}
		if (nr != nil && fieldByWord(v, nr, n) != nil) || (tr != nil && fieldByWord(v, tr, n) != nil) {
			continue // «área trabajo», «persona Marta»: a datum, not a subject
		}
		for _, name := range v.order {
			if v.namesResource(n, v.resources[name]) {
				return ParseResult{Reason: "tail: names a resource (" + name + ")"}
			}
		}
	}
	// the shape of a log entry: a clock span, or something that HAPPENED (a
	// past-tense verb) on a named day or part of a day
	if infinitiveLead {
		// the to-do: «anota pedir video de Máximo» — the infinitive is the
		// title, a day or «urgente» ride along like in any create
		pr := parseCreate("anota "+question, v)
		if !pr.Sure || pr.Plan.Resource != tr.Name {
			return ParseResult{Reason: "tail: " + pr.Reason}
		}
		return pr
	}
	if nr == nil {
		return ParseResult{Reason: "tail: no note resource"}
	}
	if !hasSpan && !(day != "" && hasPreterite(toks)) {
		return ParseResult{Reason: "tail: no clock span"}
	}
	lead := "anota "
	if !leadingQue {
		lead = "anota que "
	}
	pr := parseCreate(lead+question, v)
	if !pr.Sure || pr.Plan.Resource != nr.Name {
		return ParseResult{Reason: "tail: " + pr.Reason}
	}
	return pr
}

// notedVerbs are the create verbs in the first person past («anoté»,
// «qué hice el martes», «qué pasó el 23 de septiembre» — a log is asked by what happened),
// «registré»): what a person asks back («qué anoté ayer»).
var notedVerbs = set("anote", "registre", "apunte", "guarde", "note", "hice", "hicimos", "paso")

// preteriteForms are the common irregular past forms; regular ones end in
// an accented «é» / «ó» («hablé», «terminó») — Spanish morphology, no
// domain word.
// preteriteNotVerbs are the common words in «-í» that are not a past tense.
var preteriteNotVerbs = map[string]bool{"aquí": true, "allí": true, "ahí": true, "así": true}

var preteriteForms = set("estuvo", "estuve", "estuvimos", "fue", "fui", "fuimos", "tuvo", "tuve", "tuvimos", "hubo", "hizo", "hice", "hicimos", "vino", "vine", "dijo", "dije", "pudo", "pude", "puso", "puse", "quiso", "quise", "supo", "supe", "anduvo", "anduve", "trajo", "traje", "dio", "di", "vio", "vi")

// hasPreterite reports a past-tense verb in the sentence.
func hasPreterite(toks []token) bool {
	for _, t := range toks {
		if preteriteForms[t.norm] {
			return true
		}
		r := strings.ToLower(t.raw)
		if len([]rune(r)) >= 4 && (strings.HasSuffix(r, "ó") || strings.HasSuffix(r, "é")) && r != "qué" {
			return true
		}
		// «reuní», «salí», «escribí»: the first person of an -er/-ir verb
		// ends in «-í» — the owner's own «me reuní con Camilo de 4 a 5»
		// (2026-09-25) fell to the model for want of it. The adverbs that
		// end the same way are not verbs.
		if len([]rune(r)) >= 4 && strings.HasSuffix(r, "í") && !preteriteNotVerbs[r] {
			return true
		}
		if strings.HasSuffix(t.norm, "aron") || strings.HasSuffix(t.norm, "ieron") {
			return true
		}
	}
	return false
}

func parseInner(question string, v *Vocabulary) ParseResult {
	toks := tokenize(question)
	if len(toks) == 0 {
		return ParseResult{Reason: "empty"}
	}
	// 00. Two intentions in one sentence («anotá X y agendá Y», «marcá como
	// hecha … y anotá …»): the first is parsed, the second is carried on the
	// plan and said back to be asked apart (AGENDA-ASISTENTE-S1).
	if first, second := splitIntents(question); second != "" {
		pr := Parse(first, v)
		if pr.Sure && pr.Discard == "" && pr.Plan.IsWrite() {
			pr.Plan.Reason = second
			return pr
		}
	}
	// 0a. An obligation in the first person — «tengo que comprar pintura»
	// (APP-AGENDA-S2, VOZ-17): no write verb, yet the most common sentence an
	// agenda hears. Settled before the discard rule (nothing in it is an
	// "executable word") and before the write-verb walk.
	if pr := parseObligation(question, v); pr.Sure {
		return pr
	}
	// 0a'. Something DONE, in the first person — «ya hice la declaración»,
	// «terminé de lavar el carro», «la tarea del carro está lista»: the
	// transition to the finished state of the row named.
	if pr := parseDone(question, v); pr.Sure {
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
				// The third (AGENDA-ASISTENTE-S1): the FIXED FORM and its
				// tolerant cousins — «crear tarea: X, área Y, urgente»,
				// «anotá que …», «anotá pagar la luz mañana».
				if pr := parseCreate(question, v); pr.Sure {
					return pr
				}
				return ParseResult{Reason: "write verb: " + t.norm}
			}
			return ParseResult{Plan: Plan{Kind: "write", Reason: "verbo de escritura: " + t.raw}, Sure: true}
		}
	}

	// 0b. The resource word first with a colon or an infinitive («tarea:
	// lavar el carro», «Tarea organizar suscripciones») is the fixed form
	// without its verb (AGENDA-ASISTENTE-S1).
	if v != nil && v.Writable() {
		if pr := parseCreate(question, v); pr.Sure {
			return pr
		}
	}

	// 1. period phrases (multi-word first) — consume tokens. A date («del 23
	// de septiembre», «23/09») or a month said alone («de septiembre») is a
	// period too (dates.go); a weekday («el martes», «del martes») is read
	// forward here and turned back once the resource is known (lookBack: a
	// log has no coming Tuesday).
	var period *Period
	// the date/month first: «del mes de agosto» is the month, not «del mes»
	if tok, bad := scanDate(toks); bad != "" {
		return ParseResult{Plan: Plan{Kind: "unclear", Reason: "«" + bad + "» no es una fecha"}, Sure: true}
	} else if tok != "" {
		period = &Period{Range: tok}
	} else if tok, ok := scanMonth(toks, v); ok {
		period = &Period{Range: tok}
	}
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
	summaryAsked := false
	for i := range toks {
		if !toks[i].used && summaryWords[toks[i].norm] {
			toks[i].used, summaryAsked = true, true
		}
	}
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
			continue
		}
		// «por área» / «por persona»: the word names the target of ONE of
		// the resource's relations — the group key is that relation's field
		if next.used {
			continue
		}
		var rel *Field
		for _, f := range res.Fields {
			if f.Relation != "" && v.namesResource(next.norm, v.Resource(f.Relation)) {
				if rel != nil {
					rel = nil
					break
				}
				rel = f
			}
		}
		if rel != nil {
			if groupBy != "" {
				return ParseResult{Reason: "two group_by"}
			}
			groupBy = rel.Name
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

	// 5c. «vencidas» / «vencidos» — past their due date: the due time field
	// before now (a Spanish word about time, not a domain word; only where a
	// due field exists).
	if f := res.DueTimeField(); f != nil && !seenField[f.Name] {
		for i := range toks {
			if !toks[i].used && overdueWords[toks[i].norm] {
				toks[i].used = true
				seenField[f.Name] = true
				filters = append(filters, Filter{Field: f.Name, Op: "lt", Value: "now"})
				break
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

	// 7. a proper name: the run of unconsumed, unknown tokens right after a
	// preposition / «llamado» — or right after the relation's target the
	// sentence named («del cliente Ana Gómez», settled in step 2).
	match := ""
	var nameF Filter
	if labelPos >= 0 {
		parts := nameRun(toks, labelPos, nil)
		if len(parts) == 0 {
			return ParseResult{Reason: "two resources: " + res.Name + ", " + v.Resource(res.Field(labelField).Relation).Name}
		}
		match = strings.Join(parts, " ")
		nameF = Filter{Field: labelField, Op: "eq", Match: match}
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
		match = strings.Join(parts, " ")
		f, reason := nameFilter(v, res, match)
		if reason != "" {
			return ParseResult{Reason: reason}
		}
		nameF = f
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
			match = strings.Join(parts, " ")
			f, reason := nameFilter(v, res, match)
			if reason != "" {
				return ParseResult{Reason: reason}
			}
			nameF = f
			break
		}
	}
	// 7c. a CODE («el pedido ORD-1003», «la factura 4521»): a token with a
	// digit, matched against the resource's own identifier field (numero,
	// codigo, sku, placa, referencia) through the same matcher — exact
	// codes score 1.0, a near miss is asked about.
	if match == "" {
		if code, field := codeToken(toks, res); code != "" {
			match = code
			nameF = Filter{Field: field, Op: "eq", Match: code}
		}
	}
	// 7d. a bare unknown run right AFTER the resource word on a resource
	// whose own label is a NAME («persona esposa», «área trabajo») — never
	// a title («tareas vencidas» is an adjective, not a row). Nothing else
	// in the sentence, one run, no operation word.
	if match == "" && len(filters) == 0 && period == nil && op == "" {
		if lf := res.LabelFields(); len(lf) > 0 && isNameField(lf[0]) {
			for i := 1; i < len(toks); i++ {
				if toks[i].used || stopwords[toks[i].norm] {
					continue
				}
				if !toks[i-1].used || !v.namesResource(toks[i-1].norm, res) {
					break
				}
				parts := nameRun(toks, i, nil)
				if len(parts) > 0 && !looksInfinitive(normalize(parts[0])) {
					match = strings.Join(parts, " ")
					nameF = Filter{Field: lf[0], Op: "eq", Match: match}
				}
				break
			}
		}
	}
	if match != "" {
		filters = append(filters, nameF)
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
	if summaryAsked {
		// «resumen de tareas» = the count by state when the resource has
		// one, else the plain count (VOZ-21's cousin: a summary OF one
		// resource is a breakdown, the summary of the DAY is the digest).
		if op != "" && op != "list" && op != "count" {
			return ParseResult{Reason: "summary with " + op}
		}
		op = "count"
		if groupBy == "" {
			if sf := res.StateField(); sf != nil && sf.Groupable() {
				groupBy = sf.Name
			}
		}
	}
	lookBack(res, period)
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
	// a leading article is part of how a row is named («la tarea de los
	// ajustes de reto»): skipped, not a stop
	for j < len(toks) && !toks[j].used && articles[toks[j].norm] {
		j++
	}
	if j > i && (j >= len(toks) || toks[j].used || stopwords[toks[j].norm]) {
		return nil
	}
	content := func(k int) bool {
		return k < len(toks) && !toks[k].used && !stopwords[toks[k].norm] && !countWords[toks[k].norm] && !listWords[toks[k].norm] && !extraStop[toks[k].norm]
	}
	for j < len(toks) {
		if content(j) {
			parts = append(parts, toks[j].raw)
			j++
			continue
		}
		// an inner «de» («ajustes de reto», «derechos de grado», «pago de la
		// luz») continues the name when a content word follows — never a
		// time word, a schema word or a stop («de Fabián de mañana» ends)
		if len(parts) > 0 && !toks[j].used && (toks[j].norm == "de" || toks[j].norm == "del") {
			k := j + 1
			for k < len(toks) && !toks[k].used && articles[toks[k].norm] {
				k++
			}
			if content(k) && !isTimeWord(toks[k].norm) {
				for x := j; x < k; x++ {
					parts = append(parts, toks[x].raw)
				}
				j = k
				continue
			}
		}
		break
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

// groupTarget answers the resource that is being GROUPED when the other
// one is named right after «por» and is the target of one of its relations
// («registros por área» → registros); it frees the target's words so the
// group-by step can read them. nil when the shape is not that.
func groupTarget(toks []token, a, b *Resource, mentions []resourceMention) *Resource {
	try := func(src, tgt *Resource) *Resource {
		n := 0
		for _, f := range src.Fields {
			if f.Relation == tgt.Name {
				n++
			}
		}
		if n != 1 {
			return nil
		}
		for _, m := range mentions {
			if m.res == tgt && m.start > 0 && toks[m.start-1].norm == "por" {
				for k := m.start; k < m.end; k++ {
					toks[k].used = false
				}
				return src
			}
		}
		return nil
	}
	if r := try(a, b); r != nil {
		return r
	}
	return try(b, a)
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
	if len(mentions) == 0 {
		// «qué anoté ayer», «qué registré hoy»: the note verb in the first
		// person past names the note resource (the verbs are the engine's)
		if nr := noteResource(v); nr != nil {
			for i, t := range toks {
				if !t.used && notedVerbs[t.norm] {
					toks[i].used = true
					mentions = append(mentions, resourceMention{res: nr, start: i, end: i + 1})
					break
				}
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
		// «registros por área», «tareas por persona»: the second resource,
		// right after «por», is the TARGET of the first's relation — a group
		// key, not a second subject. Its words are handed back to the
		// group-by step (4), which reads «por <relation target>».
		if r := groupTarget(toks, a, b, mentions); r != nil {
			return r, "", -1, ""
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
// target has a name-like label field; else res's own name-like field. With
// SEVERAL places (VOZ-20: an area AND a person, plus the row's own title) it
// returns them all as candidates and the engine tries each — the parser no
// longer gives up on «las tareas de Esposa».
func nameField(v *Vocabulary, res *Resource) (string, []string, string) {
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
	own := ""
	if lf := res.LabelFields(); len(lf) > 0 && nameRank(lf[0]) < len(primaryParts) {
		own = lf[0]
	}
	switch len(cands) {
	case 1:
		return cands[0], nil, ""
	case 0:
		if own != "" {
			return own, nil, ""
		}
		return "", nil, "name with no place to match"
	default:
		if own != "" {
			cands = append(cands, own)
		}
		return "", cands, ""
	}
}

// ownNameField is the resource's own name-like label field («titulo»,
// «nombre»), "" when its label is not a name.
func ownNameField(res *Resource) string {
	if lf := res.LabelFields(); len(lf) > 0 && nameRank(lf[0]) < len(primaryParts) {
		return lf[0]
	}
	return ""
}

// nameFilter builds the filter for a proper name: placed when the resource
// has one place for it, a multi-field one when it has several.
func nameFilter(v *Vocabulary, res *Resource, match string) (Filter, string) {
	mf, cands, reason := nameField(v, res)
	switch {
	case mf != "":
		return Filter{Field: mf, Op: "eq", Match: match}, ""
	case len(cands) > 0:
		return Filter{Op: "eq", Match: match, Fields: cands}, ""
	}
	return Filter{}, reason
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
	helpPhrases  = set("ayuda", "help", "que puedo hacer", "que puedo hacer aca", "que puedo hacer con vos", "que puedo hacer contigo", "que puedo hacer con esto", "que hago", "que puedo preguntar", "que puedo preguntarte", "que te puedo preguntar", "que puedo pedir", "que puedo pedirte", "que sabes hacer", "que sabes", "que podes hacer", "que puedes hacer", "que haces", "como funciona", "como funcionas", "como te uso", "que preguntas puedo hacer", "que preguntas respondes", "que me podes decir", "que me puedes decir", "que comandos hay", "cuales son los comandos", "instrucciones", "menu")
	helpPrefixes = []string{"que puedo preguntar", "que te puedo preguntar", "que puedo pedir", "que sabes hacer", "que podes hacer", "que puedes hacer", "como funciona", "que preguntas puedo", "que comandos"}
	// The fixed commands, as said to the question door (VOZ-21).
	summaryPhrases = set("resumen", "el resumen", "resumen de hoy", "el resumen de hoy", "resumen del dia", "dame el resumen", "mandame el resumen", "que paso hoy", "que paso hoy?", "resumen de hoy por favor", "el parte", "parte del dia", "resumen del dia de hoy", "resumen de ayer", "que hay de nuevo", "novedades")
	censusPhrases  = set("estado", "el estado", "estado general", "como esta todo", "como vamos", "como va todo", "cuantos hay de cada cosa", "censo", "estado de todo", "como estamos")
	spendPhrases   = set("gasto", "el gasto", "cuanto gaste", "cuanto llevo gastado", "cuanto va el gasto", "cuanto cuesta esto", "gasto del modelo", "cuanto gastamos", "cuanto he gastado", "cuanto gasto", "que cuestan las preguntas", "gastos del modelo")
)

// preDiscard returns the discard code when the sentence contains NOTHING the
// grammar could execute AND matches a recognizable non-question shape. Any
// operation word, schema word (resource, alias, value, field), period or
// write verb means "let the parser and, if needed, the model decide".
func preDiscard(toks []token, v *Vocabulary) string {
	if v == nil {
		return ""
	}
	joined := strings.TrimSpace(joinedNorms(toks))
	// The fixed commands said to this door (VOZ-21) and the living guide
	// (Part B) are recognized BEFORE the executable-word check: «estado» is
	// also a field, «cómo creo una tarea» names a resource.
	switch {
	case summaryPhrases[joined]:
		return "summary"
	case censusPhrases[joined]:
		return "census"
	case spendPhrases[joined]:
		return "spend"
	}
	if topic, res := guideTopic(toks, v); topic != "" {
		if res != "" {
			return "guide:" + topic + ":" + res
		}
		return "guide:" + topic
	}
	if hasExecutableWord(toks, v) {
		return ""
	}
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
	// a date or a month said alone is a period («qué hice el 23 de septiembre»)
	probe := make([]token, len(toks))
	copy(probe, toks)
	if tok, bad := scanDate(probe); tok != "" || bad != "" {
		return true
	}
	if _, ok := scanMonth(probe, v); ok {
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
	case "summary", "census", "spend":
		return "pide un comando fijo (" + code + ")"
	case "bare_name":
		return "es solo un nombre, sin qué preguntar"
	}
	return code
}

// transitionVerbs introduce a state change; the state itself follows
// ("como hecha", "a pagada", "en cancelada") or is the verb's own stem
// ("cancelá" → cancelada, "confirmá" → confirmada).
var transitionVerbs = set("marca", "marcá", "marcar", "marcame", "marcala", "marcalo", "pasa", "pasá", "pasar", "pasala", "pasalo", "pone", "poné", "poner", "pon", "ponele", "ponela", "ponelo", "ponle", "ponla", "ponlo",
	"cambia", "cambia", "cambiar", "cambiale", "deja", "deja", "dejar", "dejala", "dejalo", "actualiza", "actualiza", "actualizar", "da", "dá", "dar", "dale")
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
	if reason == "no resource named" {
		// «poné en curso la declaración de renta»: the state value said
		// belongs to exactly one resource's state field — that resource
		if r := resourceByStateValue(toks, v); r != nil {
			res, labelField, labelPos, reason = r, "", -1, ""
		}
	}
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
	match, matchPos := "", -1
	var nameF Filter
	if labelPos >= 0 {
		parts := nameRun(toks, labelPos, transitionLinkers)
		if len(parts) > 0 {
			match, matchPos = strings.Join(parts, " "), labelPos
			nameF = Filter{Field: labelField, Op: "eq", Match: match}
		}
	}
	for i := 0; i < len(toks); i++ {
		if toks[i].used || !prepositions[toks[i].norm] {
			continue
		}
		// a content run right BEFORE the preposition is the same name («la
		// declaración de renta», «los derechos de grado»): the run starts
		// there and the inner «de» joins the rest
		start := i + 1
		k := i - 1
		for k >= 0 && !toks[k].used && !stopwords[toks[k].norm] && !transitionLinkers[toks[k].norm] && !prepositions[toks[k].norm] {
			k--
		}
		if k < i-1 {
			start = k + 1
		}
		parts := nameRun(toks, start, transitionLinkers)
		if len(parts) == 0 {
			continue
		}
		if match != "" {
			return ParseResult{Reason: "two names"}
		}
		match, matchPos = strings.Join(parts, " "), start
		f, reason := nameFilter(v, res, match)
		if reason != "" {
			return ParseResult{Reason: reason}
		}
		nameF = f
		toks[i].used = true
	}
	if match == "" {
		if code, field := codeToken(toks, res); code != "" {
			match = code
			nameF = Filter{Field: field, Op: "eq", Match: code}
		}
	}
	// DATA the sentence carries for the row («cierra la tarea buscar frutas,
	// tiempo real 30 minutos»): the same reader the done path uses.
	data := map[string]any{sf.Name: target}
	if amb := rowData(v, res, toks, sf, data); amb != "" {
		return ParseResult{Plan: Plan{Kind: "unclear", Reason: amb}, Sure: true}
	}

	// EVERY content word still unused belongs to the same row name — the
	// rule «ya hice …» already used (2026-09-26): «marca como hecha la tarea
	// arreglar el techo» lost «techo» to the article, and «cierra la tarea
	// hablar con Norberto» lost «Norberto» to the preposition run, so the
	// sentence was refused over a word that was part of the title.
	var restParts []string
	restPos := -1
	for i := range toks {
		if toks[i].used || stopwords[toks[i].norm] || transitionLinkers[toks[i].norm] {
			continue
		}
		if restPos < 0 {
			restPos = i
		}
		restParts = append(restParts, toks[i].raw)
		toks[i].used = true
	}
	if len(restParts) > 0 {
		rest := strings.Join(restParts, " ")
		switch {
		case match == "":
			match = rest
		case matchPos >= 0 && restPos < matchPos:
			match = rest + " " + match // the words came FIRST in the sentence
		default:
			match += " " + rest
		}
		f, reason := nameFilter(v, res, match)
		if reason != "" {
			return ParseResult{Reason: reason}
		}
		nameF = f
	}
	if match != "" {
		where = append(where, nameF)
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
	p := Plan{Kind: "update", Resource: res.Name, Where: where, Data: data}
	if err := p.Validate(v); err != nil {
		return ParseResult{Reason: "invalid: " + err.Error()}
	}
	return ParseResult{Plan: p, Sure: true}
}

// resourceByStateValue is the ONE updatable resource whose state field has a
// value (or alias) said in the sentence; nil when none or several.
func resourceByStateValue(toks []token, v *Vocabulary) *Resource {
	joined := joinedNorms(toks)
	var found *Resource
	for _, name := range v.order {
		r := v.resources[name]
		sf := r.StateField()
		if sf == nil || !r.CanUpdate {
			continue
		}
		for _, vf := range sf.valueForms() {
			if strings.Contains(joined, " "+vf.form+" ") {
				if found != nil && found != r {
					return nil
				}
				found = r
				break
			}
		}
	}
	return found
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

// looksInfinitive reports whether a normalized word is a Spanish infinitive
// («pagar», «hacer», «ir»): what a to-do starts with. Not a stopword, ends in
// -ar/-er/-ir, and «ir» itself.
func looksInfinitive(w string) bool {
	if stopwords[w] || w == "" {
		return false
	}
	if w == "ir" {
		return true
	}
	// an infinitive with a clitic («decirle», «mandarme», «decírselo»)
	for _, cl := range []string{"selo", "sela", "selos", "selas", "melo", "mela", "telo", "tela", "me", "te", "se", "le", "lo", "la", "nos", "les", "los", "las"} {
		if strings.HasSuffix(w, cl) && len(w) > len(cl)+3 {
			base := strings.TrimSuffix(w, cl)
			if strings.HasSuffix(base, "ar") || strings.HasSuffix(base, "er") || strings.HasSuffix(base, "ir") {
				return true
			}
		}
	}
	if len(w) < 4 {
		return false
	}
	return strings.HasSuffix(w, "ar") || strings.HasSuffix(w, "er") || strings.HasSuffix(w, "ir")
}

// isNameField reports whether a label field holds a NAME (nombre, name,
// apellido, razón social) rather than a title or a subject.
func isNameField(field string) bool {
	n := normalize(field)
	for _, p := range []string{"nombre", "name", "apellido", "razon"} {
		if strings.Contains(n, p) {
			return true
		}
	}
	return false
}

// splitIntents cuts «… y <write verb> …» into the first sentence and the
// rest, as said. Only a second WRITE verb splits: «llamé al latonero y no
// contestó» is one note.
func splitIntents(question string) (first, second string) {
	words := strings.Fields(question)
	for i := 1; i+1 < len(words); i++ {
		if normalize(words[i]) != "y" {
			continue
		}
		n := normalize(words[i+1])
		if createVerbs[n] || scheduleVerbs[n] || transitionVerbs[n] || writeVerbs[n] {
			// the first part must itself carry an order
			hasVerb := false
			for _, w := range words[:i] {
				wn := normalize(w)
				if createVerbs[wn] || scheduleVerbs[wn] || transitionVerbs[wn] || writeVerbs[wn] {
					hasVerb = true
				}
			}
			joined := " " + normalize(strings.Join(words[:i], " ")) + " "
			for _, ph := range obligationPhrases {
				if strings.Contains(joined, " "+ph+" ") {
					hasVerb = true
				}
			}
			if !hasVerb {
				return question, ""
			}
			return strings.TrimSpace(strings.Join(words[:i], " ")), strings.TrimSpace(strings.Join(words[i+1:], " "))
		}
	}
	return question, ""
}

// doneLeads open a sentence that says a to-do is finished.
var doneLeads = []string{"ya hice", "ya termine", "termine de", "ya termine de", "termine", "ya pague", "ya lo hice", "ya la hice", "ya esta hecha", "ya esta hecho", "ya esta lista", "ya esta listo", "ya quedo", "listo con", "ya hicimos", "hice", "acabe de", "ya acabe de",
	// CLOSING it — the word an owner reaches for first («¿cómo cierro una
	// tarea?», 2026-09-26). The STATE is the schema's single finished one
	// (doneState), never a wired «hecha».
	"cierra", "cierra la", "cierrame", "cierro", "cerra", "cerrar", "cerre", "cerramos", "dar por cerrada", "da por cerrada", "doy por cerrada", "completa", "complete", "ya complete", "completamos", "finalice", "ya finalice", "finaliza",
	"ya"}

// doneTrailers close it («… está lista», «… ya está», «… quedó hecha»).
var doneTrailers = []string{"esta lista", "esta listo", "ya esta", "quedo lista", "quedo listo", "esta hecha", "esta hecho", "ya quedo", "esta terminada", "esta terminado"}

// rowData reads the DATA a state-change sentence carries for the row
// («cierra la tarea buscar frutas, tiempo real 30 minutos», 2026-09-26): a
// field said by its OWN name words plus its value. An app may REQUIRE it to
// close — the owner's agenda has a hook that refuses «hecha» without the
// minutes — and before this those words landed in the row's NAME, so the row
// was never found. Consumed tokens are marked used. A non-empty return is the
// reply for a bare duration that several numeric fields could mean.
func rowData(v *Vocabulary, res *Resource, toks []token, sf *Field, data map[string]any) string {
	for i := 0; i < len(toks); i++ {
		if toks[i].used {
			continue
		}
		f, n := fieldByWords(v, res, toks, i)
		if f == nil || f == sf || f.Auto {
			continue
		}
		// the value: the words after the field, up to the next field word
		end := i + n
		for end < len(toks) && !toks[end].used {
			if f2, _ := fieldByWords(v, res, toks, end); f2 != nil && f2 != f {
				break
			}
			end++
		}
		if end == i+n {
			continue
		}
		var valParts []string
		for k := i + n; k < end; k++ {
			valParts = append(valParts, toks[k].raw)
		}
		joined := strings.ToLower(strings.Join(valParts, " "))
		set := false
		if f.IsNumeric() {
			if val, ok := numericRun(f, joined); ok {
				data[f.Name], set = val, true
			}
		}
		if !set {
			seg := make([]ctok, 0, end-(i+n))
			for k := i + n; k < end; k++ {
				seg = append(seg, ctok{raw: toks[k].raw, norm: toks[k].norm})
			}
			var refs []Ref
			probe := map[string]any{}
			if setField(v, res, f, seg, probe, &refs) {
				for k, val := range probe {
					if _, taken := data[k]; !taken {
						data[k] = val
					}
				}
				set = true
			}
		}
		if !set {
			continue
		}
		for k := i; k < end; k++ {
			toks[k].used = true
		}
		i = end - 1
	}
	// a bare duration («…, 30 minutos») with SEVERAL numeric fields it could
	// mean is not guessed: the reply names the two ways to say it
	if len(durationFields(res)) > 1 {
		for i := 0; i+1 < len(toks); i++ {
			if toks[i].used || toks[i+1].used {
				continue
			}
			if !unitRe.MatchString(toks[i].norm + " " + toks[i+1].norm) {
				continue
			}
			var forms []string
			for _, f := range durationFields(res) {
				words := strings.TrimSuffix(strings.ReplaceAll(f.Name, "_", " "), " min")
				forms = append(forms, "«"+words+" "+toks[i].raw+" "+toks[i+1].raw+"»")
			}
			return "field_choice: No sé a cuál de los dos te refieres. Dime " + strings.Join(forms, " o ")
		}
	}
	return ""
}

// parseDone settles «ya hice X» / «X está lista» as the transition of the
// to-do row X to its finished state: the terminal state that is not a
// cancellation (by the cancel/anular/rechazar stems — Spanish, not a
// domain), when the machine has exactly one such state.
func parseDone(question string, v *Vocabulary) ParseResult {
	if v == nil || !v.Writable() {
		return ParseResult{Reason: "done: read-only vocabulary"}
	}
	toks := tokenize(question)
	joined := strings.TrimSpace(joinedNorms(toks))
	lead, trailer := "", ""
	for _, l := range doneLeads {
		if strings.HasPrefix(joined+" ", l+" ") {
			lead = l
			break
		}
	}
	for _, tr := range doneTrailers {
		if strings.HasSuffix(" "+joined, " "+tr) {
			trailer = tr
			break
		}
	}
	if lead == "" && trailer == "" {
		return ParseResult{Reason: "no done phrase"}
	}
	if lead == "ya" && trailer == "" {
		return ParseResult{Reason: "done: bare ya"}
	}
	// the resource: the one the sentence NAMES when it has a lifecycle
	// («cierra el compromiso del dentista» closes the appointment), else the
	// single to-do resource («ya hice arreglar el techo»)
	res := taskResource(v)
	for i := range toks {
		if toks[i].used {
			continue
		}
		for _, name := range v.ResourceNames() {
			r := v.Resource(name)
			if r == nil || r == res || r.StateField() == nil || !r.CanUpdate || !v.namesResource(toks[i].norm, r) {
				continue
			}
			res = r
			break
		}
	}
	if res == nil || !res.CanUpdate {
		return ParseResult{Reason: "done: no single to-do resource"}
	}
	sf := res.StateField()
	if sf == nil {
		return ParseResult{Reason: "done: no state field"}
	}
	target := doneState(sf)
	if target == "" {
		return ParseResult{Reason: "done: no single finished state"}
	}
	if lead != "" && !consumePhrase(toks, lead) {
		return ParseResult{Reason: "done: lead not consumed"}
	}
	if trailer != "" && !consumePhrase(toks, trailer) {
		return ParseResult{Reason: "done: trailer not consumed"}
	}
	// the row: the resource word may be said («la tarea del carro»); the
	// rest names it — tried against every place a name can be (VOZ-20)
	for i := range toks {
		if !toks[i].used && v.namesResource(toks[i].norm, res) {
			toks[i].used = true
		}
	}
	// the data the sentence carries («cierra la tarea X, tiempo real 30
	// minutos»): read BEFORE the name, or those words would be part of it
	data := map[string]any{sf.Name: target}
	if amb := rowData(v, res, toks, sf, data); amb != "" {
		return ParseResult{Plan: Plan{Kind: "unclear", Reason: amb}, Sure: true}
	}
	var parts []string
	for _, t := range toks {
		if t.used || stopwords[t.norm] || t.norm == "que" {
			continue
		}
		parts = append(parts, t.raw)
	}
	if len(parts) == 0 {
		return ParseResult{Reason: "done: no row named"}
	}
	name := strings.Join(parts, " ")
	f, reason := nameFilter(v, res, name)
	if reason != "" {
		return ParseResult{Reason: reason}
	}
	p := Plan{Kind: "update", Resource: res.Name, Where: []Filter{f}, Data: data}
	if err := p.Validate(v); err != nil {
		return ParseResult{Reason: "done invalid: " + err.Error()}
	}
	return ParseResult{Plan: p, Sure: true}
}

// doneState is the finished state of a machine: the terminal states minus
// the cancellations; exactly one, else "".
func doneState(sf *Field) string {
	var done []string
	for _, st := range sf.Terminal {
		n := normalize(st)
		cancel := false
		for _, stem := range []string{"cancel", "anul", "rechaz", "descart", "abandon"} {
			if strings.HasPrefix(n, stem) {
				cancel = true
			}
		}
		for _, a := range sf.Aliases[st] {
			for _, stem := range []string{"cancel", "anul", "rechaz", "descart"} {
				if strings.HasPrefix(normalize(a), stem) {
					cancel = true
				}
			}
		}
		if !cancel {
			done = append(done, st)
		}
	}
	if len(done) == 1 {
		return done[0]
	}
	return ""
}
