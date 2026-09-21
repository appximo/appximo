package ask

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/appximo/appximo/pkg/schema"
)

// ── an agenda (the VOZ-ESCRITURAS-S1 use case): tareas ↔ personas ──

func agendaSchema() *schema.APISchema {
	return &schema.APISchema{
		Schema: "https://appximo.com/schema/v1", Version: "1", Name: "Agenda",
		Resources: map[string]schema.ResourceSchema{
			"tareas": {Fields: map[string]schema.FieldDef{
				"titulo":     {Type: "string", Required: true},
				"persona_id": {Type: "uuid", Relation: "personas"},
				"prioridad":  {Type: "string", Enum: []string{"normal", "urgente"}, Default: "normal"},
				"vence_en":   {Type: "time"},
				"notas":      {Type: "text"},
				"estado": {Type: "string", Enum: []string{"pendiente", "hecha", "cancelada"}, Default: "pendiente", StateMachine: &schema.StateMachine{
					Initial: []string{"pendiente"}, Pending: []string{"pendiente"},
					Transitions: map[string][]string{"pendiente": {"hecha", "cancelada"}, "hecha": {}, "cancelada": {}},
				}},
				"creado_en": {Type: "time", Auto: schema.AutoCreate},
			}},
			"personas":   {Fields: map[string]schema.FieldDef{"nombre": {Type: "string", Required: true}, "telefono": {Type: "string"}}},
			"gastos":     {Fields: map[string]schema.FieldDef{"concepto": {Type: "string", Required: true}, "monto_cents": {Type: "int64", Required: true}, "categoria_id": {Type: "uuid", Relation: "categorias", Required: true}}},
			"categorias": {Fields: map[string]schema.FieldDef{"nombre": {Type: "string", Required: true}}},
		},
	}
}

// memWriter records every write and answers like the engine would.
type memWriter struct {
	exec   *memExec
	writes []string
	refuse *WriteError
	seq    int
}

func (w *memWriter) Write(_ context.Context, kind, resource, id string, data map[string]any) (map[string]any, error) {
	w.writes = append(w.writes, fmt.Sprintf("%s %s %s %v", kind, resource, id, sortedData(data)))
	if w.refuse != nil {
		return nil, w.refuse
	}
	switch kind {
	case "create":
		w.seq++
		row := map[string]any{"id": fmt.Sprintf("new-%d", w.seq)}
		for k, v := range data {
			row[k] = v
		}
		w.exec.rows[resource] = append(w.exec.rows[resource], row)
		return row, nil
	case "update":
		for _, row := range w.exec.rows[resource] {
			if fmt.Sprint(row["id"]) == id {
				for k, v := range data {
					row[k] = v
				}
				return row, nil
			}
		}
		return nil, &WriteError{Status: 404, Msg: "not found"}
	}
	return nil, &WriteError{Status: 400, Msg: "bad op"}
}

func sortedData(m map[string]any) string {
	var parts []string
	for _, k := range sortedKeys(m) {
		parts = append(parts, k+"="+fmt.Sprint(m[k]))
	}
	return strings.Join(parts, ",")
}

const fabianID = "bbbbbbbb-0000-0000-0000-000000000001"

func agendaFixtures() *memExec {
	return &memExec{rows: map[string][]map[string]any{
		"personas": {
			{"id": fabianID, "nombre": "Fabián Gómez"},
			{"id": "bbbbbbbb-0000-0000-0000-000000000002", "nombre": "Marta Ruiz"},
		},
		"tareas": {
			{"id": "t1", "titulo": "Pagar el agua", "estado": "pendiente", "creado_en": now.UTC().Format(time.RFC3339)},
		},
		"categorias": {{"id": "cat1", "nombre": "Servicios"}},
	}}
}

