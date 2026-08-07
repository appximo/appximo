package schema

import (
	"encoding/json"
	"strings"
	"testing"
)

func loadPublic(t *testing.T, raw string) (*APISchema, []ValidationError) {
	t.Helper()
	var s APISchema
	if err := json.Unmarshal([]byte(raw), &s); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return &s, Validate(&s)
}

const publicBase = `{
  "$schema":"x","version":"1","name":"blog",
  "resources": { "articulos": { "fields": {
    "titulo": { "type": "string", "required": true, "minLength": 1 },
    "estado": { "type": "string", "enum": ["borrador","publicado"], "default": "borrador" },
    "notas":  { "type": "text" }
  } } },
  "rbac": {
    "roles": { "admin": { "resources": "*", "actions": ["*"] } },
    "public": { "articulos": { "actions": ["read"],
      "conditions": { "field": "estado", "op": "eq", "val": "publicado" },
      "fields": ["id","titulo","estado"] } }
  }
}`

// TestPublicBlock_ValidSchemaLoads — ADR-026: the canonical anonymous-blog
// declaration is valid, and the strict-key layer accepts the new rbac.public key.
func TestPublicBlock_ValidSchemaLoads(t *testing.T) {
	if _, err := LoadFromBytes([]byte(publicBase)); err != nil {
		t.Fatalf("canonical public schema must load: %v", err)
	}
}

// TestPublicBlock_ReadOnlyEnforced — any action but exactly ["read"] rejects.
func TestPublicBlock_ReadOnlyEnforced(t *testing.T) {
	for _, actions := range []string{`["read","create"]`, `["*"]`, `["create"]`, `[]`} {
		raw := strings.Replace(publicBase, `"actions": ["read"],`, `"actions": `+actions+`,`, 1)
		_, errs := loadPublic(t, raw)
		found := false
		for _, e := range errs {
			if e.Rule == "public_read_only" {
				found = true
			}
		}
		if !found {
			t.Fatalf("actions %s must be rejected as public_read_only, got %v", actions, errs)
		}
	}
}

// TestPublicBlock_IdentityValRejected — $user_id in a public condition is a load
// ERROR (an anonymous request has no identity), not the authenticated warning.
func TestPublicBlock_IdentityValRejected(t *testing.T) {
	raw := strings.Replace(publicBase, `"val": "publicado"`, `"val": "$user_id"`, 1)
	_, errs := loadPublic(t, raw)
	found := false
	for _, e := range errs {
		if e.Rule == "public_condition_identity" {
			found = true
			if !strings.Contains(e.Fix, "literal") {
				t.Errorf("fix must point at a literal val: %+v", e)
			}
		}
	}
	if !found {
		t.Fatalf("$user_id in rbac.public must be a load error, got %v", errs)
	}
}

// TestPublicBlock_UnknownNamesRejected — resource, condition field and allowlist
// entries must exist (same guarantees as authenticated permissions).
func TestPublicBlock_UnknownNamesRejected(t *testing.T) {
	for _, c := range []struct{ find, replace, wantRule string }{
		{`"articulos": { "actions": ["read"],`, `"fantasmas": { "actions": ["read"],`, "unknown_resource"},
		{`"field": "estado"`, `"field": "ghost"`, "unknown_field"},
		{`"fields": ["id","titulo","estado"]`, `"fields": ["id","ghost"]`, "unknown_field"},
	} {
		raw := strings.Replace(publicBase, c.find, c.replace, 1)
		_, errs := loadPublic(t, raw)
		found := false
		for _, e := range errs {
			if e.Rule == c.wantRule && strings.HasPrefix(e.Field, "rbac.public") {
				found = true
			}
		}
		if !found {
			t.Fatalf("replace %q: want rule %s under rbac.public, got %v", c.replace, c.wantRule, errs)
		}
	}
}

// TestPublicBlock_ReservedRoleName — rbac.roles may not declare "$public".
func TestPublicBlock_ReservedRoleName(t *testing.T) {
	raw := strings.Replace(publicBase, `"admin": { "resources": "*", "actions": ["*"] }`,
		`"admin": { "resources": "*", "actions": ["*"] }, "$public": { "resources": "*", "actions": ["read"] }`, 1)
	_, errs := loadPublic(t, raw)
	found := false
	for _, e := range errs {
		if e.Rule == "reserved_role_name" {
			found = true
		}
	}
	if !found {
		t.Fatalf("a role literally named $public must be rejected, got %v", errs)
	}
}

// TestPublicBlock_FilesGrantActionsOnly — the built-in store is grantable, read
// only, actions only.
func TestPublicBlock_FilesGrant(t *testing.T) {
	raw := strings.Replace(publicBase, `"public": {`, `"public": { "files": { "actions": ["read"] },`, 1)
	if _, errs := loadPublic(t, raw); len(errs) != 0 {
		t.Fatalf("public files read grant must be valid, got %v", errs)
	}
	raw = strings.Replace(publicBase, `"public": {`, `"public": { "files": { "actions": ["read"], "fields": ["id"] },`, 1)
	_, errs := loadPublic(t, raw)
	found := false
	for _, e := range errs {
		if e.Rule == "files_grant_actions_only" {
			found = true
		}
	}
	if !found {
		t.Fatalf("fields on the public files grant must be rejected, got %v", errs)
	}
}
