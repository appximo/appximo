package summary

import (
	"strings"
	"testing"
	"time"
)

// VOZ-DELTA-S1: the digest compares against ONE baseline (the last snapshot
// from a previous day), words the change, distinguishes what arrived today
// from what has waited for months, folds what is exactly as yesterday, says
// "first summary" with dignity, and decides — by the schema's policy —
// whether the scheduled send speaks or stays silent.

func base(day string, att map[string]ResourceAttention, level string) *Snapshot {
	return &Snapshot{Day: day, Level: level, Attention: att}
}

func TestCompose_FirstTimeSaysSoNeverAZero(t *testing.T) {
	facts := []Facts{{Resource: "facturas", HasState: true, Attention: map[string]int64{"pendiente": 16}, AttentionTotal: 16}}
	r := Compose("T", "t", "2026-09-18", facts, nil)
	if r.Baseline != "" || !r.Changed || r.Level != LevelRed {
		t.Fatalf("first digest: baseline=%q changed=%v level=%s", r.Baseline, r.Changed, r.Level)
	}
	if !strings.Contains(r.Headline, "primer resumen, sin comparación") {
		t.Errorf("first digest must say it has no comparison: %s", r.Headline)
	}
	if strings.Contains(r.Text, "+16") || strings.Contains(r.Text, "desde ayer") {
		t.Errorf("no invented delta on the first digest:\n%s", r.Text)
	}
}

func TestCompose_DeltaAndNewTodayWeighDifferently(t *testing.T) {
	facts := []Facts{
		{Resource: "facturas", HasState: true, Attention: map[string]int64{"pendiente": 19}, AttentionTotal: 19, NewToday: 3, HasNewToday: true},
		{Resource: "ordenes", HasState: true, Attention: map[string]int64{"pagada": 6}, AttentionTotal: 6},
		{Resource: "pagos", HasState: true, Attention: map[string]int64{"pendiente": 2}, AttentionTotal: 2},
	}
	b := base("2026-09-17", map[string]ResourceAttention{
		"facturas": {States: map[string]int64{"pendiente": 16}, Total: 16},
		"ordenes":  {States: map[string]int64{"pagada": 8}, Total: 8},
		"pagos":    {States: map[string]int64{"pendiente": 2}, Total: 2},
	}, LevelRed)
	ApplyBaseline(facts, b)
	r := Compose("T", "t", "2026-09-18", facts, b)
	if r.Baseline != "2026-09-17" || r.Level != LevelRed || !r.Changed {
		t.Fatalf("baseline=%q level=%s changed=%v reasons=%v", r.Baseline, r.Level, r.Changed, r.ChangeReasons)
	}
	for _, want := range []string{"27 esperan acción · +1 desde ayer · 3 llegaron hoy", "⏳ 19 esperan acción (+3 desde ayer · 3 llegaron hoy) (pendiente: 19)", "⏳ 6 esperan acción (−2 desde ayer) (pagada: 6)", "⏸ Igual que ayer: pagos (pendiente: 2)"} {
		if !strings.Contains(r.Text, want) {
			t.Errorf("want %q in:\n%s", want, r.Text)
		}
	}
	if strings.Contains(r.Text, "<b>pagos</b>") {
		t.Errorf("a resource exactly as yesterday must be folded, not a block:\n%s", r.Text)
	}
}

func TestCompose_SameAsYesterdayIsAmberAndUnchanged(t *testing.T) {
	facts := []Facts{{Resource: "facturas", HasState: true, Attention: map[string]int64{"pendiente": 16}, AttentionTotal: 16, HasNewToday: true}}
	b := base("2026-09-17", map[string]ResourceAttention{"facturas": {States: map[string]int64{"pendiente": 16}, Total: 16}}, LevelAmber)
	ApplyBaseline(facts, b)
	r := Compose("T", "t", "2026-09-18", facts, b)
	if r.Level != LevelAmber || r.Changed {
		t.Fatalf("the same red every morning must NOT be red nor a change: level=%s changed=%v reasons=%v", r.Level, r.Changed, r.ChangeReasons)
	}
	if !strings.Contains(r.Headline, "igual que ayer") {
		t.Errorf("headline must say it is the same: %s", r.Headline)
	}
	// Yesterday red (the first digest, novelty), today amber with the same
	// stock: the light went DOWN only because the novelty aged — NOT a change
	// (the same red every morning must not sneak back as "red → amber").
	b2 := base("2026-09-17", b.Attention, LevelRed)
	r2 := Compose("T", "t", "2026-09-18", facts, b2)
	if r2.Changed {
		t.Errorf("red → amber with the same stock is not news: %v", r2.ChangeReasons)
	}
	// Green yesterday, amber today (stock appeared without arriving today, e.g.
	// created yesterday after the digest): UP is a change.
	b3 := base("2026-09-17", map[string]ResourceAttention{}, LevelGreen)
	facts3 := []Facts{{Resource: "facturas", HasState: true, Attention: map[string]int64{"pendiente": 1}, AttentionTotal: 1, HasNewToday: true}}
	ApplyBaseline(facts3, b3)
	r3 := Compose("T", "t", "2026-09-18", facts3, b3)
	if !r3.Changed {
		t.Errorf("green → attention is a change: %v", r3.ChangeReasons)
	}
}

