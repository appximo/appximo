//go:build integration

package appximo

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/appximo/appximo/tests/helpers"
)

// MOTOR-TIPO-JSON-S1 (ADR-028): a `json` field holds a JSON VALUE on every
// door, in both directions. Before the fix an object/array/number/boolean on a
// `json` field was a 500 (pgx cannot bind a Go map into a TEXT column), a
// non-JSON string was stored verbatim, and every read returned the stored text
// as an ESCAPED STRING. These tests fail against that code — every one of them
// was run against the pre-fix tree first (docs/audits/JSON_TYPE_AUDIT_S1.md).

const jsonFieldSchemaJSON = `{
  "$schema": "https://appximo.com/schema/v1",
  "version": "1",
  "name": "jsonfield",
  "resources": {
    "declarations": {
      "fields": {
        "nit":  { "type": "string" },
        "data": { "type": "json" }
      },
      "relations": {
        "lines": { "type": "has_many", "target": "lines", "fk": "declaration_id" }
      }
    },
    "lines": {
      "fields": {
        "declaration_id": { "type": "uuid", "relation": "declarations" },
        "payload":        { "type": "json" }
      }
    },
    "forms": {
      "fields": {
        "name":     { "type": "string", "required": true },
        "metadata": { "type": "json", "required": true }
      }
    },
    "docs": {
      "fields": {
        "title": { "type": "string" },
        "doc":   { "type": "jsonb" }
      }
    }
  },
  "rbac": {
    "roles": {
      "admin": { "resources": "*", "actions": ["*"] }
    }
  }
}`

// A realistic document — the shape of a complete tax declaration, not {"nit":"900"}.
func realisticDeclaration(n int) map[string]any {
	renglones := make([]any, 0, n)
	for i := 0; i < n; i++ {
		renglones = append(renglones, map[string]any{
			"renglon": i + 1, "concepto": fmt.Sprintf("Concepto %03d — ingresos brutos por actividad", i+1),
			"valor": float64(1500000 + i*1237), "detalle": map[string]any{
				"cuenta": fmt.Sprintf("4135%04d", i), "soportes": []any{
					map[string]any{"tipo": "factura", "numero": fmt.Sprintf("FE-%06d", i), "fecha": "2025-03-14"},
					map[string]any{"tipo": "nota", "numero": fmt.Sprintf("NC-%06d", i), "fecha": "2025-03-20"},
				}, "aplica_retencion": i%3 == 0, "observaciones": nil,
			},
		})
	}
	return map[string]any{
		"formulario": "210", "periodo": 2025, "contribuyente": map[string]any{
			"nit": "900123456", "dv": 7, "razon_social": "ACME S.A.S.", "direccion": map[string]any{
				"pais": "CO", "departamento": "Risaralda", "ciudad": "Pereira", "lineas": []any{"Cra 7 # 12-34", "Oficina 501"}},
			"responsabilidades": []any{"O-13", "O-15", "R-99-PN"},
		},
		"renglones": renglones, "totales": map[string]any{"ingresos": 1.5e9, "impuesto": 123456789.5, "saldo_a_pagar": 0},
		"firmas": []any{map[string]any{"rol": "representante", "nombre": "M. Acosta", "fecha": "2025-04-01T10:00:00Z"}},
	}
}

