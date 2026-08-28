package schema

import (
	"encoding/json"
	"testing"
)

func jsonRes() *ResourceSchema {
	return &ResourceSchema{Fields: map[string]FieldDef{
		"title": {Type: "string"},
		"data":  {Type: "json"},
		"doc":   {Type: "jsonb"},
		"n":     {Type: "int64"},
	}}
}

func TestCoerceJSONFields_JSONTakesEveryValueAsCanonicalText(t *testing.T) {
	cases := []struct {
		name string
		in   any
		want string
	}{
		{"object", map[string]any{"z": 1, "a": []any{1, map[string]any{"b": nil}}}, `{"a":[1,{"b":null}],"z":1}`},
		{"array", []any{1, 2, 3}, `[1,2,3]`},
		{"number", float64(42), `42`},
		{"bool", true, `true`},
		{"string is JSON text, compacted, order kept", "{ \"z\" : 1, \"a\": 12345678901234567890 }", `{"z":1,"a":12345678901234567890}`},
		{"string number", "123", `123`},
		{"string quoted", `"x"`, `"x"`},
		{"raw message", json.RawMessage(`[ 1 ]`), `[1]`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			body := map[string]any{"data": c.in, "title": "t", "n": float64(3)}
			if errs := CoerceJSONFields(jsonRes(), body); len(errs) != 0 {
				t.Fatalf("unexpected errors: %v", errs)
			}
			if got, _ := body["data"].(string); got != c.want {
				t.Fatalf("want %s, got %v (%T)", c.want, body["data"], body["data"])
			}
			if body["title"] != "t" || body["n"] != float64(3) {
				t.Fatalf("other fields must be untouched: %v", body)
			}
			// idempotent — the shared cores may run it twice (before-hook)
			if errs := CoerceJSONFields(jsonRes(), body); len(errs) != 0 || body["data"] != c.want {
				t.Fatalf("second pass must be a no-op, got %v %v", errs, body["data"])
			}
		})
	}
}

func TestCoerceJSONFields_NonJSONStringIsANamedTypeError_BothTypes(t *testing.T) {
	for _, bad := range []string{"hola mundo", "", "[1,", "not json at all"} {
		body := map[string]any{"data": bad, "doc": bad}
		errs := CoerceJSONFields(jsonRes(), body)
		if len(errs) != 2 || errs[0].Field != "data" || errs[1].Field != "doc" || errs[0].Rule != "type" || errs[1].Rule != "type" {
			t.Fatalf("%q: want two type errors (data, doc), got %v", bad, errs)
		}
		if body["data"] != bad {
			t.Fatalf("a rejected value must be left as sent (the response names it), got %v", body["data"])
		}
	}
}

func TestCoerceJSONFields_NullAndJsonbPassThrough(t *testing.T) {
	doc := map[string]any{"k": 1}
	body := map[string]any{"data": nil, "doc": doc, "title": nil}
	if errs := CoerceJSONFields(jsonRes(), body); len(errs) != 0 {
		t.Fatalf("null and a jsonb map are fine: %v", errs)
	}
	if body["data"] != nil {
		t.Fatalf("null must stay null (SQL NULL, governed by required), got %v", body["data"])
	}
	if _, ok := body["doc"].(map[string]any); !ok {
		t.Fatalf("a jsonb map goes to pgx as is, got %T", body["doc"])
	}
	// jsonb string = JSON text, validated, left as the string pgx passes through
	body = map[string]any{"doc": `{"k":1}`}
	if errs := CoerceJSONFields(jsonRes(), body); len(errs) != 0 || body["doc"] != `{"k":1}` {
		t.Fatalf("jsonb JSON-text string must pass unchanged, got %v %v", errs, body["doc"])
	}
	if errs := CoerceJSONFields(nil, body); errs != nil {
		t.Fatalf("nil resource: no-op")
	}
}

func TestPromoteJSONText(t *testing.T) {
	row := map[string]any{"data": `{"nit":"900"}`, "title": `{"looks":"like json"}`, "other": "x"}
	PromoteJSONText(row, []string{"data"})
	if _, ok := row["data"].(json.RawMessage); !ok {
		t.Fatalf("a valid JSON text must become a RawMessage, got %T", row["data"])
	}
	if _, ok := row["title"].(string); !ok {
		t.Fatalf("a column not listed must stay a string")
	}
	b, _ := json.Marshal(row)
	if string(b) != `{"data":{"nit":"900"},"other":"x","title":"{\"looks\":\"like json\"}"}` {
		t.Fatalf("encoder must emit the value natively, got %s", b)
	}
	// legacy non-JSON text (pre-ADR-028 rows) stays a readable string, never an error
	legacy := map[string]any{"data": "hola mundo", "n": nil}
	PromoteJSONText(legacy, []string{"data", "n", "missing"})
	if legacy["data"] != "hola mundo" || legacy["n"] != nil {
		t.Fatalf("legacy text / null must be left alone, got %v", legacy)
	}
	PromoteJSONText(nil, []string{"data"}) // no panic
	PromoteJSONTextRows([]map[string]any{{"data": "[1]"}}, nil)
}

func TestJSONTextColumns(t *testing.T) {
	r := &ResourceSchema{Fields: map[string]FieldDef{"b": {Type: "json"}, "a": {Type: "json"}, "c": {Type: "jsonb"}, "d": {Type: "string"}}}
	got := r.JSONTextColumns()
	if len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Fatalf("want [a b], got %v", got)
	}
	if !r.HasJSONFields() || (&ResourceSchema{Fields: map[string]FieldDef{"d": {Type: "string"}}}).HasJSONFields() {
		t.Fatalf("HasJSONFields must reflect json/jsonb presence")
	}
	if (&ResourceSchema{Fields: map[string]FieldDef{"c": {Type: "jsonb"}}}).JSONTextColumns() != nil {
		t.Fatalf("jsonb is not a text column")
	}
}
