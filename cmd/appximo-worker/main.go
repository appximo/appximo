// Command appximo-worker is the outbox consumer AND the workflow executor
// (ADR-016 §Class 2 + ADR-031): a SEPARATE process that drains public.outbox,
// runs each event through its consumer, and drives the schema's `workflows`
// (event triggers as outbox consumers; cron triggers on a leader-elected
// scheduler — pg_try_advisory_lock, no extra infrastructure). It connects to the
// SAME Postgres as the engine (DATABASE_URL) with DEDICATED connections (never
// the engine's pool), LISTENs on outbox.NotifyChannel as a wake-up hint, and
// polls the table — the durable source of truth — as a fallback.
//
// THE CLAIM IS TOPIC-SCOPED (AUTO-1, AUTOMATIZACION-S1): every consumer declares
// the topics it owns, and the worker never claims — never acknowledges, never
// destroys — a row outside that set. A pending row nobody owns stays pending,
// where the engine's outbox observability names it (oldest-pending-age gauge,
// /admin/outbox, the alerter) and this process warns about it once a minute.
// The old default (echo: ack EVERYTHING) silently destroyed business events
// with a success face; nobody in the industry marks a job done because no
// handler exists.
//
// Modes (APPXIMO_WORKER_MODE):
//
//	auto      (default) the shipped, generic worker: executes the tenants'
//	          declared `workflows` (event + cron) and — when SMTP_HOST is set —
//	          delivers `email.send` (auth reset/verification mail). Consumes
//	          NOTHING else: app-specific topics need an app consumer (a Router
//	          in a consumer binary; `appximo backend-spec` explains).
//	echo      DEV loopback: acks ONLY echo.* topics, loudly refuses the rest.
//	writeback SERVICE-JWT-V1 demo (PATCHes created rows' status).
//	xlsx      the FileJob consumer (one resource's .created events).
//	email     the transactional email consumer alone.
package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/rs/zerolog"

	"github.com/appximo/appximo/pkg/consumers"
	"github.com/appximo/appximo/pkg/db"
	"github.com/appximo/appximo/pkg/dotenv"
	"github.com/appximo/appximo/pkg/extensions"
	"github.com/appximo/appximo/pkg/files"
	"github.com/appximo/appximo/pkg/logging"
	"github.com/appximo/appximo/pkg/outbox"
	"github.com/appximo/appximo/pkg/telegram"
	"github.com/appximo/appximo/pkg/worker"
	"github.com/appximo/appximo/pkg/workflows"
)

// version / revision are stamped at build time by scripts/build-worker.sh via
// -ldflags -X (release tag or short SHA; "dev"/"unknown" on a plain local build),
// mirroring the engine so a deployed worker is traceable to its build.
var (
	version  = "dev"
	revision = "unknown"
)