func newJSONFieldApp(t *testing.T) *App {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "jsonfield.json")
	if err := os.WriteFile(p, []byte(jsonFieldSchemaJSON), 0o600); err != nil {
		t.Fatalf("write schema: %v", err)
	}
	app, err := New(Config{SchemaPath: p, DSN: itConnStr, JWTSecret: helpers.JWTSecret, AdminKey: helpers.AdminKey, Env: "test"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { app.pool.Close() })
	mustRegister(t, app, Route{Method: "POST", Path: "/api/_jctx_create", Handler: func(ctx Ctx) error {
		var body struct {
			Resource string         `json:"resource"`
			Data     map[string]any `json:"data"`
		}
		if err := ctx.Bind(&body); err != nil {
			return ctx.Error(400, "invalid body", err)
		}
		row, err := ctx.Insert(body.Resource, body.Data)
		if err != nil {
			return err
		}
		return ctx.JSON(201, row)
	}})
	mustRegister(t, app, Route{Method: "PATCH", Path: "/api/_jctx_update", Handler: func(ctx Ctx) error {
		var body struct {
			Resource string         `json:"resource"`
			ID       string         `json:"id"`
			Data     map[string]any `json:"data"`
		}
		if err := ctx.Bind(&body); err != nil {
			return ctx.Error(400, "invalid body", err)
		}
		row, err := ctx.Update(body.Resource, body.ID, body.Data)
		if err != nil {
			return err
		}
		if row == nil {
			return ctx.Error(404, "not found", nil)
		}
		return ctx.JSON(200, row)
	}})
	return app
}

func jsonEq(a, b any) bool {
	ab, _ := json.Marshal(a)
	bb, _ := json.Marshal(b)
	var x, y any
	_ = json.Unmarshal(ab, &x)
	_ = json.Unmarshal(bb, &y)
	return reflect.DeepEqual(x, y)
}

func mustJSON(v any) string { b, _ := json.Marshal(v); return string(b) }

func expectJSONField(t *testing.T, door string, resp *http.Response, wantStatus int, field string, want any) map[string]any {
	t.Helper()
	body := decode(t, resp)
	if resp.StatusCode != wantStatus {
		t.Fatalf("%s: want %d, got %d — body %v", door, wantStatus, resp.StatusCode, body)
	}
	if !jsonEq(body[field], want) {
		t.Fatalf("%s: field %q must come back NATIVELY as %s, got %s (%T)", door, field, mustJSON(want), mustJSON(body[field]), body[field])
	}
	return body
}

func expectTypeRule(t *testing.T, door string, resp *http.Response, field string) {
	t.Helper()
	body := decode(t, resp)
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("%s: a non-JSON string on a %s field must be 422, got %d — body %v", door, field, resp.StatusCode, body)
	}
	fields, _ := body["fields"].([]any)
	for _, f := range fields {
		m, _ := f.(map[string]any)
		if m["field"] == field && m["rule"] == "type" {
			return
		}
	}
	t.Fatalf("%s: 422 must name field %q with rule \"type\", got %v", door, field, body)
}

