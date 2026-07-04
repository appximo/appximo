package migration

import (
	"context"
	"encoding/json"
	"hash/fnv"
	"log"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/miguelangel/appitools/pkg/schema"
	"github.com/miguelangel/appitools/pkg/tenant"
	"github.com/redis/go-redis/v9"
)

// Exported stream constants so callers (cmd_serve, tests) use the same values.
const (
	StreamName    = "migrations"
	ConsumerGroup = "migration-workers"
	maxRetries    = 3 // total additional attempts after the first try
)

// MigrationWorker consumes jobs from the Redis Stream and applies tenant migrations.
type MigrationWorker struct {
	redis *redis.Client
	db    *pgxpool.Pool
	cache *tenant.SchemaCache

	// BlockTime is how long XReadGroup waits for new messages before looping.
	// Default: 5s. Set to a smaller value in tests.
	BlockTime time.Duration

	// BackoffBase is the unit delay before re-enqueueing a failed job.
	// Delays: BackoffBase * 1, BackoffBase * 2, BackoffBase * 4 (exponential).
	// Default: 1s. Set to a smaller value in tests.
	BackoffBase time.Duration

	// LockRetryDelay is how long to wait before re-enqueueing a job whose tenant
	// advisory lock is held by another worker. Default: 2s.
	LockRetryDelay time.Duration

	// ConsumerName identifies this worker in the Redis consumer group.
	// Must be unique per running worker instance. Default: "worker-1".
	ConsumerName string
}

func NewMigrationWorker(r *redis.Client, db *pgxpool.Pool, cache *tenant.SchemaCache) *MigrationWorker {
	return &MigrationWorker{
		redis:          r,
		db:             db,
		cache:          cache,
		BlockTime:      5 * time.Second,
		BackoffBase:    time.Second,
		LockRetryDelay: 2 * time.Second,
		ConsumerName:   "worker-1",
	}
}

// Run starts the consumer loop. It blocks until ctx is cancelled.
func (w *MigrationWorker) Run(ctx context.Context) {
	// Create consumer group idempotently. "0" = deliver all pending messages on restart.
	err := w.redis.XGroupCreateMkStream(ctx, StreamName, ConsumerGroup, "0").Err()
	if err != nil && !isBusyGroup(err) {
		log.Printf("migration worker [%s]: create consumer group: %v", w.ConsumerName, err)
		return
	}

	log.Printf("migration worker [%s]: listening on stream %q", w.ConsumerName, StreamName)

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		msgs, err := w.redis.XReadGroup(ctx, &redis.XReadGroupArgs{
			Group:    ConsumerGroup,
			Consumer: w.ConsumerName,
			Streams:  []string{StreamName, ">"},
			Count:    10,
			Block:    w.BlockTime,
		}).Result()

		if err == redis.Nil {
			continue // block timeout — no new messages, loop again
		}
		if err != nil {
			if ctx.Err() != nil {
				return // clean shutdown
			}
			log.Printf("migration worker [%s]: xreadgroup: %v", w.ConsumerName, err)
			time.Sleep(time.Second)
			continue
		}

		for _, stream := range msgs {
			for _, msg := range stream.Messages {
				w.process(ctx, msg)
			}
		}
	}
}

