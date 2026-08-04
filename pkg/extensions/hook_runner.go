package extensions

import (
	"context"
	"encoding/json"
	"log"

	"github.com/appximo/appximo/pkg/schema"
)

// defaultAfterHookConcurrency bounds the number of after_create webhook
// dispatches that may be in flight at once. Without a bound, a burst of creates
// would spawn one unbounded goroutine each (every one holding an HTTP client with
// a 10s timeout), which can exhaust goroutines, sockets, and file descriptors —
// the classic fire-and-forget DoS amplification. 256 is generous for normal
// traffic; excess dispatches are dropped (and logged) rather than queued.
const defaultAfterHookConcurrency = 256

// HookRunner orchestrates JS sandbox, WASM runtime, and webhook dispatch for
// lifecycle hooks.
type HookRunner struct {
	sandbox    *JSSandbox
	dispatcher *WebhookDispatcher
	// wasm runs Capa 3 (WASM) hooks. nil when the WASM runtime is not configured;
	// a wasm-typed hook then fails closed (Proceed=false).
	wasm *WasmRunner
	// afterSem bounds concurrent async after-hook dispatches. A token is taken in
	// FireAfterHook (the caller's goroutine) before the dispatch goroutine is
	// spawned, so a full buffer means the dispatch is dropped immediately instead
	// of piling up goroutines.
	afterSem chan struct{}
}

// NewHookRunner creates a HookRunner backed by the provided sandbox and a
// production (SSRF-safe, HTTPS-only) webhook dispatcher. No WASM runtime.
func NewHookRunner(sandbox *JSSandbox) *HookRunner {
	return &HookRunner{
		sandbox:    sandbox,
		dispatcher: NewWebhookDispatcher(),
		afterSem:   make(chan struct{}, defaultAfterHookConcurrency),
	}
}

// NewHookRunnerWithWasm creates a HookRunner with a WASM runtime (Capa 3) wired
// in alongside the JS sandbox and webhook dispatcher. A nil dispatcher falls
// back to the production dispatcher.
func NewHookRunnerWithWasm(sandbox *JSSandbox, dispatcher *WebhookDispatcher, wasm *WasmRunner) *HookRunner {
	if dispatcher == nil {
		dispatcher = NewWebhookDispatcher()
	}
	return &HookRunner{
		sandbox:    sandbox,
		dispatcher: dispatcher,
		wasm:       wasm,
		afterSem:   make(chan struct{}, defaultAfterHookConcurrency),
	}
}

// NewHookRunnerWithDispatcher creates a HookRunner with an explicit dispatcher
// and an explicit bound on concurrent async dispatches. maxConcurrent <= 0 falls
// back to the default. Used by tests to inject an insecure (loopback) dispatcher
// or a small concurrency bound.
func NewHookRunnerWithDispatcher(sandbox *JSSandbox, dispatcher *WebhookDispatcher, maxConcurrent int) *HookRunner {
	if maxConcurrent <= 0 {
		maxConcurrent = defaultAfterHookConcurrency
	}
	return &HookRunner{
		sandbox:    sandbox,
		dispatcher: dispatcher,
		afterSem:   make(chan struct{}, maxConcurrent),
	}
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
	case "wasm":
		return hr.runWasmBeforeHook(ctx, hook, payload, userCtx)
	case "webhook":
		// Unreachable from a validated schema: a `webhook` before-hook is rejected
		// at load (ENG-19 — this branch used to claim "fire-and-forget
		// notifications" while dispatching NOTHING, the accepted-and-silent shape).
		// Kept as a fail-open no-op for defense in depth: a hook type that cannot
		// run must not block writes if it somehow reaches here.
		return &HookResult{Proceed: true, Data: payload}, nil
	default:
		return &HookResult{Proceed: true, Data: payload}, nil
	}
}

// runWasmBeforeHook executes a Capa 3 (WASM) before_create hook. The payload is
// passed to the module as JSON bytes; the module returns the (possibly
// transformed) payload as JSON, which becomes HookResult.Data. Any error fails
// closed (Proceed=false) so a broken module never silently lets a write through.
func (hr *HookRunner) runWasmBeforeHook(
	ctx context.Context,
	hook *schema.HookConfig,
	payload map[string]any,
	userCtx map[string]any,
) (*HookResult, error) {
	if hr.wasm == nil {
		return &HookResult{Proceed: false, Error: "wasm runtime not configured"}, nil
	}
	in, err := json.Marshal(payload)
	if err != nil {
		return &HookResult{Proceed: false, Error: "wasm hook: marshal payload: " + err.Error()}, nil
	}
	tenantID, _ := userCtx["tenant_id"].(string)
	out, err := hr.wasm.ExecuteNamed(ctx, tenantID, hook.WasmModule, hook.WasmFn, in)
	if err != nil {
		return &HookResult{Proceed: false, Error: "wasm hook: " + err.Error()}, nil
	}
	data := payload
	if len(out) > 0 {
		var decoded map[string]any
		if err := json.Unmarshal(out, &decoded); err != nil {
			return &HookResult{Proceed: false, Error: "wasm hook: output is not a JSON object: " + err.Error()}, nil
		}
		if decoded != nil {
			data = decoded
		}
	}
	return &HookResult{Proceed: true, Data: data}, nil
}

// FireAfterHook dispatches the after_create hook on a bounded background
// goroutine and returns immediately, so the request never blocks on webhook
// latency. It returns true if the dispatch was admitted, false if the dispatch
// pool was saturated and the hook was dropped (logged). A nil hook is a no-op
// that returns true.
//
// This is the production entry point used by the create handler; prefer it over
// `go hr.RunAfterHook(...)` so the number of in-flight dispatches stays bounded.
func (hr *HookRunner) FireAfterHook(hook *schema.HookConfig, event string, record map[string]any, tenantID string) bool {
	if hook == nil {
		return true
	}
	select {
	case hr.afterSem <- struct{}{}:
	default:
		log.Printf("WEBHOOK [%s] dispatch pool saturated (cap=%d); dropping %s %s hook",
			tenantID, cap(hr.afterSem), hook.Type, event)
		return false
	}
	go func() {
		defer func() { <-hr.afterSem }()
		// Detached context: the after-hook dispatch must survive the request it was
		// triggered by (fire-and-forget). The dispatcher enforces its own per-request
		// timeout and retry budget.
		hr.RunAfterHook(context.Background(), hook, event, record, tenantID)
	}()
	return true
}

// RunAfterHook fires an after-hook synchronously on the calling goroutine. event is
// the REAL lifecycle event ("after_create" | "after_update") so the webhook carries
// the correct X-Appximo-Event header (SEC-AUDIT-V2 Hallazgo B). Production code
// should call FireAfterHook (which bounds concurrency and returns immediately);
// RunAfterHook is the worker it invokes. Only "webhook" after-hooks do anything —
// js/wasm after-hooks are rejected at schema load (see schema.Validate), so the
// js/wasm branch here is unreachable from a validated schema and kept fail-safe.
func (hr *HookRunner) RunAfterHook(
	ctx context.Context,
	hook *schema.HookConfig,
	event string,
	record map[string]any,
	tenantID string,
) {
	if hook == nil {
		return
	}
	switch hook.Type {
	case "webhook":
		hr.dispatcher.Dispatch(ctx, hook, event, record, tenantID)
	default:
		// js/wasm after-hooks are rejected at load — a no-op here is defense in depth.
	}
}
