package codegen

import (
	"context"

	"github.com/appximo/appximo/pkg/ask"
	"github.com/appximo/appximo/pkg/askspend"
)

// AskRuntime is the per-app state of the question path that BuildRouter
// cannot own (VOZ-SIN-IA-S1): the spend ledger with its caps and alerts, and
// the plan cache. app.go builds one per app and installs it in every request
// context through WithAskRuntime (the same seam as the deployed provider), so
// N in-process apps never share a wallet or a cache. nil = no caps, no cache
// (tests, bare BuildRouter callers).
type AskRuntime struct {
	Ledger *askspend.Ledger
	Cache  *ask.PlanCache
}

type askRuntimeKey struct{}

// WithAskRuntime installs rt in ctx.
func WithAskRuntime(ctx context.Context, rt *AskRuntime) context.Context {
	return context.WithValue(ctx, askRuntimeKey{}, rt)
}

// AskRuntimeFromCtx returns the runtime installed for this request, if any.
func AskRuntimeFromCtx(ctx context.Context) *AskRuntime {
	rt, _ := ctx.Value(askRuntimeKey{}).(*AskRuntime)
	return rt
}
