package codegen

import (
	"encoding/json"
	"testing"

	"github.com/miguelangel/appitools/pkg/schema"
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