func writeDeps(m *scripted, e *memExec) (Deps, *memWriter, *PendingStore) {
	w := &memWriter{exec: e}
	st := NewPendingStore()
	v := BuildWithWrites(agendaSchema(), "", func(string) (bool, []string) { return true, nil }, func(r string) (bool, bool) { return true, r != "personas" || true })
	d := Deps{Vocab: v, Model: m, Exec: e, Now: now, Write: w, Pending: st, PendingKey: "t|dueno|u1"}
	return d, w, st
}

func TestWrite_ValidateRejectsWhatTheGrammarForbids(t *testing.T) {
	v := BuildWithWrites(agendaSchema(), "", func(string) (bool, []string) { return true, nil }, func(r string) (bool, bool) { return r != "categorias", r == "tareas" })
	cases := []struct{ name, in, want string }{
		{"literal in relation", `{"kind":"create","resource":"tareas","data":{"titulo":"x","persona_id":"Fabián"}}`, `{"match"`},
		{"id in data", `{"kind":"create","resource":"tareas","data":{"id":"abc","titulo":"x"}}`, "never write an id"},
		{"engine-owned", `{"kind":"create","resource":"tareas","data":{"titulo":"x","creado_en":"today"}}`, "engine-owned"},
		{"null empties", `{"kind":"update","resource":"tareas","where":[{"field":"estado","op":"eq","value":"pendiente"}],"data":{"notas":null}}`, "never empties"},
		{"empty string", `{"kind":"create","resource":"tareas","data":{"titulo":""}}`, "empty string"},
		{"bad enum", `{"kind":"create","resource":"tareas","data":{"titulo":"x","prioridad":"altísima"}}`, "not one of normal|urgente"},
		{"bad time token", `{"kind":"create","resource":"tareas","data":{"titulo":"x","vence_en":"2026-13-45"}}`, "not a time token"},
		{"computed date ok", `{"kind":"create","resource":"tareas","data":{"titulo":"x","vence_en":"next_friday 15:00"}}`, ""},
		{"non-initial state", `{"kind":"create","resource":"tareas","data":{"titulo":"x","estado":"hecha"}}`, "declared initial states"},
		{"update needs where", `{"kind":"update","resource":"tareas","data":{"estado":"hecha"}}`, "update needs where"},
		{"role may not create", `{"kind":"create","resource":"categorias","data":{"nombre":"x"}}`, "may not create categorias"},
		{"role may not update", `{"kind":"update","resource":"personas","where":[{"field":"nombre","op":"eq","match":"Marta"}],"data":{"telefono":"1"}}`, "may not update personas"},
		{"unknown field", `{"kind":"create","resource":"tareas","data":{"titulo":"x","color":"rojo"}}`, "has no field"},
		{"no delete kind", `{"kind":"delete","resource":"tareas","where":[{"field":"estado","value":"hecha"}]}`, "is not one of"},
		{"no data", `{"kind":"create","resource":"tareas"}`, "needs data"},
		{"read keys on a write", `{"kind":"create","resource":"tareas","data":{"titulo":"x"},"limit":3}`, "takes only resource, data"},
	}
	for _, c := range cases {
		p, err := ParsePlan(c.in)
		if err == nil {
			err = p.Validate(v)
		}
		switch {
		case c.want == "" && err != nil:
			t.Errorf("%s: unexpected error %v", c.name, err)
		case c.want != "" && (err == nil || !strings.Contains(err.Error(), c.want)):
			t.Errorf("%s: want error containing %q, got %v", c.name, c.want, err)
		}
	}
}