func TestCompose_RedToGreenIsGoodNewsAndAChange(t *testing.T) {
	facts := []Facts{{Resource: "facturas", HasState: true, Attention: map[string]int64{}, AttentionTotal: 0, HasNewToday: true}}
	b := base("2026-09-17", map[string]ResourceAttention{"facturas": {States: map[string]int64{"pendiente": 5}, Total: 5}}, LevelRed)
	ApplyBaseline(facts, b)
	r := Compose("T", "t", "2026-09-18", facts, b)
	if r.Level != LevelGreen || !r.Changed {
		t.Fatalf("level=%s changed=%v", r.Level, r.Changed)
	}
	for _, want := range []string{"Nada que atender — ayer esperaban 5", "✅ ya no espera nada (ayer esperaban 5)"} {
		if !strings.Contains(r.Text, want) {
			t.Errorf("want %q in:\n%s", want, r.Text)
		}
	}
}

func TestDecide_PolicyAndHeartbeat(t *testing.T) {
	changed := Report{Changed: true, Text: "x"}
	Decide(&changed, DefaultPolicy, 3)
	if !changed.ShouldSend || changed.SendReason != "changes" || changed.SilentStreak != 0 {
		t.Errorf("changes → send, streak reset: %+v", changed)
	}
	quiet := Report{Changed: false, Text: "x"}
	Decide(&quiet, DefaultPolicy, 0)
	if quiet.ShouldSend || quiet.SendReason != "silent" || quiet.SilentStreak != 1 {
		t.Errorf("no change → silent, streak 1: %+v", quiet)
	}
	hb := Report{Changed: false, Text: "x"}
	Decide(&hb, Policy{Notify: "changes", QuietDays: 3}, 2)
	if !hb.ShouldSend || hb.SendReason != "heartbeat" || hb.SilentStreak != 0 || !strings.Contains(hb.Text, "3 días sin novedad. Sigo acá") {
		t.Errorf("third silent run with quiet_days=3 → heartbeat, streak reset: %+v", hb)
	}
	never := Report{Changed: false, Text: "x"}
	Decide(&never, Policy{Notify: "changes", QuietDays: 0}, 40)
	if never.ShouldSend || never.SilentStreak != 41 {
		t.Errorf("quiet_days=0 never heartbeats: %+v", never)
	}
	always := Report{Changed: false, Text: "x"}
	Decide(&always, Policy{Notify: "always"}, 5)
	if !always.ShouldSend || always.SendReason != "always" {
		t.Errorf("always → send: %+v", always)
	}
}

func TestScheduledLine(t *testing.T) {
	if !strings.Contains(ScheduledLine(nil), "nunca corrió") {
		t.Error("nil → never ran")
	}
	at := time.Date(2026, 9, 18, 7, 0, 0, 0, time.UTC)
	sent := &Snapshot{ScheduledAt: &at, Decision: "sent:changes"}
	if !strings.Contains(ScheduledLine(sent), "enviado (changes)") {
		t.Error(ScheduledLine(sent))
	}
	quiet := &Snapshot{ScheduledAt: &at, Decision: "silent", SilentStreak: 3}
	if !strings.Contains(ScheduledLine(quiet), "callado a propósito, 3 días sin novedad") {
		t.Error(ScheduledLine(quiet))
	}
}

func TestSnapshotOfAndApplyBaseline_RoundTrip(t *testing.T) {
	facts := []Facts{
		{Resource: "a", HasState: true, Attention: map[string]int64{"x": 2}, AttentionTotal: 2},
		{Resource: "b", HasState: true, Attention: map[string]int64{}, AttentionTotal: 0},
		{Resource: "c"},
	}
	s := SnapshotOf("t", "r", "2026-09-17", LevelRed, facts)
	if len(s.Attention) != 2 || s.Attention["a"].Total != 2 {
		t.Fatalf("snapshot: %+v", s.Attention)
	}
	today := []Facts{
		{Resource: "a", HasState: true, Attention: map[string]int64{"x": 5}, AttentionTotal: 5},
		{Resource: "d", HasState: true, Attention: map[string]int64{"y": 1}, AttentionTotal: 1},
	}
	ApplyBaseline(today, s)
	if !today[0].HasPrev || today[0].PrevTotal != 2 || today[0].Delta() != 3 {
		t.Errorf("a: %+v", today[0])
	}
	if !today[1].HasPrev || today[1].PrevTotal != 0 || len(today[1].Prev) != 0 {
		t.Errorf("d entered attention today — HasPrev with an empty Prev: %+v", today[1])
	}
	r := Compose("T", "t", "2026-09-18", today, s)
	if !strings.Contains(r.Text, "(nuevo desde ayer)") {
		t.Errorf("a resource that entered attention says so:\n%s", r.Text)
	}
}
