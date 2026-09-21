package ask

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/appximo/appximo/pkg/schema"
)

// agendaRangeSchema is the MOTOR-AGENDA-S1 world: eventos with a declared
// range (inicio..fin, no overlap per owner unless ocupa=false), personas.
func agendaRangeSchema() *schema.APISchema {
	return &schema.APISchema{
		Schema: "https://appximo.com/schema/v1", Version: "1", Name: "Agenda",
		Resources: map[string]schema.ResourceSchema{
			"eventos": {
				Fields: map[string]schema.FieldDef{
					"titulo":     {Type: "string", Required: true},
					"persona_id": {Type: "uuid", Relation: "personas"},
					"inicio":     {Type: "time", Required: true},
					"fin":        {Type: "time", Required: true},
					"dueno_id":   {Type: "uuid"},
					"ocupa":      {Type: "bool", Default: true},
					"creado_en":  {Type: "time", Auto: schema.AutoCreate},
				},
				Ranges: map[string]schema.RangeDef{"horario": {Start: "inicio", End: "fin",
					NoOverlap: &schema.NoOverlapDef{Scope: []string{"dueno_id"}, When: &schema.WhenDef{Field: "ocupa", Op: "eq", Val: true}}}},
				Aliases: []string{"compromiso", "cita", "reunion"},
			},
			"personas": {Fields: map[string]schema.FieldDef{"nombre": {Type: "string", Required: true}}},
		},
	}
}

func agendaRangeFixtures() *memExec {
	// now = sáb 19 sep 2026 10:00 BOG; tomorrow = dom 20 sep.
	at := func(day int, h int) string {
		return time.Date(2026, 9, day, h, 0, 0, 0, now.Location()).UTC().Format(time.RFC3339)
	}
	return &memExec{rows: map[string][]map[string]any{
		"personas": {{"id": fabianID, "nombre": "Fabián Gómez"}},
		"eventos": {
			{"id": "e1", "titulo": "reunión con Fabián", "inicio": at(20, 16), "fin": at(20, 17), "ocupa": true, "persona_id": fabianID},
			{"id": "e2", "titulo": "gimnasio", "inicio": at(20, 18), "fin": at(20, 19), "ocupa": true},
			{"id": "e3", "titulo": "dentista", "inicio": at(24, 9), "fin": at(24, 10), "ocupa": true}, // jueves 24
		},
	}}
}

type fakeConflicts struct {
	exec  *memExec
	calls int
}

// Conflicts mirrors the engine's query over the fake rows: blocking rows
// (ocupa=true) overlapping [start,end).
func (f *fakeConflicts) Conflicts(_ context.Context, resource, rangeName, start, end string, scope map[string]string, excludeID string) ([]map[string]any, error) {
	f.calls++
	var out []map[string]any
	for _, r := range f.exec.rows[resource] {
		if r["ocupa"] != true || r["id"] == excludeID {
			continue
		}
		if r["inicio"].(string) < end && r["fin"].(string) > start {
			out = append(out, r)
		}
	}
	return out, nil
}

func agendaDeps(m *scripted) (Deps, *memWriter, *fakeConflicts) {
	e := agendaRangeFixtures()
	w := &memWriter{exec: e}
	fc := &fakeConflicts{exec: e}
	v := BuildWithWrites(agendaRangeSchema(), "", func(string) (bool, []string) { return true, nil }, func(string) (bool, bool) { return true, true })
	d := Deps{Vocab: v, Model: m, Exec: e, Now: now, Write: w, Pending: NewPendingStore(), PendingKey: "t|dueno|u1", Conflicts: fc}
	return d, w, fc
}

