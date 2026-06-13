// Package worker implements the outbox consumer (ADR-016 §Class 2): a SEPARATE
// process (cmd/appitools-worker) that drains rows the engine wrote to
// public.outbox and runs each through a Processor. It is deliberately NOT a
// goroutine inside the engine — it survives engine restarts and never touches the
// engine's request hot path (the engine only WRITES to the outbox; the worker
// only READS).
//
// Delivery model — at-least-once:
//
//	LISTEN outbox.NotifyChannel   ← wake-up hint (ephemeral; lost while down)
//	  + periodic poll fallback    ← the TABLE is the durable source of truth
//	  → SELECT … WHERE state='pending' FOR UPDATE SKIP LOCKED LIMIT N   (one tx)
//	  → Processor.Process(row) per row
//	  → UPDATE … sent_at=now(), state='sent'    (success)
//	    UPDATE … attempts+1 [, state='failed']  (failure / exhausted)
//	  → COMMIT
//
// FOR UPDATE SKIP LOCKED makes concurrent workers claim DISJOINT rows, so a row
// is processed by at most one worker at a time. A worker that dies mid-batch
// aborts its tx; the locks release, sent_at stays NULL, and another worker
// reclaims the row — hence delivery is at-least-once and Processors MUST be
// idempotent (see Processor).
package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/rs/zerolog"

	"github.com/miguelangel/appitools/pkg/outbox"
)

// Beginner is any pgx handle that can start a transaction — both *pgx.Conn and
// *pgxpool.Pool satisfy it. Drain takes a Beginner so tests can pass a pool (to
// exercise concurrent SKIP LOCKED) while the running worker passes a dedicated
// *pgx.Conn.
type Beginner interface {
	Begin(ctx context.Context) (pgx.Tx, error)
}

// Connector opens a fresh, DEDICATED Postgres connection. The worker calls it for
// the LISTEN connection and for the drain connection, and again after a drop. It
// must NOT hand out a pooled connection: a listener needs a permanent connection,
// and a pool silently breaks LISTEN by rotating the underlying conn (ADR-016 /
// the outbox investigation).
type Connector func(ctx context.Context) (*pgx.Conn, error)

// Row is one claimed outbox event handed to a Processor.
type Row struct {
	ID       int64           // public.outbox.id — also the natural idempotency key
	TenantID string          // owning tenant, read from the row (not re-derived)
	Topic    string          // e.g. "echo.test"
	Payload  json.RawMessage // the JSONB payload, copied out of pgx's buffer
	Attempts int             // delivery attempts BEFORE this one (0 on the first)
}

// Processor runs the business logic for one outbox Row.
//
// Idempotency is MANDATORY. Because delivery is at-least-once (a worker that
// crashes after the side-effect but before COMMIT releases the lock with sent_at
// still NULL, so the row is redelivered), Process may be called more than once for
// the same Row.ID. A real consumer with external side-effects must dedupe on a
// stable idempotency key — Row.ID is the natural choice: record "event <id>
// handled" in the consumer's own table (ideally in the SAME tx as a write-back
// side-effect) and no-op on the second delivery.
//
// The echo consumer is trivially idempotent: it only logs, so a redelivery just
// logs twice. Returning a non-nil error keeps the row pending (sent_at stays
// NULL, attempts incremented) for retry until maxAttempts, after which the row is
// parked in state='failed' and never retried again.
type Processor interface {
	Process(ctx context.Context, row Row) error
}

// ProcessorFunc adapts a plain function to the Processor interface.
type ProcessorFunc func(ctx context.Context, row Row) error

// Process implements Processor.
func (f ProcessorFunc) Process(ctx context.Context, row Row) error { return f(ctx, row) }

// DrainResult summarizes one Drain (one transaction / one batch).
type DrainResult struct {
	Processed int // rows marked 'sent' this batch
	Failed    int // rows that errored this batch (incl. those parked 'failed')
}

