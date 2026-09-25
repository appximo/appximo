package ask

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"time"
)

// The LIVING GUIDE (AGENDA-ASISTENTE-S1, Part B): the assistant teaches how
// to use it. Not help text — a guide GENERATED from the schema of each app,
// by levels, that teaches how to build a sentence: the structure, then one
// complete example that can be repeated as is, with the resources, fields,
// states, aliases and rows of THIS app. Self-verified like `ayuda`: an
// example promised as free is parsed before it is shown; one the parser
// cannot settle on this schema is never promised. Free and paid are always
// apart, with the price. Spoken in parts: a long level offers «más» instead
// of a wall. Nothing here knows any domain word.
//
// Levels (what the owner says → what the guide answers):
//   ayuda / qué puedo hacer          → the menu: what the app has, the four doors
//   cómo creo algo / cómo creo una X → per resource: structure + a full example
//   qué puedo preguntar              → the question shapes, on this schema
//   qué campos tiene una X           → the fields, in words, with their forms
//   cómo filtro por fecha            → periods, ranges, combinations, last N
//   más                              → the next part of the last level

// GuideStore remembers, per identity, where the guide left off (a level in
// parts), so «más» continues it. Ten minutes, in memory.
type GuideStore struct {
	mu   sync.Mutex
	next map[string]guideCursor
}

type guideCursor struct {
	topic, res string
	part       int
	expires    time.Time
}

// NewGuideStore makes an empty store.
func NewGuideStore() *GuideStore { return &GuideStore{next: map[string]guideCursor{}} }

const guideTTL = 10 * time.Minute

