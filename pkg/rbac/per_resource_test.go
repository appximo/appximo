package rbac

import (
	"encoding/json"
	"testing"
)

// perResourcePolicy is a policy exercising the G2 per-resource form alongside a
// legacy role-global role and an admin, so one fixture covers every shape.
//
//   - member  : per-resource — projects (workspace-scoped read + field allowlist),
//     messages (a DIFFERENT condition column — participation), tags (no condition),
//     posts (read-all + write-own via condition_actions).
//   - owner   : LEGACY role-global condition user_id=$user_id over two resources.
//   - admin   : wildcard, no condition.
const perResourcePolicy = `{
  "roles": {
    "member": {
      "permissions": {
        "projects": { "actions": ["read"], "fields": ["id","name"],
                      "conditions": { "field": "workspace_id", "op": "eq", "val": "$user_id" } },
        "messages": { "actions": ["read"],
                      "conditions": { "field": "conversation_id", "op": "eq", "val": "$user_id" } },
        "tags":     { "actions": ["read"] },
        "posts":    { "actions": ["read","create","update","delete"],
                      "conditions": { "field": "author_id", "op": "eq", "val": "$user_id" },
                      "condition_actions": ["create","update","delete"] }
      }
    },
    "owner": {
      "resources": ["projects","messages"],
      "actions": ["read","update"],
      "conditions": { "field": "user_id", "op": "eq", "val": "$user_id" }
    },
    "admin": { "resources": "*", "actions": ["*"] }
  }
}`

func mustPolicy(t *testing.T, raw string) *Policy {
	t.Helper()
	var p Policy
	if err := json.Unmarshal([]byte(raw), &p); err != nil {
		t.Fatalf("unmarshal policy: %v", err)
	}
	return &p
}

const memberUID = "user-11111111"

func TestEvaluate_PerResource_OwnConditionPerResource(t *testing.T) {
	p := mustPolicy(t, perResourcePolicy)
	ev := EvalContext{Role: "member", UserID: memberUID}

	// projects: workspace_id condition + field allowlist.
	r := p.Evaluate(ev, "projects", "read")
	if !r.Allowed {
		t.Fatal("member should read projects")
	}
	if r.Condition == nil || r.Condition.Field != "workspace_id" || r.Condition.Value != memberUID {
		t.Fatalf("projects condition = %+v; want workspace_id=%s", r.Condition, memberUID)
	}
	if len(r.AllowedFields) != 2 || r.AllowedFields[0] != "id" || r.AllowedFields[1] != "name" {
		t.Fatalf("projects fields = %v; want [id name]", r.AllowedFields)
	}

	// messages: a DIFFERENT column — the per-resource condition must be its own, not
	// projects' workspace_id (the no-leak-between-resources guarantee).
	r = p.Evaluate(ev, "messages", "read")
	if r.Condition == nil || r.Condition.Field != "conversation_id" {
		t.Fatalf("messages condition = %+v; want conversation_id (NOT another resource's column)", r.Condition)
	}
	if r.Condition.Field == "workspace_id" {
		t.Fatal("LEAK: messages received projects' workspace_id condition")
	}
}

func TestEvaluate_PerResource_NoConditionSeesAll(t *testing.T) {
	p := mustPolicy(t, perResourcePolicy)
	r := p.Evaluate(EvalContext{Role: "member", UserID: memberUID}, "tags", "read")
	if !r.Allowed {
		t.Fatal("member should read tags")
	}
	if r.Condition != nil {
		t.Fatalf("tags has no declared condition; got %+v (should see all rows)", r.Condition)
	}
}

func TestEvaluate_PerResource_ReadAllWriteOwn(t *testing.T) {
	p := mustPolicy(t, perResourcePolicy)
	ev := EvalContext{Role: "member", UserID: memberUID}

	// read is NOT in condition_actions ⇒ unconditional (read all posts).
	if r := p.Evaluate(ev, "posts", "read"); r.Condition != nil {
		t.Fatalf("posts read should be unconditional (read all); got condition %+v", r.Condition)
	}
	// create/update/delete ARE in condition_actions ⇒ owner-scoped (write own).
	for _, action := range []string{"create", "update", "delete"} {
		r := p.Evaluate(ev, "posts", action)
		if !r.Allowed {
			t.Fatalf("member should %s posts", action)
		}
		if r.Condition == nil || r.Condition.Field != "author_id" || r.Condition.Value != memberUID {
			t.Fatalf("posts %s condition = %+v; want author_id=%s", action, r.Condition, memberUID)
		}
	}
}

