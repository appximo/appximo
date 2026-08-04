package schema

import (
	"encoding/json"
	"strings"
	"testing"
)

func checkErrs(t *testing.T, raw string) []ValidationError {
	t.Helper()
	return CheckUnknownKeys(json.RawMessage(raw))
}

func TestCheckUnknownKeys_ValidSchemaPasses(t *testing.T) {
	valid := `{
		"$schema": "https://appximo.com/schema/v1", "version": "1", "name": "t",
		"resources": {
			"tasks": {
				"fields": {"title": {"type": "string", "required": true, "maxLength": 200}},
				"hooks": {"after_create": {"type": "webhook", "url": "https://x.example", "hmac_secret_env": "HOOK_SECRET"}},
				"indexes": [{"fields": ["title"]}]
			}
		},
		"rbac": {"roles": {"admin": {"resources": "*", "actions": ["*"]}}},
		"workflows": {"w": {"trigger": {"type": "event", "event": "after_create", "resource": "tasks"},
			"steps": [{"name": "s1", "type": "webhook", "ref": "x", "config": {"anything": "goes"}}]}}
	}`
	if errs := checkErrs(t, valid); len(errs) != 0 {
		t.Fatalf("valid schema rejected: %v", errs)
	}
}

func TestCheckUnknownKeys_RejectsTheDropletTypos(t *testing.T) {
	// The exact clean-room failure: "webhooks" (not "hooks") at resource level
	// and "secret" (not "hmac_secret_env") inside a hook — both previously
	// swallowed in silence.
	raw := `{
		"$schema": "s", "version": "1",
		"resources": {
			"tasks": {
				"fields": {"title": {"type": "string"}},
				"webhooks": {"on_create": {"url": "http://x"}},
				"hooks": {"after_create": {"type": "webhook", "url": "https://x", "secret": "oops"}}
			}
		}
	}`
	errs := checkErrs(t, raw)
	joined := make([]string, len(errs))
	for i, e := range errs {
		joined[i] = e.Error()
	}
	all := strings.Join(joined, "\n")
	for _, want := range []string{
		`unknown key "webhooks"`,
		`valid keys: fields, hooks, indexes`,
		`unknown key "secret"`,
		`hmac_secret_env`,
	} {
		if !strings.Contains(all, want) {
			t.Errorf("errors missing %q in:\n%s", want, all)
		}
	}
}

func TestCheckUnknownKeys_RejectsUnknownHookEvent(t *testing.T) {
	raw := `{"$schema":"s","version":"1","resources":{"tasks":{
		"fields":{"title":{"type":"string"}},
		"hooks":{"on_create":{"type":"webhook","url":"https://x"}}}}}`
	errs := checkErrs(t, raw)
	if len(errs) == 0 {
		t.Fatal("unknown hook event accepted")
	}
	if !strings.Contains(errs[0].Error(), "after_create") {
		t.Errorf("error should list valid events, got: %v", errs[0])
	}
}

func TestCheckUnknownKeys_TopLevelTypo(t *testing.T) {
	errs := checkErrs(t, `{"$schema":"s","version":"1","resorces":{}}`)
	if len(errs) != 1 || !strings.Contains(errs[0].Error(), `unknown key "resorces"`) {
		t.Fatalf("want one resorces typo error, got %v", errs)
	}
}
