package schema

import (
	"encoding/json"
	"strings"
	"testing"
)

// --- AutoValue JSON round-trip -------------------------------------------------

func TestAutoValue_UnmarshalForms(t *testing.T) {
	cases := []struct {
		in   string
		want AutoValue
	}{
		{`true`, AutoLegacy},
		{`false`, AutoOff},
		{`"create"`, AutoCreate},
		{`"update"`, AutoUpdate},
	}
	for _, c := range cases {
		var got AutoValue
		if err := json.Unmarshal([]byte(c.in), &got); err != nil {
			t.Fatalf("unmarshal %s: %v", c.in, err)
		}
		if got != c.want {
			t.Errorf("unmarshal %s = %q, want %q", c.in, got, c.want)
		}
	}
	// A number is neither form — a named error, not a silent zero.
	var bad AutoValue
	if err := json.Unmarshal([]byte(`1`), &bad); err == nil {
		t.Error("unmarshal 1: expected error")
	}
}

func TestAutoValue_MarshalRoundTrip(t *testing.T) {
	// The editor round-trip contract: legacy re-exports as `true`, the explicit
	// roles as their strings, off as `false` (omitted via omitempty in structs).
	for in, want := range map[AutoValue]string{
		AutoLegacy: `true`, AutoCreate: `"create"`, AutoUpdate: `"update"`, AutoOff: `false`,
	} {
		b, err := json.Marshal(in)
		if err != nil {
			t.Fatalf("marshal %q: %v", in, err)
		}
		if string(b) != want {
			t.Errorf("marshal %q = %s, want %s", in, b, want)
		}
	}
	// omitempty: an off value disappears from a FieldDef, like the old bool.
	b, err := json.Marshal(FieldDef{Type: "time"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "auto") {
		t.Errorf("AutoOff should be omitted: %s", b)
	}
}

// --- Refresh derivation (the ONE source) --------------------------------------

func TestAutoRefreshColumns(t *testing.T) {
	res := &ResourceSchema{Fields: map[string]FieldDef{
		"updated_at":    {Type: "time", Auto: AutoLegacy}, // documented legacy magic
		"created_at":    {Type: "time", Auto: AutoLegacy}, // never refreshed
		"modificado_en": {Type: "time", Auto: AutoUpdate}, // explicit role, Spanish name
		"creado_en":     {Type: "time", Auto: AutoCreate}, // explicit create, never refreshed
		"frozen_update": {Type: "time", Auto: AutoLegacy}, // legacy on another name: frozen
		"title":         {Type: "string"},
	}}
	got := res.AutoRefreshColumns()
	want := []string{"modificado_en", "updated_at"}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("AutoRefreshColumns = %v, want %v", got, want)
	}
	// An explicit "create" on the literal updated_at OPTS OUT of the magic.
	optOut := &ResourceSchema{Fields: map[string]FieldDef{
		"updated_at": {Type: "time", Auto: AutoCreate},
	}}
	if cols := optOut.AutoRefreshColumns(); len(cols) != 0 {
		t.Fatalf(`auto:"create" on updated_at must not refresh, got %v`, cols)
	}
}

// --- Validator: closed value set + type=time ----------------------------------

func autoSchema(field string, fd FieldDef) *APISchema {
	return &APISchema{
		Schema: "https://appximo.com/schema/v1", Version: "1", Name: "t",
		Resources: map[string]ResourceSchema{
			"tasks": {Fields: map[string]FieldDef{field: fd, "title": {Type: "string"}}},
		},
	}
}

func TestValidate_AutoValueClosedSet(t *testing.T) {
	errs := Validate(autoSchema("stamp", FieldDef{Type: "time", Auto: AutoValue("yes")}))
	found := false
	for _, e := range errs {
		if e.Rule == "invalid_auto" {
			found = true
			if !strings.Contains(e.Message, `"yes"`) {
				t.Errorf("error should name the bad value: %s", e.Message)
			}
		}
	}
	if !found {
		t.Fatalf("invalid_auto not reported: %v", errs)
	}
}

func TestValidate_AutoRequiresTime(t *testing.T) {
	for _, av := range []AutoValue{AutoLegacy, AutoCreate, AutoUpdate} {
		errs := Validate(autoSchema("stamp", FieldDef{Type: "string", Auto: av}))
		found := false
		for _, e := range errs {
			if e.Rule == "auto_requires_time" {
				found = true
			}
		}
		if !found {
			t.Errorf("auto=%q on string: auto_requires_time not reported: %v", av, errs)
		}
	}
	// time is fine, all three forms.
	for _, av := range []AutoValue{AutoLegacy, AutoCreate, AutoUpdate} {
		for _, e := range Validate(autoSchema("stamp", FieldDef{Type: "time", Auto: av})) {
			if e.Rule == "auto_requires_time" || e.Rule == "invalid_auto" {
				t.Errorf("auto=%q on time should validate, got %v", av, e)
			}
		}
	}
}

// --- The auto_update_intent warning -------------------------------------------

func TestWarnings_AutoUpdateIntent(t *testing.T) {
	warnOn := func(field string, fd FieldDef) bool {
		for _, w := range Warnings(autoSchema(field, fd)) {
			if w.Rule == "auto_update_intent" {
				return true
			}
		}
		return false
	}
	// The observed corruption: a Spanish modification timestamp, legacy auto.
	if !warnOn("modificado_en", FieldDef{Type: "time", Auto: AutoLegacy}) {
		t.Error("modificado_en + legacy auto should warn")
	}
	if !warnOn("fecha_actualizacion", FieldDef{Type: "time", Auto: AutoLegacy}) {
		t.Error("fecha_actualizacion + legacy auto should warn")
	}
	if !warnOn("last_modified", FieldDef{Type: "time", Auto: AutoLegacy}) {
		t.Error("last_modified + legacy auto should warn")
	}
	// The documented magic name stays silent.
	if warnOn("updated_at", FieldDef{Type: "time", Auto: AutoLegacy}) {
		t.Error("updated_at + legacy auto must not warn")
	}
	// A creation timestamp under a domain name is the engine's own examples'
	// pattern — silent.
	if warnOn("placed_at", FieldDef{Type: "time", Auto: AutoLegacy}) {
		t.Error("placed_at + legacy auto must not warn")
	}
	// Both explicit roles silence it — that is the fix the warning names.
	if warnOn("modificado_en", FieldDef{Type: "time", Auto: AutoUpdate}) {
		t.Error(`modificado_en + auto:"update" must not warn`)
	}
	if warnOn("modificado_en", FieldDef{Type: "time", Auto: AutoCreate}) {
		t.Error(`modificado_en + auto:"create" must not warn`)
	}
}

// --- The graphql_list_query_shadowed warning ----------------------------------

func TestWarnings_GraphQLListShadow(t *testing.T) {
	sch := func(name string) *APISchema {
		return &APISchema{
			Schema: "https://appximo.com/schema/v1", Version: "1", Name: "t",
			Resources: map[string]ResourceSchema{
				name: {Fields: map[string]FieldDef{"title": {Type: "string"}}},
			},
		}
	}
	hit := func(name string) bool {
		for _, w := range Warnings(sch(name)) {
			if w.Rule == "graphql_list_query_shadowed" {
				return true
			}
		}
		return false
	}
	for _, name := range []string{"menu", "media", "lineas_orden"} {
		if !hit(name) {
			t.Errorf("%q singularizes to itself — should warn", name)
		}
	}
	for _, name := range []string{"tasks", "categorias", "orders"} {
		if hit(name) {
			t.Errorf("%q should not warn", name)
		}
	}
}
