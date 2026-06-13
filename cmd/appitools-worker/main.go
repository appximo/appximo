// Command appitools-worker is the outbox consumer (ADR-016 §Class 2): a SEPARATE
// process that drains public.outbox and runs each event through a Processor. It
// connects to the SAME Postgres as the engine (DATABASE_URL) with DEDICATED
// connections (never the engine's pool), LISTENs on outbox.NotifyChannel as a
// wake-up hint, and polls the table — the durable source of truth — as a fallback.
//
// Scope (WORKER-V1): the only Processor wired here is the echo consumer, which
// handles the echo.test topic that the engine's POST /api/_echo emits. It logs the
// event and marks it sent — the minimal end-to-end proof that the async Class 2
// loop lives. Real consumers (XLSX, email, write-back with a service JWT) and the
// CRUD hookup are later bricks (ADR-016).
package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/jackc/pgx/v5"
	"github.com/rs/zerolog"

	"github.com/miguelangel/appitools/pkg/logging"
	"github.com/miguelangel/appitools/pkg/worker"
)

func main() {
	logging.Init(os.Getenv("APPITOOLS_ENV"))

	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		logging.Log.Fatal().Msg("DATABASE_URL environment variable is required")
	}

	// Cancelled on SIGINT/SIGTERM → the worker finishes its current batch, joins
	// the listener goroutine, closes its connections, and Run returns cleanly.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// DEDICATED connection factory: pgx.Connect, NOT the engine pool. A LISTEN
	// connection must be permanent, and a pool would rotate it out from under the
	// listener (breaking LISTEN silently). The worker calls this for the listen
	// conn, the drain conn, and again on every reconnect.
	connect := func(ctx context.Context) (*pgx.Conn, error) {
		return pgx.Connect(ctx, dsn)
	}

	w := worker.New(connect, echoProcessor{log: logging.Log}, worker.Config{}, logging.Log)

	logging.Log.Info().Msg("appitools-worker starting")
	if err := w.Run(ctx); err != nil && ctx.Err() == nil {
		logging.Log.Fatal().Err(err).Msg("appitools-worker exited with error")
	}
	logging.Log.Info().Msg("appitools-worker stopped")
}

// echoProcessor is the WORKER-V1 consumer: it logs the event and returns nil. It
// is trivially idempotent (a redelivery just logs twice), which is exactly the
// property at-least-once delivery requires — see worker.Processor for where a real
// consumer with side-effects would place its idempotency-key check.
type echoProcessor struct {
	log zerolog.Logger
}

// Process implements worker.Processor for the echo.test topic.
func (p echoProcessor) Process(_ context.Context, row worker.Row) error {
	p.log.Info().
		Int64("id", row.ID).
		Str("tenant_id", row.TenantID).
		Str("topic", row.Topic).
		RawJSON("payload", row.Payload).
		Msg("worker: processed outbox event")
	return nil
}
