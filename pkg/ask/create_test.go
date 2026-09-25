package ask

import (
	"context"
	"strings"
	"testing"
	"time"
)

// The fixed form and its tolerant cousins (Part C) on Miguel's real schema
// (miguelAgendaSchema, help_test.go): any order, with or without the field
// word, comma or «y», the title as said; a name tried against both targets
// (VOZ-20); a to-do from an infinitive; a note from «anota que».
func TestCreate_FixedFormInAnyOrder(t *testing.T) {
	v := miguelVocab()
	cases := []struct {
		q       string
		res     string
		title   string
		data    map[string]any
		refs    int
		softRef bool
	}{
		{"crear tarea: arreglar las puertas del auto, área casa, urgente", "tareas", "arreglar las puertas del auto", map[string]any{"urgente": true}, 0, false},
		{"crear tarea: urgente, área casa, arreglar las puertas del auto", "tareas", "arreglar las puertas del auto", map[string]any{"urgente": true}, 0, false},
		{"crear tarea arreglar las puertas del auto y urgente y área casa", "tareas", "arreglar las puertas del auto", map[string]any{"urgente": true}, 0, false},
		{"nueva tarea, revisar el contrato, trabajo, urgente", "tareas", "revisar el contrato", map[string]any{"urgente": true}, 1, true},
		{"crear tarea: revisar el contrato, persona Marta, mañana", "tareas", "revisar el contrato", map[string]any{"vence_en": "tomorrow"}, 0, false},
		{"tarea: lavar el carro el sábado", "tareas", "lavar el carro", map[string]any{"vence_en": "next_saturday"}, 0, false},
		{"Tarea organizar suscripciones", "tareas", "organizar suscripciones", nil, 0, false},
		{"anota pagar la luz mañana", "tareas", "pagar la luz", map[string]any{"vence_en": "tomorrow"}, 0, false},
		{"anota llamar a Fabián urgente para mañana", "tareas", "llamar a Fabián", map[string]any{"urgente": true, "vence_en": "tomorrow"}, 1, true},
		{"anota una tarea para Marta", "tareas", "", nil, 1, false},
		{"crear compromiso: almuerzo con Marta, el jueves de 12 a 1", "compromisos", "almuerzo", map[string]any{"inicio": "next_thursday 12:00", "fin": "next_thursday 13:00"}, 0, false},
		{"crear compromiso: el jueves de 12 a 1, almuerzo, con Marta", "compromisos", "almuerzo", map[string]any{"inicio": "next_thursday 12:00", "fin": "next_thursday 13:00"}, 0, false},
		{"anota pagar la luz mañana, 30 minutos", "tareas", "pagar la luz", map[string]any{"vence_en": "tomorrow", "duracion_estimada_min": float64(30)}, 0, false},
	}
	for _, c := range cases {
		r := Parse(c.q, v)
		if !r.Sure || r.Plan.Kind != "create" || r.Plan.Resource != c.res {
			t.Fatalf("%q: sure=%v %s %s (%s)", c.q, r.Sure, r.Plan.Kind, r.Plan.Resource, r.Reason)
		}
		if got, _ := r.Plan.Data["titulo"].(string); got != c.title {
			t.Errorf("%q: title %q, want %q", c.q, got, c.title)
		}
		for k, want := range c.data {
			if got := r.Plan.Data[k]; got != want {
				t.Errorf("%q: %s = %v, want %v", c.q, k, got, want)
			}
		}
		if len(r.Plan.Refs) != c.refs {
			t.Errorf("%q: refs %+v, want %d", c.q, r.Plan.Refs, c.refs)
		} else if c.refs == 1 && r.Plan.Refs[0].Soft != c.softRef {
			t.Errorf("%q: ref soft=%v, want %v", c.q, r.Plan.Refs[0].Soft, c.softRef)
		}
		if strings.HasPrefix(c.q, "crear compromiso") {
			// compromisos has ONE people-like relation here: «con Marta» is placed directly
			if m, _ := r.Plan.Data["persona_id"].(map[string]any); m == nil || m["match"] != "Marta" {
				t.Errorf("%q: persona %+v", c.q, r.Plan.Data["persona_id"])
			}
		}
	}
	// what must NOT be a create
	for _, q := range []string{"tareas urgentes", "Tareas antier", "Eventos listar", "tarea de tipo de trabajo", "cuántas tareas hay", "Listar áreas"} {
		if r := Parse(q, v); r.Sure && r.Plan.Kind == "create" {
			t.Errorf("%q must not be a create: %+v", q, r.Plan)
		}
	}
	// «anota que …» is the note, never the agenda — even with a clock
	r := Parse("anota que la plataforma se cayó hoy de 2 a 4 de la tarde", v)
	if !r.Sure || r.Plan.Resource != "registros" || r.Plan.Data["cuando"] != "today 14:00" || r.Plan.Data["hasta"] != "today 16:00" {
		t.Fatalf("a note with a lapse: %+v (%s)", r.Plan, r.Reason)
	}
	// «no se me olvide» is an obligation; «hay que … mañana» lands the day on the due field
	for q, want := range map[string]string{"no se me olvide comprar pintura": "", "hay que llamar al banco mañana": "tomorrow"} {
		r := Parse(q, v)
		if !r.Sure || r.Plan.Kind != "create" || r.Plan.Resource != "tareas" || (want != "" && r.Plan.Data["vence_en"] != want) {
			t.Errorf("%q: %+v (%s)", q, r.Plan, r.Reason)
		}
	}
	// two intentions: the first is taken, the second is said back
	r = Parse("anota comprar pintura y agenda reunión con Fabián mañana a las 4", v)
	if !r.Sure || r.Plan.Resource != "tareas" || r.Plan.Data["titulo"] != "comprar pintura" || !strings.Contains(r.Plan.Reason, "agenda") {
		t.Fatalf("two intentions: %+v (%s)", r.Plan, r.Reason)
	}
}

