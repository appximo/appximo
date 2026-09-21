package schema

import (
	"strings"
	"testing"
)

func rangeFixture(rangesJSON string) *APISchema {
	raw := `{"$schema":"https://appximo.com/schema/v1","version":"1","name":"t",
	"resources":{"eventos":{"fields":{"titulo":{"type":"string","required":true},"inicio":{"type":"time"},"fin":{"type":"time"},
	"dueno_id":{"type":"uuid"},"ocupa":{"type":"bool","default":true},"estado":{"type":"string","enum":["ok","cancelado"]},
	"notas":{"type":"text"},"zona":{"type":"string","format":"timezone"},"creado_en":{"type":"time","auto":"create"}},
	"ranges":` + rangesJSON + `}},
	"rbac":{"roles":{"admin":{"resources":"*","actions":["*"]}}}}`
	s, err := LoadFromBytes([]byte(raw))
	if err != nil {
		return nil
	}
	return s
}

func rulesOf(errs []ValidationError) string {
	var out []string
	for _, e := range errs {
		out = append(out, e.Rule)
	}
	return strings.Join(out, ",")
}

// TestRanges_ValidateNamesEveryMistake (MOTOR-AGENDA-S1): the load-time rules
// of a range, each a named error with a fix — never a runtime surprise.
func TestRanges_ValidateNamesEveryMistake(t *testing.T) {
	cases := map[string]string{
		`{"horario":{"start":"inicio","end":"fin","no_overlap":{"scope":["dueno_id"],"when":{"field":"ocupa","op":"eq","val":true}},"timezone_field":"zona","default_duration":"30m"}}`: "",
		`{"horario":{"start":"inicio","end":"fin","no_overlap":{"when":{"field":"estado","op":"ne","val":"cancelado"}}}}`:                                                             "",
		`{"horario":{"start":"inicio","end":"nope"}}`:                                                                                                                                    "unknown_range_field",
		`{"horario":{"start":"titulo","end":"fin"}}`:                                                                                                                                     "range_field_not_time",
		`{"horario":{"start":"creado_en","end":"fin"}}`:                                                                                                                                  "range_field_auto",
		`{"horario":{"start":"inicio","end":"inicio"}}`:                                                                                                                                  "range_same_field",
		`{"titulo":{"start":"inicio","end":"fin"}}`:                                                                                                                                      "range_shadows_field",
		`{"horario":{"start":"inicio","end":"fin","no_overlap":{"scope":["ghost"]}}}`:                                                                                                    "unknown_scope_field",
		`{"horario":{"start":"inicio","end":"fin","no_overlap":{"scope":["notas"]}}}`:                                                                                                    "scope_type_unsupported",
		`{"horario":{"start":"inicio","end":"fin","no_overlap":{"scope":["inicio"]}}}`:                                                                                                   "scope_is_range_field",
		`{"horario":{"start":"inicio","end":"fin","no_overlap":{"when":{"field":"estado","op":"gt","val":"ok"}}}}`:                                                                       "invalid_when_op",
		`{"horario":{"start":"inicio","end":"fin","no_overlap":{"when":{"field":"estado","op":"eq","val":"zzz"}}}}`:                                                                      "invalid_when_value",
		`{"horario":{"start":"inicio","end":"fin","no_overlap":{"when":{"field":"ocupa","op":"eq","val":"yes"}}}}`:                                                                       "invalid_when_value",
		`{"horario":{"start":"inicio","end":"fin","timezone_field":"titulo"}}`:                                                                                                           "range_timezone_unvalidated",
		`{"horario":{"start":"inicio","end":"fin","default_duration":"soon"}}`:                                                                                                           "invalid_duration",
	}
	for js, want := range cases {
		s := rangeFixture(js)
		if s == nil {
			t.Errorf("%s: did not load (strict keys?)", js)
			continue
		}
		got := rulesOf(Validate(s))
		if want == "" && got != "" {
			t.Errorf("%s: want valid, got %s", js, got)
		}
		if want != "" && !strings.Contains(got, want) {
			t.Errorf("%s: want rule %s, got %q", js, want, got)
		}
	}
	// Unknown keys are rejected at the strict-key layer.
	if s := rangeFixture(`{"horario":{"start":"inicio","end":"fin","bogus":1}}`); s != nil {
		t.Errorf("an unknown range key must be rejected at load")
	}
}

// TestRanges_SQLShapes pins the constraint text the migration renders and the
// canonical CHECK expression (verified live against PG16).
func TestRanges_SQLShapes(t *testing.T) {
	s := rangeFixture(`{"horario":{"start":"inicio","end":"fin","no_overlap":{"scope":["dueno_id"],"when":{"field":"ocupa","op":"eq","val":true}}}}`)
	res := s.Resources["eventos"]
	r := res.Ranges["horario"]
	if got := RangeCheckExpression("inicio", "fin"); got != "(((inicio IS NULL) OR (fin IS NULL) OR (inicio < fin)))" {
		t.Errorf("check expression: %s", got)
	}
	def := RangeExclusionDefinition(r, res.Fields)
	want := `EXCLUDE USING gist ("dueno_id" WITH =, tstzrange("inicio", "fin", '[)') WITH &&) WHERE ("inicio" IS NOT NULL AND "fin" IS NOT NULL AND ("ocupa" = true))`
	if def != want {
		t.Errorf("exclusion:\n got %s\nwant %s", def, want)
	}
	sym := RangeExclusionName("eventos", "horario", r)
	if !strings.HasPrefix(sym, "excl_eventos_horario_") || len(sym) != len("excl_eventos_horario_")+8 {
		t.Errorf("symbol shape: %s", sym)
	}
	r2 := r
	r2.NoOverlap = &NoOverlapDef{Scope: []string{"dueno_id"}}
	if RangeExclusionName("eventos", "horario", r2) == sym {
		t.Errorf("a changed rule must change the symbol")
	}
	if !ValidateTimezoneName("America/Bogota") || ValidateTimezoneName("UTC-5") || ValidateTimezoneName("+05:00") || ValidateTimezoneName("Marte/Olimpo") {
		t.Errorf("timezone format: names yes, offsets and nonsense no")
	}
	if r.DefaultDurationValue().String() != "1h0m0s" {
		t.Errorf("default duration = %v", r.DefaultDurationValue())
	}
	// The write-time order rule: both bounds present and reversed → range_order on the end field.
	errs := RangeOrderViolations(res, map[string]any{"inicio": "2026-09-22T17:00:00Z", "fin": "2026-09-22T16:00:00Z"})
	if len(errs) != 1 || errs[0].Field != "fin" || errs[0].Rule != "range_order" {
		t.Errorf("range_order: %+v", errs)
	}
	if errs := RangeOrderViolations(res, map[string]any{"fin": "2026-09-22T16:00:00Z"}); len(errs) != 0 {
		t.Errorf("one bound only defers to the database CHECK, got %+v", errs)
	}
}
