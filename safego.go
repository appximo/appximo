package appitools

import (
	"context"
	"fmt"
	"runtime/debug"
	"time"

	chimiddleware "github.com/go-chi/chi/v5/middleware"
	"golang.org/x/sync/errgroup"

	"github.com/miguelangel/appitools/pkg/logging"
)

// defaultSafeGoTimeout bounds a Ctx.SafeGo goroutine's CONTEXT when neither
// Config.SafeGoTimeoutSeconds nor APPITOOLS_SAFEGO_TIMEOUT is set. A
// fire-and-forget background task should be short; 30s is generous. It cancels
// the context — fn must honor cancellation to stop; a deadline cannot forcibly
// kill a goroutine that ignores it.
const defaultSafeGoTimeout = 30 * time.Second

// requestID returns the chi request id carried in ctx (empty if none), used to
// correlate a SafeGo goroutine's logs back to the request that spawned it.
func requestID(ctx context.Context) string { return chimiddleware.GetReqID(ctx) }

// runSafe is the body of every Ctx.SafeGo goroutine: it applies the independent
// bounded deadline, RECOVERS any panic (the whole point — a bare goroutine
// panic crashes the multi-tenant process), records it on the metric + a
// structured log, and runs fn. It takes only value-typed, request-independent
// arguments so nothing request-scoped (the tx, the ResponseWriter) is captured.
func runSafe(base context.Context, timeout time.Duration, tenantID, reqID string, onPanic func(), fn func(context.Context)) {
	ctx := base
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(base, timeout)
		defer cancel()
	}
	defer func() {
		if rvr := recover(); rvr != nil {
			if onPanic != nil {
				onPanic()
			}
			logging.Log.Error().
				Str("tenant_id", tenantID).
				Str("request_id", reqID).
				Interface("panic", rvr).
				Bytes("stack", debug.Stack()).
				Msg("recovered panic in Ctx.SafeGo goroutine")
		}
	}()
	fn(ctx)
}

// SafeParallel runs tasks concurrently with at most `limit` of them in flight at
// once (backpressure — a handler never spawns an unbounded number of goroutines)
// and RECOVERS a panic in any task into an error, so one bad task can never
// crash the process. This is the sanctioned in-request fan-out primitive: a raw
// errgroup does NOT recover — a panicking errgroup goroutine takes the whole
// process down, exactly the failure mode Phase 0 (LIBRARY-HARDEN-S1) closes.
//
// It waits for every task and returns the FIRST non-nil error (a recovered panic
// is reported as an error). ctx is the handler's context — cancel it (e.g. via
// Route.Timeout) to abort the tasks still running. limit <= 0 means unbounded
// (one goroutine per task); prefer a small bound sized to the work. Unlike
// Ctx.SafeGo (detached, fire-and-forget), SafeParallel's tasks share the
// request's lifetime and the handler waits for their results — so a task MAY use
// the handler's transaction, provided the tasks do not write it concurrently
// (pgx.Tx is not safe for concurrent use; parallelise reads or independent work,
// serialise writes on the tx).
func SafeParallel(ctx context.Context, limit int, tasks ...func(context.Context) error) error {
	g, gctx := errgroup.WithContext(ctx)
	if limit > 0 {
		g.SetLimit(limit)
	}
	for _, task := range tasks {
		task := task
		g.Go(func() (err error) {
			defer func() {
				if rvr := recover(); rvr != nil {
					err = fmt.Errorf("appitools: recovered panic in SafeParallel task: %v", rvr)
					logging.Log.Error().
						Interface("panic", rvr).
						Bytes("stack", debug.Stack()).
						Msg("recovered panic in SafeParallel task")
				}
			}()
			return task(gctx)
		})
	}
	return g.Wait()
}