// miguelFixtures is a lab like the seeded one: areas, people, a few rows.
func miguelFixtures() *memExec {
	return &memExec{rows: map[string][]map[string]any{
		"areas":    {{"id": "a1", "nombre": "casa"}, {"id": "a2", "nombre": "trabajo"}, {"id": "a3", "nombre": "salud"}},
		"personas": {{"id": "p1", "nombre": "Fabián Gómez"}, {"id": "p2", "nombre": "Fabiana Torres"}, {"id": "p3", "nombre": "Marta Ruiz"}},
		"tareas": {{"id": "t1", "titulo": "arreglar el techo", "estado": "pendiente", "urgente": true, "persona_id": "p1", "area_id": "a1"},
			{"id": "t2", "titulo": "hacer ajustes de reto", "estado": "pendiente", "urgente": true, "area_id": "a2"}},
		"compromisos": {},
	}}
}

func miguelDeps(e *memExec) (Deps, *memWriter) {
	v := miguelVocab()
	w := &memWriter{exec: e}
	return Deps{Vocab: v, Exec: e, Now: now, Write: w, Pending: NewPendingStore(), PendingKey: "t|dueno|u1", Guide: NewGuideStore(), GuideKey: "t|dueno|u1", ModelOff: "disabled"}, w
}

// VOZ-20 end to end: a bare «trabajo» is the area; «Fabián» the person; an
// unknown bare word stays in the title; a name that fits two places asks.
func TestCreate_NamesTriedAgainstEveryTarget(t *testing.T) {
	e := miguelFixtures()
	d, w := miguelDeps(e)
	r := Answer(context.Background(), d, "crear tarea: revisar el contrato, trabajo, urgente")
	if r.Kind != "confirm" || r.Source != "parser" || r.Pending == nil {
		t.Fatalf("want confirm, got %s/%s: %s", r.Kind, r.Source, r.Text)
	}
	if r.Pending.Data["area_id"] != "a2" || r.Pending.Labels["area_id"] != "trabajo" || r.Pending.Data["urgente"] != true {
		t.Fatalf("a bare area name is the area: %+v", r.Pending.Data)
	}
	Answer(context.Background(), d, "no")
	r = Answer(context.Background(), d, "anota llamar a Fabián para mañana")
	if r.Kind != "confirm" || r.Pending.Data["persona_id"] != "p1" || !strings.Contains(r.Text, "Fabián Gómez") || !strings.Contains(r.Text, "llamar a Fabián") {
		t.Fatalf("a name inside the title fills the relation and stays in the title: %s %+v", r.Kind, r.Pending)
	}
	Answer(context.Background(), d, "no")
	r = Answer(context.Background(), d, "nueva tarea, revisar el contrato, jardín")
	if r.Kind != "confirm" || r.Pending.Data["titulo"] != "revisar el contrato jardín" {
		t.Fatalf("an unknown bare word joins the title: %s %+v", r.Kind, r.Pending.Data)
	}
	Answer(context.Background(), d, "no")
	// reads: «tareas de trabajo» → the area; «tareas de Fabi» → asks which person
	r = Answer(context.Background(), d, "tareas de trabajo")
	if r.Kind != "answer" || r.Source != "parser" || !strings.Contains(r.Text, "trabajo") {
		t.Fatalf("tareas de trabajo: %s %s %q", r.Kind, r.Source, r.Text)
	}
	r = Answer(context.Background(), d, "tareas de Fabi")
	if r.Kind != "ambiguous" || !strings.Contains(r.Text, "persona Fabián Gómez") || !strings.Contains(r.Text, "persona Fabiana Torres") {
		t.Fatalf("tareas de Fabi: %s %s", r.Kind, r.Text)
	}
	r = Answer(context.Background(), d, "tareas de Zutano")
	if r.Kind != "not_found" || !strings.Contains(r.Text, "area, persona o titulo") {
		t.Fatalf("tareas de Zutano: %s %s", r.Kind, r.Text)
	}
	// a transition names the row by its own title
	r = Answer(context.Background(), d, "marca como hecha la tarea del techo")
	if r.Kind != "confirm" || r.Pending == nil || r.Pending.RowID != "t1" {
		t.Fatalf("la tarea del techo: %s %s", r.Kind, r.Text)
	}
	r = Answer(context.Background(), d, "sí")
	if r.Kind != "written" || len(w.writes) != 1 || !strings.Contains(w.writes[0], "estado=hecha") {
		t.Fatalf("written: %s %v", r.Kind, w.writes)
	}
}

