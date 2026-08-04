// Command worker is the FRAMEWORK-MODE outbox consumer for the backend-guide
// example — the other half of the async story in docs/BACKEND_SPEC_LLM.md §6.
//
// The engine's `Ctx.Enqueue(topic, payload)` writes a job in your handler's
// transaction (so the job exists if and only if the business write committed) and
// fires a notify on commit. THIS process drains it. It is a SEPARATE binary from
// the engine on purpose: a slow or crashing consumer must never hold a request
// open, pin a pool connection, or take the API down with it.
//
// You do not need the shipped `appximo-worker` binary to do this — pkg/worker is
// a library, and a consumer is ~40 lines:
//
//	DATABASE_URL=... go run ./examples/backend-guide/worker
//
// Scale it by running N IDENTICAL copies: the drain uses
// `SELECT … FOR UPDATE SKIP LOCKED`, so instances never collide on a row.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/jackc/pgx/v5"
	"github.com/rs/zerolog"

	"github.com/appximo/appximo/pkg/consumers"
	"github.com/appximo/appximo/pkg/worker"
)

func main() {
	log := zerolog.New(os.Stderr).With().Timestamp().Logger()

	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		log.Fatal().Msg("DATABASE_URL is required")
	}

	// Cancelled on SIGINT/SIGTERM: the worker finishes the batch it is on, closes
	// its connections, and Run returns cleanly.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// DEDICATED connections (pgx.Connect), never a pool: a LISTEN connection must
	// be permanent, and a pool would rotate it out from under the listener —
	// silently breaking the wake-up path and leaving you on the poll fallback.
	connect := func(ctx context.Context) (*pgx.Conn, error) { return pgx.Connect(ctx, dsn) }

	// A Router dispatches by topic. One worker, N event types — this is the
	// composition to reach for: a single-topic worker ACKS the topics it does not
	// own, so running two DIFFERENT single-topic workers against one outbox
	// silently drops events under SKIP LOCKED.
	router := consumers.NewRouter(log)
	router.Handle("email.send", worker.ProcessorFunc(sendEmail))
	router.Handle("enrollments.created", worker.ProcessorFunc(onEnrollmentCreated))

	w := worker.New(connect, router, worker.Config{}, log)
	log.Info().Msg("backend-guide worker starting")
	if err := w.Run(ctx); err != nil && ctx.Err() == nil {
		log.Fatal().Err(err).Msg("worker exited with error")
	}
	log.Info().Msg("worker stopped")
}

// sendEmail is a Processor: it receives the row your handler enqueued.
//
// Delivery is AT-LEAST-ONCE, so a Processor MUST be idempotent — the same row can
// arrive twice (a crash between the side effect and the ack). Make the side effect
// idempotent at the destination (a provider idempotency key, an upsert on a unique
// column), not with an `if already_done` that races with itself.
//
// Returning nil ACKS the row. Returning an error keeps it for retry with backoff:
// return an error ONLY for transient failures. A permanent one (a malformed
// payload, a rejected address) should be RECORDED and acked — otherwise it is
// retried until it exhausts max_attempts, burning the queue on a job that can
// never succeed.
func sendEmail(ctx context.Context, row worker.Row) error {
	var payload struct {
		Template string `json:"template"`
		To       string `json:"to"`
	}
	if err := json.Unmarshal(row.Payload, &payload); err != nil {
		// PERMANENT: no retry will fix a payload that does not parse.
		return nil
	}
	if payload.To == "" {
		return nil // permanent, same reasoning
	}
	// Real work goes here (SMTP, a provider API…). A transient provider failure is
	// the one case that should `return err` so the row is retried.
	fmt.Printf("worker: would send %q to %s (tenant %s)\n", payload.Template, payload.To, row.TenantID)
	return nil
}

// onEnrollmentCreated consumes the event the SCHEMA emits: `"events": ["create"]`
// on the enrollments resource makes the engine write `enrollments.created` to the
// outbox in the same transaction as the INSERT — no handler code at all.
//
// The payload is deliberately LEAN — {id, tenant_id, resource, action} — so a
// consumer that needs the row reads it back. Write results BACK through the ENGINE
// API (worker.NewEngineClient), never straight into the tenant's tables: that way
// the write inherits the engine's validation and RBAC instead of bypassing both.
func onEnrollmentCreated(_ context.Context, row worker.Row) error {
	var payload struct {
		ID       string `json:"id"`
		TenantID string `json:"tenant_id"`
	}
	if err := json.Unmarshal(row.Payload, &payload); err != nil {
		return nil // permanent
	}
	fmt.Printf("worker: enrollment %s created in tenant %s\n", payload.ID, payload.TenantID)
	return nil
}