func TestJSONField_EveryDoorAcceptsEveryJSONValue(t *testing.T) {
	app := newJSONFieldApp(t)
	srv := newServerFor(t, app)
	tenant := "jsonall"
	helpers.RegisterTenant(t, itPool, tenant, mustLoadSchemaBytes(t, jsonFieldSchemaJSON))
	host := tenant + ".localhost"
	admin := helpers.GenToken(t, "admin", "u1", tenant)

	deep := realisticDeclaration(3)
	values := []struct {
		name string
		val  any
	}{
		{"object", map[string]any{"nit": "900"}},
		{"array-root", []any{1, 2, 3}},
		{"deep-nested", deep},
		{"number", 42},
		{"bool", true},
		{"string-value-is-json-text", "{\"nit\":\"900\"}"}, // read as JSON TEXT: the object comes back
	}
	wantOf := func(v any) any {
		if s, ok := v.(string); ok {
			var out any
			_ = json.Unmarshal([]byte(s), &out)
			return out
		}
		return v
	}

	for _, v := range values {
		t.Run("REST POST "+v.name, func(t *testing.T) {
			resp := do(t, srv, http.MethodPost, "/api/declarations", host, admin, `{"data":`+mustJSON(v.val)+`}`)
			body := expectJSONField(t, "POST", resp, 201, "data", wantOf(v.val))
			id := fmt.Sprint(body["id"])
			// parity: the same value comes back from the get and the list
			expectJSONField(t, "GET by id", do(t, srv, http.MethodGet, "/api/declarations/"+id, host, admin, ""), 200, "data", wantOf(v.val))
			list := decode(t, do(t, srv, http.MethodGet, "/api/declarations?filter[id][eq]="+id, host, admin, ""))
			rows, _ := list["data"].([]any)
			if len(rows) != 1 || !jsonEq(rows[0].(map[string]any)["data"], wantOf(v.val)) {
				t.Fatalf("list parity: want %s, got %v", mustJSON(wantOf(v.val)), list)
			}
		})
	}

	t.Run("batch create object", func(t *testing.T) {
		resp := do(t, srv, http.MethodPost, "/api/transaction", host, admin, `{"operations":[{"op":"create","resource":"declarations","data":{"data":{"nit":"905","x":[1,{"y":null}]}}}]}`)
		body := decode(t, resp)
		if resp.StatusCode != 200 {
			t.Fatalf("batch: want 200, got %d %v", resp.StatusCode, body)
		}
		results, _ := body["results"].([]any)
		if len(results) != 1 || !jsonEq(results[0].(map[string]any)["data"], map[string]any{"nit": "905", "x": []any{1, map[string]any{"y": nil}}}) {
			t.Fatalf("batch result must carry the native value, got %v", body)
		}
	})

	t.Run("GraphQL create object + read", func(t *testing.T) {
		q := `{"query":"mutation{createDeclaration(input:{data:{nit:\"907\", items:[1,2]}}){id data}}"}`
		body := decode(t, do(t, srv, http.MethodPost, "/graphql", host, admin, q))
		if body["errors"] != nil {
			t.Fatalf("GraphQL create with an object must succeed (the field is the JSON scalar), got %v", body["errors"])
		}
		created := body["data"].(map[string]any)["createDeclaration"].(map[string]any)
		want := map[string]any{"nit": "907", "items": []any{1, 2}}
		if !jsonEq(created["data"], want) {
			t.Fatalf("GraphQL create must return the native value, got %v", created)
		}
		id := fmt.Sprint(created["id"])
		rq := fmt.Sprintf(`{"query":"{ declarations(filter:{id:{eq:\"%s\"}}) { data { id data } } }"}`, id)
		rb := decode(t, do(t, srv, http.MethodPost, "/graphql", host, admin, rq))
		if rb["errors"] != nil {
			// id filter may not exist in this GraphQL surface; fall back to the plain list
			rb = decode(t, do(t, srv, http.MethodPost, "/graphql", host, admin, `{"query":"{ declarations(per_page:100) { data { id data } } }"}`))
		}
		rows := rb["data"].(map[string]any)["declarations"].(map[string]any)["data"].([]any)
		found := false
		for _, r := range rows {
			m := r.(map[string]any)
			if fmt.Sprint(m["id"]) == id {
				found = true
				if !jsonEq(m["data"], want) {
					t.Fatalf("GraphQL read must return the native value, got %v", m["data"])
				}
			}
		}
		if !found {
			t.Fatalf("GraphQL read did not find the created row")
		}
		// a string variable is JSON text — the escaped form keeps working
		vq := `{"query":"mutation($d: JSON){createDeclaration(input:{data:$d}){data}}","variables":{"d":"{\"nit\":\"908\"}"}}`
		vb := decode(t, do(t, srv, http.MethodPost, "/graphql", host, admin, vq))
		if vb["errors"] != nil || !jsonEq(vb["data"].(map[string]any)["createDeclaration"].(map[string]any)["data"], map[string]any{"nit": "908"}) {
			t.Fatalf("GraphQL string-as-JSON-text must store the object, got %v", vb)
		}
	})

	t.Run("required json takes an object", func(t *testing.T) {
		resp := do(t, srv, http.MethodPost, "/api/forms", host, admin, `{"name":"f1","metadata":{"k":1}}`)
		expectJSONField(t, "POST forms", resp, 201, "metadata", map[string]any{"k": 1})
		resp = do(t, srv, http.MethodPost, "/api/forms", host, admin, `{"name":"f3","metadata":null}`)
		if resp.StatusCode != 422 {
			t.Fatalf("required json + null must stay 422 required, got %d", resp.StatusCode)
		}
	})
}

