package codegen

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/appximo/appximo/pkg/schema"
)

func customTestSchema(t *testing.T) *schema.APISchema {
	t.Helper()
	s, err := schema.LoadFromBytes([]byte(`{
		"$schema": "https://appximo.com/schema/v1",
		"version": "1",
		"name": "custom-routes-test",
		"resources": { "tareas": { "fields": { "titulo": { "type": "string" } } } }
	}`))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	return s
}

// ENG-33: registered custom routes appear in the generated OpenAPI with the
// facts the engine knows — method, path, auth mode, role, byte-serving — and
// the author's optional summary.
func TestOpenAPIIncludesCustomRoutes(t *testing.T) {
	s := customTestSchema(t)
	routes := []CustomRoute{
		{Method: "POST", Path: "/api/checkout", Public: true, Summary: "Guest checkout"},
		{Method: "GET", Path: "/api/reportes/{id}", RequireRole: "dueno"},
		{Method: "GET", Path: "/api/catalogo-imagen", Public: true, ByteServing: true},
	}
	out, err := GenerateOpenAPIJSONWithRoutes(s, "/", routes)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(out, &doc); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	paths := doc["paths"].(map[string]any)

	checkout, ok := paths["/api/checkout"].(map[string]any)
	if !ok {
		t.Fatal("custom route /api/checkout missing from OpenAPI paths")
	}
	post := checkout["post"].(map[string]any)
	if post["summary"] != "Guest checkout" {
		t.Errorf("summary not taken from Route.Description: %v", post["summary"])
	}
	if post["x-public"] != true {
		t.Error("public route not marked x-public")
	}
	if sec, ok := post["security"].([]any); !ok || len(sec) != 0 {
		t.Errorf("public route must carry security: [] (got %v)", post["security"])
	}
	if post["x-appximo-custom-route"] != true {
		t.Error("custom route not marked x-appximo-custom-route")
	}

	rep := paths["/api/reportes/{id}"].(map[string]any)["get"].(map[string]any)
	if rep["x-required-role"] != "dueno" {
		t.Errorf("RequireRole not published: %v", rep["x-required-role"])
	}
	if _, hasSec := rep["security"]; hasSec {
		t.Error("authenticated route must inherit the global bearerAuth (no security override)")
	}
	params, _ := rep["parameters"].([]any)
	if len(params) != 1 {
		t.Fatalf("expected 1 path parameter for {id}, got %v", params)
	}
	if p := params[0].(map[string]any); p["name"] != "id" || p["in"] != "path" {
		t.Errorf("path parameter mis-derived: %v", p)
	}

	img := paths["/api/catalogo-imagen"].(map[string]any)["get"].(map[string]any)
	if img["x-byte-serving"] != true {
		t.Error("ByteServing route not marked x-byte-serving")
	}
	resp := img["responses"].(map[string]any)
	if _, ok := resp["200"]; !ok {
		t.Error("byte-serving route should document the 200 binary response")
	}
	if img["summary"] != "Custom endpoint registered by the application" {
		t.Errorf("empty Description should publish the generic summary, got %v", img["summary"])
	}
}

// A nil route list produces a document byte-identical to the plain generator —
// the pure `serve` binary and the CLI keep their exact previous contract.
func TestOpenAPINilRoutesIsIdentical(t *testing.T) {
	s := customTestSchema(t)
	plain, err := GenerateOpenAPIJSON(s, "/")
	if err != nil {
		t.Fatalf("plain: %v", err)
	}
	withNil, err := GenerateOpenAPIJSONWithRoutes(s, "/", nil)
	if err != nil {
		t.Fatalf("withNil: %v", err)
	}
	if !bytes.Equal(plain, withNil) {
		t.Error("nil custom routes must not change the generated document")
	}
}

// The FILES-1 evaluation core, exercised over a fake metadata fetch.
func TestCheckFilePoliciesCore(t *testing.T) {
	res := &schema.ResourceSchema{Fields: map[string]schema.FieldDef{
		"imagen": {Type: "file", Accept: schema.StringList{"image"}, MaxBytes: 5 << 20},
		"nota":   {Type: "string"},
	}}
	files := map[string]struct {
		size int64
		ct   string
	}{
		"11111111-1111-4111-8111-111111111111": {1 << 20, "image/jpeg"},
		"22222222-2222-4222-8222-222222222222": {6 << 20, "image/png"},
		"33333333-3333-4333-8333-333333333333": {1 << 20, "application/pdf"},
	}
	fetch := func(id string) (int64, string, bool, error) {
		f, ok := files[id]
		return f.size, f.ct, ok, nil
	}

	// A conforming file attaches.
	errs, err := checkFilePolicies(res, map[string]any{"imagen": "11111111-1111-4111-8111-111111111111"}, fetch)
	if err != nil || len(errs) != 0 {
		t.Fatalf("conforming file rejected: %v %v", errs, err)
	}
	// Oversize → file_policy naming max_bytes.
	errs, _ = checkFilePolicies(res, map[string]any{"imagen": "22222222-2222-4222-8222-222222222222"}, fetch)
	if len(errs) != 1 || errs[0].Rule != "file_policy" || errs[0].Field != "imagen" {
		t.Fatalf("oversize file not rejected as file_policy: %v", errs)
	}
	// Wrong family → file_policy.
	errs, _ = checkFilePolicies(res, map[string]any{"imagen": "33333333-3333-4333-8333-333333333333"}, fetch)
	if len(errs) != 1 || errs[0].Rule != "file_policy" {
		t.Fatalf("wrong-type file not rejected: %v", errs)
	}
	// Unknown id → NOT this check's finding (the FK owns existence).
	errs, err = checkFilePolicies(res, map[string]any{"imagen": "44444444-4444-4444-8444-444444444444"}, fetch)
	if err != nil || len(errs) != 0 {
		t.Fatalf("unknown id must fall through to the FK: %v %v", errs, err)
	}
	// Null / absent / non-file fields are ignored.
	for _, vals := range []map[string]any{
		{"imagen": nil}, {"nota": "x"}, {},
	} {
		if errs, _ := checkFilePolicies(res, vals, fetch); len(errs) != 0 {
			t.Fatalf("vals %v should not trigger the policy: %v", vals, errs)
		}
	}
}