// Corrections on a pending write (Part A «corregir»): the day, the clock, a
// flag — re-confirmed, never executed; nothing recognizable still cancels.
func TestCorrection_ReissuesTheConfirmation(t *testing.T) {
	e := miguelFixtures()
	d, w := miguelDeps(e)
	r := Answer(context.Background(), d, "anota regar las plantas para pasado mañana")
	if r.Kind != "confirm" || r.Pending.Data["vence_en"] == nil {
		t.Fatalf("setup: %s %s", r.Kind, r.Text)
	}
	id := r.Pending.ID
	r = Answer(context.Background(), d, "no, mejor el viernes")
	if r.Kind != "confirm" || r.Source != "confirm" || r.Pending == nil || r.Pending.ID != id || !strings.Contains(r.Text, "Cambié vence en") || !strings.Contains(r.Labels("vence_en"), "el viernes") {
		t.Fatalf("day correction: %s %s labels=%v", r.Kind, r.Text, r.Pending.Labels)
	}
	r = Answer(context.Background(), d, "sí pero urgente")
	if r.Kind != "confirm" || r.Pending.Data["urgente"] != true || !strings.Contains(r.Text, "Cambié urgente") {
		t.Fatalf("flag correction: %s %s", r.Kind, r.Text)
	}
	if len(w.writes) != 0 {
		t.Fatalf("a correction never writes: %v", w.writes)
	}
	r = Answer(context.Background(), d, "cuántas tareas hay")
	if r.Kind != "answer" || !strings.Contains(r.Text, "Cancelé la escritura") {
		t.Fatalf("an unrelated question cancels and is answered: %s %s", r.Kind, r.Text)
	}
	// the agenda: «mejor a las 5» keeps the day, moves the hour, keeps the length
	r = Answer(context.Background(), d, "agenda reunión con Fabián mañana de 4 a 5")
	if r.Kind != "confirm" || r.Pending.Data["persona_id"] != "p1" {
		t.Fatalf("setup agenda: %s %s", r.Kind, r.Text)
	}
	r = Answer(context.Background(), d, "mejor a las 5")
	if r.Kind != "confirm" || !strings.Contains(r.Speech, "cinco de la tarde") || !strings.Contains(r.Speech, "seis de la tarde") {
		t.Fatalf("hour correction: %s %s", r.Kind, r.Speech)
	}
	r = Answer(context.Background(), d, "que sea el jueves")
	if r.Kind != "confirm" || !strings.Contains(r.Speech, "jueves") || !strings.Contains(r.Speech, "cinco de la tarde") {
		t.Fatalf("day correction on the agenda keeps the hour: %s %s", r.Kind, r.Speech)
	}
}

// Labels is a test helper: the label of one field of the pending.
func (r Result) Labels(field string) string {
	if r.Pending == nil {
		return ""
	}
	return r.Pending.Labels[field]
}

