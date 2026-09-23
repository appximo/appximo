package ask

import (
	"strings"

	"github.com/appximo/appximo/pkg/schema"
)

// HelpExamples composes the «ayuda» reply from the SCHEMA the caller's role
// sees — never a hand-written list, so it cannot drift from the app: for each
// resource (the owner's `summary.resources` order first) the phrases the
// deterministic parser settles for free — a count, the resource by a declared
// state alias, a period, its bool flags, «los últimos N», a group-by, the
// agenda's «qué tengo mañana», the obligation «tengo que…» and a confirmed
// transition — and, apart, the shapes only the model can plan (creates with
// details, any sentence with words the schema does not declare). Two
// renderings: Telegram HTML (with the costs) and a SPEECH in short sentences,
// one pause per example, no symbols, no prices, no digits — what Siri reads.
func HelpExamples(v *Vocabulary, canWrite bool) (text, speech string) {
	app := "esta app"
	if v != nil && strings.TrimSpace(v.AppName) != "" {
		app = strings.TrimSpace(v.AppName)
	}
	var free, model []helpLine
	if v != nil {
		free, model = helpLines(v, canWrite)
		// The free section is SELF-VERIFIED: an example is shown only if the
		// parser settles it on this very schema (a placeholder name stands in
		// for the owner's). A phrase the parser cannot settle here — a
		// transition on a resource with two relation targets, say — is never
		// promised as free.
		kept := free[:0]
		for _, l := range free {
			if pr := Parse(strings.ReplaceAll(l.text, "[nombre]", "Ana"), v); pr.Sure {
				kept = append(kept, l)
			}
		}
		free = kept
	}
	// ── Telegram HTML
	var b strings.Builder
	b.WriteString("ℹ️ <b>Qué puedo hacer en " + esc(app) + "</b>\n")
	if len(free) > 0 {
		b.WriteString("\n<b>Al instante y gratis</b> (las entiendo sin el modelo, US$ 0):\n")
		for _, l := range free {
			b.WriteString("• «" + esc(l.text) + "»\n")
		}
	}
	if len(model) > 0 {
		b.WriteString("\n<b>Con el modelo</b> (≈ US$ 0,003 cada una, tarda un segundo):\n")
		for _, l := range model {
			if l.quoted {
				b.WriteString("• «" + esc(l.text) + "»\n")
			} else {
				b.WriteString("• " + esc(l.text) + "\n")
			}
		}
	}
	b.WriteString("\nTambién: <b>resumen</b> (qué pasó hoy), <b>estado</b> (cuántos hay de cada cosa), <b>gasto</b> (qué cuestan las preguntas; solo quien administra), <b>ayuda</b>.")
	if canWrite {
		b.WriteString(" Antes de escribir te muestro exactamente qué voy a anotar y espero tu <b>sí</b>. Nunca borro nada.")
	}
	text = b.String()
	// ── speech: short sentences, a pause per example, nothing to spell out
	var sp []string
	if len(free) > 0 {
		sp = append(sp, "Estas frases las respondo al instante y sin costo.")
		for _, l := range free {
			sp = append(sp, sentence(l.speech))
		}
	}
	if len(model) > 0 {
		sp = append(sp, "Estas otras las piensa el modelo y cuestan unos centavos.")
		for _, l := range model {
			sp = append(sp, sentence(l.speech))
		}
	}
	sp = append(sp, "También podés decir resumen, estado, o ayuda.")
	if canWrite {
		sp = append(sp, "Antes de escribir te digo qué voy a anotar y espero tu sí. Nunca borro nada.")
	}
	speech = strings.Join(sp, " ")
	return text, speech
}

type helpLine struct {
	text, speech string
	quoted       bool // the text is an example phrase (quoted) vs a description
}

func ex(text string) helpLine { return helpLine{text: text, speech: text, quoted: true} }

