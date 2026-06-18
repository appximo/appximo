package schema_test

import (
	"encoding/json"
	"testing"

	"github.com/miguelangel/appitools/pkg/schema"
)

// parseSchema unmarshals a raw schema JSON into an *APISchema for Validate tests
// (the per-resource RBAC checks live in Validate, not the strict-key pass).
func parseSchema(t *testing.T, raw string) *schema.APISchema {
	t.Helper()
	var s schema.APISchema
	if err := json.Unmarshal([]byte(raw), &s); err != nil {
		t.Fatalf("unmarshal schema: %v", err)
	}
	return &s
}

// perResourceSchema is a minimal but realistic per-resource RBAC schema: a
// `member` role scoping projects by workspace_id and messages by conversation_id
// (a DIFFERENT column), plus read-all/write-own posts.
const perResourceSchema = `{
  "$schema": "https://appitools.dev/schema/v1", "version": "1", "name": "lab",
  "resources": {
    "projects": { "fields": { "workspace_id": { "type": "uuid" }, "name": { "type": "string" } } },
    "messages": { "fields": { "conversation_id": { "type": "uuid" }, "body": { "type": "text" } } },
    "posts":    { "fields": { "author_id": { "type": "uuid" }, "title": { "type": "string" } } }
  },
  "rbac": { "roles": {
    "member": { "permissions": {
      "projects": { "actions": ["read"], "fields": ["id","name"],
                    "conditions": { "field": "workspace_id", "op": "eq", "val": "$user_id" } },
      "messages": { "actions": ["read"],
                    "conditions": { "field": "conversation_id", "op": "eq", "val": "$user_id" } },
      "posts":    { "actions": ["read","create","update","delete"],
                    "conditions": { "field": "author_id", "op": "eq", "val": "$user_id" },
                    "condition_actions": ["create","update","delete"] }
    } },
    "admin": { "resources": "*", "actions": ["*"] }
  } }
}`

func TestValidateRBAC_ValidPerResource(t *testing.T) {
	errs := schema.Validate(parseSchema(t, perResourceSchema))
	if len(errs) != 0 {
		t.Fatalf("expected a valid per-resource schema, got: %v", errs)
	}
}

func TestValidateRBAC_ConditionFieldMustExistOnResource(t *testing.T) {
	// workspace_id does NOT exist on messages — a condition over it must fail at
	// LOAD, never become a masked 500 at runtime.
	raw := `{
      "$schema":"x","version":"1","name":"x",
      "resources": { "messages": { "fields": { "body": { "type": "text" } } } },
      "rbac": { "roles": { "member": { "permissions": {
        "messages": { "actions":["read"], "conditions": { "field":"workspace_id","op":"eq","val":"$user_id" } }
      } } } }
    }`
	errs := schema.Validate(parseSchema(t, raw))
	if !hasError(errs, "does not exist on resource") {
		t.Fatalf("expected condition-field-does-not-exist error, got: %v", errs)
	}
}

func TestValidateRBAC_BothFormsRejected(t *testing.T) {
	raw := `{
      "$schema":"x","version":"1","name":"x",
      "resources": { "posts": { "fields": { "author_id": { "type": "uuid" } } } },
      "rbac": { "roles": { "mix": {
        "resources": ["posts"], "actions": ["read"],
        "permissions": { "posts": { "actions": ["read"] } }
      } } }
    }`
	errs := schema.Validate(parseSchema(t, raw))
	if !hasError(errs, "EITHER the role-global form") {
		t.Fatalf("expected mutual-exclusivity error, got: %v", errs)
	}
}

func TestValidateRBAC_UnknownResourceInPermissions(t *testing.T) {
	raw := `{
      "$schema":"x","version":"1","name":"x",
      "resources": { "posts": { "fields": { "title": { "type": "string" } } } },
      "rbac": { "roles": { "member": { "permissions": {
        "ghosts": { "actions": ["read"] }
      } } } }
    }`
	errs := schema.Validate(parseSchema(t, raw))
	if !hasError(errs, "unknown resource") {
		t.Fatalf("expected unknown-resource error, got: %v", errs)
	}
}

