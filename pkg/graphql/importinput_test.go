package graphql_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	gqlhandler "github.com/appximo/appximo/pkg/graphql"
	"github.com/appximo/appximo/pkg/rbac"
	"github.com/appximo/appximo/pkg/schema"
)

// WRITE-ASYMMETRY-S1 — the GraphQL half of the governed-field door.
//
// A resource with NO import declaration keeps its historical create input:
// `id` and auto fields are not part of the type at all (structural rejection,
// verified live in the session matrix). A resource that DECLARES import
// exposes its declared governed fields as OPTIONAL create inputs; WHO may
// supply them is decided at resolve time by schema.GovernedFieldViolations —
// the same single source every other door consults. This test pins the
// structural half via introspection (no DB: introspection never touches
// resolvers).

func importGQLSchema() *schema.APISchema {
	return &schema.APISchema{
		Schema:  "https://appximo.com/schema/v1",
		Version: "1",
		Name:    "imp",
		Resources: map[string]schema.ResourceSchema{
			"lots": {
				Fields: map[string]schema.FieldDef{
					"title":      {Type: "string", Required: true},
					"created_at": {Type: "time", Auto: schema.AutoCreate},
					"updated_at": {Type: "time", Auto: schema.AutoLegacy},
				},
				Import: &schema.ImportConfig{Roles: []string{"admin"}, Fields: []string{"id", "created_at"}},
			},
			"notes": {
				Fields: map[string]schema.FieldDef{
					"title":      {Type: "string", Required: true},
					"created_at": {Type: "time", Auto: schema.AutoCreate},
				},
			},
		},
		RBAC: schema.RBACPolicy{Roles: map[string]schema.RolePolicy{
			"admin": {Resources: json.RawMessage(`"*"`), Actions: []string{"*"}},
		}},
	}
}

func introspectInput(t *testing.T, srv *httptest.Server, typeName string) map[string]bool {
	t.Helper()
	q := `{"query":"{ __type(name: \"` + typeName + `\") { inputFields { name type { kind name } } } }"}`
	resp, err := http.Post(srv.URL, "application/json", strings.NewReader(q))
	if err != nil {
		t.Fatalf("introspection request: %v", err)
	}
	defer resp.Body.Close()
	var out struct {
		Data struct {
			Type struct {
				InputFields []struct {
					Name string `json:"name"`
					Type struct {
						Kind string `json:"kind"`
						Name string `json:"name"`
					} `json:"type"`
				} `json:"inputFields"`
			} `json:"__type"`
		} `json:"data"`
		Errors []any `json:"errors"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(out.Errors) > 0 {
		t.Fatalf("introspection errors: %v", out.Errors)
	}
	fields := map[string]bool{}
	for _, f := range out.Data.Type.InputFields {
		if f.Type.Kind == "NON_NULL" && (f.Name == "id" || f.Name == "created_at" || f.Name == "updated_at") {
			t.Errorf("governed input field %q must be OPTIONAL, got NON_NULL", f.Name)
		}
		fields[f.Name] = true
	}
	return fields
}

func TestImportCreateInput(t *testing.T) {
	var policy rbac.Policy
	h := gqlhandler.BuildHandler(importGQLSchema(), nil, nil, &policy, nil, true /* introspection */)
	srv := httptest.NewServer(h)
	defer srv.Close()

	// Import-declared resource: declared subset (id, created_at) present as
	// optional inputs; the undeclared governed field (updated_at) absent.
	lots := introspectInput(t, srv, "LotInput")
	for _, want := range []string{"id", "created_at", "title"} {
		if !lots[want] {
			t.Errorf("LotInput must carry %q, got %v", want, lots)
		}
	}
	if lots["updated_at"] {
		t.Errorf("LotInput must NOT carry updated_at (outside the declared subset), got %v", lots)
	}

	// Plain resource: historical shape — no governed field in the input.
	notes := introspectInput(t, srv, "NoteInput")
	if notes["id"] || notes["created_at"] {
		t.Errorf("NoteInput must NOT carry governed fields, got %v", notes)
	}

	// Update inputs NEVER carry governed fields — import is create-only.
	lotsUpd := introspectInput(t, srv, "LotUpdateInput")
	if lotsUpd["id"] || lotsUpd["created_at"] || lotsUpd["updated_at"] {
		t.Errorf("LotUpdateInput must NOT carry governed fields (import is create-only), got %v", lotsUpd)
	}
}