// helpLines derives the examples. Every word comes from the schema (names,
// declared aliases, states) or from the parser's own closed grammar (count,
// period, last-N, group-by, obligation, transition words); the only invented
// content is the sample task/meeting the create examples carry, marked as
// such by [nombre] / «alguien».
func helpLines(v *Vocabulary, canWrite bool) (free, model []helpLine) {
	// the owner's own ranking first (summary.resources, kept as Listed), then
	// the rest — at most four resources, or the help outgrows a phone screen.
	names := append([]string(nil), v.listedOrder...)
	for _, n := range v.ResourceNames() {
		if r := v.Resource(n); r != nil && !r.Listed {
			names = append(names, n)
		}
	}
	if len(names) > 4 {
		names = names[:4]
	}
	agenda := agendaResource(v)
	task := taskResource(v)
	lastDone, groupDone, boolDone := false, false, false
	for _, n := range names {
		r := v.Resource(n)
		if r == nil {
			continue
		}
		plural, fem := resourceWords(r)
		// a count — the first thing the parser answers.
		q := "cuántos"
		if fem {
			q = "cuántas"
		}
		free = append(free, ex(q+" "+plural+" hay"))
		// the resource by a declared state, said as the owner says it.
		if sf := r.StateField(); sf != nil {
			if w := stateWord(sf); w != "" {
				free = append(free, ex(plural+" "+w))
			}
		}
		// a period, when the resource has a time the parser can bound.
		if r.DefaultTimeField() != "" {
			if agenda == r {
				free = append(free, ex(plural+" de mañana"))
			} else {
				free = append(free, ex(plural+" de hoy"))
			}
		}
		// its bool flags, by their own name («tareas urgentes») — a flag that
		// is normally OFF is the one people ask for; one that defaults to
		// true («ocupa») is not a question anyone asks.
		if !boolDone {
			for _, f := range r.Fields {
				if f.Type == "bool" && !f.Auto && !(f.HasDefault && f.Default == true) {
					if w := pluralForm(f.Name); w != "" {
						free = append(free, ex(plural+" "+w))
						boolDone = true
						break
					}
				}
			}
		}
		// «los últimos 3 …» — once, on a resource with a creation timestamp.
		if !lastDone && r.DefaultTimeField() != "" {
			free = append(free, helpLine{text: "los últimos 3 " + plural, speech: "los últimos tres " + plural, quoted: true})
			lastDone = true
		}
		// a group-by — once, on a state field.
		if !groupDone {
			if sf := r.StateField(); sf != nil && sf.Groupable() {
				free = append(free, ex(plural+" por "+strings.ReplaceAll(sf.Name, "_", " ")))
				groupDone = true
			}
		}
	}
	if agenda != nil {
		free = append(free, ex("qué tengo mañana"), ex("cuándo estoy libre el jueves"))
	}
	if canWrite {
		if task != nil {
			// «tengo que…» is settled by the parser (APP-AGENDA-S2, VOZ-17).
			free = append(free, ex("tengo que llamar al banco"))
			if sf := task.StateField(); sf != nil && len(sf.Transitions) > 0 {
				if target := transitionWord(sf); target != "" {
					_, fem := resourceWords(task)
					art := "el"
					if fem {
						art = "la"
					}
					sing := singularWordOf(task)
					free = append(free, helpLine{text: "marcá como " + target + " " + art + " " + sing + " de [nombre]", speech: "marcá como " + target + " " + art + " " + sing + " de alguien", quoted: true})
				}
			}
			sing, art := singularWithArticle(task)
			model = append(model, helpLine{text: "anotá " + art + " " + sing + " con detalles: «llamar a [nombre] el viernes, urgente»", speech: "anotá " + art + " " + sing + " con detalles, como llamar a alguien el viernes, urgente", quoted: false})
		}
		if agenda != nil && agenda != task {
			sing, art := singularWithArticle(agenda)
			model = append(model, helpLine{text: "agendá " + art + " " + sing + " con [nombre] mañana a las 3", speech: "agendá " + art + " " + sing + " con alguien mañana a las tres", quoted: true})
		}
	}
	model = append(model, helpLine{text: "cualquier pregunta con palabras que " + strings.TrimSpace(v.AppName) + " no conoce (la responde el modelo, si puede)", speech: "y cualquier pregunta con palabras que " + strings.TrimSpace(v.AppName) + " no conoce", quoted: false})
	if strings.TrimSpace(v.AppName) == "" {
		last := &model[len(model)-1]
		last.text = "cualquier pregunta con palabras que el schema no conoce (la responde el modelo, si puede)"
		last.speech = "y cualquier pregunta con palabras que el schema no conoce"
	}
	return free, model
}