// The fixed commands said to this door (VOZ-21) and the living guide (Part B).
func TestGuideAndCommands_AtZeroCost(t *testing.T) {
	e := miguelFixtures()
	d, _ := miguelDeps(e)
	called := ""
	d.Summary = func(ctx context.Context, what string) (Result, error) {
		called = what
		return Result{Kind: what, Text: "📋 <b>Resumen</b>\n🟢 Nada que atender", Headline: "Nada que atender"}, nil
	}
	for q, want := range map[string]string{"Resumen": "summary", "resumen de hoy": "summary", "Estado": "census", "gasto": "spend"} {
		r := Answer(context.Background(), d, q)
		if r.Kind != want || r.Source != "parser" || r.CostUSD != 0 || called != want {
			t.Fatalf("%q: kind=%s source=%s called=%s", q, r.Kind, r.Source, called)
		}
	}
	// the guide: levels, structure + a full example that WORKS, free vs paid
	r := Answer(context.Background(), d, "cómo creo algo")
	if r.Kind != "guide" || r.Source != "parser" || r.CostUSD != 0 {
		t.Fatalf("cómo creo algo: %s %s", r.Kind, r.Source)
	}
	for _, want := range []string{"crear tarea: [qué]", "Por ejemplo", "gratis", "Hay más"} {
		if !strings.Contains(r.Text, want) {
			t.Fatalf("guide lacks %q:\n%s", want, r.Text)
		}
	}
	// the example the guide gives is settled by the parser as a create
	ex := between(r.Text, "Por ejemplo: «<b>", "</b>»")
	if pr := Parse(ex, d.Vocab); !pr.Sure || pr.Plan.Kind != "create" || pr.Plan.Resource != "tareas" {
		t.Fatalf("the guide's example %q does not work: %+v (%s)", ex, pr.Plan, pr.Reason)
	}
	m := SpeechMetrics(r.Speech)
	if m.Digits > 0 || m.Symbols > 0 || m.MaxWordsSentence > 24 {
		t.Fatalf("guide speech metrics %+v: %q", m, r.Speech)
	}
	r2 := Answer(context.Background(), d, "más")
	if r2.Kind != "guide" || r2.Text == r.Text || !strings.Contains(r2.Text, "agenda") {
		t.Fatalf("«más» continues with the next resource: %s %s", r2.Kind, r2.Text)
	}
	r3 := Answer(context.Background(), d, "cómo creo una tarea")
	if r3.Kind != "guide" || !strings.Contains(r3.Text, "cualquier orden") {
		t.Fatalf("cómo creo una tarea: %s %s", r3.Kind, r3.Text)
	}
	r4 := Answer(context.Background(), d, "más")
	if r4.Kind != "guide" || !strings.Contains(r4.Text, "tengo que revisar el contrato") {
		t.Fatalf("the detail's second part lists the free ways: %s %s", r4.Kind, r4.Text)
	}
	r5 := Answer(context.Background(), d, "qué campos tiene una tarea")
	if r5.Kind != "guide" || !strings.Contains(r5.Text, "casa, trabajo, salud") || !strings.Contains(r5.Text, "por hacer") || !strings.Contains(r5.Text, "Hay más (1 de 2)") {
		t.Fatalf("fields: %s %s", r5.Kind, r5.Text)
	}
	// the screen and the voice carry the SAME six items; «más» continues both
	if strings.Contains(r5.Text, "urgente (sí o no)") || strings.Contains(r5.Speech, "urgente") || !strings.Contains(r5.Speech, "salud") {
		t.Fatalf("page one carries six items on both channels: %s || %s", r5.Text, r5.Speech)
	}
	r5b := Answer(context.Background(), d, "más")
	if r5b.Kind != "guide" || !strings.Contains(r5b.Text, "urgente (sí o no)") || !strings.Contains(r5b.Speech, "Urgente, sí o no") || !strings.Contains(r5b.Text, "cómo creo una tarea") {
		t.Fatalf("fields page two: %s || %s", r5b.Text, r5b.Speech)
	}
	if strings.Contains(r5.Speech, "en_curso") || !strings.Contains(r5.Speech, "en curso") {
		t.Fatalf("a schema word is spoken without its underscore: %s", r5.Speech)
	}
	r6 := Answer(context.Background(), d, "cómo filtro por fecha")
	if r6.Kind != "guide" || !strings.Contains(r6.Text, "de esta semana") || !strings.Contains(r6.Text, "Hay más (1 de") {
		t.Fatalf("filters: %s %s", r6.Kind, r6.Text)
	}
	r6b := Answer(context.Background(), d, "más")
	if r6b.Kind != "guide" || !strings.Contains(r6b.Text, "el jueves de 10 a 11") || !strings.Contains(r6b.Speech, "el jueves de diez a once") {
		t.Fatalf("filters page two: %s || %s", r6b.Text, r6b.Speech)
	}
	// every «…» example of the filters level is parser-sure, on both pages
	for _, ex := range append(examples(r6.Text), examples(r6b.Text)...) {
		if pr := Parse(ex, d.Vocab); !pr.Sure {
			t.Errorf("filters example %q not sure: %s", ex, pr.Reason)
		}
	}
	r7 := Answer(context.Background(), d, "ayuda")
	if r7.Kind != "help" || !strings.Contains(r7.Text, "cómo creo algo") {
		t.Fatalf("menu: %s %s", r7.Kind, r7.Text)
	}
}

func between(s, a, b string) string {
	i := strings.Index(s, a)
	if i < 0 {
		return ""
	}
	s = s[i+len(a):]
	j := strings.Index(s, b)
	if j < 0 {
		return s
	}
	return s[:j]
}

func examples(text string) []string {
	var out []string
	for _, l := range strings.Split(text, "\n") {
		if strings.HasPrefix(l, "• «") {
			out = append(out, strings.TrimSuffix(strings.TrimPrefix(l, "• «"), "»"))
		}
	}
	return out
}

