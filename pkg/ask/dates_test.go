package ask

import (
	"context"
	"strings"
	"testing"
	"time"
)

// A log looks back (AGENDA-ASISTENTE-S1 addendum, 2026-09-25): on the
// owner's real question «registros del martes» the parser used to read
// «martes» as a proper name and «el martes» as the coming one; a date was
// not a period at all. Now a weekday on a resource that records what
// happened is the past one, a date needs no year, a month is a period, and
// the agenda keeps looking forward.
func TestLog_LooksBackOnWeekdaysDatesAndMonths(t *testing.T) {
	v := miguelVocab()
	cases := []struct{ q, kind, rng string }{
		{"registros del martes", "list", "last_tuesday"},
		{"qué anoté el martes", "list", "last_tuesday"},
		{"qué hice el martes", "list", "last_tuesday"},
		{"qué pasó el 23 de septiembre", "list", "past_date:09-23"},
		{"qué hicimos en agosto", "list", "past_month:08"},
		{"registros del martes pasado", "list", "last_tuesday"},
		{"registros del 23 de septiembre", "list", "past_date:09-23"},
		{"registros del 23 de septiembre de 2025", "list", "date:2025-09-23"},
		{"registros del 23/09", "list", "past_date:09-23"},
		{"registros del 23/9/2026", "list", "date:2026-09-23"},
		{"registros del veintitrés de septiembre", "list", "past_date:09-23"},
		{"registros del treinta y uno de diciembre", "list", "past_date:12-31"},
		{"registros del primero de octubre", "list", "past_date:10-01"},
		{"registros de septiembre", "list", "past_month:09"},
		{"cuántos registros hay en agosto", "count", "past_month:08"},
		{"registros del mes de agosto", "list", "past_month:08"},
		{"registros de septiembre de 2025", "list", "month:2025-09"},
		{"tareas del martes", "list", "last_tuesday"}, // by its creation stamp: created that Tuesday
		// the agenda looks forward
		{"compromisos del martes", "list", "next_tuesday"},
		{"compromisos del martes pasado", "list", "last_tuesday"},
		{"compromisos del martes que viene", "list", "next_tuesday"},
		{"compromisos del próximo martes", "list", "next_tuesday"},
		{"qué tengo el 23 de septiembre", "list", "date:09-23"},
		{"qué tengo el 3 de octubre de 2026", "list", "date:2026-10-03"},
	}
	for _, c := range cases {
		pr := Parse(c.q, v)
		if !pr.Sure || pr.Plan.Kind != c.kind || pr.Plan.Period == nil || pr.Plan.Period.Range != c.rng || len(pr.Plan.Filters) != 0 {
			t.Errorf("%q: sure=%v kind=%s period=%+v filters=%v (%s)", c.q, pr.Sure, pr.Plan.Kind, pr.Plan.Period, pr.Plan.Filters, pr.Reason)
		}
	}
	// an impossible date is named, at zero cost, never read as a name
	pr := Parse("registros del 31 de febrero", v)
	if !pr.Sure || pr.Plan.Kind != "unclear" || !strings.Contains(pr.Plan.Reason, "«31 de febrero» no es una fecha") {
		t.Errorf("31 de febrero: %+v (%s)", pr.Plan, pr.Reason)
	}
	// a bare «del 23» is NOT a date (a code, a name)
	if pr := Parse("registros del 23", v); pr.Sure && pr.Plan.Period != nil {
		t.Errorf("bare del 23 read as a date: %+v", pr.Plan.Period)
	}
	// writes: a date the owner said, the note looks back, a task's deadline
	// and the agenda do not
	for q, want := range map[string]map[string]string{
		"anota que el 23 de septiembre trabajé en el flujo de seguros de 8 a 4": {"cuando": "past_date:09-23 08:00", "hasta": "past_date:09-23 16:00"},
		"tengo que pagar la luz para el 30 de septiembre":                       {"vence_en": "date:09-30"},
		"agenda dentista el 3 de octubre a las 2 de la tarde":                   {"inicio": "date:10-03 14:00"},
		"anota que el martes pasado hablé con el contador a las 3":              {"cuando": "last_tuesday 15:00"},
	} {
		pr := Parse(q, v)
		if !pr.Sure || pr.Plan.Kind != "create" {
			t.Errorf("%q: %v %s", q, pr.Plan.Kind, pr.Reason)
			continue
		}
		for k, val := range want {
			if pr.Plan.Data[k] != val {
				t.Errorf("%q: %s = %v, want %s", q, k, pr.Plan.Data[k], val)
			}
		}
	}
}