func TestWrite_TimeTokensResolveInTheAppsDay(t *testing.T) {
	// now is Saturday 2026-09-19 10:00 BOG
	cases := map[string]string{
		"today":               "2026-09-19 00:00",
		"tomorrow":            "2026-09-20 00:00",
		"tomorrow 15:00":      "2026-09-20 15:00",
		"next_saturday":       "2026-09-26 00:00", // strictly after today
		"next_monday 08:30":   "2026-09-21 08:30",
		"next_week":           "2026-09-21 00:00",
		"end_of_month":        "2026-09-30 00:00",
		"2026-10-05":          "2026-10-05 00:00",
		"2026-10-05 14:15":    "2026-10-05 14:15",
		"day_after_tomorrow":  "2026-09-21 00:00",
		"now":                 "2026-09-19 10:00",
		"next_friday":         "2026-09-25 00:00",
		"next_monday 25:00":   "",
		"someday":             "",
		"2026-02-30":          "",
		"tomorrow 3 de tarde": "",
	}
	for tok, want := range cases {
		got, _, ok := ResolveTimeValue(tok, now)
		if want == "" {
			if ok {
				t.Errorf("%q: expected rejection, got %v", tok, got)
			}
			continue
		}
		if !ok || got.Format("2006-01-02 15:04") != want {
			t.Errorf("%q: want %s, got %v (ok=%v)", tok, want, got.Format("2006-01-02 15:04"), ok)
		}
	}
	for said, tok := range map[string]string{"mañana": "tomorrow", "el viernes a las 3": "next_friday 15:00", "para el viernes a las 15:30": "next_friday 15:30", "el viernes a las 3 de la mañana": "next_friday 03:00", "mañana de 4 a 5": "tomorrow 16:00", "a las 10": "today 10:00", "pasado mañana": "day_after_tomorrow", "hoy": "today", "2026-11-02": "2026-11-02", "cuando pueda": ""} {
		if got := spanishTimeToken(said); got != tok {
			t.Errorf("spanishTimeToken(%q) = %q, want %q", said, got, tok)
		}
	}
}

func TestWrite_YesIsExactAndAmbiguousYesNeverExecutes(t *testing.T) {
	for _, y := range []string{"sí", "Si", "dale", "OK", "confirmo", "listo.", "de acuerdo", "hacelo"} {
		if !IsYes(y) {
			t.Errorf("%q should be a yes", y)
		}
	}
	for _, n := range []string{"sí pero para el lunes", "creo que sí", "si mañana", "sí, urgente", "bueno", "mmm", "no sé", "sisi claro que no"} {
		if IsYes(n) {
			t.Errorf("%q must NOT be a yes", n)
		}
	}
	for _, n := range []string{"no", "No.", "cancelar", "cancelá", "olvidalo", "nada"} {
		if !IsNo(n) {
			t.Errorf("%q should be a no", n)
		}
	}
}