func TestJSONField_LibraryParity(t *testing.T) {
	app := newJSONFieldApp(t)
	srv := newServerFor(t, app)
	tenant := "jsonlib"
	helpers.RegisterTenant(t, itPool, tenant, mustLoadSchemaBytes(t, jsonFieldSchemaJSON))
	host := tenant + ".localhost"
	admin := helpers.GenToken(t, "admin", "u1", tenant)

	resp := do(t, srv, http.MethodPost, "/api/_jctx_create", host, admin, `{"resource":"declarations","data":{"data":{"nit":"lib","n":[1,2,{"z":true}]}}}`)
	body := decode(t, resp)
	if resp.StatusCode != 201 {
		t.Fatalf("Ctx.Insert with an object must succeed, got %d %v — THE SAME ENCODE FAILURE AS REST", resp.StatusCode, body)
	}
	id := fmt.Sprint(body["id"])
	// The library hands the handler the stored TEXT (documented row type); the HTTP read is native.
	if s, ok := body["data"].(string); !ok || !json.Valid([]byte(s)) {
		t.Fatalf("Ctx.Insert must return the json column as the stored JSON text string, got %T %v", body["data"], body["data"])
	}
	expectJSONField(t, "GET after Ctx.Insert", do(t, srv, http.MethodGet, "/api/declarations/"+id, host, admin, ""), 200, "data", map[string]any{"nit": "lib", "n": []any{1, 2, map[string]any{"z": true}}})

	resp = do(t, srv, http.MethodPatch, "/api/_jctx_update", host, admin, `{"resource":"declarations","id":"`+id+`","data":{"data":[9,8]}}`)
	if resp.StatusCode != 200 {
		t.Fatalf("Ctx.Update with an array must succeed, got %d %v", resp.StatusCode, decode(t, resp))
	}
	expectJSONField(t, "GET after Ctx.Update", do(t, srv, http.MethodGet, "/api/declarations/"+id, host, admin, ""), 200, "data", []any{9, 8})

	resp = do(t, srv, http.MethodPost, "/api/_jctx_create", host, admin, `{"resource":"declarations","data":{"data":"hola mundo"}}`)
	expectTypeRule(t, "Ctx.Insert", resp, "data")
	resp = do(t, srv, http.MethodPatch, "/api/_jctx_update", host, admin, `{"resource":"declarations","id":"`+id+`","data":{"data":""}}`)
	expectTypeRule(t, "Ctx.Update", resp, "data")
}