func TestResolve_PastWeekdaysDatesAndMonths(t *testing.T) {
	sat := time.Date(2026, 9, 19, 10, 0, 0, 0, time.FixedZone("BOG", -5*3600)) // a Saturday
	day := func(y int, m time.Month, d int) time.Time { return time.Date(y, m, d, 0, 0, 0, 0, sat.Location()) }
	cases := []struct {
		rng        string
		from, to   time.Time
		words      string
		unresolved bool
	}{
		{"last_tuesday", day(2026, 9, 15), day(2026, 9, 16), "el martes pasado (mar 15 sep)", false},
		{"last_saturday", day(2026, 9, 12), day(2026, 9, 13), "el sábado pasado (sáb 12 sep)", false}, // strictly before today
		{"date:09-23", day(2026, 9, 23), day(2026, 9, 24), "el miércoles 23 de septiembre", false},
		{"past_date:09-23", day(2025, 9, 23), day(2025, 9, 24), "el martes 23 de septiembre de 2025", false}, // not come yet this year
		{"past_date:09-19", day(2026, 9, 19), day(2026, 9, 20), "el sábado 19 de septiembre", false},         // today counts
		{"past_date:02-29", day(2024, 2, 29), day(2024, 3, 1), "el jueves 29 de febrero de 2024", false},     // the last leap year
		{"date:2025-12-30", day(2025, 12, 30), day(2025, 12, 31), "el martes 30 de diciembre de 2025", false},
		{"date:01-03", day(2027, 1, 3), day(2027, 1, 4), "el domingo 3 de enero de 2027", false}, // the coming one
		{"date:09-19", day(2026, 9, 19), day(2026, 9, 20), "el sábado 19 de septiembre", false},  // today counts
		{"month:01", day(2027, 1, 1), day(2027, 2, 1), "en enero de 2027", false},
		{"month:09", day(2026, 9, 1), day(2026, 10, 1), "en septiembre", false},
		{"past_month:12", day(2025, 12, 1), day(2026, 1, 1), "en diciembre de 2025", false},
		{"past_month:09", day(2026, 9, 1), day(2026, 10, 1), "en septiembre", false}, // the month in progress
		{"month:2025-02", day(2025, 2, 1), day(2025, 3, 1), "en febrero de 2025", false},
		{"date:02-29", time.Time{}, time.Time{}, "", true}, // 2026 has no February 29
	}
	for _, c := range cases {
		w, ok := Resolve(c.rng, sat)
		if c.unresolved {
			if ok {
				t.Errorf("%s resolved to %v", c.rng, w)
			}
			continue
		}
		if !ok || !w.From.Equal(c.from) || !w.To.Equal(c.to) || w.Words != c.words {
			t.Errorf("%s: ok=%v %v–%v %q", c.rng, ok, w.From, w.To, w.Words)
		}
	}
	for tok, ok := range map[string]bool{"date:09-23": true, "date:2025-09-23": true, "past_date:12-31": true, "month:09": true, "past_month:01": true, "month:2025-09": true,
		"date:13-01": false, "date:02-30": false, "past_date:2025-09-23": false, "month:13": false, "past_month:2025-09": false, "date:9-23": false, "last_tuesday": true, "next_tuesday": true, "last_week": true, "nope": false} {
		if got := validRange(tok); got != ok {
			t.Errorf("validRange(%s)=%v, want %v", tok, got, ok)
		}
	}
	// the write side reads the same tokens
	if tm, words, ok := ResolveTimeValue("past_date:09-23 08:00", sat); !ok || tm.Year() != 2025 || tm.Hour() != 8 || words != "el martes 23 de septiembre de 2025 a las 08:00" {
		t.Errorf("write past_date: %v %q %v", tm, words, ok)
	}
	if tm, words, ok := ResolveTimeValue("date:10-03 14:00", sat); !ok || tm.Month() != 10 || tm.Day() != 3 || tm.Hour() != 14 || words != "el sábado 3 de octubre a las 14:00" {
		t.Errorf("write date: %v %q %v", tm, words, ok)
	}
	// the follow-up answer to «¿para cuándo?» reads a date too
	for said, want := range map[string]string{"el 23 de septiembre a las 3": "date:09-23 15:00", "23/09": "date:09-23", "el 3 de octubre de 2026": "date:2026-10-03", "el viernes a las 3": "next_friday 15:00"} {
		if got := spanishTimeToken(said); got != want {
			t.Errorf("follow-up %q → %q, want %q", said, got, want)
		}
	}
	if NumberWords(2025) != "dos mil veinticinco" || NumberWords(1999) != "mil novecientos noventa y nueve" || NumberWords(2000) != "dos mil" {
		t.Errorf("years: %q %q %q", NumberWords(2025), NumberWords(1999), NumberWords(2000))
	}
	if s := SpokenNumbers("el martes 23 de septiembre de 2025"); s != "el martes veintitrés de septiembre de dos mil veinticinco" {
		t.Errorf("spoken year: %q", s)
	}
}