func main() {
	// --version: what fleet-audit.sh and a deploy verification ask.
	if len(os.Args) > 1 && (os.Args[1] == "--version" || os.Args[1] == "version") {
		fmt.Printf("appximo-worker %s (%s)\n", version, revision)
		return
	}

	// Same .env contract as the engine (F1): the real environment wins, .env
	// fills gaps — so the systemd unit's EnvironmentFile and a bare terminal run
	// see the same config.
	dotenvLoaded := dotenv.Load()
	logging.Init(os.Getenv("APPXIMO_ENV"))
	log := logging.Log
	if dotenvLoaded > 0 {
		log.Info().Int("vars", dotenvLoaded).Msg("worker: loaded .env from the working directory (environment wins over it)")
	}

	// ── configuration: strict, fail-fast, defaults reported (AUTO-2) ──
	env := newEnvSet()
	dsn := env.Require("DATABASE_URL", "the Postgres the engine writes the outbox to")
	mode := env.Enum("APPXIMO_WORKER_MODE", "auto", "auto", "echo", "writeback", "xlsx", "email")
	if os.Getenv("APPXIMO_WORKER_MODE") == "" && env.Opt("APPXIMO_WORKER_WRITEBACK") == "on" {
		mode = "writeback" // legacy flag, still honored
	}
	batch := env.Int("APPXIMO_WORKER_BATCH", 50, 1)
	maxAttempts := env.Int("APPXIMO_WORKER_MAX_ATTEMPTS", 5, 1)
	poll := env.Dur("APPXIMO_WORKER_POLL", 5*time.Second)
	refresh := env.Dur("APPXIMO_WORKER_SCHEMA_REFRESH", time.Minute)

	// Engine-client settings (used by auto/writeback/xlsx; harmless otherwise).
	engineURL := env.Str("APPXIMO_ENGINE_URL", "http://localhost:8080")
	tenantDomain := env.Str("APPXIMO_TENANT_DOMAIN", "localhost")
	role := env.Str("APPXIMO_WORKER_ROLE", "service_worker")
	resource := env.Str("APPXIMO_WORKER_RESOURCE", "filejobs")
	filesDir := env.Opt("APPXIMO_FILES_DIR")
	emailTopic := env.Str("APPXIMO_EMAIL_TOPIC", consumers.DefaultEmailTopic)
	smtpHost := env.Opt("SMTP_HOST")
	smtpPort := env.Str("SMTP_PORT", "587")
	smtpUser := env.Opt("SMTP_USER")
	smtpPass := env.Opt("SMTP_PASS")
	smtpFrom := env.Opt("SMTP_FROM")
	jwtSecret := env.Opt("JWT_SECRET")
	// Scheduled Telegram digest (VOZ-ESCALON1-S1): a cron workflow enqueues
	// tgSummaryTopic; when the bot is configured here, auto mode drains it and
	// sends the digest AS tgSummaryRole. These are APPXIMO_TELEGRAM_* (not
	// APPXIMO_WORKER_*), so they are outside the strict-key sweep below.
	tgToken := env.Opt("APPXIMO_TELEGRAM_BOT_TOKEN")
	tgChat := env.Opt("APPXIMO_TELEGRAM_CHAT_ID")
	tgSummaryRole := env.Opt("APPXIMO_TELEGRAM_SUMMARY_ROLE")
	// The identity the digest is computed AS (APP-AGENDA-S2): a personal app
	// scopes rows by the owner's id — the same key the engine's command
	// channel reads, so the chat and the morning digest see the same rows.
	tgSummaryUser := env.Opt("APPXIMO_TELEGRAM_SUMMARY_USER_ID")
	tgSummaryTopic := env.Str("APPXIMO_TELEGRAM_SUMMARY_TOPIC", "summary.telegram")

	needsEngine := mode == "auto" || mode == "writeback" || mode == "xlsx"
	if needsEngine && jwtSecret == "" {
		env.invalid = append(env.invalid, fmt.Sprintf("JWT_SECRET is required for mode %q (it signs the scoped service JWT the worker uses on the engine API)", mode))
	}
	if (mode == "email" || (mode == "auto" && smtpHost != "")) && smtpFrom == "" && smtpHost != "" {
		env.invalid = append(env.invalid, "SMTP_FROM is required when SMTP_HOST is set (the sender identity)")
	}
	if mode == "email" && smtpHost == "" {
		env.invalid = append(env.invalid, `APPXIMO_WORKER_MODE=email requires SMTP_HOST (and SMTP_PORT/SMTP_FROM)`)
	}
	env.CheckUnknown("APPXIMO_WORKER_")
	env.FailFast(log)
	env.Report(log)

	// Cancelled on SIGINT/SIGTERM → the worker finishes its current batch, joins
	// the listener goroutine, closes its connections, and Run returns cleanly.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// DEDICATED connection factory: pgx.Connect, NOT the engine pool. A LISTEN
	// connection must be permanent, and a pool would rotate it out from under the
	// listener (breaking LISTEN silently). Also used for the scheduler's
	// leadership connection (the advisory lock IS the session).
	connect := func(ctx context.Context) (*pgx.Conn, error) {
		return pgx.Connect(ctx, dsn)
	}

	clients := newClientCache(engineURL, tenantDomain, jwtSecret, role)

	var proc worker.Processor
	switch mode {
	case "auto":
		proc = buildAuto(ctx, dsn, connect, clients, refresh, smtpHost, smtpPort, smtpUser, smtpPass, smtpFrom, emailTopic, tgToken, tgChat, tgSummaryRole, tgSummaryUser, tgSummaryTopic, log)
	case "writeback":
		log.Info().Str("engine_url", engineURL).Str("tenant_domain", tenantDomain).Str("role", role).
			Msg("worker: write-back demo enabled (authenticated PATCH via engine API; owns *.created)")
		proc = worker.NewWritebackProcessor(clients.raw(role), "done", log)
	case "xlsx":
		proc = newXLSXProcessor(ctx, dsn, clients.raw(role), resource, filesDir, log)
	case "email":
		proc = newEmailProcessor(smtpHost, smtpPort, smtpUser, smtpPass, smtpFrom, emailTopic, log)
	case "echo":
		log.Warn().Msg("worker: ECHO mode is a dev loopback — it acknowledges ONLY echo.* topics; any business event stays pending (and this worker names it once a minute). It never marks foreign topics sent (AUTO-1).")
		proc = echoProcessor{log: log}
	}

	w := worker.New(connect, proc, worker.Config{
		BatchSize:    batch,
		MaxAttempts:  maxAttempts,
		PollInterval: poll,
	}, log)

	if owner, ok := proc.(worker.TopicOwner); ok {
		set := owner.Topics()
		log.Info().Str("mode", mode).
			Str("topics_exact", strings.Join(set.Exact, ",")).
			Str("topics_prefixes", strings.Join(set.Prefixes, ",")).
			Str("topics_suffixes", strings.Join(set.Suffixes, ",")).
			Msg("worker: claim is SCOPED to these topics (dynamic sets refresh with the tenant schemas); everything else stays pending and visible")
	}

	log.Info().Str("version", version).Str("revision", revision).Str("mode", mode).Msg("appximo-worker starting")
	if err := w.Run(ctx); err != nil && ctx.Err() == nil {
		log.Fatal().Err(err).Msg("appximo-worker exited with error")
	}
	log.Info().Msg("appximo-worker stopped")
}

