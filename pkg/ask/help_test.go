package ask

import (
	"context"
	"regexp"
	"strings"
	"testing"

	"github.com/appximo/appximo/pkg/schema"
)

// miguelAgendaSchema mirrors the REAL agenda on the 58 (APP-AGENDA-S1): a bool
// `urgente` on tareas (no priority enum), declared state aliases, compromisos
// with a range and the alias «evento» the owner actually said, areas.
func miguelAgendaSchema() *schema.APISchema {
	return &schema.APISchema{
		Schema: "https://appximo.com/schema/v1", Version: "1", Name: "Agenda",
		Summary: &schema.SummaryConfig{Resources: []string{"compromisos", "tareas", "personas"}},
		Resources: map[string]schema.ResourceSchema{
			"tareas": {
				Aliases: []string{"cosa", "quehacer"},
				Fields: map[string]schema.FieldDef{
					"titulo":                {Type: "string", Required: true},
					"urgente":               {Type: "bool", Default: false},
					"vence_en":              {Type: "time"},
					"cerrada_en":            {Type: "time"},
					"duracion_estimada_min": {Type: "int"},
					"tiempo_real_min":       {Type: "int"},
					"area_id":               {Type: "uuid", Relation: "areas"},
					"persona_id":            {Type: "uuid", Relation: "personas"},
					"estado": {Type: "string", Enum: []string{"pendiente", "en_curso", "hecha", "cancelada"}, Default: "pendiente",
						Aliases: map[string][]string{"pendiente": {"por hacer", "abierta"}, "hecha": {"lista", "terminada", "completada", "completa"}},
						StateMachine: &schema.StateMachine{Initial: []string{"pendiente"}, Pending: []string{"pendiente"},
							Transitions: map[string][]string{"pendiente": {"en_curso", "hecha", "cancelada"}, "en_curso": {"hecha", "cancelada"}, "hecha": {}, "cancelada": {}}}},
					"dueno_id":  {Type: "uuid"},
					"creado_en": {Type: "time", Auto: schema.AutoCreate},
				},
			},
			"compromisos": {
				Aliases: []string{"cita", "reunion", "evento"},
				Fields: map[string]schema.FieldDef{
					"titulo":     {Type: "string", Required: true},
					"inicio":     {Type: "time", Required: true},
					"fin":        {Type: "time", Required: true},
					"ocupa":      {Type: "bool", Default: true},
					"persona_id": {Type: "uuid", Relation: "personas"},
					"dueno_id":   {Type: "uuid"},
					"estado":     {Type: "string", Enum: []string{"programado", "hecho", "cancelado"}, Default: "programado", Aliases: map[string][]string{"programado": {"agendado"}}},
					"creado_en":  {Type: "time", Auto: schema.AutoCreate},
				},
				Ranges: map[string]schema.RangeDef{"horario": {Start: "inicio", End: "fin",
					NoOverlap: &schema.NoOverlapDef{Scope: []string{"dueno_id"}, When: &schema.WhenDef{Field: "ocupa", Op: "eq", Val: true}}}},
			},
			"registros": {
				Aliases: []string{"nota", "apunte"},
				Fields: map[string]schema.FieldDef{
					"texto":      {Type: "text", Required: true},
					"cuando":     {Type: "time", Default: "now"},
					"hasta":      {Type: "time"},
					"persona_id": {Type: "uuid", Relation: "personas"},
					"area_id":    {Type: "uuid", Relation: "areas"},
					"dueno_id":   {Type: "uuid"},
					"creado_en":  {Type: "time", Auto: schema.AutoCreate},
				},
				Ranges: map[string]schema.RangeDef{"lapso": {Start: "cuando", End: "hasta"}},
			},
			"areas":    {Aliases: []string{"ambito"}, Fields: map[string]schema.FieldDef{"nombre": {Type: "string", Required: true}}},
			"personas": {Aliases: []string{"contacto"}, Fields: map[string]schema.FieldDef{"nombre": {Type: "string", Required: true}}},
		},
	}
}

func miguelVocab() *Vocabulary {
	return BuildWithWrites(miguelAgendaSchema(), "Agenda", func(string) (bool, []string) { return true, nil }, func(string) (bool, bool) { return true, true })
}