// The replies: a log answered by the day it read; an impossible date named;
// the year spoken in words; and the owner's real note «anota que me reuní
// con Camilo de 4 a 5» (2026-09-25) — «con Camilo» is the text of the log
// and a SOFT link: the person is attached when it exists, the note is
// written either way, and the verb Siri swallowed («me reuní con Camilo de
// cuatro a cinco») is still the note.
func TestReplies_LogDaysAndNoteWithSomeone(t *testing.T) {
	e := miguelFixtures()
	d, _ := miguelDeps(e)
	r := Answer(context.Background(), d, "registros del martes")
	if r.Kind != "answer" || r.Source != "parser" || !strings.Contains(r.Understood, "el martes pasado (mar 15 sep)") || !strings.Contains(r.Speech, "martes quince de septiembre") {
		t.Fatalf("del martes: %s %s | %s | %s", r.Kind, r.Source, r.Understood, r.Speech)
	}
	r = Answer(context.Background(), d, "registros del 23 de septiembre")
	if r.Kind != "answer" || !strings.Contains(r.Text, "el martes 23 de septiembre de 2025") || !strings.Contains(r.Speech, "de dos mil veinticinco") || strings.ContainsAny(r.Speech, "0123456789") {
		t.Fatalf("del 23: %s | %s | %s", r.Kind, r.Text, r.Speech)
	}
	r = Answer(context.Background(), d, "registros del 31 de febrero")
	if r.Kind != "unclear" || r.Source != "parser" || r.CostUSD != 0 || !strings.Contains(r.Text, "«31 de febrero» no es una fecha") {
		t.Fatalf("31 de febrero: %s %s %s", r.Kind, r.Source, r.Text)
	}
	r = Answer(context.Background(), d, "registros del 29 de febrero de 2026")
	if r.Kind != "unclear" || r.CostUSD != 0 || !strings.Contains(r.Text, "«29 de febrero de 2026» no es una fecha") {
		t.Fatalf("29 de febrero de 2026: %s %s", r.Kind, r.Text)
	}
	if r := Answer(context.Background(), d, "registros del 31/02"); r.Kind != "unclear" || !strings.Contains(r.Text, "«31/02» no es una fecha") {
		t.Fatalf("31/02: %s %s", r.Kind, r.Text)
	}
	// a model plan may carry a date this year lacks: on the agenda it is
	// said, never a zero window; the log looks back to the last leap year
	if r := execute(context.Background(), d, Plan{Kind: "list", Resource: "compromisos", Period: &Period{Range: "date:02-29"}}); r.Kind != "unclear" || !strings.Contains(r.Text, "Esa fecha no existe (29 de febrero)") {
		t.Fatalf("date:02-29 in 2026: %s %s", r.Kind, r.Text)
	}
	if r := execute(context.Background(), d, Plan{Kind: "list", Resource: "registros", Period: &Period{Range: "date:02-29"}}); r.Kind != "answer" || !strings.Contains(r.Text, "29 de febrero de 2024") {
		t.Fatalf("date:02-29 on the log: %s %s", r.Kind, r.Text)
	}
	for _, q := range []string{"anota que me reuní con Camilo de 4 a 5", "me reuní con Camilo de cuatro a cinco"} {
		r = Answer(context.Background(), d, q)
		if r.Kind != "confirm" || r.Source != "parser" || !strings.Contains(r.Text, "texto: <b>me reuní con Camilo</b>") || strings.Contains(r.Text, "persona:") || !strings.Contains(r.Text, "a las 16:00") {
			t.Fatalf("%q: %s %s\n%s", q, r.Kind, r.Source, r.Text)
		}
		if c := Answer(context.Background(), d, "no"); c.Kind != "cancelled" {
			t.Fatalf("cancel: %s", c.Kind)
		}
	}
	r = Answer(context.Background(), d, "me reuní con Fabián de cuatro a cinco")
	if r.Kind != "confirm" || !strings.Contains(r.Text, "persona: <b>Fabián Gómez</b>") || !strings.Contains(r.Text, "texto: <b>me reuní con Fabián</b>") {
		t.Fatalf("with a known person: %s\n%s", r.Kind, r.Text)
	}
	if c := Answer(context.Background(), d, "no"); c.Kind != "cancelled" {
		t.Fatalf("cancel: %s", c.Kind)
	}
	if len(e.rows["registros"]) != 0 {
		t.Fatalf("a cancelled note was written: %v", e.rows["registros"])
	}
	// the guide teaches the forms, every one parser-sure
	seen := ""
	for _, q := range []string{"cómo filtro por fecha", "más", "más"} {
		g := Answer(context.Background(), d, q)
		seen += g.Text
	}
	for _, want := range []string{"registros del martes", "registros del 18 de septiembre", "registros de septiembre"} {
		if !strings.Contains(seen, "«"+want+"»") {
			t.Errorf("guide lacks %q:\n%s", want, seen)
		}
	}
}

