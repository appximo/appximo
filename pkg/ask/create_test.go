package ask

import (
	"context"
	"strings"
	"testing"
)

// The fixed form and its tolerant cousins (Part C) on Miguel's real schema
// (miguelAgendaSchema, help_test.go): any order, with or without the field
// word, comma or «y», the title as said; a name tried against both targets
// (VOZ-20); a to-do from an infinitive; a note from «anotá que».
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
		{"anotá pagar la luz mañana", "tareas", "pagar la luz", map[string]any{"vence_en": "tomorrow"}, 0, false},
		{"anotá llamar a Fabián urgente para mañana", "tareas", "llamar a Fabián", map[string]any{"urgente": true, "vence_en": "tomorrow"}, 1, true},
		{"anotá una tarea para Marta", "tareas", "", nil, 1, false},
		{"crear compromiso: almuerzo con Marta, el jueves de 12 a 1", "compromisos", "almuerzo", map[string]any{"inicio": "next_thursday 12:00", "fin": "next_thursday 13:00"}, 0, false},
		{"crear compromiso: el jueves de 12 a 1, almuerzo, con Marta", "compromisos", "almuerzo", map[string]any{"inicio": "next_thursday 12:00", "fin": "next_thursday 13:00"}, 0, false},
		{"anotá pagar la luz mañana, 30 minutos", "tareas", "pagar la luz", map[string]any{"vence_en": "tomorrow", "duracion_estimada_min": float64(30)}, 0, false},
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
	// «anotá que …» is the note, never the agenda — even with a clock
	r := Parse("anotá que la plataforma se cayó hoy de 2 a 4 de la tarde", v)
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
	r = Parse("anotá comprar pintura y agendá reunión con Fabián mañana a las 4", v)
	if !r.Sure || r.Plan.Resource != "tareas" || r.Plan.Data["titulo"] != "comprar pintura" || !strings.Contains(r.Plan.Reason, "agendá") {
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
	r = Answer(context.Background(), d, "anotá llamar a Fabián para mañana")
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
	r = Answer(context.Background(), d, "marcá como hecha la tarea del techo")
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
	r := Answer(context.Background(), d, "anotá regar las plantas para pasado mañana")
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
	r = Answer(context.Background(), d, "agendá reunión con Fabián mañana de 4 a 5")
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
	if r2.Kind != "guide" || r2.Text == r.Text || !strings.Contains(r2.Text, "agendá") {
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
	if !strings.HasPrefix(r.Speech, "Tenés once tareas:") || !strings.Contains(r.Speech, "Y seis más; mirá el panel") || m.Digits > 0 || m.Symbols > 0 || m.MaxWordsSentence > 24 {
		t.Fatalf("spoken list: %+v %q", m, r.Speech)
	}
	if strings.Count(r.Text, "• ") != 10 {
		t.Errorf("the screen keeps the page: %d bullets", strings.Count(r.Text, "• "))
	}
	r = Answer(context.Background(), d, "agendá reunión con Fabián mañana a las 4")
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
