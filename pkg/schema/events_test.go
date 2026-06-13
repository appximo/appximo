package schema

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestEmitsOnAndTopic(t *testing.T) {
	r := ResourceSchema{Events: []string{"create", "delete"}}
	if !r.EmitsOn("create") || !r.EmitsOn("delete") {
		t.Fatal("EmitsOn should be true for declared actions")
	}
	if r.EmitsOn("update") {
		t.Fatal("EmitsOn should be false for an undeclared action")
	}
	if got := EmitTopic("tasks", "create"); got != "tasks.created" {
		t.Fatalf("EmitTopic create = %q, want tasks.created", got)
	}
	if got := EmitTopic("tasks", "update"); got != "tasks.updated" {
		t.Fatalf("EmitTopic update = %q, want tasks.updated", got)
	}
	if got := EmitTopic("tasks", "delete"); got != "tasks.deleted" {
		t.Fatalf("EmitTopic delete = %q, want tasks.deleted", got)
	}
	if got := EmitTopic("tasks", "bogus"); got != "" {
		t.Fatalf("EmitTopic for unknown action should be empty, got %q", got)
	}
}

func TestEventsKeyAllowedAndValidated(t *testing.T) {
	base := `{
	  "$schema":"x","version":"1","name":"t",
	  "resources":{"tasks":{"fields":{"title":{"type":"string"}}%s}},
	  "rbac":{"roles":{"admin":{"resources":"*","actions":["*"]}}}
	}`

	t.Run("events key accepted by strict-key check", func(t *testing.T) {
		raw := json.RawMessage(strings.Replace(base, "%s", `,"events":["create","update","delete"]`, 1))
		if errs := CheckUnknownKeys(raw); len(errs) > 0 {
			t.Fatalf("events should be a valid resource key, got: %v", errs)
		}
	})

	t.Run("unknown event action rejected at Validate", func(t *testing.T) {
		var s APISchema
		raw := strings.Replace(base, "%s", `,"events":["create","read"]`, 1)
		if err := json.Unmarshal([]byte(raw), &s); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		errs := Validate(&s)
		if !hasErr(errs, "events", "unknown event action") {
			t.Fatalf("expected unknown-event-action error, got: %v", errs)
		}
	})

	t.Run("duplicate event action rejected", func(t *testing.T) {
		var s APISchema
		raw := strings.Replace(base, "%s", `,"events":["create","create"]`, 1)
		_ = json.Unmarshal([]byte(raw), &s)
		if errs := Validate(&s); !hasErr(errs, "events", "duplicate") {
			t.Fatalf("expected duplicate error, got: %v", errs)
		}
	})

	t.Run("valid events pass Validate", func(t *testing.T) {
		var s APISchema
		raw := strings.Replace(base, "%s", `,"events":["create","update","delete"]`, 1)
		_ = json.Unmarshal([]byte(raw), &s)
		if errs := Validate(&s); len(errs) > 0 {
			t.Fatalf("valid events should pass, got: %v", errs)
		}
	})
}

func hasErr(errs []ValidationError, fieldContains, msgContains string) bool {
	for _, e := range errs {
		if strings.Contains(e.Field, fieldContains) && strings.Contains(e.Message, msgContains) {
			return true
		}
	}
	return false
}