// TestAgenda_ParserReadsTheAgendaWithoutTheModel: the questions Miguel asks
// an agenda are settled by the deterministic parser — the resource is
// implied by the day, the period targets the RANGE (overlaps), «a las 4» is
// a contains, «libre» lists the gaps.
func TestAgenda_ParserReadsTheAgendaWithoutTheModel(t *testing.T) {
	v := BuildWithWrites(agendaRangeSchema(), "", func(string) (bool, []string) { return true, nil }, func(string) (bool, bool) { return true, true })
	cases := map[string]string{
		"qué tengo mañana":                      "list eventos period=tomorrow",
		"que tengo mañana":                      "list eventos period=tomorrow",
		"qué hay el jueves":                     "list eventos period=next_thursday",
		"tengo algo mañana a las 4":             "list eventos period=tomorrow at=16:00",
		"tengo algo mañana a las 10":            "list eventos period=tomorrow at=10:00",
		"cuándo estoy libre el jueves":          "free eventos period=next_thursday",
		"qué huecos tengo mañana":               "free eventos period=tomorrow",
		"cuántos compromisos tengo mañana":      "count eventos period=tomorrow",
		"qué eventos tengo la semana que viene": "list eventos period=next_week",
		"qué tengo hoy":                         "list eventos period=today",
	}
	for q, want := range cases {
		pr := Parse(q, v)
		if !pr.Sure {
			t.Errorf("%q: parser not sure: %s", q, pr.Reason)
			continue
		}
		got := pr.Plan.Kind + " " + pr.Plan.Resource
		if pr.Plan.Period != nil {
			got += " period=" + pr.Plan.Period.Range
			if pr.Plan.Period.At != "" {
				got += " at=" + pr.Plan.Period.At
			}
		}
		if got != want {
			t.Errorf("%q: got %q want %q", q, got, want)
		}
	}
	// A stray answer with a future day is STILL discarded where there is no
	// agenda (VOZ-AHORRO-S2 kept), and reaches the model where there is one.
	plain := BuildWithWrites(agendaSchema(), "", func(string) (bool, []string) { return true, nil }, func(string) (bool, bool) { return true, true })
	if pr := Parse("sí pero mejor el lunes", plain); pr.Discard == "" {
		t.Errorf("without an agenda «sí pero mejor el lunes» is a stray answer, got %+v", pr)
	}
}

// TestAgenda_ExecutesOverTheRangeAndWordsAnAgenda: «qué tengo mañana» hits
// filter[horario][overlaps]=<tomorrow>, sorted by start, worded as blocks.
func TestAgenda_ExecutesOverTheRangeAndWordsAnAgenda(t *testing.T) {
	d, _, _ := agendaDeps(&scripted{})
	r := Answer(context.Background(), d, "qué tengo mañana")
	if r.Kind != "answer" || r.Source != "parser" {
		t.Fatalf("want a parser answer, got %s/%s: %s", r.Kind, r.Source, r.Text)
	}
	e := d.Exec.(*memExec)
	last := e.calls[len(e.calls)-1]
	if !strings.Contains(last, "filter%5Bhorario%5D%5Boverlaps%5D=") || !strings.Contains(last, "sort=inicio") {
		t.Fatalf("the agenda must filter by the range and sort by start: %s", last)
	}
	for _, want := range []string{"16:00–17:00 reunión con Fabián", "18:00–19:00 gimnasio", "<b>2</b>"} {
		if !strings.Contains(r.Text, want) {
			t.Errorf("agenda text lacks %q:\n%s", want, r.Text)
		}
	}
	// «a las 4» → contains 16:00 → only the meeting.
	r = Answer(context.Background(), d, "tengo algo mañana a las 4")
	if !strings.Contains(r.Text, "reunión con Fabián") || strings.Contains(r.Text, "gimnasio") {
		t.Errorf("contains 16:00 must list the meeting only:\n%s", r.Text)
	}
	// Free gaps of Thursday: dentist 09–10 → free 00:00–09:00 and 10:00–00:00.
	r = Answer(context.Background(), d, "cuándo estoy libre el jueves")
	if r.Kind != "answer" || !strings.Contains(r.Text, "Libre") || !strings.Contains(r.Text, "10:00–00:00") || !strings.Contains(r.Text, "00:00–09:00") {
		t.Errorf("free gaps wrong:\n%s", r.Text)
	}
	// A day with nothing.
	r = Answer(context.Background(), d, "qué tengo el viernes")
	if !strings.Contains(r.Text, "Nada") {
		t.Errorf("empty day must say nothing is scheduled:\n%s", r.Text)
	}
}