// A verb-less span on a day to come is the agenda's, never a note: the
// corpus caught «bloqueá mañana de 2 a 4 para estudiar» confirmed as a
// registro for tomorrow (a log has no tomorrow). Blocking time is a schedule
// verb.
func TestTail_NeverANoteForTomorrow(t *testing.T) {
	v := miguelVocab()
	for q, want := range map[string][2]string{
		"bloqueá mañana de 2 a 4 para estudiar":        {"tomorrow 14:00", "tomorrow 16:00"},
		"apartá el jueves de 10 a 12 para el dentista": {"next_thursday 10:00", "next_thursday 12:00"},
	} {
		pr := Parse(q, v)
		if !pr.Sure || pr.Plan.Kind != "create" || pr.Plan.Resource != "compromisos" || pr.Plan.Data["inicio"] != want[0] || pr.Plan.Data["fin"] != want[1] {
			t.Errorf("%q: sure=%v %s %s %v (%s)", q, pr.Sure, pr.Plan.Kind, pr.Plan.Resource, pr.Plan.Data, pr.Reason)
		}
	}
	for _, q := range []string{"mañana de 2 a 4 reunión con el contador", "pasado mañana de 9 a 10 gimnasio"} {
		if pr := Parse(q, v); pr.Sure && pr.Plan.Resource == "registros" {
			t.Errorf("%q read as a note: %v", q, pr.Plan.Data)
		}
	}
}