func (g *GuideStore) put(key, topic, res string, part int) {
	if g == nil || key == "" {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if part < 0 {
		delete(g.next, key)
		return
	}
	g.next[key] = guideCursor{topic: topic, res: res, part: part, expires: time.Now().Add(guideTTL)}
	for k, c := range g.next {
		if time.Now().After(c.expires) {
			delete(g.next, k)
		}
	}
}

func (g *GuideStore) get(key string) (guideCursor, bool) {
	if g == nil || key == "" {
		return guideCursor{}, false
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	c, ok := g.next[key]
	if !ok || time.Now().After(c.expires) {
		return guideCursor{}, false
	}
	return c, true
}

// guidePart is one spoken-size piece of a level: Telegram HTML + speech.
type guidePart struct {
	text, speech string
}

// ── recognizing what the owner asks ───────────────────────────────────────

var guideCreateLeads = []string{"como creo", "como se crea", "como crear", "como agendar", "como anotar", "como registrar", "como cargar", "como agregar", "como hago una", "como hago un", "como se hace para crear", "como se hace una", "como se hace un", "como hago para hacer", "que digo para agendar", "que digo para anotar", "como le digo que cree", "como le pido que cree", "como cargo", "como anoto", "como agendo", "como registro", "como agrego", "como se anota", "como se agenda", "como se registra", "como hago para crear", "como hago para anotar", "como hago para agendar", "que digo para crear", "como te digo que crees", "como te pido que crees", "como creo", "como puedo crear", "como puedo anotar", "como hago una", "como hago un", "ensename a crear", "enseñame a crear"}
var guideAskLeads = []string{"que puedo preguntar", "que puedo preguntarte", "que te puedo preguntar", "como pregunto", "que preguntas", "que se puede preguntar", "que me podes responder", "que me puedes responder", "que puedo consultar", "como consulto", "que puedo pedir", "que puedo pedirte", "que preguntas puedo", "ensename a preguntar", "enseñame a preguntar", "como se pregunta"}
var guideFieldsLeads = []string{"que campos tiene", "que campos tienen", "que campo tiene", "que campo lleva", "que campos lleva", "que campos hay en", "que informacion tiene", "que info tiene", "que tiene un", "que tiene una", "que datos tiene", "que datos lleva", "que lleva un", "que lleva una", "campos de", "que campos", "que campo", "que puedo poner en", "que se le puede poner a", "que datos tiene un", "que datos tiene una", "que puedo ponerle a", "que le puedo poner a"}
var guideFilterLeads = []string{"como filtro", "como busco", "que filtros", "como pregunto por fecha", "como pido por fecha", "como busco por", "como filtro por", "que periodos", "como se filtra", "como filtrar", "como buscar", "que fechas entendes", "que fechas entiendes", "como pregunto por periodo", "por periodo", "como combino", "como pregunto por"}
var guideMoreWords = set("mas", "segui", "sigue", "continua", "continuá", "y que mas", "otro", "otra", "siguiente", "seguir", "mas ejemplos", "dame mas", "y despues", "que mas", "continuar", "sigue", "dale segui", "dale", "seguime")

// guideTopic recognizes a guide request. Returns the topic ("" when none)
// and the resource named, if any.
func guideTopic(toks []token, v *Vocabulary) (topic, res string) {
	if v == nil {
		return "", ""
	}
	joined := strings.TrimSpace(joinedNorms(toks))
	named := ""
	for _, t := range toks {
		for _, name := range v.order {
			if v.namesResource(t.norm, v.resources[name]) {
				named = name
			}
		}
	}
	starts := func(leads []string) bool {
		for _, l := range leads {
			if joined == l || strings.HasPrefix(joined, l+" ") {
				return true
			}
		}
		return false
	}
	switch {
	case guideMoreWords[joined]:
		return "more", ""
	case joined == "crear" || joined == "crear algo" || joined == "anotar" || joined == "agendar" || joined == "registrar" || createVerbs[joined] || scheduleVerbs[joined]:
		if scheduleVerbs[joined] && !createVerbs[joined] {
			if ag := agendaResource(v); ag != nil && named == "" {
				named = ag.Name
			}
		}
		return "create", named
	case starts(guideCreateLeads):
		return "create", named
	case starts(guideAskLeads):
		return "ask", named
	case starts(guideFieldsLeads):
		return "fields", named
	case starts(guideFilterLeads):
		return "filters", ""
	}
	return "", ""
}

// ── rendering ─────────────────────────────────────────────────────────────

// Guide answers a guide request: the first part of the level (or the next
// one for «more»), and remembers where it stopped.
func Guide(ctx context.Context, d Deps, topic, res string) Result {
	part := 0
	if topic == "more" {
		c, ok := d.Guide.get(d.GuideKey)
		if !ok {
			// nothing to continue: the menu
			topic, res, part = "menu", "", 0
		} else {
			topic, res, part = c.topic, c.res, c.part
		}
	}
	parts := renderGuide(ctx, d, topic, res)
	if len(parts) == 0 {
		parts = renderGuide(ctx, d, "menu", "")
	}
	if part >= len(parts) {
		part = len(parts) - 1
	}
	p := parts[part]
	r := Result{Kind: "guide", Source: "parser", Headline: guideHeadline(topic, res), Text: p.text, Speech: p.speech, Detail: "guide: " + topic}
	if topic == "menu" {
		r.Kind = "help"
	}
	if part+1 < len(parts) {
		r.Text += fmt.Sprintf("\n\n<i>Hay más (%d de %d). Di <b>más</b> para seguir.</i>", part+1, len(parts))
		r.Speech = strings.TrimSpace(r.Speech) + " Hay más. Di más para seguir."
		d.Guide.put(d.GuideKey, topic, res, part+1)
	} else {
		d.Guide.put(d.GuideKey, topic, res, -1)
	}
	return r
}

func guideHeadline(topic, res string) string {
	switch topic {
	case "create":
		if res != "" {
			return "Cómo crear " + singular(res)
		}
		return "Cómo crear algo"
	case "ask":
		return "Qué puedes preguntar"
	case "fields":
		if res != "" {
			return "Qué tiene " + singular(res)
		}
		return "Qué campos hay"
	case "filters":
		return "Cómo filtrar"
	}
	return "Qué puedo hacer"
}

func renderGuide(ctx context.Context, d Deps, topic, res string) []guidePart {
	v := d.Vocab
	switch topic {
	case "create":
		if res != "" {
			if r := v.Resource(res); r != nil && r.CanCreate {
				return guideCreate(ctx, d, r, true)
			}
		}
		var parts []guidePart
		n := 0
		for _, name := range creatableOrder(v) {
			r := v.Resource(name)
			ps := guideCreate(ctx, d, r, false)
			if len(ps) == 0 {
				continue
			}
			parts = append(parts, ps[0])
			n++
			if n == 4 {
				break
			}
		}
		if len(parts) > 0 {
			parts[len(parts)-1].text += "\n\nPara el detalle de uno: «cómo creo " + singularWord(creatableOrder(v)[0]) + "»."
			parts[len(parts)-1].speech += " Para el detalle de uno di: cómo creo " + singularWord(creatableOrder(v)[0]) + "."
		}
		return parts
	case "ask":
		return guideAsk(ctx, d)
	case "fields":
		if res != "" {
			if r := v.Resource(res); r != nil {
				return guideFields(ctx, d, r)
			}
		}
		var parts []guidePart
		for _, name := range guideResources(v) {
			parts = append(parts, guideFields(ctx, d, v.Resource(name))...)
		}
		return parts
	case "filters":
		return guideFilters(ctx, d)
	}
	return guideMenu(ctx, d)
}

// creatableOrder lists the creatable resources: the to-do, the agenda and
// the note first (the three doors), then the rest in the owner's order.
func creatableOrder(v *Vocabulary) []string {
	var out []string
	add := func(r *Resource) {
		if r != nil && r.CanCreate && !contains(out, r.Name) {
			out = append(out, r.Name)
		}
	}
	add(taskResource(v))
	add(agendaResource(v))
	add(noteResource(v))
	for _, n := range guideResources(v) {
		add(v.Resource(n))
	}
	return out
}

// guideResources is the owner's order (summary.resources) then the rest.
func guideResources(v *Vocabulary) []string {
	names := append([]string(nil), v.listedOrder...)
	for _, n := range v.ResourceNames() {
		if !contains(names, n) {
			names = append(names, n)
		}
	}
	return names
}

func singularWord(res string) string {
	sing := singular(res)
	if strings.HasSuffix(sing, "a") {
		return "una " + sing
	}
	return "un " + sing
}

// exampleLabel fetches ONE real row label of a resource (a real area, a real
// person) so the example can be repeated as is; "" when none can be read.
func exampleLabel(ctx context.Context, d Deps, target *Resource) string {
	if d.Exec == nil || target == nil {
		return ""
	}
	labels := target.LabelFields()
	if len(labels) == 0 {
		return ""
	}
	rows, _, err := d.Exec.List(ctx, target.Name, url.Values{"per_page": {"1"}, "fields": {"id," + strings.Join(labels, ",")}})
	if err != nil || len(rows) == 0 {
		return ""
	}
	return labelOf(rows[0], firstN(labels, 1))
}

// exampleLabels fetches up to n row labels.
func exampleLabels(ctx context.Context, d Deps, target *Resource, n int) []string {
	if d.Exec == nil || target == nil {
		return nil
	}
	labels := target.LabelFields()
	if len(labels) == 0 {
		return nil
	}
	rows, _, err := d.Exec.List(ctx, target.Name, url.Values{"per_page": {fmt.Sprint(n)}, "fields": {"id," + strings.Join(labels, ",")}})
	if err != nil {
		return nil
	}
	var out []string
	for _, row := range rows {
		if l := labelOf(row, firstN(labels, 1)); l != "" {
			out = append(out, l)
		}
	}
	return out
}

// verifiedCreate reports whether the parser settles phrase as a create of
// res — the self-check before a form is promised as free.
func verifiedCreate(v *Vocabulary, phrase, res string) bool {
	pr := Parse(strings.ReplaceAll(phrase, "[nombre]", "Ana"), v)
	return pr.Sure && pr.Discard == "" && pr.Plan.Kind == "create" && pr.Plan.Resource == res
}

// verifiedRead reports whether the parser settles a question.
func verifiedRead(v *Vocabulary, phrase string) bool {
	pr := Parse(strings.ReplaceAll(phrase, "[nombre]", "Ana"), v)
	return pr.Sure && pr.Discard == "" && !pr.Plan.IsWrite() && pr.Plan.Kind != "unclear" && pr.Plan.Kind != "write"
}

// guideCreate: the structure and a full example for ONE resource; detailed
// adds the optional data and the free alternatives.
func guideCreate(ctx context.Context, d Deps, r *Resource, detailed bool) []guidePart {
	v := d.Vocab
	sing := singular(r.Name)
	art := singularWord(r.Name)
	title := titleField(r)
	if title == nil {
		if lf := r.LabelFields(); len(lf) > 0 {
			title = r.Field(lf[0])
		}
	}
	// the sample title, by the kind of resource — generic phrases
	sample := "revisar el contrato"
	isNote := r == noteResource(v)
	isAgenda := r.Range() != nil && r == agendaResource(v)
	if isNote {
		sample = "hablé del contrato con el banco"
	} else if isAgenda {
		sample = "reunión"
	} else if title != nil && isNameField(title.Name) {
		sample = "Ana Pérez" // a resource named by a person's name
	}
	// the data words: field word + a real example value where possible
	type datum struct{ structure, example, speech, spokenName string }
	var data []datum
	for _, f := range r.Fields {
		if f.Auto || f == title || f.Type == "uuid" || f.Type == "json" || f.Type == "jsonb" || f.Type == "file" {
			continue
		}
		fw := fieldWords(f)
		switch {
		case f.Relation != "":
			t := v.Resource(f.Relation)
			label := exampleLabel(ctx, d, t)
			if label == "" {
				label = "[nombre]"
			}
			data = append(data, datum{structure: fw + " [cuál]", example: fw + " " + label, speech: fw + " " + label, spokenName: "qué " + fw})
		case f.Type == "bool":
			if f.HasDefault && f.Default == true {
				continue
			}
			data = append(data, datum{structure: "[" + fw + "]", example: fw, speech: fw, spokenName: "si es " + fw})
		case len(f.Enum) > 0 && !f.HasMachine:
			data = append(data, datum{structure: "[" + strings.Join(firstN(f.Enum, 3), " / ") + "]", example: f.Enum[0], speech: spokenWord(f.Enum[0]), spokenName: "qué " + fw})
		case f.IsNumeric() && !f.Money && f == durationField(r):
			data = append(data, datum{structure: "[30 minutos]", example: "30 minutos", speech: "treinta minutos", spokenName: "cuántos minutos"})
		}
	}
	// time: a range or a due day
	timeStructure, timeExample, timeSpeech, timeName := "", "", "", ""
	if rg := r.Range(); rg != nil {
		timeStructure, timeExample, timeSpeech, timeName = "[día] de [hora] a [hora]", "mañana de 4 a 5", "mañana de cuatro a cinco", "el día y de qué hora a qué hora"
		if isNote {
			timeExample, timeSpeech = "hoy de 4 a 5", "hoy de cuatro a cinco"
		}
	} else if f := r.DueTimeField(); f != nil {
		timeStructure, timeExample, timeSpeech, timeName = "[mañana / el viernes]", "el viernes", "el viernes", "para cuándo"
	}
	var structure, example, speechEx, spokenData []string
	opener := "crear " + sing + ": "
	spokenOpener := "crear " + sing
	if isAgenda {
		opener, spokenOpener = "agenda ", "agenda"
		structure = append(structure, "agenda [qué]")
	} else if isNote {
		opener, spokenOpener = "anota que ", "anota que"
		structure = append(structure, "anota que [qué pasó]")
	} else {
		structure = append(structure, "crear "+sing+": [qué]")
	}
	example = append(example, opener+sample)
	speechEx = append(speechEx, spokenOpener+" "+sample)
	spokenData = append(spokenData, map[bool]string{true: "qué pasó", false: "qué"}[isNote])
	for i, dt := range data {
		if i >= 3 {
			break
		}
		structure = append(structure, dt.structure)
		example = append(example, dt.example)
		speechEx = append(speechEx, dt.speech)
		spokenData = append(spokenData, dt.spokenName)
	}
	if timeStructure != "" {
		structure = append(structure, timeStructure)
		example = append(example, timeExample)
		speechEx = append(speechEx, timeSpeech)
		spokenData = append(spokenData, timeName)
	}
	sep := ", "
	if isAgenda {
		sep = " "
	}
	phrase := strings.Join(example, sep)
	// the spoken example has no colon (a voice says a pause): both forms
	// must parse before either is promised as free
	spokenPhrase := strings.Join(example, sep)
	if !isAgenda && !isNote {
		spokenPhrase = strings.Replace(spokenPhrase, sing+": ", sing+", ", 1)
	}
	free := verifiedCreate(v, phrase, r.Name) && verifiedCreate(v, spokenPhrase, r.Name)
	var b strings.Builder
	fmt.Fprintf(&b, "✍️ <b>Para crear %s</b> di: «%s».\n", esc(art), esc(strings.Join(structure, sep)))
	fmt.Fprintf(&b, "Por ejemplo: «<b>%s</b>»", esc(phrase))
	if free {
		b.WriteString(" — la entiendo al instante, gratis.")
	} else {
		b.WriteString(" — la piensa el modelo (≈ US$ 0,003).")
	}
	b.WriteString("\nLos datos van en cualquier orden, separados por comas o pausas; el que no digas, lo pregunto o lo dejo vacío. Antes de escribir te muestro todo y espero tu <b>sí</b>.")
	sp := []string{
		sentence("Para crear " + art + " di " + spokenOpener + " y después los datos: " + joinSpoken(spokenData)),
		"En cualquier orden, separados por pausas.",
		sentence("Por ejemplo: " + strings.Join(speechEx, ", ")),
	}
	if free {
		sp = append(sp, "La entiendo al instante y sin costo.")
	} else {
		sp = append(sp, "Esa la piensa el modelo y cuesta unos centavos.")
	}
	sp = append(sp, "El dato que no digas, lo pregunto o lo dejo vacío.", "Antes de escribir te muestro todo y espero tu sí.")
	parts := []guidePart{{text: b.String(), speech: strings.Join(sp, " ")}}
	if !detailed {
		return parts
	}
	// part 2: the other free ways to say it (verified), and what each datum is
	// the other free ways: the text as typed and the same phrase as a
	// voice says it (numbers in words — a spoken example never carries a
	// raw digit)
	type way struct{ text, speech string }
	var alt []way
	add := func(ws ...way) {
		for _, w := range ws {
			if verifiedCreate(v, w.text, r.Name) {
				alt = append(alt, w)
			}
		}
	}
	if r == taskResource(v) {
		add(way{"tengo que " + sample, ""}, way{"anota " + sample + " mañana", ""}, way{"acuérdate de " + sample, ""})
	}
	if isAgenda {
		add(way{"agenda " + sample + " el jueves a las 10", "agenda " + sample + " el jueves a las diez"}, way{"agenda " + sample + " mañana a las 3 por una hora", "agenda " + sample + " mañana a las tres por una hora"})
	}
	if isNote {
		add(way{"registra que " + sample + " a las 3", "registra que " + sample + " a las tres"}, way{"anota que " + sample + " hoy de 2 a 4", "anota que " + sample + " hoy de dos a cuatro"}, way{"anota que " + sample + " ayer de 9 a 10", "anota que " + sample + " ayer de nueve a diez"}, way{"anota que " + sample + " esta mañana", ""})
	}
	var b2 strings.Builder
	var sp2 []string
	if len(alt) > 0 {
		b2.WriteString("<b>También entiendo, gratis:</b>\n")
		for _, a := range alt {
			fmt.Fprintf(&b2, "• «%s»\n", esc(a.text))
		}
		sp2 = append(sp2, "También entiendo, sin costo:")
		for _, a := range alt {
			if a.speech != "" {
				sp2 = append(sp2, sentence(a.speech))
			} else {
				sp2 = append(sp2, sentence(a.text))
			}
		}
	}
	b2.WriteString("<b>Los datos que puede llevar:</b>\n")
	sp2 = append(sp2, "Los datos que puede llevar:")
	for _, dt := range data {
		fmt.Fprintf(&b2, "• %s\n", esc(dt.structure))
		sp2 = append(sp2, sentence(dt.spokenName+", por ejemplo "+dt.speech))
	}
	if timeStructure != "" {
		fmt.Fprintf(&b2, "• %s\n", esc(timeStructure))
		sp2 = append(sp2, sentence(timeName+", por ejemplo "+timeSpeech))
	}
	if sf := r.StateField(); sf != nil {
		fmt.Fprintf(&b2, "El estado no se dice al crear: nace en «%s» y lo cambiás después («marca como %s …»).", esc(strings.Join(sf.Initial, " / ")), esc(transitionWord(sf)))
		sp2 = append(sp2, "El estado no se dice al crear: nace en "+spokenWord(strings.Join(sf.Initial, " o "))+" y lo cambiás después.")
	}
	parts = append(parts, guidePart{text: strings.TrimSpace(b2.String()), speech: strings.Join(sp2, " ")})
	return parts
}

// joinSpoken joins the names of the data as a voice lists them: «qué, cuántos
// minutos, si es urgente y para cuándo».
func joinSpoken(names []string) string {
	switch len(names) {
	case 0:
		return ""
	case 1:
		return names[0]
	}
	return strings.Join(names[:len(names)-1], ", ") + " y " + names[len(names)-1]
}

// spokenWord is a schema word as a voice says it: the underscore is a space.
func spokenWord(w string) string { return strings.ReplaceAll(w, "_", " ") }

// guideAsk: the question shapes on THIS schema, free ones verified.
func guideAsk(ctx context.Context, d Deps) []guidePart {
	v := d.Vocab
	var free []string
	var freeSp []string
	add := func(ph, sp string) {
		if verifiedRead(v, ph) {
			free = append(free, ph)
			if sp == "" {
				sp = ph
			}
			freeSp = append(freeSp, sp)
		}
	}
	names := guideResources(v)
	for i, n := range names {
		if i >= 3 {
			break
		}
		r := v.Resource(n)
		plural, fem := resourceWords(r)
		q := "cuántos"
		if fem {
			q = "cuántas"
		}
		add(q+" "+plural+" hay", "")
		if sf := r.StateField(); sf != nil {
			if w := stateWord(sf); w != "" {
				add(plural+" "+w, "")
			}
		}
		for _, f := range r.Fields {
			if f.Type == "bool" && !f.Auto && !(f.HasDefault && f.Default == true) {
				add(plural+" "+pluralForm(f.Name), "")
				break
			}
		}
		for _, f := range r.Fields {
			if f.Relation != "" {
				if t := v.Resource(f.Relation); t != nil {
					if label := exampleLabel(ctx, d, t); label != "" {
						add(plural+" de "+label, "")
						break
					}
				}
			}
		}
		if r.DefaultTimeField() != "" {
			if r == agendaResource(v) {
				add(plural+" de mañana", "")
			} else {
				add(plural+" de esta semana", "")
			}
		}
	}
	if len(names) > 0 {
		r := v.Resource(names[0])
		plural, _ := resourceWords(r)
		add("los últimos 3 "+plural, "los últimos tres "+plural)
		if sf := r.StateField(); sf != nil && sf.Groupable() {
			add(plural+" por "+strings.ReplaceAll(sf.Name, "_", " "), "")
		}
	}
	if agendaResource(v) != nil {
		add("qué tengo mañana", "")
		add("qué tengo el jueves a las 10", "qué tengo el jueves a las diez")
		add("cuándo estoy libre el viernes", "")
	}
	items := make([]guideItem, len(free))
	for i := range free {
		items[i] = guideItem{text: "• «" + esc(free[i]) + "»", speech: sentence(freeSp[i])}
	}
	return pageParts(
		"❓ <b>Pregunta con la palabra de la cosa y, si quieres, un estado, un nombre o un período.</b>\n<b>Al instante y gratis:</b>",
		"Pregunta con la palabra de la cosa y, si quieres, un estado, un nombre o un período. Estas las respondo al instante y sin costo:",
		items,
		"<b>Con el modelo</b> (≈ US$ 0,003): cualquier frase con palabras que la app no conoce; un texto libre («qué me dijo …») no es un dato y no se responde.\nPara combinar condiciones o filtrar por fecha: «cómo filtro por fecha».",
		"Una frase con palabras que la app no conoce la piensa el modelo y cuesta unos centavos. Para filtrar por fecha di: cómo filtro por fecha.",
		6)
}

// guideFields: the fields of one resource in words, with the forms they take.
func guideFields(ctx context.Context, d Deps, r *Resource) []guidePart {
	v := d.Vocab
	art := singularWord(r.Name)
	var lines, sp []string
	for _, f := range r.Fields {
		if f.Auto || f.Type == "uuid" && f.Relation == "" || f.Type == "json" || f.Type == "jsonb" || f.Type == "file" {
			continue
		}
		fw := fieldWords(f)
		desc := ""
		switch {
		case f.Relation != "":
			t := v.Resource(f.Relation)
			labels := exampleLabels(ctx, d, t, 4)
			if len(labels) > 0 {
				desc = fmt.Sprintf("%s (una de tus %s: %s)", fw, f.Relation, strings.Join(labels, ", "))
			} else {
				desc = fmt.Sprintf("%s (una de tus %s)", fw, f.Relation)
			}
		case len(f.Enum) > 0:
			vals := strings.Join(f.Enum, ", ")
			desc = fmt.Sprintf("%s (%s)", fw, vals)
			var al []string
			for _, val := range f.Enum {
				al = append(al, f.Aliases[val]...)
			}
			if len(al) > 0 {
				desc += " — también entiendo " + strings.Join(firstN(al, 6), ", ")
			}
		case f.Type == "bool":
			desc = fw + " (sí o no)"
		case f.Type == "time":
			desc = fw + " (una fecha, con hora si quieres)"
		case f.IsNumeric():
			desc = fw + " (un número)"
		default:
			desc = fw + " (texto)"
		}
		if f.Required && !f.HasDefault {
			desc += ", obligatorio"
		}
		lines = append(lines, desc)
		sp = append(sp, sentence(spokenWord(strings.NewReplacer(" — ", ". ", " (", ", ", ")", "").Replace(desc))))
	}
	if rg := r.Range(); rg != nil {
		note := fmt.Sprintf("%s y %s forman un horario", fieldWords(r.Field(rg.Start)), fieldWords(r.Field(rg.End)))
		if rg.NoOverlap {
			note += " que no se puede pisar: si agendás encima, te aviso antes de escribir"
		}
		lines = append(lines, note)
		sp = append(sp, sentence(note))
	}
	items := make([]guideItem, len(lines))
	for i := range lines {
		items[i] = guideItem{text: "• " + esc(lines[i]), speech: sp[i]}
	}
	return pageParts(
		fmt.Sprintf("🧩 <b>%s tiene:</b>", esc(strings.ToUpper(art[:1])+art[1:])),
		sentence(strings.ToUpper(art[:1])+art[1:]+" tiene estos datos"),
		items,
		fmt.Sprintf("Para crearla: «cómo creo %s».", esc(art)),
		"Para crearla di: cómo creo "+art+".",
		6)
}

// guideFilters: periods, ranges, combinations, last N — verified on the
// first resources.
func guideFilters(ctx context.Context, d Deps) []guidePart {
	v := d.Vocab
	names := guideResources(v)
	if len(names) == 0 {
		return nil
	}
	r := v.Resource(names[0])
	plural, _ := resourceWords(r)
	var ex, exSp []string
	add := func(ph, sp string) {
		if verifiedRead(v, ph) {
			ex = append(ex, ph)
			if sp == "" {
				sp = ph
			}
			exSp = append(exSp, sp)
		}
	}
	for _, p := range []string{"de hoy", "de ayer", "de antier", "de esta semana", "de la semana pasada", "del mes pasado", "de los últimos 7 días"} {
		add(plural+" "+p, plural+" "+strings.ReplaceAll(p, "7", "siete"))
	}
	if ag := agendaResource(v); ag != nil {
		ap, _ := resourceWords(ag)
		add(ap+" de mañana", "")
		add(ap+" de la semana que viene", "")
		add("qué tengo el jueves de 10 a 11", "qué tengo el jueves de diez a once")
		add("tengo algo mañana a las 4", "tengo algo mañana a las cuatro")
	}
	// combinations: a state + a period, a bool + a period, a name + a state
	if sf := r.StateField(); sf != nil {
		if w := stateWord(sf); w != "" {
			add(plural+" "+w+" de esta semana", "")
		}
	}
	for _, f := range r.Fields {
		if f.Type == "bool" && !f.Auto && !(f.HasDefault && f.Default == true) {
			add(plural+" "+pluralForm(f.Name)+" de esta semana", "")
			if sf := r.StateField(); sf != nil {
				if w := stateWord(sf); w != "" {
					add(plural+" "+pluralForm(f.Name)+" "+w, "")
				}
			}
			break
		}
	}
	for _, f := range r.Fields {
		if f.Relation != "" {
			if t := v.Resource(f.Relation); t != nil {
				if label := exampleLabel(ctx, d, t); label != "" {
					if sf := r.StateField(); sf != nil {
						if w := stateWord(sf); w != "" {
							add(plural+" de "+label+" "+w, "")
						}
					}
					break
				}
			}
		}
	}
	add("los últimos 3 "+plural, "los últimos tres "+plural)
	add("cuántos "+plural+" hay por "+func() string {
		if sf := r.StateField(); sf != nil {
			return strings.ReplaceAll(sf.Name, "_", " ")
		}
		return "estado"
	}(), "")
	items := make([]guideItem, len(ex))
	for i := range ex {
		items[i] = guideItem{text: "• «" + esc(ex[i]) + "»", speech: sentence(exSp[i])}
	}
	return pageParts(
		"📅 <b>Fechas y filtros que entiendo, gratis:</b>",
		"Fechas y filtros que entiendo sin costo:",
		items,
		"Dos condiciones se juntan diciéndolas seguidas. Lo que no está acá lo piensa el modelo (≈ US$ 0,003).",
		"Dos condiciones se juntan diciéndolas seguidas. Lo que no está acá lo piensa el modelo y cuesta unos centavos.",
		6)
}

// guideMenu: level 0 — what the app has and the four doors.
func guideMenu(ctx context.Context, d Deps) []guidePart {
	v := d.Vocab
	app := strings.TrimSpace(v.AppName)
	if app == "" {
		app = "esta app"
	}
	things := firstN(append([]string{}, guideResources(v)...), 6)
	// the example resource: the to-do when there is one, else the first
	ex := things[0]
	if t := taskResource(v); t != nil {
		ex = t.Name
	}
	exPlural, fem := resourceWords(v.Resource(ex))
	q := "cuántos"
	if fem {
		q = "cuántas"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "ℹ️ <b>%s</b> tiene: %s.\n", esc(app), esc(strings.Join(things, ", ")))
	b.WriteString("Puedes <b>preguntar</b> («" + esc(q+" "+exPlural+" hay") + "»)")
	if v.Writable() && len(creatableOrder(v)) > 0 {
		b.WriteString(", <b>crear</b> («crear " + esc(singular(creatableOrder(v)[0])) + ": …»), <b>cambiar</b> («marca como … la …»)")
	}
	b.WriteString(" y pedir el <b>resumen</b> del día.\n")
	b.WriteString("Para aprender: <b>cómo creo algo</b> · <b>qué puedo preguntar</b> · <b>qué campos tiene " + esc(singularWord(ex)) + "</b> · <b>cómo filtro por fecha</b>. Y <b>más</b> para seguir cualquiera.")
	sp := []string{
		sentence(app + " tiene: " + spokenWord(strings.Join(things, ", "))),
		"Puedes preguntar, por ejemplo " + q + " " + exPlural + " hay. Crear, cambiar y pedir el resumen del día.",
		"Para aprender di: cómo creo algo. Qué puedo preguntar. Qué campos tiene " + singularWord(ex) + ". O cómo filtro por fecha.",
		"Y más, para seguir cualquiera.",
	}
	return []guidePart{{text: b.String(), speech: strings.Join(sp, " ")}}
}

// guideItem is one line of a level, in both registers.
type guideItem struct{ text, speech string }

// pageParts breaks a level into parts of at most n items — the SAME items on
// the screen and in the voice, so «más» continues both from the same place
// (a voice never reads twenty items; a screen that showed them all while the
// voice read six would leave the listener a page behind). The intro opens
// the first part, the outro closes the last.
func pageParts(introText, introSpeech string, items []guideItem, outroText, outroSpeech string, n int) []guidePart {
	var parts []guidePart
	for i := 0; i < len(items) || i == 0; i += n {
		j := i + n
		if j > len(items) {
			j = len(items)
		}
		var tl, sl []string
		if i == 0 {
			tl, sl = append(tl, introText), append(sl, introSpeech)
		} else {
			tl = append(tl, "<i>(sigue)</i>")
		}
		for _, it := range items[i:j] {
			tl, sl = append(tl, it.text), append(sl, it.speech)
		}
		if j == len(items) {
			if outroText != "" {
				tl = append(tl, outroText)
			}
			if outroSpeech != "" {
				sl = append(sl, outroSpeech)
			}
		}
		parts = append(parts, guidePart{text: strings.Join(tl, "\n"), speech: strings.Join(sl, " ")})
		if len(items) == 0 {
			break
		}
	}
	return parts
}
