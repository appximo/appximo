package graphql_test

import (
	"encoding/json"
	"testing"

	gqlhandler "github.com/miguelangel/appitools/pkg/graphql"
	"github.com/miguelangel/appitools/pkg/rbac"
	"github.com/miguelangel/appitools/pkg/schema"
)

// BUG2: a resource whose fields are ALL uuid generates empty Order/Filter input
// objects (uuid is neither orderable nor filterable). graphql-go panics on an
// input object with zero fields, so BuildHandler used to crash at boot. The build
// must now succeed (the empty inputs are simply omitted). No DB needed — the
// schema is built synchronously; resolvers (which use tdb) never run here.
func TestBuildHandler_OnlyUUIDResource_NoPanic(t *testing.T) {
	s := &schema.APISchema{
		Schema:  "https://appitools.dev/schema/v1",
		Version: "1",
		Name:    "bug2",
		Resources: map[string]schema.ResourceSchema{
			// only-uuid resource: no orderable/filterable fields → empty Order/Filter.
			"links": {Fields: map[string]schema.FieldDef{
				"src_id": {Type: "uuid"},
				"dst_id": {Type: "uuid"},
			}},
			// a normal resource alongside, to ensure the rest still builds.
			"tasks": {Fields: map[string]schema.FieldDef{
				"title": {Type: "string"},
			}},
			// only-auto resource: no writable input fields → no createX mutation.
			"events": {Fields: map[string]schema.FieldDef{
				"created_at": {Type: "time", Auto: true},
			}},
		},
		RBAC: schema.RBACPolicy{Roles: map[string]schema.RolePolicy{
			"admin": {Resources: json.RawMessage(`"*"`), Actions: []string{"*"}},
		}},
	}

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("BuildHandler panicked on only-uuid/only-auto resources: %v", r)
		}
	}()

	var policy rbac.Policy
	if h := gqlhandler.BuildHandler(s, nil, nil, &policy, nil); h == nil {
		t.Fatal("BuildHandler returned nil")
	}
}