// TestParser_MiguelsWordsThatFellToTheModel: the exact sentences the 58's
// history showed as model fallbacks («unknown word: urgentes / Listar /
// eventos / completas / antier») now settle without the model.
func TestParser_MiguelsWordsThatFellToTheModel(t *testing.T) {
	v := miguelVocab()
	cases := []struct {
		q, kind, res string
		filter       *Filter
		period       string
	}{
		{"Qué tareas urgentes tengo", "list", "tareas", &Filter{Field: "urgente", Op: "eq", Value: true}, ""},
		{"tareas no urgentes", "list", "tareas", &Filter{Field: "urgente", Op: "eq", Value: false}, ""},
		{"cuántas tareas urgentes hay", "count", "tareas", &Filter{Field: "urgente", Op: "eq", Value: true}, ""},
		{"Listar compromisos", "list", "compromisos", nil, ""},
		{"Listar áreas", "list", "areas", nil, ""},
		{"Eventos", "list", "compromisos", nil, ""},
		{"Eventos listar", "list", "compromisos", nil, ""},
		{"Tareas completas", "list", "tareas", &Filter{Field: "estado", Op: "eq", Value: "hecha"}, ""},
		{"Tareas antier", "list", "tareas", nil, "day_before_yesterday"},
	}
	for _, c := range cases {
		r := Parse(c.q, v)
		if !r.Sure {
			t.Fatalf("%q: not sure — %s", c.q, r.Reason)
		}
		if r.Plan.Kind != c.kind || r.Plan.Resource != c.res {
			t.Fatalf("%q: got %s %s, want %s %s", c.q, r.Plan.Kind, r.Plan.Resource, c.kind, c.res)
		}
		if c.filter != nil {
			found := false
			for _, f := range r.Plan.Filters {
				if f.Field == c.filter.Field && f.Op == c.filter.Op && f.Value == c.filter.Value {
					found = true
				}
			}
			if !found {
				t.Fatalf("%q: filters %+v lack %+v", c.q, r.Plan.Filters, *c.filter)
			}
		}
		if c.period != "" && (r.Plan.Period == nil || r.Plan.Period.Range != c.period) {
			t.Fatalf("%q: period %+v, want %s", c.q, r.Plan.Period, c.period)
		}
		if err := r.Plan.Validate(v); err != nil {
			t.Fatalf("%q: plan invalid: %v", c.q, err)
		}
	}
	// the bool wording in the understood line
	if got := describeFilter(v.Resource("tareas").Field("urgente"), Filter{Field: "urgente", Op: "eq", Value: true}); got != "urgente: sí" {
		t.Fatalf("describeFilter bool: %q", got)
	}
}

// TestHelp_ExamplesComeFromTheSchema: «ayuda» lists phrases, not resources,
// split into parser-free and model; every example the parser section shows
// IS settled by the parser; the speech carries no symbol, price or digit.
func TestHelp_ExamplesComeFromTheSchema(t *testing.T) {
	v := miguelVocab()
	text, speech := HelpExamples(v, true)
	for _, want := range []string{"«cuántas tareas hay»", "«tareas por hacer»", "«tareas urgentes»", "«compromisos de mañana»", "«qué tengo mañana»", "«tengo que llamar al banco»", "US$ 0", "Con el modelo", "<b>resumen</b>"} {
		if !strings.Contains(text, want) {
			t.Fatalf("help text lacks %q:\n%s", want, text)
		}
	}
	// tareas points at TWO relation targets (area, persona): since VOZ-20 the
	// parser settles «marcá como hecha la tarea de [nombre]» anyway (the
	// engine tries each target), so the self-check keeps it as free.
	if !strings.Contains(text, "marcá como hecha la tarea de [nombre]") {
		t.Fatalf("help lacks the transition the parser now settles:\n%s", text)
	}
	if strings.Contains(text, "Puedo contar, listar o sumar sobre") {
		t.Fatalf("help still lists resources instead of examples:\n%s", text)
	}
	// every quoted example under the free section must be parser-sure
	free := text[strings.Index(text, "Al instante"):strings.Index(text, "Con el modelo")]
	for _, m := range regexp.MustCompile(`«([^»]+)»`).FindAllStringSubmatch(free, -1) {
		q := strings.ReplaceAll(m[1], "[nombre]", "Ana")
		r := Parse(q, v)
		if !r.Sure {
			t.Fatalf("free example %q is NOT parser-sure: %s", q, r.Reason)
		}
	}
	for _, bad := range []string{"US$", "«", "»", "•", "[nombre]", "⚙", "<b>", "≈", "3 "} {
		if strings.Contains(speech, bad) {
			t.Fatalf("speech carries %q:\n%s", bad, speech)
		}
	}
	if regexp.MustCompile(`[0-9]`).MatchString(speech) {
		t.Fatalf("speech carries a digit:\n%s", speech)
	}
	// short sentences: one pause per example, none longer than ~90 chars
	for _, s := range strings.Split(speech, ". ") {
		if len([]rune(s)) > 110 {
			t.Fatalf("speech sentence too long for a voice assistant (%d runes): %q", len([]rune(s)), s)
		}
	}
	if !strings.Contains(speech, "sin costo") || !strings.Contains(speech, "cuestan unos centavos") || !strings.Contains(speech, "Nunca borro nada") {
		t.Fatalf("speech lacks the free/model split or the write promise:\n%s", speech)
	}
}

