package main

import (
	"encoding/json"
	"testing"

	"github.com/miguelangel/appitools/pkg/schema"
)

func canarySchema(t *testing.T, rbacJSON string) *schema.APISchema {
	t.Helper()
	var rbac schema.RBACPolicy
	if err := json.Unmarshal([]byte(rbacJSON), &rbac); err != nil {
		t.Fatalf("rbac fixture: %v", err)
	}
	return &schema.APISchema{
		Resources: map[string]schema.ResourceSchema{
			"tasks":    {},
			"comments": {},
		},
		RBAC: rbac,
	}
}

func TestFirstResourceNameIsDeterministic(t *testing.T) {
	s := canarySchema(t, `{"roles":{}}`)
	if got := firstResourceName(s); got != "comments" {
		t.Fatalf("firstResourceName = %q, want alphabetical first %q", got, "comments")
	}
	if got := firstResourceName(&schema.APISchema{}); got != "" {
		t.Fatalf("empty schema: got %q, want \"\"", got)
	}
}

func TestCanaryRolePicksAReader(t *testing.T) {
	cases := []struct {
		name, rbac, want string
	}{
		{"wildcard role", `{"roles":{"admin":{"resources":"*","actions":["*"]}}}`, "admin"},
		{"explicit read on list", `{"roles":{"viewer":{"resources":["comments"],"actions":["read"]}}}`, "viewer"},
		{"skips write-only role", `{"roles":{"writer":{"resources":"*","actions":["create"]},"zreader":{"resources":"*","actions":["read"]}}}`, "zreader"},
		{"skips role scoped to other resource", `{"roles":{"other":{"resources":["tasks"],"actions":["read"]}}}`, ""},
		{"no roles", `{"roles":{}}`, ""},
	}
	for _, c := range cases {
		s := canarySchema(t, c.rbac)
		if got := canaryRole(s, "comments"); got != c.want {
			t.Errorf("%s: canaryRole = %q, want %q", c.name, got, c.want)
		}
	}
}
