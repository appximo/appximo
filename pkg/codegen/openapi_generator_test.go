package codegen

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/appximo/appximo/pkg/schema"
	"gopkg.in/yaml.v3"
)

func testSchema() *schema.APISchema {
	return &schema.APISchema{
		Name:    "todo-api",
		Version: "1",
		Resources: map[string]schema.ResourceSchema{
			"tasks": {
				Fields: map[string]schema.FieldDef{
					"title":  {Type: "string", Required: true},
					"status": {Type: "string", Enum: []string{"open", "done"}},
				},
			},
		},
	}
}

func TestGenerateOpenAPIJSON_ValidAndComplete(t *testing.T) {
	raw, err := GenerateOpenAPIJSON(testSchema(), "/")
	if err != nil {
		t.Fatalf("GenerateOpenAPIJSON: %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("emitted spec is not valid JSON: %v", err)
	}
	paths, _ := doc["paths"].(map[string]any)
	if paths == nil {
		t.Fatal("spec has no paths")
	}
	// Auth-as-product endpoints are documented.
	for _, p := range []string{"/auth/login", "/auth/signup", "/auth/refresh", "/auth/reset/request", "/auth/mfa/verify"} {
		if _, ok := paths[p]; !ok {
			t.Errorf("OpenAPI spec missing auth path %s", p)
		}
	}
	// File store routes are documented (no "files" resource in this schema).
	if _, ok := paths["/api/files"]; !ok {
		t.Error("OpenAPI spec missing /api/files")
	}
	// Schema-derived resource routes still present.
	if _, ok := paths["/api/tasks"]; !ok {
		t.Error("OpenAPI spec missing /api/tasks")
	}

	comps, _ := doc["components"].(map[string]any)
	schemas, _ := comps["schemas"].(map[string]any)
	if _, ok := schemas["ValidationErrorResponse"]; !ok {
		t.Error("spec must model the 422 ValidationErrorResponse (fields[])")
	}
	if _, ok := schemas["AuthResult"]; !ok {
		t.Error("spec must model AuthResult")
	}
	// PaginationLinks was removed (the live engine returns meta only, no links).
	if _, ok := schemas["PaginationLinks"]; ok {
		t.Error("PaginationLinks must NOT be in the spec — the live list response has no links object")
	}
	// The per-resource ListResponse must not advertise a links field.
	if lr, ok := schemas["TasksListResponse"].(map[string]any); ok {
		props, _ := lr["properties"].(map[string]any)
		if _, hasLinks := props["links"]; hasLinks {
			t.Error("TasksListResponse must not advertise a links field the engine never returns")
		}
		if _, hasMeta := props["meta"]; !hasMeta {
			t.Error("TasksListResponse must advertise meta")
		}
	} else {
		t.Error("TasksListResponse schema missing")
	}
}

func TestGenerateOpenAPI_YAMLStillValid(t *testing.T) {
	raw, err := GenerateOpenAPI(testSchema(), "/")
	if err != nil {
		t.Fatalf("GenerateOpenAPI: %v", err)
	}
	var doc map[string]any
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("emitted YAML is invalid: %v", err)
	}
	if doc["openapi"] != "3.0.3" {
		t.Errorf("openapi version = %v, want 3.0.3", doc["openapi"])
	}
}

// TestAuthPathsUnauthenticated checks the unauthenticated auth flows override the
// global bearerAuth requirement with security: [].
func TestAuthPathsUnauthenticated(t *testing.T) {
	raw, _ := GenerateOpenAPIJSON(testSchema(), "/")
	var doc map[string]any
	_ = json.Unmarshal(raw, &doc)
	paths := doc["paths"].(map[string]any)
	login := paths["/auth/login"].(map[string]any)["post"].(map[string]any)
	sec, ok := login["security"]
	if !ok {
		t.Fatal("/auth/login must declare security (empty array to override global bearer)")
	}
	arr, ok := sec.([]any)
	if !ok || len(arr) != 0 {
		t.Fatalf("/auth/login security must be an empty array (no auth), got %v", sec)
	}
}