func TestJSONField_UpdateDoorsAndRejections(t *testing.T) {
	app := newJSONFieldApp(t)
	srv := newServerFor(t, app)
	tenant := "jsonupd"
	helpers.RegisterTenant(t, itPool, tenant, mustLoadSchemaBytes(t, jsonFieldSchemaJSON))
	host := tenant + ".localhost"
	admin := helpers.GenToken(t, "admin", "u1", tenant)

	id := fmt.Sprint(decode(t, do(t, srv, http.MethodPost, "/api/declarations", host, admin, `{"nit":"x","data":{"v":1}}`))["id"])

	t.Run("PATCH object", func(t *testing.T) {
		expectJSONField(t, "PATCH", do(t, srv, http.MethodPatch, "/api/declarations/"+id, host, admin, `{"data":{"nit":"901","deep":{"a":[1,{"b":2}]}}}`), 200, "data", map[string]any{"nit": "901", "deep": map[string]any{"a": []any{1, map[string]any{"b": 2}}}})
	})
	t.Run("PATCH array", func(t *testing.T) {
		expectJSONField(t, "PATCH", do(t, srv, http.MethodPatch, "/api/declarations/"+id, host, admin, `{"data":[1]}`), 200, "data", []any{1})
	})
	t.Run("PATCH string is JSON text", func(t *testing.T) {
		expectJSONField(t, "PATCH", do(t, srv, http.MethodPatch, "/api/declarations/"+id, host, admin, `{"data":"{\"nit\":\"902\"}"}`), 200, "data", map[string]any{"nit": "902"})
	})
	t.Run("PUT object", func(t *testing.T) {
		expectJSONField(t, "PUT", do(t, srv, http.MethodPut, "/api/declarations/"+id, host, admin, `{"nit":"y","data":{"nit":"903"}}`), 200, "data", map[string]any{"nit": "903"})
	})
	t.Run("PATCH null", func(t *testing.T) {
		expectJSONField(t, "PATCH", do(t, srv, http.MethodPatch, "/api/declarations/"+id, host, admin, `{"data":null}`), 200, "data", nil)
	})
	t.Run("batch update object", func(t *testing.T) {
		resp := do(t, srv, http.MethodPost, "/api/transaction", host, admin, `{"operations":[{"op":"update","resource":"declarations","id":"`+id+`","data":{"data":{"b":true}}}]}`)
		body := decode(t, resp)
		if resp.StatusCode != 200 {
			t.Fatalf("batch update: want 200, got %d %v", resp.StatusCode, body)
		}
		if !jsonEq(body["results"].([]any)[0].(map[string]any)["data"], map[string]any{"b": true}) {
			t.Fatalf("batch update result must be native, got %v", body)
		}
	})
	t.Run("GraphQL update object", func(t *testing.T) {
		q := fmt.Sprintf(`{"query":"mutation{updateDeclaration(id:\"%s\", input:{data:{g:[1]}}){data}}"}`, id)
		body := decode(t, do(t, srv, http.MethodPost, "/graphql", host, admin, q))
		if body["errors"] != nil || !jsonEq(body["data"].(map[string]any)["updateDeclaration"].(map[string]any)["data"], map[string]any{"g": []any{1}}) {
			t.Fatalf("GraphQL update with an object must succeed natively, got %v", body)
		}
	})

	for _, bad := range []string{`"hola mundo"`, `""`, `"[1,"`, `"not json at all"`} {
		t.Run("rejects "+bad, func(t *testing.T) {
			expectTypeRule(t, "POST", do(t, srv, http.MethodPost, "/api/declarations", host, admin, `{"data":`+bad+`}`), "data")
			expectTypeRule(t, "PATCH", do(t, srv, http.MethodPatch, "/api/declarations/"+id, host, admin, `{"data":`+bad+`}`), "data")
			expectTypeRule(t, "PUT", do(t, srv, http.MethodPut, "/api/declarations/"+id, host, admin, `{"nit":"z","data":`+bad+`}`), "data")
			resp := do(t, srv, http.MethodPost, "/api/transaction", host, admin, `{"operations":[{"op":"create","resource":"declarations","data":{"data":`+bad+`}}]}`)
			expectTypeRule(t, "batch", resp, "data")
			// jsonb gets the same named 422 (it used to be an anonymous 400 from Postgres 22P02)
			expectTypeRule(t, "POST jsonb", do(t, srv, http.MethodPost, "/api/docs", host, admin, `{"doc":`+bad+`}`), "doc")
		})
	}
	t.Run("GraphQL rejects a non-JSON string with the S44 fields", func(t *testing.T) {
		q := fmt.Sprintf(`{"query":"mutation{updateDeclaration(id:\"%s\", input:{data:\"hola mundo\"}){data}}"}`, id)
		body := decode(t, do(t, srv, http.MethodPost, "/graphql", host, admin, q))
		if body["errors"] == nil || !strings.Contains(fmt.Sprint(body["errors"]), "data") {
			t.Fatalf("GraphQL must reject a non-JSON string naming the field, got %v", body)
		}
	})
}

