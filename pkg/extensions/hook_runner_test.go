package extensions_test

import (
	"context"
	"testing"

	"github.com/appximo/appximo/pkg/extensions"
	"github.com/appximo/appximo/pkg/schema"
)

func newRunner() *extensions.HookRunner {
	return extensions.NewHookRunner(extensions.NewJSSandbox())
}

// nil hook → Proceed true, Data identical to payload.
func TestRunBeforeHook_NilHook(t *testing.T) {
	hr := newRunner()
	payload := map[string]any{"name": "test"}

	result, err := hr.RunBeforeHook(context.Background(), nil, payload, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Proceed {
		t.Error("expected Proceed=true for nil hook")
	}
	if result.Data["name"] != "test" {
		t.Errorf("expected Data to equal payload, got %v", result.Data)
	}
}

// JS hook that modifies data → Create should use the modified body.
func TestRunBeforeHook_JSModifiesData(t *testing.T) {
	hr := newRunner()
	hook := &schema.HookConfig{
		Type:   "js",
		Script: `data.code = data.code + "-NORMALIZED"; result.data = data;`,
	}
	payload := map[string]any{"code": "GU-001"}

	result, err := hr.RunBeforeHook(context.Background(), hook, payload, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Proceed {
		t.Fatalf("expected Proceed=true, got false (Error: %s)", result.Error)
	}
	if result.Data["code"] != "GU-001-NORMALIZED" {
		t.Errorf("expected normalized code, got %v", result.Data["code"])
	}
}

// JS hook that sets proceed=false → RunBeforeHook returns Proceed=false.
func TestRunBeforeHook_JSRejects(t *testing.T) {
	hr := newRunner()
	hook := &schema.HookConfig{
		Type:   "js",
		Script: `result.proceed = false; result.error = "validation failed";`,
	}

	result, err := hr.RunBeforeHook(context.Background(), hook, map[string]any{}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Proceed {
		t.Error("expected Proceed=false")
	}
	if result.Error == "" {
		t.Error("expected non-empty Error")
	}
}

// Webhook before_create never blocks — always returns Proceed=true.
func TestRunBeforeHook_WebhookAlwaysProceeds(t *testing.T) {
	hr := newRunner()
	hook := &schema.HookConfig{
		Type: "webhook",
		URL:  "https://example.com/hook",
	}
	payload := map[string]any{"key": "val"}

	result, err := hr.RunBeforeHook(context.Background(), hook, payload, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Proceed {
		t.Error("expected webhook before hook to always Proceed=true")
	}
}