// TestAgenda_ParserSchedulesWithoutTheModel: «agendá reunión con Fabián
// mañana de 4 a 5» → a create with both bounds as tokens and the person
// matched, US$ 0.
func TestAgenda_ParserSchedulesWithoutTheModel(t *testing.T) {
	v := BuildWithWrites(agendaRangeSchema(), "", func(string) (bool, []string) { return true, nil }, func(string) (bool, bool) { return true, true })
	cases := map[string]map[string]any{
		"agendá reunión con Fabián mañana de 4 a 5":                      {"titulo": "reunión", "persona_id": "match:Fabián", "inicio": "tomorrow 16:00", "fin": "tomorrow 17:00"},
		"anotá dentista el jueves a las 9":                               {"titulo": "dentista", "inicio": "next_thursday 09:00"},
		"agendame gimnasio mañana a las 6 por dos horas":                 {"titulo": "gimnasio", "inicio": "tomorrow 18:00", "fin": "tomorrow 20:00"},
		"programá llamada con Marta hoy a las 3 de la tarde hasta las 4": {"titulo": "llamada", "persona_id": "match:Marta", "inicio": "today 15:00", "fin": "today 16:00"},
		"agendá revisión del auto el lunes de 8 a 9 de la mañana":        {"titulo": "revisión del auto", "inicio": "next_monday 08:00", "fin": "next_monday 09:00"},
		"agendá reunión de 11 a 1":                                       {"titulo": "reunión", "inicio": "today 11:00", "fin": "today 13:00"},
	}
	for q, want := range cases {
		pr := Parse(q, v)
		if !pr.Sure || pr.Plan.Kind != "create" || pr.Plan.Resource != "eventos" {
			t.Errorf("%q: not settled: sure=%v kind=%s reason=%s", q, pr.Sure, pr.Plan.Kind, pr.Reason)
			continue
		}
		for k, wv := range want {
			got := pr.Plan.Data[k]
			if m, ok := got.(map[string]any); ok {
				got = "match:" + m["match"].(string)
			}
			if got != wv {
				t.Errorf("%q: %s = %v, want %v", q, k, got, wv)
			}
		}
		if _, has := pr.Plan.Data["fin"]; has != (want["fin"] != nil) {
			t.Errorf("%q: fin presence = %v, want %v", q, has, want["fin"] != nil)
		}
	}
	// Not settled: no clock, a state transition verb, two names.
	for _, q := range []string{"agendá reunión con Fabián", "agendá algo cuando puedas", "marcá como hecha la reunión"} {
		if pr := parseSchedule(q, v); pr.Sure {
			t.Errorf("%q must not be settled by the schedule parser: %+v", q, pr.Plan)
		}
	}
}