func TestWrite_CreateResolvesTheNameThenConfirmsThenWrites(t *testing.T) {
	m := &scripted{replies: []string{plan(Plan{Kind: "create", Resource: "tareas", Data: map[string]any{
		"titulo": "Llamar a Fabián para arreglar el techo", "persona_id": map[string]any{"match": "Fabian"}, "prioridad": "urgente", "vence_en": "tomorrow"}})}}
	d, w, st := writeDeps(m, agendaFixtures())
	r := Answer(context.Background(), d, "Anotá llamar a Fabián para arreglar el techo, urgente, para mañana")
	if r.Kind != "confirm" || r.Pending == nil {
		t.Fatalf("want confirm, got %s: %s", r.Kind, r.Text)
	}
	for _, want := range []string{"Voy a crear", "Fabián Gómez", "urgente", "mañana (dom 20 sep)", "Llamar a Fabián para arreglar el techo", "¿Confirmás?"} {
		if !strings.Contains(r.Text, want) {
			t.Errorf("confirmation lacks %q:\n%s", want, r.Text)
		}
	}
	if strings.Contains(r.Speech, "<b>") {
		t.Errorf("speech carries HTML: %s", r.Speech)
	}
	if len(w.writes) != 0 {
		t.Fatalf("NOTHING may be written before the confirmation; got %v", w.writes)
	}
	if st.Len() != 1 {
		t.Fatalf("one pending expected, got %d", st.Len())
	}
	if !strings.Contains(m.systems[0], "WRITES —") || !strings.Contains(m.systems[0], "[may create+update]") {
		t.Errorf("system prompt lacks the write section / abilities")
	}
	// An ambiguous yes cancels. VOZ-AHORRO-S2 (Part C): «sí pero mejor para
	// el lunes» names nothing the grammar could execute, so the parser is
	// SURE it is a stray answer to the confirmation — cancelled, said, and
	// NOT sent to the model (ADR-037 paid a model call here; the real
	// history showed it bought a «no entendí»).
	firstID := r.Pending.ID
	calls := m.calls
	r2 := Answer(context.Background(), d, "sí pero mejor para el lunes")
	if len(w.writes) != 0 {
		t.Fatalf("an ambiguous yes must not write; got %v", w.writes)
	}
	if !strings.Contains(r2.Text, "Cancelé la escritura que estaba pendiente") || !strings.Contains(r2.Text, "no es un <b>sí</b>") {
		t.Errorf("the cancellation must be said, with why: %s", r2.Text)
	}
	if st.ByID(firstID, d.PendingKey) != nil {
		t.Fatalf("the first pending must be gone")
	}
	if r2.Kind != "unclear" || r2.Source != "parser" || r2.CostUSD != 0 || m.calls != calls || r2.Pending != nil {
		t.Fatalf("a stray confirmation is settled by the parser at zero cost: kind=%s source=%s cost=%v calls=%d→%d", r2.Kind, r2.Source, r2.CostUSD, calls, m.calls)
	}
	// A sentence that DOES carry an order after the cancelled yes is planned
	// as a new question (the scripted model re-plans the same create).
	r2 = Answer(context.Background(), d, "sí, anotá llamar a Fabián para arreglar el techo, urgente, para mañana")
	if r2.Kind != "confirm" || r2.Pending == nil || r2.Pending.ID == firstID {
		t.Fatalf("an order after a stray yes is re-planned as a new pending: %s %s", r2.Kind, r2.Text)
	}
	r = r2
	// A plain yes executes through the writer.
	r3 := Answer(context.Background(), d, "sí")
	if r3.Kind != "written" || len(r3.Written) != 1 || r3.Written[0].Resource != "tareas" {
		t.Fatalf("want written, got %s: %s", r3.Kind, r3.Text)
	}
	if len(w.writes) != 1 || !strings.Contains(w.writes[0], "persona_id="+fabianID) || !strings.Contains(w.writes[0], "vence_en=2026-09-20T05:00:00Z") || !strings.Contains(w.writes[0], "prioridad=urgente") {
		t.Fatalf("the write must carry the RESOLVED id and the resolved time: %v", w.writes)
	}
	if strings.Contains(w.writes[0], "Fabian") && !strings.Contains(w.writes[0], "titulo=Llamar a Fabián") {
		t.Fatalf("a dictated name must never land in a relation field: %v", w.writes)
	}
	if r3.Source != "confirm" || r3.CostUSD != 0 {
		t.Errorf("a confirmation costs nothing and names its source: %s %v", r3.Source, r3.CostUSD)
	}
	if !strings.Contains(r3.Text, "Listo") {
		t.Errorf("outcome text: %s", r3.Text)
	}
	// Redaction keeps the shape only.
	if got := Redact("Anotá llamar a Fabián…", r.Plan); got != "[create tareas: persona_id, prioridad, titulo, vence_en]" {
		t.Errorf("redact = %q", got)
	}
}