// buildAuto assembles the shipped generic worker: the workflow executor (event
// consumers + leader-elected cron scheduler) plus, when SMTP is configured, the
// email consumer — everything topic-scoped through one Router.
func buildAuto(ctx context.Context, dsn string, connect worker.Connector, clients *clientCache, refresh time.Duration, smtpHost, smtpPort, smtpUser, smtpPass, smtpFrom, emailTopic, tgToken, tgChat, tgSummaryRole, tgSummaryUser, tgSummaryTopic string, log zerolog.Logger) worker.Processor {
	pool, err := db.NewPool(ctx, dsn)
	if err != nil {
		log.Fatal().Err(err).Msg("worker: open pool (workflows store + schema source)")
	}
	if err := outbox.EnsureTable(ctx, pool); err != nil {
		log.Fatal().Err(err).Msg("worker: ensure outbox table")
	}
	if err := workflows.EnsureTables(ctx, pool); err != nil {
		log.Fatal().Err(err).Msg("worker: ensure workflow tables")
	}

	src := workflows.NewSource(pool, refresh, log)
	store := workflows.NewStore(pool)
	exec := &workflows.Executor{
		Clients:    clients.factory(),
		Store:      store,
		Dispatcher: extensions.NewWebhookDispatcher(),
		Log:        log,
	}
	go src.Run(ctx)

	scheduler := &workflows.Scheduler{
		Connect: connect,
		Src:     src,
		Exec:    exec,
		Store:   store,
		Log:     log,
	}
	// Per-row reminders (MOTOR-AGENDA-S1): the "time" trigger is swept by the
	// same leader tick, so exactly one worker asks and claims.
	sweeper := &workflows.Sweeper{Src: src, Exec: exec, Store: store}
	scheduler.Sweep = sweeper.Sweep
	go scheduler.Run(ctx)

	router := consumers.NewRouter(log)
	router.HandleOwner("workflows(dynamic)", &workflows.EventConsumer{Src: src, Exec: exec, Log: log})
	if smtpHost != "" {
		router.Handle(emailTopic, newEmailProcessor(smtpHost, smtpPort, smtpUser, smtpPass, smtpFrom, emailTopic, log))
	}
	// Scheduled Telegram digest consumer (VOZ-ESCALON1-S1): only when the bot is
	// configured AND a role is named. A cron workflow enqueues tgSummaryTopic;
	// this drains it, fetches GET /api/summary as tgSummaryRole, and sends it.
	summaryEnabled := tgToken != "" && tgChat != "" && tgSummaryRole != ""
	if summaryEnabled {
		tgClient, terr := telegram.New(tgToken, tgChat)
		if terr != nil {
			log.Fatal().Err(terr).Msg("worker: APPXIMO_TELEGRAM_* set for the scheduled digest but invalid")
		}
		router.HandleOwner("summary.telegram", consumers.NewSummaryProcessor(clients.rawAs(tgSummaryRole, tgSummaryUser), tgClient, tgSummaryTopic, log))
		// message.telegram (MOTOR-AGENDA-S1): a workflow's `enqueue` with a
		// `text` — what a per-row reminder says. Same bot, same chat.
		router.HandleOwner("message.telegram", consumers.NewMessageProcessor(tgClient, consumers.MessageTopic, log))
	} else if tgToken != "" || tgChat != "" || tgSummaryRole != "" {
		log.Warn().Msg("worker: the scheduled Telegram digest needs APPXIMO_TELEGRAM_BOT_TOKEN + APPXIMO_TELEGRAM_CHAT_ID + APPXIMO_TELEGRAM_SUMMARY_ROLE together — it is DISABLED until all three are set (a cron workflow enqueuing summary.telegram would then stay pending and visible)")
	}
	log.Info().Bool("email", smtpHost != "").Bool("telegram_digest", summaryEnabled).
		Msg("worker: AUTO mode — workflow executor (event + leader-elected cron) enabled; topics follow the tenants' deployed schemas")
	return router
}