// TestAgenda_ConfirmationShowsTheConflictAndAYesFlipsOcupa is what Miguel
// asked for: «agendá reunión de 4 a 5» with something already at 4:30 →
// the confirmation names it; «sí» writes the row as NOT blocking.
func TestAgenda_ConfirmationShowsTheConflictAndAYesFlipsOcupa(t *testing.T) {
	d, w, fc := agendaDeps(&scripted{})
	r := Answer(context.Background(), d, "agendá dentista mañana de 4 y media a 5 y media")
	if r.Kind != "confirm" || r.Pending == nil {
		t.Fatalf("want confirm, got %s (%s): %s", r.Kind, r.Source, r.Text)
	}
	if fc.calls != 1 {
		t.Fatalf("the conflict check must run once before confirming, ran %d", fc.calls)
	}
	for _, want := range []string{"Ya tenés", "reunión con Fabián", "de 16:00 a 17:00", "¿Igual lo agendo?", "no bloquea el horario"} {
		if !strings.Contains(r.Text, want) {
			t.Errorf("confirmation lacks %q:\n%s", want, r.Text)
		}
	}
	if r.Source != "parser" || r.CostUSD != 0 {
		t.Errorf("the schedule is the parser's, US$ 0: source=%s cost=%v", r.Source, r.CostUSD)
	}
	if len(w.writes) != 0 {
		t.Fatalf("nothing written before the yes: %v", w.writes)
	}
	r2 := Answer(context.Background(), d, "sí")
	if r2.Kind != "written" {
		t.Fatalf("yes must write: %s %s", r2.Kind, r2.Text)
	}
	if len(w.writes) != 1 || !strings.Contains(w.writes[0], "ocupa=false") || !strings.Contains(w.writes[0], "titulo=dentista") {
		t.Fatalf("the row must be saved as not blocking (ocupa=false): %v", w.writes)
	}
	// A slot with nothing: no warning, plain confirmation, the default
	// duration filled and said.
	r = Answer(context.Background(), d, "agendá almuerzo mañana a las 12")
	if r.Kind != "confirm" || strings.Contains(r.Text, "Ya tenés") || !strings.Contains(r.Text, "¿Confirmás?") {
		t.Fatalf("free slot must confirm plainly:\n%s", r.Text)
	}
	if !strings.Contains(r.Text, "13:00") || !strings.Contains(r.Text, "por defecto") {
		t.Errorf("the default duration must be filled and said:\n%s", r.Text)
	}
}

// TestAgenda_NonInvertibleRuleAsksForAnotherTime: when the rule cannot be
// flipped (estado ≠ cancelada), the owner is asked for another time and a
// new time re-checks.
func TestAgenda_NonInvertibleRuleAsksForAnotherTime(t *testing.T) {
	s := agendaRangeSchema()
	res := s.Resources["eventos"]
	res.Fields["estado"] = schema.FieldDef{Type: "string", Enum: []string{"ok", "cancelada"}, Default: "ok"}
	res.Ranges["horario"] = schema.RangeDef{Start: "inicio", End: "fin", NoOverlap: &schema.NoOverlapDef{When: &schema.WhenDef{Field: "estado", Op: "ne", Val: "cancelada"}}}
	s.Resources["eventos"] = res
	e := agendaRangeFixtures()
	for _, row := range e.rows["eventos"] {
		row["estado"] = "ok"
	}
	fc := &fakeConflicts{exec: e}
	v := BuildWithWrites(s, "", func(string) (bool, []string) { return true, nil }, func(string) (bool, bool) { return true, true })
	w := &memWriter{exec: e}
	d := Deps{Vocab: v, Model: &scripted{}, Exec: e, Now: now, Write: w, Pending: NewPendingStore(), PendingKey: "t|dueno|u1", Conflicts: fc}
	r := Answer(context.Background(), d, "agendá dentista mañana de 4 a 5")
	if r.Kind != "conflict" || !strings.Contains(r.Text, "no permite encimar") {
		t.Fatalf("a non-invertible rule asks for another time: %s: %s", r.Kind, r.Text)
	}
	r = Answer(context.Background(), d, "mañana de 7 a 8 de la noche")
	if r.Kind != "confirm" || strings.Contains(r.Text, "Ya tenés") || !strings.Contains(r.Text, "19:00") || !strings.Contains(r.Text, "20:00") {
		t.Fatalf("the new time must confirm without a collision:\n%s", r.Text)
	}
	if fc.calls != 2 {
		t.Errorf("re-checked once per time said, ran %d", fc.calls)
	}
}