func (w *MigrationWorker) process(ctx context.Context, msg redis.XMessage) {
	tenantID, _ := msg.Values["tenant_id"].(string)
	schemaStr, _ := msg.Values["schema"].(string)
	retryStr, _ := msg.Values["retry_count"].(string)
	attempt, _ := strconv.Atoi(retryStr)

	// Parse schema first — discard immediately if unparseable (no point locking).
	var apiSchema schema.APISchema
	if err := json.Unmarshal([]byte(schemaStr), &apiSchema); err != nil {
		log.Printf("migration worker [%s]: unparseable schema for %q: %v — discarding",
			w.ConsumerName, tenantID, err)
		w.ack(ctx, msg.ID)
		return
	}

	pgSchema := "tenant_" + tenantID
	lockKey := tenantLockKey(pgSchema)

	// Acquire a dedicated connection for the advisory lock.
	// Session-level locks are tied to a specific connection — we must hold this
	// connection until the migration finishes, then unlock before releasing it.
	lockConn, err := w.db.Acquire(ctx)
	if err != nil {
		log.Printf("migration worker [%s]: acquire lock conn for %q: %v",
			w.ConsumerName, tenantID, err)
		return // leave in PEL — will be re-delivered on next XReadGroup
	}

	var locked bool
	if err := lockConn.QueryRow(ctx,
		"SELECT pg_try_advisory_lock($1)", lockKey,
	).Scan(&locked); err != nil {
		log.Printf("migration worker [%s]: advisory lock query for %q: %v",
			w.ConsumerName, tenantID, err)
		lockConn.Release()
		return
	}

	if !locked {
		// Another worker holds the lock for this tenant — re-enqueue and yield.
		lockConn.Release()
		log.Printf("migration worker [%s]: tenant %q locked by peer, re-enqueueing in %s",
			w.ConsumerName, tenantID, w.LockRetryDelay)
		select {
		case <-ctx.Done():
			w.ack(ctx, msg.ID)
			return
		case <-time.After(w.LockRetryDelay):
		}
		w.redis.XAdd(ctx, &redis.XAddArgs{ //nolint:errcheck
			Stream: StreamName,
			Values: map[string]any{
				"tenant_id":   tenantID,
				"schema":      schemaStr,
				"retry_count": strconv.Itoa(attempt), // NOT incremented — not an error retry
			},
		})
		w.ack(ctx, msg.ID)
		return
	}

	// Advisory lock acquired — release it (and the connection) on exit.
	defer func() {
		unlockCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, err := lockConn.Exec(unlockCtx, "SELECT pg_advisory_unlock($1)", lockKey); err != nil {
			log.Printf("migration worker [%s]: advisory unlock for %q: %v",
				w.ConsumerName, tenantID, err)
		}
		lockConn.Release()
	}()

	// Run migration (uses a separate connection from the pool).
	migErr := ApplyTenantMigration(ctx, w.db, pgSchema, &apiSchema)

	if migErr != nil {
		log.Printf("migration worker [%s]: apply failed for %q (attempt %d/%d): %v",
			w.ConsumerName, tenantID, attempt+1, maxRetries+1, migErr)

		if attempt >= maxRetries {
			log.Printf("migration worker [%s]: giving up on %q after %d attempts",
				w.ConsumerName, tenantID, maxRetries+1)
			w.ack(ctx, msg.ID)
			w.logMigration(ctx, tenantID, "failed", migErr.Error())
			return
		}

		delay := time.Duration(1<<uint(attempt)) * w.BackoffBase
		select {
		case <-ctx.Done():
			w.ack(ctx, msg.ID)
			return
		case <-time.After(delay):
		}
		w.redis.XAdd(ctx, &redis.XAddArgs{ //nolint:errcheck
			Stream: StreamName,
			Values: map[string]any{
				"tenant_id":   tenantID,
				"schema":      schemaStr,
				"retry_count": strconv.Itoa(attempt + 1),
			},
		})
		w.ack(ctx, msg.ID)
		return
	}

	w.ack(ctx, msg.ID)
	w.logMigration(ctx, tenantID, "ok", "")
	if w.cache != nil {
		w.cache.Invalidate(tenantID)
	}
	log.Printf("migration worker [%s]: migration ok for tenant %q", w.ConsumerName, tenantID)
}

func (w *MigrationWorker) ack(ctx context.Context, msgID string) {
	if err := w.redis.XAck(ctx, StreamName, ConsumerGroup, msgID).Err(); err != nil {
		log.Printf("migration worker [%s]: xack %s: %v", w.ConsumerName, msgID, err)
	}
}

func (w *MigrationWorker) logMigration(ctx context.Context, tenantID, status, errMsg string) {
	if _, err := w.db.Exec(ctx, `
		INSERT INTO public.migration_log
		  (tenant_id, status, error, started_at, finished_at)
		VALUES ($1, $2, NULLIF($3,''), now(), now())`,
		tenantID, status, errMsg,
	); err != nil {
		log.Printf("migration worker [%s]: insert log for %q: %v", w.ConsumerName, tenantID, err)
	}
}

// tenantLockKey returns a stable int64 advisory-lock key for the given pg schema name.
// Uses FNV-64a so the mapping is deterministic across processes and restarts.
func tenantLockKey(pgSchema string) int64 {
	h := fnv.New64a()
	h.Write([]byte(pgSchema))
	return int64(h.Sum64()) // wrapping uint64→int64 is safe; PG accepts negative int64
}

func isBusyGroup(err error) bool {
	return err != nil && strings.Contains(err.Error(), "BUSYGROUP")
}