// TestOpenAPIVendorExtensions pins the Part-F contract (FIELD-FEEDBACK-S1,
// FE5 + PATRON-BACKOFFICE §6): the generated document carries everything a
// GENERIC tool needs — the FK target column (x-appximo-references, defaulted
// to "id"), the file-field marking with its attach policy, the state machine,
// and the virtual files resource declared at the document root — while filter
// query parameters stay extension-free.
func TestOpenAPIVendorExtensions(t *testing.T) {
	s := &schema.APISchema{
		Name: "ext-api", Version: "1",
		Resources: map[string]schema.ResourceSchema{
			"instructores": {Fields: map[string]schema.FieldDef{
				"user_id": {Type: "uuid", Unique: true},
				"nombre":  {Type: "string", Required: true},
			}},
			"reservas": {Fields: map[string]schema.FieldDef{
				"instructor_id": {Type: "uuid", Relation: "instructores", References: "user_id"},
				"miembro_id":    {Type: "uuid", Relation: "instructores"}, // references defaults to id
				"comprobante":   {Type: "file", Accept: schema.StringList{"image", "pdf"}, MaxBytes: 2 << 20},
				"estado": {Type: "string", Enum: []string{"solicitada", "confirmada", "cancelada"},
					StateMachine: &schema.StateMachine{
						Initial: []string{"solicitada"},
						Transitions: map[string][]string{
							"solicitada": {"confirmada", "cancelada"},
							"confirmada": {"cancelada"},
						},
					}},
			}},
		},
	}
	raw, err := GenerateOpenAPIJSON(s, "/")
	if err != nil {
		t.Fatalf("GenerateOpenAPIJSON: %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("not JSON: %v", err)
	}

	comp := doc["components"].(map[string]any)["schemas"].(map[string]any)
	props := comp["Reservas"].(map[string]any)["properties"].(map[string]any)

	// FK with explicit references → the declared column.
	inst := props["instructor_id"].(map[string]any)
	if inst["x-appximo-relation"] != "instructores" || inst["x-appximo-references"] != "user_id" {
		t.Fatalf("instructor_id extensions wrong: %v", inst)
	}
	// FK without references → defaulted to "id" EXPLICITLY (the common case is
	// where the FE5 blind spot would otherwise stay open).
	miem := props["miembro_id"].(map[string]any)
	if miem["x-appximo-references"] != "id" {
		t.Fatalf("miembro_id must default x-appximo-references to id: %v", miem)
	}
	// File field: marked + policy. Before, it was indistinguishable from a FK.
	f := props["comprobante"].(map[string]any)
	if f["x-appximo-file"] != true {
		t.Fatalf("file field not marked: %v", f)
	}
	if acc, _ := f["x-appximo-accept"].([]any); len(acc) != 2 || acc[0] != "image" {
		t.Fatalf("accept policy not emitted: %v", f)
	}
	if f["x-appximo-max-bytes"] != float64(2<<20) {
		t.Fatalf("max_bytes not emitted: %v", f)
	}
	if f["x-appximo-relation"] != nil {
		t.Fatalf("a file field is NOT a relation: %v", f)
	}
	// State machine: initial + full transitions map, terminal state present
	// with an empty list.
	est := props["estado"].(map[string]any)
	ini, _ := est["x-appximo-initial"].([]any)
	if len(ini) != 1 || ini[0] != "solicitada" {
		t.Fatalf("initial wrong: %v", est)
	}
	trans, _ := est["x-appximo-transitions"].(map[string]any)
	if len(trans) != 3 {
		t.Fatalf("transitions must cover every known state: %v", trans)
	}
	if outs, _ := trans["cancelada"].([]any); len(outs) != 0 {
		t.Fatalf("terminal state must be present with no exits: %v", trans)
	}
	if outs, _ := trans["solicitada"].([]any); len(outs) != 2 {
		t.Fatalf("solicitada must have 2 exits: %v", trans)
	}
	// The input schemas carry the same extensions (a form builder reads Input).
	inProps := comp["ReservasInput"].(map[string]any)["properties"].(map[string]any)
	if inProps["instructor_id"].(map[string]any)["x-appximo-references"] != "user_id" {
		t.Fatal("Input schema must carry the extensions too")
	}

	// Virtual resource declared at the document root…
	vr, _ := doc["x-appximo-virtual-resources"].(map[string]any)
	filesDecl, _ := vr["files"].(map[string]any)
	if filesDecl == nil {
		t.Fatalf("x-appximo-virtual-resources must declare files: %v", doc["x-appximo-virtual-resources"])
	}
	if acts, _ := filesDecl["actions"].([]any); len(acts) != 3 {
		t.Fatalf("files actions wrong: %v", filesDecl)
	}
	// …and its operations tagged.
	paths := doc["paths"].(map[string]any)
	up := paths["/api/files"].(map[string]any)["post"].(map[string]any)
	if up["x-appximo-virtual-resource"] != "files" {
		t.Fatalf("upload op not tagged: %v", up)
	}

	// Filter query parameters stay extension-free (oaFieldType, not
	// oaPropertySchema): no x-appximo-* key in any parameter schema.
	list := paths["/api/reservas"].(map[string]any)["get"].(map[string]any)
	if params, ok := list["parameters"].([]any); ok {
		var walk func(v any) bool
		walk = func(v any) bool {
			switch t := v.(type) {
			case map[string]any:
				for k, vv := range t {
					if strings.HasPrefix(k, "x-appximo-") {
						return true
					}
					if walk(vv) {
						return true
					}
				}
			case []any:
				for _, vv := range t {
					if walk(vv) {
						return true
					}
				}
			}
			return false
		}
		if walk(params) {
			t.Fatalf("filter parameters must not carry x-appximo-* extensions")
		}
	}

	// A schema resource literally named files SHADOWS the virtual store: no
	// root declaration then.
	s2 := &schema.APISchema{Name: "shadow", Version: "1", Resources: map[string]schema.ResourceSchema{
		"files": {Fields: map[string]schema.FieldDef{"name": {Type: "string"}}},
	}}
	raw2, err := GenerateOpenAPIJSON(s2, "/")
	if err != nil {
		t.Fatalf("shadowed: %v", err)
	}
	var doc2 map[string]any
	_ = json.Unmarshal(raw2, &doc2)
	if _, present := doc2["x-appximo-virtual-resources"]; present {
		t.Fatal("a shadowed files store must not be declared virtual")
	}
}

// ENG-40: the aggregate endpoint (G3) must appear in the served contract with
// its own parameter vocabulary, and inherit the public marking when the
// resource is anonymously readable (an aggregate is a read of the same set).
func TestOpenAPIAggregatePath(t *testing.T) {
	s := testSchema()
	s.Resources["orders"] = schema.ResourceSchema{
		Fields: map[string]schema.FieldDef{
			"total_cents": {Type: "int64"},
			"created":     {Type: "time"},
			"status":      {Type: "string"},
			"attrs":       {Type: "jsonb"},
		},
	}
	s.RBAC.Public = map[string]schema.ResourcePermission{
		"orders": {Actions: []string{"read"}},
	}
	raw, err := GenerateOpenAPIJSON(s, "/")
	if err != nil {
		t.Fatalf("GenerateOpenAPIJSON: %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	paths := doc["paths"].(map[string]any)
	for _, p := range []string{"/api/tasks/aggregate", "/api/orders/aggregate"} {
		if _, ok := paths[p]; !ok {
			t.Fatalf("missing %s in served contract", p)
		}
	}
	agg := paths["/api/orders/aggregate"].(map[string]any)["get"].(map[string]any)
	// public resource → aggregate carries the anonymous marking
	if agg["x-public"] != true {
		t.Errorf("aggregate of a public resource must carry x-public: true")
	}
	if sec, ok := agg["security"].([]any); !ok || len(sec) != 0 {
		t.Errorf("aggregate of a public resource must carry security: []")
	}
	names := map[string]string{}
	for _, pr := range agg["parameters"].([]any) {
		pm := pr.(map[string]any)
		names[pm["name"].(string)], _ = pm["description"].(string)
	}
	for _, want := range []string{"count", "sum", "avg", "min", "max", "group_by", "search", "filter[status]"} {
		if _, ok := names[want]; !ok {
			t.Errorf("aggregate op missing parameter %q", want)
		}
	}
	// eligibility mirrors query.BuildAggregate: jsonb is not groupable, time
	// is min/max-eligible but not summable.
	if !strings.Contains(names["sum"], "total_cents") || strings.Contains(names["sum"], "created") {
		t.Errorf("sum eligibility wrong: %q", names["sum"])
	}
	if !strings.Contains(names["min"], "created") {
		t.Errorf("min must list time fields: %q", names["min"])
	}
	if strings.Contains(names["group_by"], "attrs") {
		t.Errorf("group_by must not list jsonb fields: %q", names["group_by"])
	}
	// list-only parameters are a 400 on this endpoint — never advertised
	for _, banned := range []string{"page", "per_page", "sort", "order"} {
		if _, ok := names[banned]; ok {
			t.Errorf("aggregate op must not advertise list-only parameter %q", banned)
		}
	}
	// the tasks aggregate (no public grant) stays authenticated
	tagg := paths["/api/tasks/aggregate"].(map[string]any)["get"].(map[string]any)
	if _, ok := tagg["x-public"]; ok {
		t.Errorf("non-public resource's aggregate must not be marked public")
	}
	// the response component exists
	comps := doc["components"].(map[string]any)["schemas"].(map[string]any)
	if _, ok := comps["AggregateResponse"]; !ok {
		t.Error("missing AggregateResponse component")
	}
}

// TestOpenAPI_TransactionEndpointIsPublished — MIGRACION-CONFIANZA-S1. The
// batch endpoint has existed in every published version, yet an external
// migration on v0.1.10 concluded "it does not exist" because /openapi.json —
// the document this engine calls the authority for EXISTENCE — did not list
// it. The served contract must name it, with the request/response shapes.
func TestOpenAPI_TransactionEndpointIsPublished(t *testing.T) {
	s := &schema.APISchema{Resources: map[string]schema.ResourceSchema{
		"tasks": {Fields: map[string]schema.FieldDef{"title": {Type: "string"}}},
	}}
	raw, err := GenerateOpenAPIJSON(s, "http://x")
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	paths := doc["paths"].(map[string]any)
	tx, ok := paths["/api/transaction"].(map[string]any)
	if !ok {
		t.Fatal("/api/transaction is missing from the served contract")
	}
	post, ok := tx["post"].(map[string]any)
	if !ok || post["x-appximo-transaction"] != true {
		t.Fatalf("/api/transaction must be a POST flagged x-appximo-transaction: %v", tx)
	}
	if _, has := tx["get"]; has {
		t.Error("the batch endpoint is POST-only (GET answers 405)")
	}
	resp := post["responses"].(map[string]any)
	for _, code := range []string{"200", "400", "401", "403", "404", "409", "413", "422"} {
		if _, ok := resp[code]; !ok {
			t.Errorf("response %s missing", code)
		}
	}
	schemas := doc["components"].(map[string]any)["schemas"].(map[string]any)
	for _, name := range []string{"TransactionRequest", "TransactionOperation", "TransactionGuard", "TransactionResponse", "TransactionErrorResponse"} {
		if _, ok := schemas[name]; !ok {
			t.Errorf("component %s missing", name)
		}
	}
	req := schemas["TransactionRequest"].(map[string]any)["properties"].(map[string]any)["operations"].(map[string]any)
	if req["maxItems"] != float64(DefaultMaxTxOps) {
		t.Errorf("the published cap must equal DefaultMaxTxOps (%d), got %v", DefaultMaxTxOps, req["maxItems"])
	}
}