// Claimed is the number of rows locked this batch (Processed + Failed).
func (r DrainResult) Claimed() int { return r.Processed + r.Failed }

// Drain claims up to batchSize pending rows with FOR UPDATE SKIP LOCKED, runs each
// through proc, and records the outcome — all inside ONE transaction. A processing
// error does NOT abort the batch: that row's attempts is incremented (and its
// state flips to 'failed' once attempts reaches maxAttempts) while the batch's
// other rows still commit.
//
// db may be any pgx handle that can Begin a tx. For concurrent draining, call
// Drain from multiple goroutines on a *pgxpool.Pool (or on separate *pgx.Conn):
// SKIP LOCKED guarantees each row is claimed by at most one of them. log may be
// nil. Returns the per-batch tallies.
func Drain(ctx context.Context, db Beginner, proc Processor, batchSize, maxAttempts int, log *zerolog.Logger) (DrainResult, error) {
	if log == nil {
		nop := zerolog.Nop()
		log = &nop
	}
	var res DrainResult

	tx, err := db.Begin(ctx)
	if err != nil {
		return res, fmt.Errorf("worker: begin tx: %w", err)
	}
	// Rollback is a no-op after a successful Commit; on any early return it aborts
	// the tx so the locks release and the rows stay pending for another worker.
	// context.Background so cleanup still runs if ctx was the thing that cancelled.
	defer tx.Rollback(context.Background()) //nolint:errcheck

	// Claim pending rows. state='pending' matches the partial index
	// idx_outbox_pending and excludes rows already parked as 'failed' (which also
	// carry sent_at IS NULL). sent_at IS NULL is the durable "not yet delivered"
	// barrier — re-asserted for clarity even though 'pending' implies it.
	rows, err := tx.Query(ctx, `
		SELECT id, tenant_id, topic, payload, attempts
		FROM public.outbox
		WHERE state = 'pending' AND sent_at IS NULL
		ORDER BY created_at
		FOR UPDATE SKIP LOCKED
		LIMIT $1`, batchSize)
	if err != nil {
		return res, fmt.Errorf("worker: claim pending: %w", err)
	}

	// Collect the whole batch before issuing any UPDATE: pgx multiplexes a single
	// connection, so the rows cursor MUST be closed before the same tx runs the
	// per-row UPDATEs below. Copy each payload out of pgx's reusable scan buffer.
	var batch []Row
	for rows.Next() {
		var r Row
		var payload []byte
		if err := rows.Scan(&r.ID, &r.TenantID, &r.Topic, &payload, &r.Attempts); err != nil {
			rows.Close()
			return res, fmt.Errorf("worker: scan row: %w", err)
		}
		r.Payload = append(json.RawMessage(nil), payload...)
		batch = append(batch, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return res, fmt.Errorf("worker: iterate rows: %w", err)
	}

	for _, r := range batch {
		// NOTE: the echo Processor has no DB side-effect, so a returned error
		// leaves this tx healthy and the per-row UPDATE below always succeeds. A
		// write-back consumer that mutates the DB must run its writes under a
		// SAVEPOINT (or be handed the tx) so a failure can roll back JUST that row
		// without poisoning the batch — that is the documented extension point.
		if perr := proc.Process(ctx, r); perr != nil {
			// Failure: keep the row pending for retry (sent_at stays NULL); park it
			// in 'failed' once attempts reaches the cap so it is never retried again.
			state := "pending"
			if r.Attempts+1 >= maxAttempts {
				state = "failed"
			}
			if _, err := tx.Exec(ctx,
				`UPDATE public.outbox SET attempts = attempts + 1, state = $2 WHERE id = $1`,
				r.ID, state,
			); err != nil {
				return res, fmt.Errorf("worker: record failure id=%d: %w", r.ID, err)
			}
			res.Failed++
			log.Warn().
				Int64("id", r.ID).
				Str("tenant_id", r.TenantID).
				Str("topic", r.Topic).
				Int("attempt", r.Attempts+1).
				Int("max_attempts", maxAttempts).
				Str("state", state).
				Err(perr).
				Msg("worker: outbox row processing failed")
			continue
		}
		// Success: sent_at is the delivery barrier. Committing it is what makes this
		// row invisible to every future claim.
		if _, err := tx.Exec(ctx,
			`UPDATE public.outbox SET sent_at = now(), state = 'sent', attempts = attempts + 1 WHERE id = $1`,
			r.ID,
		); err != nil {
			return res, fmt.Errorf("worker: mark sent id=%d: %w", r.ID, err)
		}
		res.Processed++
	}

	if err := tx.Commit(ctx); err != nil {
		return res, fmt.Errorf("worker: commit batch: %w", err)
	}
	return res, nil
}

// Config tunes the worker. Zero values fall back to the defaults applied by New.
type Config struct {
	Channel      string        // LISTEN channel; default outbox.NotifyChannel
	BatchSize    int           // rows claimed per Drain; default 50
	MaxAttempts  int           // attempts before a row is parked 'failed'; default 5
	PollInterval time.Duration // fallback poll cadence; default 5s
	BackoffMin   time.Duration // reconnect backoff floor; default 2s
	BackoffMax   time.Duration // reconnect backoff ceiling; default 30s
}

// Worker consumes public.outbox via LISTEN/NOTIFY with a polling fallback.
type Worker struct {
	connect Connector
	proc    Processor
	cfg     Config
	log     zerolog.Logger
}

// New builds a Worker, applying defaults for any zero Config field.
func New(connect Connector, proc Processor, cfg Config, log zerolog.Logger) *Worker {
	if cfg.Channel == "" {
		cfg.Channel = outbox.NotifyChannel
	}
	if cfg.BatchSize <= 0 {
		cfg.BatchSize = 50
	}
	if cfg.MaxAttempts <= 0 {
		cfg.MaxAttempts = 5
	}
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = 5 * time.Second
	}
	if cfg.BackoffMin <= 0 {
		cfg.BackoffMin = 2 * time.Second
	}
	if cfg.BackoffMax <= 0 {
		cfg.BackoffMax = 30 * time.Second
	}
	return &Worker{connect: connect, proc: proc, cfg: cfg, log: log}
}