func TestValidateRBAC_UnknownAction(t *testing.T) {
	raw := `{
      "$schema":"x","version":"1","name":"x",
      "resources": { "posts": { "fields": { "title": { "type": "string" } } } },
      "rbac": { "roles": { "member": { "permissions": {
        "posts": { "actions": ["read","frobnicate"] }
      } } } }
    }`
	errs := schema.Validate(parseSchema(t, raw))
	if !hasError(errs, "unknown action") {
		t.Fatalf("expected unknown-action error, got: %v", errs)
	}
}

func TestValidateRBAC_ConditionActionsMustBeSubset(t *testing.T) {
	// condition_actions lists "delete", which is not in actions.
	raw := `{
      "$schema":"x","version":"1","name":"x",
      "resources": { "posts": { "fields": { "author_id": { "type": "uuid" } } } },
      "rbac": { "roles": { "member": { "permissions": {
        "posts": { "actions": ["read","update"],
                   "conditions": { "field":"author_id","op":"eq","val":"$user_id" },
                   "condition_actions": ["update","delete"] }
      } } } }
    }`
	errs := schema.Validate(parseSchema(t, raw))
	if !hasError(errs, "not in actions") {
		t.Fatalf("expected condition_actions-subset error, got: %v", errs)
	}
}

func TestValidateRBAC_ConditionActionsRequiresCondition(t *testing.T) {
	raw := `{
      "$schema":"x","version":"1","name":"x",
      "resources": { "posts": { "fields": { "author_id": { "type": "uuid" } } } },
      "rbac": { "roles": { "member": { "permissions": {
        "posts": { "actions": ["read","update"], "condition_actions": ["update"] }
      } } } }
    }`
	errs := schema.Validate(parseSchema(t, raw))
	if !hasError(errs, "condition_actions requires conditions") {
		t.Fatalf("expected condition_actions-requires-conditions error, got: %v", errs)
	}
}

func TestValidateRBAC_FieldAllowlistMustExist(t *testing.T) {
	raw := `{
      "$schema":"x","version":"1","name":"x",
      "resources": { "posts": { "fields": { "title": { "type": "string" } } } },
      "rbac": { "roles": { "member": { "permissions": {
        "posts": { "actions": ["read"], "fields": ["id","title","nope"] }
      } } } }
    }`
	errs := schema.Validate(parseSchema(t, raw))
	if !hasError(errs, "field \"nope\" does not exist") {
		t.Fatalf("expected field-allowlist-existence error, got: %v", errs)
	}
}

// ── SEC-AUDIT-V1 ────────────────────────────────────────────────────────────

// TestValidateRBAC_ConditionOpEqOnly proves a non-eq RBAC condition operator is
// rejected at load on BOTH forms (Hallazgo 1): the engine only enforces equality for
// row conditions, so a declared non-eq operator (which would be silently ignored)
// must not be accepted — "declared == applied".
func TestValidateRBAC_ConditionOpEqOnly(t *testing.T) {
	perResource := `{
      "$schema":"x","version":"1","name":"x",
      "resources": { "posts": { "fields": { "author_id": { "type": "uuid" }, "views": { "type": "int" } } } },
      "rbac": { "roles": { "member": { "permissions": {
        "posts": { "actions": ["read"], "conditions": { "field":"views","op":"gt","val":"0" } }
      } } } }
    }`
	if !hasError(schema.Validate(parseSchema(t, perResource)), "only equality") {
		t.Fatalf("per-resource: expected non-eq op rejection, got: %v", schema.Validate(parseSchema(t, perResource)))
	}

	roleGlobal := `{
      "$schema":"x","version":"1","name":"x",
      "resources": { "posts": { "fields": { "author_id": { "type": "uuid" } } } },
      "rbac": { "roles": { "owner": {
        "resources": ["posts"], "actions": ["read"],
        "conditions": { "field":"author_id","op":"ne","val":"$user_id" }
      } } }
    }`
	if !hasError(schema.Validate(parseSchema(t, roleGlobal)), "only equality") {
		t.Fatalf("role-global: expected non-eq op rejection, got: %v", schema.Validate(parseSchema(t, roleGlobal)))
	}

	// eq (and an omitted op) are accepted.
	okEq := `{
      "$schema":"x","version":"1","name":"x",
      "resources": { "posts": { "fields": { "author_id": { "type": "uuid" } } } },
      "rbac": { "roles": { "owner": {
        "resources": ["posts"], "actions": ["read"],
        "conditions": { "field":"author_id","op":"eq","val":"$user_id" }
      } } }
    }`
	if errs := schema.Validate(parseSchema(t, okEq)); len(errs) != 0 {
		t.Fatalf("eq condition should be valid, got: %v", errs)
	}
}