func TestWrite_MissingRequiredIsAskedNotInvented(t *testing.T) {
	m := &scripted{replies: []string{plan(Plan{Kind: "create", Resource: "gastos", Data: map[string]any{"concepto": "Gas"}})}}
	d, w, _ := writeDeps(m, agendaFixtures())
	r := Answer(context.Background(), d, "anotá un gasto de gas")
	if r.Kind != "ask_field" || r.Pending.Field != "categoria_id" {
		t.Fatalf("want a question for the first missing REQUIRED field (categoria_id), got %s / %q: %s", r.Kind, r.Pending.Field, r.Text)
	}
	r = Answer(context.Background(), d, "servicios")
	if r.Kind != "ask_field" || r.Pending.Field != "monto_cents" {
		t.Fatalf("then the amount, got %s / %s: %s", r.Kind, r.Pending.Field, r.Text)
	}
	r = Answer(context.Background(), d, "cuarenta")
	if r.Kind != "ask_field" || !strings.Contains(r.Text, "no es un monto") {
		t.Fatalf("a value that does not fit is re-asked: %s: %s", r.Kind, r.Text)
	}
	r = Answer(context.Background(), d, "45.000")
	if r.Kind != "confirm" || !strings.Contains(r.Text, "45.000") || !strings.Contains(r.Text, "Servicios") {
		t.Fatalf("want confirm with the amount and the category: %s: %s", r.Kind, r.Text)
	}
	r = Answer(context.Background(), d, "dale")
	if r.Kind != "written" || len(w.writes) != 1 || !strings.Contains(w.writes[0], "monto_cents=4500000") || !strings.Contains(w.writes[0], "categoria_id=cat1") {
		t.Fatalf("write: %s %v", r.Kind, w.writes)
	}
}

func TestWrite_UpdateFindsTheOneRowAndPreChecksTheTransition(t *testing.T) {
	e := agendaFixtures()
	e.rows["tareas"] = append(e.rows["tareas"], map[string]any{"id": "t2", "titulo": "Llamar a Fabián", "estado": "pendiente", "persona_id": fabianID})
	m := &scripted{replies: []string{plan(Plan{Kind: "update", Resource: "tareas",
		Where: []Filter{{Field: "persona_id", Op: "eq", Match: "Fabian"}}, Data: map[string]any{"estado": "hecha"}})}}
	d, w, _ := writeDeps(m, e)
	r := Answer(context.Background(), d, "marcá como hecha la tarea de Fabián")
	if r.Kind != "confirm" || r.Pending.RowID != "t2" {
		t.Fatalf("want confirm on t2, got %s (%v): %s", r.Kind, r.Pending, r.Text)
	}
	if !strings.Contains(r.Text, "pendiente → <b>hecha</b>") || !strings.Contains(r.Text, "Llamar a Fabián") {
		t.Errorf("the confirmation names the row and the transition: %s", r.Text)
	}
	r = Answer(context.Background(), d, "confirmo")
	if r.Kind != "written" || len(w.writes) != 1 || w.writes[0] != "update tareas t2 estado=hecha" {
		t.Fatalf("write: %s %v", r.Kind, w.writes)
	}
	// Now it is hecha: the same order is refused BEFORE any confirmation.
	m.calls = 0
	r = Answer(context.Background(), d, "marcá como hecha la tarea de Fabián")
	if r.Kind != "answer" || !strings.Contains(r.Text, "ya está en <b>hecha</b>") {
		t.Fatalf("want 'ya está así', got %s: %s", r.Kind, r.Text)
	}
	// And a move out of a terminal state is refused with the machine's words.
	m.replies = []string{plan(Plan{Kind: "update", Resource: "tareas", Where: []Filter{{Field: "persona_id", Op: "eq", Match: "Fabian"}}, Data: map[string]any{"estado": "cancelada"}})}
	m.calls = 0
	r = Answer(context.Background(), d, "cancelá la tarea de Fabián")
	if r.Kind != "forbidden" || !strings.Contains(r.Text, "estado final") {
		t.Fatalf("want a forbidden transition, got %s: %s", r.Kind, r.Text)
	}
	if len(w.writes) != 1 {
		t.Fatalf("nothing else may have been written: %v", w.writes)
	}
}

