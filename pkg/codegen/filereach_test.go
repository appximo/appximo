package codegen

import (
	"testing"

	"github.com/appximo/appximo/pkg/schema"
)

// FILES-3 (2026-09-28): the file fields a schema declares are the columns the
// reachability rule looks in. A schema with none installs no guard at all.
func TestFileRefColumns_AreTheDeclaredFileFields(t *testing.T) {
	s := &schema.APISchema{Resources: map[string]schema.ResourceSchema{
		"notas": {Fields: map[string]schema.FieldDef{
			"titulo":     {Type: "string"},
			"adjunto_id": {Type: "file"},
			"dueno_id":   {Type: "uuid"},
		}},
		"personas": {Fields: map[string]schema.FieldDef{
			"nombre": {Type: "string"},
			"foto":   {Type: "file"},
		}},
		"areas": {Fields: map[string]schema.FieldDef{"nombre": {Type: "string"}}},
	}}
	got := FileRefColumns(s)
	want := []FileRefColumn{{Resource: "notas", Column: "adjunto_id"}, {Resource: "personas", Column: "foto"}}
	if len(got) != len(want) {
		t.Fatalf("columns %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("column %d = %+v, want %+v", i, got[i], want[i])
		}
	}
	none := &schema.APISchema{Resources: map[string]schema.ResourceSchema{
		"areas": {Fields: map[string]schema.FieldDef{"nombre": {Type: "string"}}},
	}}
	if cols := FileRefColumns(none); len(cols) != 0 {
		t.Errorf("a schema with no file field must install no guard, got %+v", cols)
	}
}