// TestValidateRBAC_RoleGlobalFieldExistence proves the legacy role-global form now
// validates conditions.field and the fields allowlist against the role's resources
// (Hallazgo 3) — a typo is caught at load instead of failing at runtime.
func TestValidateRBAC_RoleGlobalFieldExistence(t *testing.T) {
	condTypo := `{
      "$schema":"x","version":"1","name":"x",
      "resources": { "posts": { "fields": { "author_id": { "type": "uuid" } } } },
      "rbac": { "roles": { "owner": {
        "resources": ["posts"], "actions": ["read"],
        "conditions": { "field":"autor_id","op":"eq","val":"$user_id" }
      } } }
    }`
	if !hasError(schema.Validate(parseSchema(t, condTypo)), "does not exist on any of the role's resources") {
		t.Fatalf("expected role-global condition-field typo rejection, got: %v", schema.Validate(parseSchema(t, condTypo)))
	}

	fieldsTypo := `{
      "$schema":"x","version":"1","name":"x",
      "resources": { "posts": { "fields": { "title": { "type": "string" } } } },
      "rbac": { "roles": { "viewer": {
        "resources": ["posts"], "actions": ["read"], "fields": ["id","title","nope"]
      } } }
    }`
	if !hasError(schema.Validate(parseSchema(t, fieldsTypo)), "does not exist on any of the role's resources") {
		t.Fatalf("expected role-global allowlist typo rejection, got: %v", schema.Validate(parseSchema(t, fieldsTypo)))
	}

	// Union semantics: a fields allowlist spanning several resources is valid as long
	// as each field exists on at least one of them (price only on products, sku only
	// on variants) — mirrors how the real model-lab "public" roles are written.
	unionOK := `{
      "$schema":"x","version":"1","name":"x",
      "resources": {
        "products": { "fields": { "price": { "type": "float64" } } },
        "variants": { "fields": { "sku": { "type": "string" } } }
      },
      "rbac": { "roles": { "public": {
        "resources": ["products","variants"], "actions": ["read"],
        "fields": ["id","price","sku"]
      } } }
    }`
	if errs := schema.Validate(parseSchema(t, unionOK)); len(errs) != 0 {
		t.Fatalf("union allowlist should be valid, got: %v", errs)
	}

	// A wildcard admin with no condition/allowlist is unaffected.
	wildcard := `{
      "$schema":"x","version":"1","name":"x",
      "resources": { "posts": { "fields": { "title": { "type": "string" } } } },
      "rbac": { "roles": { "admin": { "resources": "*", "actions": ["*"] } } }
    }`
	if errs := schema.Validate(parseSchema(t, wildcard)); len(errs) != 0 {
		t.Fatalf("wildcard admin should be valid, got: %v", errs)
	}
}

// TestCheckUnknownKeys_PermissionsStrict verifies the strict-key pass covers the new
// permissions level and its nested condition (a typo there must not be silent).
func TestCheckUnknownKeys_PermissionsStrict(t *testing.T) {
	good := `{
      "$schema":"x","version":"1","name":"x",
      "resources": { "posts": { "fields": { "author_id": { "type": "uuid" } } } },
      "rbac": { "roles": { "member": { "permissions": {
        "posts": { "actions": ["read"], "conditions": { "field":"author_id","op":"eq","val":"$user_id" } }
      } } } }
    }`
	if errs := schema.CheckUnknownKeys(json.RawMessage(good)); len(errs) != 0 {
		t.Fatalf("valid permissions schema rejected by strict keys: %v", errs)
	}

	typo := `{
      "$schema":"x","version":"1","name":"x",
      "resources": { "posts": { "fields": { "author_id": { "type": "uuid" } } } },
      "rbac": { "roles": { "member": { "permissions": {
        "posts": { "actions": ["read"], "condition": { "field":"author_id","op":"eq","val":"$user_id" } }
      } } } }
    }`
	if errs := schema.CheckUnknownKeys(json.RawMessage(typo)); len(errs) == 0 {
		t.Fatal("strict keys must reject \"condition\" (singular) inside a permission entry")
	}
}
