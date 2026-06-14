// Command appitools-worker is the outbox consumer (ADR-016 §Class 2): a SEPARATE
// process that drains public.outbox and runs each event through a Processor. It
// connects to the SAME Postgres as the engine (DATABASE_URL) with DEDICATED
// connections (never the engine's pool), LISTENs on outbox.NotifyChannel as a
// wake-up hint, and polls the table — the durable source of truth — as a fallback.
//
// Scope: by default the Processor is the echo consumer, which logs the event and
// marks it sent — the minimal end-to-end proof that the async Class 2 loop lives.
//
// SERVICE-JWT-V1: setting APPITOOLS_WORKER_WRITEBACK=on swaps in the write-back
// demo consumer, which mints a SHORT-LIVED, SCOPED service JWT and PATCHes the
// created row's status back through the engine API — proving authenticated
// write-back (event → mint JWT → engine API → RBAC accepts the scoped role). It
// is a demo, not real business logic; real consumers (XLSX, email) are later
// bricks (ADR-016).
package main

import (
	"context"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/jackc/pgx/v5"
	"github.com/rs/zerolog"

	"github.com/miguelangel/appitools/pkg/consumers"
	"github.com/miguelangel/appitools/pkg/db"
	"github.com/miguelangel/appitools/pkg/files"
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

	// Pick the consumer. APPITOOLS_WORKER_MODE = echo (default) | writeback | xlsx.
	// The legacy APPITOOLS_WORKER_WRITEBACK=on still maps to "writeback".
	mode := os.Getenv("APPITOOLS_WORKER_MODE")
	if mode == "" && os.Getenv("APPITOOLS_WORKER_WRITEBACK") == "on" {
		mode = "writeback"
	}
	var proc worker.Processor
	switch mode {
	case "writeback":
		proc = newWritebackProcessor()
	case "xlsx":
		proc = newXLSXProcessor(ctx, dsn)
	default:
		proc = echoProcessor{log: logging.Log}
	}

	w := worker.New(connect, proc, worker.Config{}, logging.Log)

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

// newWritebackProcessor builds the SERVICE-JWT-V1 demo consumer from env:
//
//	JWT_SECRET                  (required) — shared with the engine, signs the service JWT
//	APPITOOLS_ENGINE_URL        engine data-plane URL   (default http://localhost:8080)
//	APPITOOLS_TENANT_DOMAIN     Host suffix per tenant  (default localhost → acme.localhost)
//	APPITOOLS_WORKER_ROLE       scoped service role     (default service_worker)
//
// The role MUST be a minimally-scoped role in the schema RBAC — never admin.
func newWritebackProcessor() worker.Processor {
	secret := os.Getenv("JWT_SECRET")
	if secret == "" {
		logging.Log.Fatal().Msg("APPITOOLS_WORKER_WRITEBACK=on requires JWT_SECRET (shared with the engine)")
	}
	engineURL := envOr("APPITOOLS_ENGINE_URL", "http://localhost:8080")
	tenantDomain := envOr("APPITOOLS_TENANT_DOMAIN", "localhost")
	role := envOr("APPITOOLS_WORKER_ROLE", "service_worker")

	client := worker.NewEngineClient(engineURL, tenantDomain, secret, role, worker.DefaultServiceTokenTTL)
	logging.Log.Info().
		Str("engine_url", engineURL).
		Str("tenant_domain", tenantDomain).
		Str("role", role).
		Dur("token_ttl", worker.DefaultServiceTokenTTL).
		Msg("worker: write-back demo enabled (authenticated PATCH via engine API)")
	return worker.NewWritebackProcessor(client, "done", logging.Log)
}

// newXLSXProcessor builds the XLSX-CONSUMER-V1 real consumer (FileJob pattern).
// Same engine-client env as the write-back demo, plus:
//
//	APPITOOLS_WORKER_RESOURCE   the jobs resource (default filejobs)
//
// The service role (APPITOOLS_WORKER_ROLE, default service_worker) must have
// read+update on that resource — read to fetch the job's file_ref, update to
// write {status, result}.
//
// File source (FILES-V1): when APPITOOLS_FILES_DIR is set, file_ref is a VFS
// file_id and the consumer streams the content-addressed blob via VFS.Get (the
// worker opens a dedicated pool for the per-tenant metadata lookup, sharing the
// SAME blob root as the engine on this host). Without it, file_ref is read as a
// local path (back-compat).
func newXLSXProcessor(ctx context.Context, dsn string) worker.Processor {
	secret := os.Getenv("JWT_SECRET")
	if secret == "" {
		logging.Log.Fatal().Msg("APPITOOLS_WORKER_MODE=xlsx requires JWT_SECRET (shared with the engine)")
	}
	engineURL := envOr("APPITOOLS_ENGINE_URL", "http://localhost:8080")
	tenantDomain := envOr("APPITOOLS_TENANT_DOMAIN", "localhost")
	role := envOr("APPITOOLS_WORKER_ROLE", "service_worker")
	resource := envOr("APPITOOLS_WORKER_RESOURCE", "filejobs")

	client := worker.NewEngineClient(engineURL, tenantDomain, secret, role, worker.DefaultServiceTokenTTL)
	proc := consumers.NewXLSXProcessor(client, resource, logging.Log)

	source := "local-path"
	if filesDir := os.Getenv("APPITOOLS_FILES_DIR"); filesDir != "" {
		pool, err := db.NewPool(ctx, dsn)
		if err != nil {
			logging.Log.Fatal().Err(err).Msg("worker: open pool for VFS metadata")
		}
		vfs := files.NewLocal(filesDir, files.NewPGStore(pool))
		proc = proc.WithFileOpener(func(ctx context.Context, tenant, fileRef string) (io.ReadCloser, error) {
			rc, _, err := vfs.Get(ctx, tenant, fileRef)
			return rc, err
		})
		source = "vfs:" + filesDir
	}

	logging.Log.Info().
		Str("engine_url", engineURL).
		Str("tenant_domain", tenantDomain).
		Str("role", role).
		Str("resource", resource).
		Str("file_source", source).
		Msg("worker: xlsx consumer enabled (streaming parse + authenticated write-back)")
	return proc
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