func TestJSONField_EmbedsSubroutesAndVolume(t *testing.T) {
	app := newJSONFieldApp(t)
	srv := newServerFor(t, app)
	tenant := "jsonemb"
	helpers.RegisterTenant(t, itPool, tenant, mustLoadSchemaBytes(t, jsonFieldSchemaJSON))
	host := tenant + ".localhost"
	admin := helpers.GenToken(t, "admin", "u1", tenant)

	doc := realisticDeclaration(120) // ~40 KB, the size of a complete declaration
	raw := mustJSON(doc)
	t.Logf("realistic document: %d bytes", len(raw))
	start := time.Now()
	resp := do(t, srv, http.MethodPost, "/api/declarations", host, admin, `{"nit":"vol","data":`+raw+`}`)
	body := expectJSONField(t, "POST volume", resp, 201, "data", doc)
	id := fmt.Sprint(body["id"])
	t.Logf("POST %d bytes: %s", len(raw), time.Since(start))
	start = time.Now()
	expectJSONField(t, "GET volume", do(t, srv, http.MethodGet, "/api/declarations/"+id, host, admin, ""), 200, "data", doc)
	t.Logf("GET  %d bytes: %s", len(raw), time.Since(start))

	line := map[string]any{"concepto": "línea", "valores": []any{1.5, 2, map[string]any{"k": "v"}}}
	lresp := do(t, srv, http.MethodPost, "/api/lines", host, admin, `{"declaration_id":"`+id+`","payload":`+mustJSON(line)+`}`)
	lineID := fmt.Sprint(expectJSONField(t, "POST line", lresp, 201, "payload", line)["id"])

	t.Run("include embed (has_many)", func(t *testing.T) {
		b := decode(t, do(t, srv, http.MethodGet, "/api/declarations/"+id+"?include=lines", host, admin, ""))
		if !jsonEq(b["data"], doc) {
			t.Fatalf("include get: the base row's json must be native, got %s", mustJSON(b["data"])[:80])
		}
		lines, _ := b["lines"].([]any)
		if len(lines) != 1 || !jsonEq(lines[0].(map[string]any)["payload"], line) {
			t.Fatalf("include get: the embedded row's json must be native, got %v", b["lines"])
		}
		lb := decode(t, do(t, srv, http.MethodGet, "/api/declarations?include=lines&filter[id][eq]="+id, host, admin, ""))
		rows, _ := lb["data"].([]any)
		if len(rows) != 1 || !jsonEq(rows[0].(map[string]any)["lines"].([]any)[0].(map[string]any)["payload"], line) {
			t.Fatalf("include list: embedded json must be native, got %v", lb)
		}
	})
	t.Run("relation subroute", func(t *testing.T) {
		expectJSONField(t, "subroute", do(t, srv, http.MethodGet, "/api/lines/"+lineID+"/declaration", host, admin, ""), 200, "data", doc)
	})
	t.Run("filter eq on canonical text", func(t *testing.T) {
		lb := decode(t, do(t, srv, http.MethodGet, "/api/lines?filter[payload][eq]="+mustJSON(line), host, admin, ""))
		rows, _ := lb["data"].([]any)
		if len(rows) != 1 {
			t.Fatalf("filter[eq] over the canonical text must match the row, got %v", lb)
		}
	})
}

// A burst of client-caused statement errors (plain 422s) must never open the
// query circuit breaker for everyone (ENG-49) — before the fix, six 422s in a
// row turned every write of the process into 503 for 8 s.
func TestJSONField_ClientErrorsNeverOpenTheBreaker(t *testing.T) {
	app := newJSONFieldApp(t)
	srv := newServerFor(t, app)
	tenant := "jsonbrk"
	helpers.RegisterTenant(t, itPool, tenant, mustLoadSchemaBytes(t, jsonFieldSchemaJSON))
	host := tenant + ".localhost"
	admin := helpers.GenToken(t, "admin", "u1", tenant)

	for i := 0; i < 40; i++ {
		resp := do(t, srv, http.MethodPost, "/api/declarations", host, admin, `{"nit":"1","ghost":1}`)
		resp.Body.Close()
		if resp.StatusCode != 422 {
			t.Fatalf("request %d: an unknown field is a 422 on every request, got %d — the breaker opened on client input", i, resp.StatusCode)
		}
	}
	resp := do(t, srv, http.MethodPost, "/api/declarations", host, admin, `{"nit":"2"}`)
	if resp.StatusCode != 201 {
		t.Fatalf("a legal write after a burst of 422s must be 201, got %d", resp.StatusCode)
	}
}