func TestWrite_SeveralRowsAskWhichAndAPickResolves(t *testing.T) {
	e := agendaFixtures()
	e.rows["tareas"] = append(e.rows["tareas"],
		map[string]any{"id": "t2", "titulo": "Llamar a Fabián", "estado": "pendiente", "persona_id": fabianID},
		map[string]any{"id": "t3", "titulo": "Pagarle a Fabián", "estado": "pendiente", "persona_id": fabianID})
	m := &scripted{replies: []string{plan(Plan{Kind: "update", Resource: "tareas",
		Where: []Filter{{Field: "persona_id", Op: "eq", Match: "Fabian"}}, Data: map[string]any{"estado": "hecha"}})}}
	d, w, _ := writeDeps(m, e)
	r := Answer(context.Background(), d, "marcá como hecha la tarea de Fabián")
	if r.Kind != "ambiguous" || r.Pending.Stage != "which" || len(r.Pending.Options) != 2 {
		t.Fatalf("want a pick among 2, got %s: %s", r.Kind, r.Text)
	}
	if len(w.writes) != 0 {
		t.Fatalf("never pick the first row silently: %v", w.writes)
	}
	r = Answer(context.Background(), d, "la 2")
	if r.Kind != "confirm" || r.Pending.RowID != "t3" {
		t.Fatalf("want confirm on t3, got %s: %s", r.Kind, r.Text)
	}
	r = Answer(context.Background(), d, "no")
	if r.Kind != "cancelled" || len(w.writes) != 0 {
		t.Fatalf("no cancels: %s %v", r.Kind, w.writes)
	}
}

func TestWrite_UnknownNameOffersToCreateAndBothConfirm(t *testing.T) {
	m := &scripted{replies: []string{plan(Plan{Kind: "create", Resource: "tareas", Data: map[string]any{
		"titulo": "Llamar a Rocío", "persona_id": map[string]any{"match": "Rocío"}}})}}
	d, w, _ := writeDeps(m, agendaFixtures())
	r := Answer(context.Background(), d, "anotá llamar a Rocío")
	if r.Kind != "not_found" || r.Pending == nil || r.Pending.Stage != "create_ref" {
		t.Fatalf("want an offer to create, got %s: %s", r.Kind, r.Text)
	}
	if !strings.Contains(r.Text, "<b>sí</b> para crearlo") {
		t.Errorf("offer text: %s", r.Text)
	}
	r = Answer(context.Background(), d, "sí")
	if len(w.writes) != 1 || w.writes[0] != "create personas  nombre=Rocío" {
		t.Fatalf("the person is created first (its own confirmation): %v", w.writes)
	}
	if r.Kind != "confirm" || !strings.Contains(r.Text, "Rocío (nuevo)") {
		t.Fatalf("then the task's confirmation with the NEW person: %s: %s", r.Kind, r.Text)
	}
	r = Answer(context.Background(), d, "sí")
	if r.Kind != "written" || len(w.writes) != 2 || !strings.Contains(w.writes[1], "persona_id=new-1") {
		t.Fatalf("the task carries the new id: %s %v", r.Kind, w.writes)
	}
}

func TestWrite_EngineRefusalIsSaidNeverASuccessFace(t *testing.T) {
	m := &scripted{replies: []string{plan(Plan{Kind: "create", Resource: "tareas", Data: map[string]any{"titulo": "x"}})}}
	d, w, _ := writeDeps(m, agendaFixtures())
	w.refuse = &WriteError{Status: 422, Msg: "validation_failed", Fields: []FieldError{{Field: "titulo", Rule: "maxLength", Message: "is too long"}}}
	Answer(context.Background(), d, "anotá x")
	r := Answer(context.Background(), d, "sí")
	if r.Kind != "rejected" || !strings.Contains(r.Text, "titulo: is too long") || !strings.Contains(r.Text, "No escribí nada") {
		t.Fatalf("want the engine's refusal in words: %s: %s", r.Kind, r.Text)
	}
	w.refuse = &WriteError{Status: 403, Msg: "forbidden"}
	Answer(context.Background(), d, "anotá x")
	r = Answer(context.Background(), d, "sí")
	if r.Kind != "forbidden" {
		t.Fatalf("403 → forbidden: %s", r.Kind)
	}
}