// Prosody (Part D): lists capped, numbers/clocks/dates in words, no symbol.
func TestProsody_SpokenReplies(t *testing.T) {
	if got := ClockWords(16, 0); got != "las cuatro de la tarde" {
		t.Errorf("ClockWords: %q", got)
	}
	if got := ClockWords(9, 30); got != "las nueve y media de la mañana" {
		t.Errorf("ClockWords: %q", got)
	}
	if got := SpokenNumbers("• 10:00–11:00 P1 dentista"); !strings.Contains(got, "de las diez a las once de la mañana") {
		t.Errorf("SpokenNumbers range (the part of the day said once): %q", got)
	}
	if got := ClockRangeWords(11, 0, 14, 0); got != "de las once de la mañana a las dos de la tarde" {
		t.Errorf("ClockRangeWords across noon: %q", got)
	}
	// the digest read through /api/ask (VOZ-21): the hourglass, the ISO date,
	// a signed delta and a trailing count are all said in words
	if got := Speech("Resumen de Agenda, 2026-09-23. ⏳ 9 esperan acción, +5 desde ayer, pendiente: 9.\nen curso: 2\n7 nuevos"); strings.ContainsRune(got, '⏳') || strings.Contains(got, "2026") || strings.Contains(got, "+") || !strings.Contains(got, "veintitrés de septiembre") || !strings.Contains(got, "nueve esperan acción, más cinco desde ayer, pendiente: nueve.") || !strings.Contains(got, "en curso: dos") || !strings.Contains(got, "siete nuevos") {
		t.Errorf("digest speech: %q", got)
	}
	if got := SpokenNumbers("mañana (lun 21 sep) a las 16:00"); !strings.Contains(got, "lunes veintiuno de septiembre") || !strings.Contains(got, "las cuatro de la tarde") {
		t.Errorf("SpokenNumbers date: %q", got)
	}
	if got := SpokenNumbers("$ 120.000 · 3 más · ORD-1003"); strings.Contains(got, "cero pedidos") || !strings.Contains(got, "tres más") || !strings.Contains(got, "ORD-1003") {
		t.Errorf("SpokenNumbers guards money and codes: %q", got)
	}
	e := miguelFixtures()
	for i := 0; i < 9; i++ {
		e.rows["tareas"] = append(e.rows["tareas"], map[string]any{"id": "x" + string(rune('a'+i)), "titulo": "tarea " + NumberWords(i+3), "estado": "pendiente", "urgente": false})
	}
	d, _ := miguelDeps(e)
	r := Answer(context.Background(), d, "tareas pendientes")
	if r.Kind != "answer" {
		t.Fatalf("%s %s", r.Kind, r.Text)
	}
	m := SpeechMetrics(r.Speech)
	if !strings.HasPrefix(r.Speech, "Tienes once tareas:") || !strings.Contains(r.Speech, "Y seis más; mira el panel") || m.Digits > 0 || m.Symbols > 0 || m.MaxWordsSentence > 24 {
		t.Fatalf("spoken list: %+v %q", m, r.Speech)
	}
	if strings.Count(r.Text, "• ") != 10 {
		t.Errorf("the screen keeps the page: %d bullets", strings.Count(r.Text, "• "))
	}
	r = Answer(context.Background(), d, "agenda reunión con Fabián mañana a las 4")
	if r.Kind != "confirm" || !strings.HasPrefix(r.Speech, "Voy a crear un compromiso. Titulo: reunión. ") || !strings.Contains(r.Speech, "Inicio: mañana") || !strings.Contains(r.Speech, "a las cuatro de la tarde") || strings.Contains(r.Speech, "16:00") || strings.Contains(r.Speech, "•") {
		t.Fatalf("spoken confirmation: %s %q", r.Kind, r.Speech)
	}
	if strings.Index(r.Speech, "Inicio:") > strings.Index(r.Speech, "Fin:") {
		t.Fatalf("the start is said before the end: %q", r.Speech)
	}
	if d.Trace = true; true {
		r = Answer(context.Background(), d, "cuántas tareas hay")
		lines := strings.Split(r.Display, "\n")
		if len(lines) != 2 || !strings.HasPrefix(lines[0], "Hay ") || !strings.HasPrefix(lines[1], "Costo: ") {
			t.Fatalf("display = speech + cost line: %q", r.Display)
		}
	}
}