// TestHelp_CostsNothingAndKeepsItsSpeech: through Answer, «ayuda» is a parser
// discard — no model (Model nil would answer "disabled" if it were reached),
// US$ 0 — and the composed speech survives (not the HTML-stripped text).
func TestHelp_CostsNothingAndKeepsItsSpeech(t *testing.T) {
	v := miguelVocab()
	e := &memExec{rows: map[string][]map[string]any{}}
	d := Deps{Vocab: v, Exec: e, Now: now, Trace: true, Write: &memWriter{exec: e}, ModelOff: "disabled"}
	for _, q := range []string{"ayuda", "qué puedo preguntar", "Ayuda"} {
		r := Answer(context.Background(), d, q)
		if (r.Kind != "help" && r.Kind != "guide") || r.Source != "parser" || r.CostUSD != 0 {
			t.Fatalf("%q: kind=%s source=%s cost=%v", q, r.Kind, r.Source, r.CostUSD)
		}
		if strings.Contains(r.Speech, "US$") || strings.Contains(r.Speech, "⚙") || (r.Kind == "help" && !strings.Contains(r.Speech, "cómo creo algo")) {
			t.Fatalf("%q: speech is not the composed one: %q", q, r.Speech)
		}
		if r.Kind == "help" && !strings.Contains(strings.ToLower(r.Display), "cuántas tareas hay") {
			t.Fatalf("%q: display lacks the examples: %q", q, r.Display)
		}
	}
}

// TestDisplay_ReadsAloudInBothTraceStates: display is the spoken form; with
// APPXIMO_ASK_TRACE on it gains ONE last line (the cost) and nothing else
// changes — a Siri shortcut that speaks display hears the same answer either
// way. No pictograph, bullet or guillemet reaches it.
func TestDisplay_ReadsAloudInBothTraceStates(t *testing.T) {
	v := miguelVocab()
	e := &memExec{rows: map[string][]map[string]any{"tareas": {{"id": "t1", "titulo": "pagar la luz", "estado": "pendiente", "urgente": true}}}}
	for _, q := range []string{"cuántas tareas urgentes hay", "tareas urgentes", "ayuda", "borrá todas las tareas"} {
		off := Answer(context.Background(), Deps{Vocab: v, Exec: e, Now: now, ModelOff: "disabled"}, q)
		on := Answer(context.Background(), Deps{Vocab: v, Exec: e, Now: now, ModelOff: "disabled", Trace: true}, q)
		if off.Display == "" || off.Display != off.Speech {
			t.Fatalf("%q: display off %q must be the speech %q", q, off.Display, off.Speech)
		}
		lines := strings.Split(on.Display, "\n")
		if len(lines) < 2 || strings.Join(lines[:len(lines)-1], "\n") != off.Display {
			t.Fatalf("%q: display on must be display off + one cost line:\noff=%q\non =%q", q, off.Display, on.Display)
		}
		if !strings.HasPrefix(lines[len(lines)-1], "Costo: ") || !strings.Contains(lines[len(lines)-1], "US$ 0") {
			t.Fatalf("%q: cost line %q", q, lines[len(lines)-1])
		}
		for _, bad := range []string{"⚙", "•", "«", "»", "✅", "🤔", "ℹ", "<"} {
			if strings.Contains(on.Display, bad) || strings.Contains(on.Speech, bad) {
				t.Fatalf("%q: %q reaches the voice: display=%q", q, bad, on.Display)
			}
		}
		if on.Speech != off.Speech || strings.Contains(on.Speech, "Costo:") {
			t.Fatalf("%q: speech must never carry the trace: %q", q, on.Speech)
		}
	}
}