func TestWrite_PendingIsPerIdentityAndExpires(t *testing.T) {
	st := NewPendingStore()
	p := &Pending{Key: "t|r|u1", Expires: time.Now().Add(time.Minute), Stage: "confirm"}
	st.Put(p)
	if st.ByID(p.ID, "t|r|u2") != nil {
		t.Fatal("another identity must not see the pending by id")
	}
	if st.ByID(p.ID, "t|r|u1") != p || st.Get("t|r|u1") != p {
		t.Fatal("the owner sees it")
	}
	q := &Pending{Key: "t|r|u1", Expires: time.Now().Add(time.Minute), Stage: "confirm"}
	if rep := st.Put(q); rep != p || st.Len() != 1 {
		t.Fatal("one pending per identity: the new one replaces the old")
	}
	q.Expires = time.Now().Add(-time.Second)
	if st.Get("t|r|u1") != nil || st.Len() != 0 {
		t.Fatal("an expired pending is gone")
	}
	// Confirm by id after expiry executes nothing.
	d, w, _ := writeDeps(&scripted{}, agendaFixtures())
	r := Confirm(context.Background(), d, "nope", "sí")
	if r.Kind != "expired" || len(w.writes) != 0 {
		t.Fatalf("unknown id: %s %v", r.Kind, w.writes)
	}
}

func TestWrite_NoWriterMeansReadOnlyInWords(t *testing.T) {
	m := &scripted{replies: []string{plan(Plan{Kind: "create", Resource: "tareas", Data: map[string]any{"titulo": "x"}})}}
	v := Build(agendaSchema(), "", func(string) (bool, []string) { return true, nil })
	d := Deps{Vocab: v, Model: m, Exec: agendaFixtures(), Now: now}
	r := Answer(context.Background(), d, "anotá x")
	// Without write abilities the vocabulary refuses the plan at validation
	// (the model gets a correction round, then unclear) — the prompt never
	// offered the write forms.
	if r.Kind != "unclear" && r.Kind != "write_refused" {
		t.Fatalf("read-only channel: %s: %s", r.Kind, r.Text)
	}
	// The parser refused the write verb before any model call; had the model
	// been asked, a read-only vocabulary must not teach the write forms.
	if len(m.systems) > 0 && strings.Contains(m.systems[0], "WRITES —") {
		t.Errorf("a read-only vocabulary must not teach the write forms")
	}
	if sys, _ := SystemPrompt(v); strings.Contains(sys, "WRITES —") {
		t.Errorf("a read-only vocabulary must not teach the write forms")
	}
}

func TestWrite_DeleteStaysRefused(t *testing.T) {
	m := &scripted{replies: []string{plan(Plan{Kind: "write", Reason: "borrar"})}}
	d, w, _ := writeDeps(m, agendaFixtures())
	r := Answer(context.Background(), d, "borrá la tarea de Fabián")
	if r.Kind != "write_refused" || !strings.Contains(r.Text, "no borrar") || len(w.writes) != 0 {
		t.Fatalf("delete by voice: %s: %s", r.Kind, r.Text)
	}
}