// The owner's real phone (2026-09-24): a Siri shortcut named after the verb
// swallows it, so «anota que …» arrives as «que …». The three sentences he
// dictated, verbatim from the agenda's history, are a note each — and the
// clock forms they carry («de siete a dos de la tarde», «de siete A.M. a dos
// P.M.», «el día de hoy») read as 07:00–14:00 today.
func TestDictationTail_VerblessLogEntryIsTheNote(t *testing.T) {
	s := miguelAgendaSchema()
	v := BuildWithWrites(s, "", func(string) (bool, []string) { return true, nil }, func(string) (bool, bool) { return true, true })
	for _, q := range []string{
		"que estudio estuvo caído hoy de siete a dos de la tarde",
		"que la plataforma estuvo caída de siete a dos de la tarde",
		"que estudio estuvo caído de siete A.M. a dos P.M. el día de hoy",
		"que la plataforma estuvo caída de 7am a 2pm hoy",
	} {
		pr := Parse(q, v)
		if !pr.Sure || pr.Plan.Kind != "create" || pr.Plan.Resource != "registros" {
			t.Fatalf("%q: want the note, got sure=%v %s %s (%s)", q, pr.Sure, pr.Plan.Kind, pr.Plan.Resource, pr.Reason)
		}
		if pr.Plan.Data["cuando"] != "today 07:00" || pr.Plan.Data["hasta"] != "today 14:00" {
			t.Errorf("%q: span %v / %v, want today 07:00 / today 14:00", q, pr.Plan.Data["cuando"], pr.Plan.Data["hasta"])
		}
		if tx, _ := pr.Plan.Data["texto"].(string); !strings.Contains(tx, "estuvo ca") || strings.Contains(tx, "siete") || strings.Contains(tx, "7am") || strings.Contains(tx, "tarde") {
			t.Errorf("%q: the text keeps what happened and not the clock: %q", q, tx)
		}
	}
	// what the tail rule must NOT touch: a question that starts with «qué»
	for _, q := range []string{"qué tengo mañana", "qué tareas hay", "qué puedo preguntar", "qué tal las ventas de ayer", "que compromisos hay de 4 a 5"} {
		pr := Parse(q, v)
		if pr.Sure && pr.Plan.Kind == "create" {
			t.Errorf("%q must never become a note: %+v", q, pr.Plan)
		}
	}
	// the clock forms on the agenda itself
	for q, want := range map[string][2]string{
		"agenda reunión hoy de 7 a 2 de la tarde":     {"today 07:00", "today 14:00"},
		"agenda reunión hoy de 4 a 5 de la tarde":     {"today 16:00", "today 17:00"},
		"agenda reunión hoy de 12 a 1":                {"today 12:00", "today 13:00"},
		"agenda reunión hoy de 1 a 3 de la mañana":    {"today 01:00", "today 03:00"},
		"agenda reunión hoy de 9 a 11 de la mañana":   {"today 09:00", "today 11:00"},
		"agenda reunión hoy de siete A.M. a dos P.M.": {"today 07:00", "today 14:00"},
		"agenda reunión mañana de 7am a 2pm":          {"tomorrow 07:00", "tomorrow 14:00"},
		"agenda reunión el día de hoy de 10 a 11":     {"today 10:00", "today 11:00"},
	} {
		pr := Parse(q, v)
		if !pr.Sure || pr.Plan.Data["inicio"] != want[0] || pr.Plan.Data["fin"] != want[1] {
			t.Errorf("%q: %v / %v, want %v (%s)", q, pr.Plan.Data["inicio"], pr.Plan.Data["fin"], want, pr.Reason)
		}
	}
	if pr := Parse("agenda dentista mañana a las 2pm", v); !pr.Sure || pr.Plan.Data["inicio"] != "tomorrow 14:00" {
		t.Errorf("glued clock alone: %v (%s)", pr.Plan.Data, pr.Reason)
	}
}