func TestEvaluate_PerResource_DenyByDefault(t *testing.T) {
	p := mustPolicy(t, perResourcePolicy)
	ev := EvalContext{Role: "member", UserID: memberUID}

	// A resource absent from the permissions map is denied.
	if r := p.Evaluate(ev, "secrets", "read"); r.Allowed {
		t.Fatal("member must be denied a resource not in its permissions (deny-by-default)")
	}
	// An action not granted on a listed resource is denied.
	if r := p.Evaluate(ev, "projects", "delete"); r.Allowed {
		t.Fatal("member must be denied an action not granted on projects")
	}
	// An unknown role is denied.
	if r := p.Evaluate(EvalContext{Role: "ghost"}, "projects", "read"); r.Allowed {
		t.Fatal("unknown role must be denied")
	}
}

func TestEvaluate_LegacyRoleGlobal_Unchanged(t *testing.T) {
	p := mustPolicy(t, perResourcePolicy)
	ev := EvalContext{Role: "owner", UserID: memberUID}

	// The legacy global condition (user_id) applies to EVERY listed resource,
	// identically across them — behaviour unchanged by G2.
	for _, resource := range []string{"projects", "messages"} {
		r := p.Evaluate(ev, resource, "read")
		if !r.Allowed {
			t.Fatalf("owner should read %s", resource)
		}
		if r.Condition == nil || r.Condition.Field != "user_id" || r.Condition.Value != memberUID {
			t.Fatalf("owner %s condition = %+v; want global user_id=%s", resource, r.Condition, memberUID)
		}
	}
	// owner has no delete action.
	if r := p.Evaluate(ev, "projects", "delete"); r.Allowed {
		t.Fatal("owner must be denied delete (not in its actions)")
	}
}

func TestAllows_PerResourceAndLegacy(t *testing.T) {
	p := mustPolicy(t, perResourcePolicy)
	cases := []struct {
		role, resource, action string
		want                   bool
	}{
		{"member", "projects", "read", true},
		{"member", "projects", "create", false}, // not granted
		{"member", "posts", "delete", true},
		{"member", "secrets", "read", false}, // not in permissions
		{"owner", "projects", "update", true},
		{"owner", "projects", "delete", false},
		{"admin", "anything", "delete", true}, // wildcard
		{"ghost", "projects", "read", false},
	}
	for _, c := range cases {
		if got := p.Allows(c.role, c.resource, c.action); got != c.want {
			t.Errorf("Allows(%q,%q,%q) = %v; want %v", c.role, c.resource, c.action, got, c.want)
		}
	}
}

func TestRoleCacheable_PerResourceAndLegacy(t *testing.T) {
	const policy = `{
      "roles": {
        "scoped":      { "permissions": { "posts": { "actions": ["read"],
                            "conditions": { "field": "author_id", "op": "eq", "val": "$user_id" } } } },
        "allowlisted": { "permissions": { "posts": { "actions": ["read"], "fields": ["id","title"] } } },
        "open":        { "permissions": { "posts": { "actions": ["read"] }, "tags": { "actions": ["read"] } } },
        "legacy_cond": { "resources": ["posts"], "actions": ["read"],
                         "conditions": { "field": "user_id", "op": "eq", "val": "$user_id" } },
        "legacy_open": { "resources": "*", "actions": ["read"] }
      }
    }`
	p := mustPolicy(t, policy)
	cases := []struct {
		role string
		want bool
	}{
		{"scoped", false},      // per-resource condition ⇒ per-user ⇒ not cacheable
		{"allowlisted", false}, // per-resource field allowlist ⇒ not cacheable
		{"open", true},         // per-resource but no condition/fields anywhere ⇒ cacheable
		{"legacy_cond", false}, // legacy global condition ⇒ not cacheable (unchanged)
		{"legacy_open", true},  // legacy, no condition/fields ⇒ cacheable (unchanged)
		{"missing", false},     // unknown role ⇒ fail safe
	}
	for _, c := range cases {
		if got := p.RoleCacheable(c.role); got != c.want {
			t.Errorf("RoleCacheable(%q) = %v; want %v", c.role, got, c.want)
		}
	}
}

// TestEvaluate_LegacyMarshalRoundTrip proves a legacy policy survives the
// schema→JSON→rbac.Policy round-trip the engine performs at boot byte-for-byte
// (the Permissions field's omitempty keeps the legacy serialization unchanged).
func TestEvaluate_LegacyMarshalRoundTrip(t *testing.T) {
	const legacy = `{"roles":{"owner":{"resources":["projects"],"actions":["read"],` +
		`"conditions":{"field":"user_id","op":"eq","val":"$user_id"}}}}`
	p := mustPolicy(t, legacy)
	out, err := json.Marshal(p)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	// Re-parse and confirm the condition still resolves — no "permissions" noise that
	// would flip the role into the per-resource branch.
	p2 := mustPolicy(t, string(out))
	r := p2.Evaluate(EvalContext{Role: "owner", UserID: memberUID}, "projects", "read")
	if r.Condition == nil || r.Condition.Field != "user_id" {
		t.Fatalf("round-trip lost the legacy condition: %+v", r.Condition)
	}
}