// Run drives the consume loop until ctx is cancelled, reconnecting with capped
// exponential backoff whenever the connection drops. It returns ctx.Err() on a
// clean shutdown.
func (w *Worker) Run(ctx context.Context) error {
	backoff := w.cfg.BackoffMin
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		// onReady resets the backoff once a connection is fully established, so a
		// long-lived worker that drops after hours reconnects from the floor again.
		err := w.runListener(ctx, func() { backoff = w.cfg.BackoffMin })
		if ctx.Err() != nil {
			return ctx.Err()
		}
		// Any non-shutdown error from runListener — a dropped connection, a failed
		// LISTEN, or a drain/query error — funnels here. The real cause is in Err();
		// the worker re-establishes both connections and re-drains the backlog.
		w.log.Warn().Err(err).Dur("backoff", backoff).Msg("worker: consume loop error; reconnecting")
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(backoff):
		}
		backoff = nextBackoff(backoff, w.cfg.BackoffMax)
	}
}

// nextBackoff doubles cur, capped at max.
func nextBackoff(cur, max time.Duration) time.Duration {
	next := cur * 2
	if next > max {
		return max
	}
	return next
}

// runListener establishes the dedicated LISTEN + drain connections, drains the
// backlog, then loops: drain on every notification and on every poll tick. It
// returns nil only on ctx cancellation; any other return is a connection error
// that Run turns into a reconnect. onReady is called once LISTEN is live so Run
// can reset its backoff.
func (w *Worker) runListener(ctx context.Context, onReady func()) error {
	// Dedicated LISTEN connection (never pooled — see Connector). Ownership passes
	// to the reader goroutine below, which closes it; until that goroutine starts
	// we close it ourselves on the error paths.
	listenConn, err := w.connect(ctx)
	if err != nil {
		return fmt.Errorf("worker: connect listen conn: %w", err)
	}
	if _, err := listenConn.Exec(ctx, "LISTEN "+pgx.Identifier{w.cfg.Channel}.Sanitize()); err != nil {
		listenConn.Close(context.Background())
		return fmt.Errorf("worker: LISTEN %s: %w", w.cfg.Channel, err)
	}

	// Separate connection for draining: WaitForNotification holds the listen conn
	// for the goroutine's lifetime, and pgx forbids concurrent use of one conn, so
	// the SELECT … FOR UPDATE batches must run on their own connection.
	drainConn, err := w.connect(ctx)
	if err != nil {
		listenConn.Close(context.Background())
		return fmt.Errorf("worker: connect drain conn: %w", err)
	}
	defer drainConn.Close(context.Background())

	onReady()
	w.log.Info().
		Str("channel", w.cfg.Channel).
		Int("batch_size", w.cfg.BatchSize).
		Dur("poll_interval", w.cfg.PollInterval).
		Msg("worker: listening on outbox")

	// wake collapses a burst of notifications into a single "drain again" signal.
	wake := make(chan struct{}, 1)
	signal := func() {
		select {
		case wake <- struct{}{}:
		default:
		}
	}

	// Reader goroutine OWNS listenConn: it blocks on WaitForNotification using a
	// child ctx and is the only thing that touches (and closes) the conn, honoring
	// pgx's one-goroutine-per-conn rule. It sends EXACTLY ONE value to listenErr,
	// then closes listenConn and listenDone, and never touches the conn again.
	listenCtx, stopListen := context.WithCancel(ctx)
	listenErr := make(chan error, 1)
	listenDone := make(chan struct{})
	go func() {
		defer close(listenDone)
		defer listenConn.Close(context.Background())
		for {
			if _, err := listenConn.WaitForNotification(listenCtx); err != nil {
				listenErr <- err
				return
			}
			signal()
		}
	}()
	// On EVERY return path: stop the reader and join it before returning, so the
	// goroutine's Close(listenConn) has happened and there is no dangling reader.
	defer func() {
		stopListen()
		<-listenDone
	}()

	ticker := time.NewTicker(w.cfg.PollInterval)
	defer ticker.Stop()

	// Drain the backlog first: rows enqueued while the worker was down emitted
	// NOTIFYs that are now lost, so the table — not the channel — drives recovery.
	signal()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case err := <-listenErr:
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return fmt.Errorf("worker: wait notification: %w", err)
		case <-ticker.C:
			signal()
		case <-wake:
			if err := w.drainAll(ctx, drainConn); err != nil {
				if ctx.Err() != nil {
					return ctx.Err()
				}
				return err
			}
		}
	}
}

// drainAll repeatedly Drains until a batch comes back not-full — i.e. the queue is
// empty (or the remainder is locked by peer workers) — so a single wake-up clears
// a whole backlog rather than one batch per notification.
func (w *Worker) drainAll(ctx context.Context, db Beginner) error {
	for {
		res, err := Drain(ctx, db, w.proc, w.cfg.BatchSize, w.cfg.MaxAttempts, &w.log)
		if err != nil {
			return err
		}
		if res.Claimed() > 0 {
			w.log.Debug().
				Int("processed", res.Processed).
				Int("failed", res.Failed).
				Msg("worker: drained batch")
		}
		// Stop when the batch wasn't full (nothing more to claim right now) or when
		// nothing succeeded — the latter avoids spinning on a batch of only-failing
		// rows; they are retried on the next poll tick, giving transients time to
		// heal. (A future next_attempt_at/backoff column would let just-failed rows
		// be skipped per-row instead of pausing the whole drain.)
		if res.Claimed() < w.cfg.BatchSize || res.Processed == 0 {
			return nil
		}
	}
}