// The human forms of a note, from the registro bank of AGENDA-ASISTENTE-S1
// (what an owner says about something that happened, with the verb and
// without it): each one is the note resource, its clocks read as a person
// means them, and the text keeps what happened and nothing else.
func TestNote_HumanForms(t *testing.T) {
	s := miguelAgendaSchema()
	v := BuildWithWrites(s, "", func(string) (bool, []string) { return true, nil }, func(string) (bool, bool) { return true, true })
	cases := []struct{ q, cuando, hasta, texto string }{
		{"apunta que hablé con el banco a las 3", "today 15:00", "", "hablé con el banco"},
		{"anota que se cayó la luz a las 3 y media por dos horas", "today 15:30", "today 17:30", "cayó la luz"},
		{"anota que estuve en el banco desde las 9 hasta las 10 y media", "today 09:00", "today 10:30", "estuve en el banco"},
		{"anota que trabajé en la declaración de renta de 8 a 11 de la mañana", "today 08:00", "today 11:00", "trabajé en la declaración de renta"},
		{"registra que la plataforma se cayó ayer de 7 a 2", "yesterday 07:00", "yesterday 14:00", "plataforma se cayó"},
		{"anota que anoche se fue la luz a las 10", "yesterday 22:00", "", "fue la luz"},
		{"anota que esta mañana fui al gimnasio de 6 a 7", "today 06:00", "today 07:00", "fui al gimnasio"},
		{"anota que el martes hablé con el contador a las 3", "last_tuesday 15:00", "", "hablé con el contador"},
		{"anota que estudio estuvo caído toda la mañana", "today", "", "estudio estuvo caído"},
		{"anota que el estudio estuvo caído entre las 7 y las 2 de la tarde", "today 07:00", "today 14:00", "estudio estuvo caído"},
		{"anota que hablé con el banco tipo 3", "today 15:00", "", "hablé con el banco"},
		{"anota que hablé con el banco como a las 3", "today 15:00", "", "hablé con el banco"},
		{"anota que hablé con el banco a eso de las 3", "today 15:00", "", "hablé con el banco"},
		{"registra: la plataforma estuvo caída de 7 a 2", "today 07:00", "today 14:00", "plataforma estuvo caída"},
		{"nota: hablé con el banco a las 3", "today 15:00", "", "hablé con el banco"},
		{"anota que se cayó la plataforma a las 7 y volvió a las 2", "today 07:00", "today 14:00", "cayó la plataforma y volvió"},
		{"anota que estudio estuvo caído de 7:30 a 14:15", "today 07:30", "today 14:15", "estudio estuvo caído"},
		{"anota que estudio estuvo caído de siete y media a dos y cuarto", "today 07:30", "today 14:15", "estudio estuvo caído"},
		{"anota que estudio estuvo caído de 7 a 14", "today 07:00", "today 14:00", "estudio estuvo caído"},
		{"anota que en la tarde se cayó la plataforma", "today", "", "cayó la plataforma"},
		{"que hablé con el banco a las 3", "today 15:00", "", "hablé con el banco"},
		{"que se cayó la luz a las 3 y media por dos horas", "today 15:30", "today 17:30", "cayó la luz"},
		{"que estuve en el banco desde las 9 hasta las 10 y media", "today 09:00", "today 10:30", "estuve en el banco"},
		{"que estudio estuvo caído toda la mañana", "today", "", "estudio estuvo caído"},
		{"que ayer se fue el agua toda la tarde", "yesterday", "", "fue el agua"},
		{"que hoy me llamó el contador", "today", "", "llamó el contador"},
		{"que se dañó la moto el lunes", "last_monday", "", "dañó la moto"},
		{"que llamé al banco tipo 3", "today 15:00", "", "llamé al banco"},
		// dictated with no «que» at all (the owner's real morning): a past
		// tense and a clock span are a log entry
		{"estudio estuvo caído de 7:30 a 2:15", "today 07:30", "today 14:15", "estudio estuvo caído"},
		{"hablé con el banco a las 3", "today 15:00", "", "hablé con el banco"},
	}
	for _, c := range cases {
		pr := Parse(c.q, v)
		if !pr.Sure || pr.Plan.Kind != "create" || pr.Plan.Resource != "registros" {
			t.Errorf("%q: want the note, got sure=%v %s %s (%s)", c.q, pr.Sure, pr.Plan.Kind, pr.Plan.Resource, pr.Reason)
			continue
		}
		got := func(k string) string { s, _ := pr.Plan.Data[k].(string); return s }
		if got("cuando") != c.cuando || got("hasta") != c.hasta {
			t.Errorf("%q: %q / %q, want %q / %q", c.q, got("cuando"), got("hasta"), c.cuando, c.hasta)
		}
		if got("texto") != c.texto {
			t.Errorf("%q: texto %q, want %q", c.q, got("texto"), c.texto)
		}
	}
	// a question is never a note: «cuántas horas trabajé de 7 a 2» counts,
	// «tareas de ayer» reads, «qué tengo mañana» reads the agenda
	for _, q := range []string{"cuántas horas trabajé de 7 a 2", "tareas de ayer", "qué tengo mañana", "compromisos de 4 a 5", "eventos de 4 a 5", "citas de mañana a las 3"} {
		if pr := Parse(q, v); pr.Sure && pr.Plan.Kind == "create" {
			t.Errorf("%q must never become a note: %+v", q, pr.Plan)
		}
	}
	// … while the singular word (or an alias) still creates
	for _, q := range []string{"compromiso de 4 a 5 con Fabián hoy", "evento de 4 a 5", "cita mañana a las 3"} {
		if pr := Parse(q, v); !pr.Sure || pr.Plan.Kind != "create" || pr.Plan.Resource != "compromisos" {
			t.Errorf("%q: want a create on the agenda, got %+v (%s)", q, pr.Plan, pr.Reason)
		}
	}
	// the singular fields question is the guide too
	if topic, _ := guideTopic(tokenize("qué campo tiene una tarea"), v); topic != "fields" {
		t.Errorf("«qué campo tiene una tarea» → guide fields, got %q", topic)
	}
	// what a person asks back
	for q, period := range map[string]string{"qué registré hoy": "today", "qué anoté ayer": "yesterday", "registros de antier": "day_before_yesterday"} {
		pr := Parse(q, v)
		if !pr.Sure || pr.Plan.Kind != "list" || pr.Plan.Resource != "registros" || pr.Plan.Period == nil || pr.Plan.Period.Range != period {
			t.Errorf("%q: %+v (%s)", q, pr.Plan, pr.Reason)
		}
	}
	// the past tokens resolve
	now := time.Date(2026, 9, 24, 10, 0, 0, 0, time.UTC) // a Thursday
	if tm, _, ok := ResolveTimeValue("last_tuesday 15:00", now); !ok || tm.Weekday() != time.Tuesday || tm.After(now) || tm.Hour() != 15 {
		t.Errorf("last_tuesday: %v %v", tm, ok)
	}
	if tm, _, ok := ResolveTimeValue("last_thursday", now); !ok || tm.Day() != 17 {
		t.Errorf("last_thursday said on a Thursday is a week ago: %v %v", tm, ok)
	}
	if tm, _, ok := ResolveTimeValue("day_before_yesterday 08:00", now); !ok || tm.Day() != 22 || tm.Hour() != 8 {
		t.Errorf("day_before_yesterday: %v %v", tm, ok)
	}
}