func TestWrite_ParserSettlesAStateTransitionWithoutTheModel(t *testing.T) {
	v := BuildWithWrites(agendaSchema(), "", func(string) (bool, []string) { return true, nil }, func(string) (bool, bool) { return true, true })
	cases := map[string]string{
		"marcá como hecha la tarea de Fabián":  `update tareas where=[persona_id≈Fabián] estado=hecha`,
		"marca como hecha la tarea de Fabian":  `update tareas where=[persona_id≈Fabian] estado=hecha`,
		"cancelá la tarea de Fabián":           `update tareas where=[persona_id≈Fabián] estado=cancelada`,
		"pasá a hecha la tarea de Marta Ruiz":  `update tareas where=[persona_id≈Marta Ruiz] estado=hecha`,
		"poné en cancelada la tarea de Fabián": `update tareas where=[persona_id≈Fabián] estado=cancelada`,
		// not settled: no row, two names, a create, free text, delete
		"marcá como hecha la tarea":                               "",
		"marcá como hecha la tarea de Fabián y de Marta":          "",
		"anotá llamar a Fabián mañana":                            "",
		"marcá como hecha la tarea urgente de Fabián para mañana": "",
		"borrá la tarea de Fabián":                                "",
		"cancelá todas las tareas":                                "",
	}
	for q, want := range cases {
		pr := Parse(q, v)
		got := ""
		if pr.Sure && pr.Plan.Kind == "update" {
			var w []string
			for _, f := range pr.Plan.Where {
				w = append(w, f.Field+"≈"+f.Match)
			}
			got = "update " + pr.Plan.Resource + " where=[" + strings.Join(w, ",") + "] estado=" + pr.Plan.Data["estado"].(string)
		}
		if got != want {
			t.Errorf("%q: got %q (sure=%v reason=%s), want %q", q, got, pr.Sure, pr.Reason, want)
		}
	}
	// "borrá" stays a deterministic refusal even on a writable vocabulary.
	if pr := Parse("borrá la tarea de Fabián", v); !pr.Sure || pr.Plan.Kind != "write" {
		t.Errorf("delete verb must be refused deterministically: %+v", pr)
	}
	// End to end: no model call, the confirmation, the write.
	m := &scripted{}
	d, w, _ := writeDeps(m, agendaFixtures())
	d.Exec.(*memExec).rows["tareas"] = append(d.Exec.(*memExec).rows["tareas"], map[string]any{"id": "t9", "titulo": "Llamar a Fabián", "estado": "pendiente", "persona_id": fabianID})
	r := Answer(context.Background(), d, "marcá como hecha la tarea de Fabián")
	if r.Kind != "confirm" || r.Source != "parser" || m.calls != 0 {
		t.Fatalf("parser-settled transition: %s %s calls=%d: %s", r.Kind, r.Source, m.calls, r.Text)
	}
	r = Answer(context.Background(), d, "sí")
	if r.Kind != "written" || len(w.writes) != 1 || w.writes[0] != "update tareas t9 estado=hecha" {
		t.Fatalf("write: %s %v", r.Kind, w.writes)
	}
}

func TestWrite_AmbiguousWhereNameIsAPickNotARetry(t *testing.T) {
	e := agendaFixtures()
	e.rows["personas"] = append(e.rows["personas"], map[string]any{"id": "bbbbbbbb-0000-0000-0000-000000000003", "nombre": "Fabiana Torres"})
	e.rows["tareas"] = append(e.rows["tareas"], map[string]any{"id": "t2", "titulo": "Llamar a Fabián", "estado": "pendiente", "persona_id": fabianID})
	m := &scripted{}
	d, w, _ := writeDeps(m, e)
	r := Answer(context.Background(), d, "marcá como hecha la tarea de Fabián")
	if r.Kind != "ambiguous" || r.Pending == nil || r.Pending.Stage != "which" || r.Pending.WhichFor != "where:persona_id" {
		t.Fatalf("want a pick among the people, got %s: %s", r.Kind, r.Text)
	}
	r = Answer(context.Background(), d, "Fabián Gómez")
	if r.Kind != "confirm" || r.Pending.RowID != "t2" {
		t.Fatalf("the pick settles the row: %s: %s", r.Kind, r.Text)
	}
	r = Answer(context.Background(), d, "sí")
	if r.Kind != "written" || len(w.writes) != 1 || w.writes[0] != "update tareas t2 estado=hecha" {
		t.Fatalf("write: %s %v", r.Kind, w.writes)
	}
	// The reply's plan keeps the name as said (never the id).
	if r.Plan == nil || r.Plan.Where[0].Match != "Fabián" {
		t.Fatalf("the plan must keep the name as said: %+v", r.Plan)
	}
}
