package main

import (
	"strings"
	"testing"
	"time"
)

// The production guard is a heuristic; these pin the two sides of it.
func TestDrillHasPublicTLD(t *testing.T) {
	for _, h := range []string{"petfriendly.appximo.com", "api.example.io", "shop.co"} {
		if !hasPublicTLD(h) {
			t.Errorf("%s should look public", h)
		}
	}
	for _, h := range []string{"localhost", "acme.localhost", "applab-target-basic.internal", "box.lan", "127.0.0.1", "10.116.0.7", "nimbus"} {
		if hasPublicTLD(h) {
			t.Errorf("%s should NOT look public", h)
		}
	}
}

func TestDrillGuardRefusesProductionWithoutFlag(t *testing.T) {
	tgt := &drillTarget{lang: "en", prodReason: "the app is served at the public domain x.com"}
	if err := tgt.guard("load"); err == nil {
		t.Fatal("load on a production-looking target must be refused without --production")
	}
	if err := tgt.guard("readonly"); err != nil {
		t.Fatalf("a read-only drill is always allowed: %v", err)
	}
	if err := tgt.guard("ephemeral"); err != nil {
		t.Fatalf("an ephemeral-tenant drill is allowed: %v", err)
	}
	tgt.production = true
	if err := tgt.guard("box"); err != nil {
		t.Fatalf("--production lifts the refusal: %v", err)
	}
}

// drillPickInsertable must fill required fields blindly only when it CAN —
// a required relation, file or pattern makes the resource non-insertable, and
// then the read path is broken instead (never a 422 that proves nothing).
func TestDrillPickInsertable(t *testing.T) {
	raw := []byte(`{"resources":{
	  "asignaciones":{"fields":{"empleado_id":{"type":"uuid","required":true,"relation":"empleados"}}},
	  "departamentos":{"fields":{"codigo":{"type":"string","required":true,"pattern":"^[A-Z]{2,5}$"},"nombre":{"type":"string","required":true}}},
	  "tareas":{"fields":{"titulo":{"type":"string","required":true,"minLength":8},"estado":{"type":"string","enum":["abierta","cerrada"],"required":true,"state_machine":{"initial":"abierta","transitions":{"abierta":["cerrada"]}}},"puntos":{"type":"int","required":true,"min":3},"creado":{"type":"time","auto":"create"},"notas":{"type":"text"}}}
	}}`)
	name, body, ok := drillPickInsertable(raw, "")
	if !ok || name != "tareas" {
		t.Fatalf("expected tareas to be the insertable one, got %q ok=%v", name, ok)
	}
	if body["estado"] != "abierta" || body["puntos"] != 3.0 || len(body["titulo"].(string)) < 8 {
		t.Fatalf("body not built from the declared rules: %v", body)
	}
	if _, has := body["creado"]; has {
		t.Fatal("an auto field must never be sent")
	}
	if _, _, ok := drillPickInsertable(raw, "asignaciones"); ok {
		t.Fatal("a required relation cannot be filled blindly")
	}
}

func TestProbeSummaryOutage(t *testing.T) {
	type s struct {
		at   time.Time
		code int
		ms   float64
	}
	t0 := time.Now().Add(-30 * time.Second)
	var samples []s
	for i := 0; i < 300; i++ {
		code := 200
		if i >= 80 && i < 105 {
			code = 0
		}
		samples = append(samples, s{t0.Add(time.Duration(i) * 100 * time.Millisecond), code, 2})
	}
	out := probeSummary(samples, func(x s) (time.Time, int, float64) { return x.at, x.code, x.ms })
	if !strings.Contains(out, "failures: 25") || !strings.Contains(out, "outage: 2.5 s") {
		t.Fatalf("unexpected summary:\n%s", out)
	}
	none := probeSummary(samples[:10], func(x s) (time.Time, int, float64) { return x.at, x.code, x.ms })
	if !strings.Contains(none, "no failures") {
		t.Fatalf("expected no-outage wording:\n%s", none)
	}
}

func TestDrillTextsAreComplete(t *testing.T) {
	for _, name := range drillOrder {
		for _, lang := range []string{"en", "es"} {
			tx, ok := drillTexts[name][lang]
			if !ok || tx.title == "" || tx.what == "" || tx.expect == "" || tx.where == "" {
				t.Errorf("drill %s lacks a complete %s explanation (what / expected / where)", name, lang)
			}
		}
		if _, ok := drillSafety[name]; !ok {
			t.Errorf("drill %s has no safety class", name)
		}
	}
	for i := 1; i <= 10; i++ {
		for _, lang := range []string{"en", "es"} {
			if x := drillChaos[i][lang]; x.title == "" || x.what == "" || x.expect == "" || x.where == "" {
				t.Errorf("chaos %d lacks a complete %s explanation", i, lang)
			}
		}
	}
}