// A note's confirmation is READ as a person says it: the end of a span on
// the same day says «hoy» like its start (never a full date beside a «hoy»),
// «la una» never «la uno», a day with no clock gets no default hour, and an
// empty log says «no hay ningún registro», not «nada agendado».
func TestNote_ConfirmationSpeech(t *testing.T) {
	e := miguelFixtures()
	d, _ := miguelDeps(e)
	r := Answer(context.Background(), d, "que hablé con el banco a las 3")
	if r.Kind != "confirm" || !strings.Contains(r.Speech, "Cuando: hoy a las tres de la tarde") || !strings.Contains(r.Speech, "Hasta: hoy a las cuatro de la tarde") {
		t.Fatalf("same-day end says hoy: %s | %s", r.Kind, r.Speech)
	}
	Answer(context.Background(), d, "no")
	r = Answer(context.Background(), d, "anota que ayer se fue el agua toda la tarde")
	if r.Kind != "confirm" || !strings.Contains(r.Speech, "Cuando: ayer") || strings.Contains(r.Speech, "Hasta") || r.Pending.Data["hasta"] != nil {
		t.Fatalf("a day without a clock has no default end: %s | %s | %v", r.Kind, r.Speech, r.Pending.Data)
	}
	Answer(context.Background(), d, "no")
	if got := ClockWords(13, 0); got != "la una de la tarde" {
		t.Errorf("ClockWords(13): %q", got)
	}
	if got := ClockWords(1, 0); got != "la una de la mañana" {
		t.Errorf("ClockWords(1): %q", got)
	}
	r = Answer(context.Background(), d, "registros de ayer")
	if r.Kind != "answer" || strings.Contains(r.Speech, "agendado") || !strings.Contains(r.Speech, "No hay ningún registro") {
		t.Errorf("an empty log is not «nada agendado»: %s | %s", r.Kind, r.Speech)
	}
}

// «registros por área» groups by the relation and names each group with the
// area's name; the interrogative «qué …» never becomes a note.
func TestGroupByRelation_AndQueIsAQuestion(t *testing.T) {
	s := miguelAgendaSchema()
	v := BuildWithWrites(s, "", func(string) (bool, []string) { return true, nil }, func(string) (bool, bool) { return true, true })
	for q, want := range map[string]string{"registros por área": "area_id", "cuántos registros hay por área": "area_id", "tareas por área": "area_id", "compromisos por persona": "persona_id"} {
		pr := Parse(q, v)
		if !pr.Sure || pr.Plan.Kind != "count" || pr.Plan.GroupBy != want {
			t.Errorf("%q: %+v (%s)", q, pr.Plan, pr.Reason)
		}
	}
	for _, q := range []string{"qué anoté hoy en trabajo", "qué registré ayer con Fabián", "qué campo tiene una tarea"} {
		if pr := Parse(q, v); pr.Sure && pr.Plan.Kind == "create" {
			t.Errorf("%q must never become a note: %+v", q, pr.Plan)
		}
	}
	e := miguelFixtures()
	d, _ := miguelDeps(e)
	r := Answer(context.Background(), d, "tareas por área")
	if r.Kind != "answer" || len(r.Groups) == 0 {
		t.Fatalf("tareas por área: %s %s", r.Kind, r.Text)
	}
	for _, g := range r.Groups {
		if len(g.Label) == 36 && strings.Count(g.Label, "-") == 4 {
			t.Errorf("a group by a relation is labelled with the name, not the id: %q", g.Label)
		}
	}
}