// resourceWords gives the resource's plural as said (the schema name, its
// first declared alias when the name is not Spanish-looking is NOT guessed —
// the schema name is what the parser always accepts) and a gender guess for
// the article: feminine when the singular ends in -a.
func resourceWords(r *Resource) (plural string, feminine bool) {
	base := strings.ReplaceAll(r.Name, "_", " ")
	sing := schema.SingularES(base)
	plural = base
	if !strings.HasSuffix(base, "s") {
		plural = pluralForm(base)
	}
	feminine = strings.HasSuffix(sing, "a")
	return plural, feminine
}

func singularWordOf(r *Resource) string {
	return schema.SingularES(strings.ReplaceAll(r.Name, "_", " "))
}

// singularWithArticle: «una tarea», «un compromiso».
func singularWithArticle(r *Resource) (sing, article string) {
	sing = singularWordOf(r)
	if strings.HasSuffix(sing, "a") {
		return sing, "una"
	}
	return sing, "un"
}

// pluralForm is a Spanish plural the parser accepts (NameForms tolerates it):
// a vowel takes -s, a consonant -es; a multi-word name stays as it is.
func pluralForm(name string) string {
	base := strings.ReplaceAll(strings.TrimSpace(name), "_", " ")
	if base == "" || strings.Contains(base, " ") || strings.HasSuffix(base, "s") {
		return base
	}
	if strings.ContainsRune("aeiou", rune(base[len(base)-1])) {
		return base + "s"
	}
	return base + "es"
}

// stateWord picks how the owner names the state that WAITS (pending first,
// then an initial state, then the first value): its first declared alias when
// there is one, else the value, pluralized the way the parser accepts.
func stateWord(sf *Field) string {
	pick := ""
	switch {
	case len(sf.Pending) > 0:
		pick = sf.Pending[0]
	case len(sf.Initial) > 0:
		pick = sf.Initial[0]
	case len(sf.Enum) > 0:
		pick = sf.Enum[0]
	}
	if pick == "" {
		return ""
	}
	if al := sf.Aliases[pick]; len(al) > 0 {
		if strings.Contains(al[0], " ") {
			return al[0]
		}
		return pluralForm(al[0])
	}
	return pluralForm(strings.ReplaceAll(pick, "_", " "))
}

// transitionWord picks a state a row can MOVE to from an initial state — a
// terminal one when there is one (the «hecha» of a task) — said by its first
// alias or its value.
func transitionWord(sf *Field) string {
	from := ""
	if len(sf.Initial) > 0 {
		from = sf.Initial[0]
	}
	targets := sf.Transitions[from]
	if len(targets) == 0 {
		return ""
	}
	pick := targets[0]
	for _, t := range targets {
		if contains(sf.Terminal, t) {
			pick = t
			break
		}
	}
	// the declared VALUE, not an alias: «lista» would double as the list verb.
	return strings.ReplaceAll(pick, "_", " ")
}

// sentence makes one spoken example: capitalized, ending in a period — the
// pause a voice assistant honors.
func sentence(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	r := []rune(s)
	r[0] = []rune(strings.ToUpper(string(r[0])))[0]
	s = string(r)
	if !strings.HasSuffix(s, ".") {
		s += "."
	}
	return s
}