// clientCache builds one EngineClient per role, lazily.
type clientCache struct {
	engineURL, domain, secret, defaultRole string

	mu    sync.Mutex
	cache map[string]*worker.EngineClient
}

func newClientCache(engineURL, domain, secret, defaultRole string) *clientCache {
	return &clientCache{engineURL: engineURL, domain: domain, secret: secret, defaultRole: defaultRole, cache: map[string]*worker.EngineClient{}}
}

func (c *clientCache) raw(role string) *worker.EngineClient {
	if role == "" {
		role = c.defaultRole
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	cl, ok := c.cache[role]
	if !ok {
		cl = worker.NewEngineClient(c.engineURL, c.domain, c.secret, role, worker.DefaultServiceTokenTTL)
		c.cache[role] = cl
	}
	return cl
}

// rawAs is raw acting AS userID (empty = the service identity). Not cached:
// one consumer, one client.
func (c *clientCache) rawAs(role, userID string) *worker.EngineClient {
	if userID == "" {
		return c.raw(role)
	}
	if role == "" {
		role = c.defaultRole
	}
	return worker.NewEngineClientAs(c.engineURL, c.domain, c.secret, role, userID, worker.DefaultServiceTokenTTL)
}

func (c *clientCache) factory() workflows.ClientFactory {
	return func(role string) workflows.EngineDoer { return c.raw(role) }
}

// echoProcessor is the DEV loopback: it acks echo.* topics (logging them) and
// owns nothing else. Before AUTOMATIZACION-S1 it was the DEFAULT and acked
// EVERY topic — the trap that would have destroyed 34 real factura.emitir rows.
type echoProcessor struct {
	log zerolog.Logger
}

// Topics implements worker.TopicOwner: echo owns exactly the echo.* namespace.
func (p echoProcessor) Topics() worker.TopicSet {
	return worker.TopicSet{Prefixes: []string{"echo."}}
}

// Process implements worker.Processor for echo.* topics.
func (p echoProcessor) Process(_ context.Context, row worker.Row) error {
	if !strings.HasPrefix(row.Topic, "echo.") {
		return fmt.Errorf("echo consumer owns only echo.* topics, got %q — refusing to acknowledge", row.Topic)
	}
	p.log.Info().
		Int64("id", row.ID).
		Str("tenant_id", row.TenantID).
		Str("topic", row.Topic).
		RawJSON("payload", row.Payload).
		Msg("worker: processed outbox event")
	return nil
}

// newXLSXProcessor builds the XLSX-CONSUMER-V1 real consumer (FileJob pattern).
// File source (FILES-V1): when APPXIMO_FILES_DIR is set, file_ref is a VFS
// file_id and the consumer streams the content-addressed blob via VFS.Get.
func newXLSXProcessor(ctx context.Context, dsn string, client *worker.EngineClient, resource, filesDir string, log zerolog.Logger) worker.Processor {
	proc := consumers.NewXLSXProcessor(client, resource, log)
	source := "local-path"
	if filesDir != "" {
		pool, err := db.NewPool(ctx, dsn)
		if err != nil {
			log.Fatal().Err(err).Msg("worker: open pool for VFS metadata")
		}
		vfs := files.NewLocal(filesDir, files.NewPGStore(pool))
		proc = proc.WithFileOpener(func(ctx context.Context, tenant, fileRef string) (io.ReadCloser, error) {
			rc, _, err := vfs.Get(ctx, tenant, fileRef)
			return rc, err
		})
		source = "vfs:" + filesDir
	}
	log.Info().Str("resource", resource).Str("file_source", source).
		Msg("worker: xlsx consumer enabled (streaming parse + authenticated write-back)")
	return proc
}

// newEmailProcessor builds the EMAIL-CONSUMER-V1 transactional-email consumer:
// STARTTLS + AUTH PLAIN via an external provider (Brevo, Resend, Mailgun, SES…).
func newEmailProcessor(host, port, user, pass, from, topic string, log zerolog.Logger) worker.Processor {
	sender, err := consumers.NewSMTPSender(consumers.SMTPConfig{
		Host: host, Port: port, Username: user, Password: pass, From: from,
	})
	if err != nil {
		log.Fatal().Err(err).Msg("worker: email consumer requires SMTP_HOST, SMTP_PORT and SMTP_FROM")
	}
	log.Info().Str("smtp_host", host).Str("smtp_port", port).Str("from", from).
		Bool("auth", user != "").Str("topic", topic).
		Msg("worker: email consumer enabled (external SMTP, templated)")
	return consumers.NewEmailProcessor(sender, log).WithTopic(topic)
}
