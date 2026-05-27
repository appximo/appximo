package extensions

import (
	"context"
	"log"

	"github.com/miguelangel/appitools/pkg/schema"
)

// WebhookDispatcher is a stub for the future async webhook implementation.
type WebhookDispatcher struct{}

// HookRunner orchestrates JS sandbox and webhook dispatch for lifecycle hooks.
type HookRunner struct {
	sandbox    *JSSandbox
	dispatcher *WebhookDispatcher
}

// NewHookRunner creates a HookRunner backed by the provided sandbox.
func NewHookRunner(sandbox *JSSandbox) *HookRunner {
	return &HookRunner{sandbox: sandbox}
}

// RunBeforeHook runs the before_create hook synchronously and returns the outcome.
// A nil hook is a no-op that returns {Proceed:true, Data:payload}.
// Webhook hooks never block: they always return Proceed:true.
func (hr *HookRunner) RunBeforeHook(
	ctx context.Context,
	hook *schema.HookConfig,
	payload map[string]any,
	userCtx map[string]any,
) (*HookResult, error) {
	if hook == nil {
		return &HookResult{Proceed: true, Data: payload}, nil
	}
	switch hook.Type {
	case "js":
		return hr.sandbox.RunHook(ctx, hook.Script, payload, userCtx)
	case "webhook":
		// before_create webhooks are fire-and-forget notifications; they never block the request.
		return &HookResult{Proceed: true, Data: payload}, nil
	default:
		return &HookResult{Proceed: true, Data: payload}, nil
	}
}

// RunAfterHook fires the after_create hook without blocking the caller.
// Designed to be called with `go hr.RunAfterHook(...)`.
func (hr *HookRunner) RunAfterHook(
	ctx context.Context,
	hook *schema.HookConfig,
	record map[string]any,
	tenantID string,
) {
	if hook == nil {
		return
	}
	switch hook.Type {
	case "webhook":
		log.Printf("WEBHOOK [%s] %s — pendiente implementar", tenantID, hook.URL)
	case "js":
		// JS after_create hooks are not supported — no-op.
	}
}
