package migration

import (
	"context"
	"encoding/json"
	"log"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"github.com/miguelangel/appitools/pkg/schema"
	"github.com/miguelangel/appitools/pkg/tenant"
)

// Exported stream constants so callers (cmd_serve, tests) use the same values.
const (
	StreamName    = "migrations"
	ConsumerGroup = "migration-workers"
	consumerName  = "worker-1"
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
}

func NewMigrationWorker(r *redis.Client, db *pgxpool.Pool, cache *tenant.SchemaCache) *MigrationWorker {
	return &MigrationWorker{
		redis:       r,
		db:          db,
		cache:       cache,
		BlockTime:   5 * time.Second,
		BackoffBase: time.Second,
	}
}

// Run starts the consumer loop. It blocks until ctx is cancelled.
func (w *MigrationWorker) Run(ctx context.Context) {
	// Create consumer group idempotently. "0" = deliver all pending messages on restart.
	err := w.redis.XGroupCreateMkStream(ctx, StreamName, ConsumerGroup, "0").Err()
	if err != nil && !isBusyGroup(err) {
		log.Printf("migration worker: create consumer group: %v", err)
		return
	}

	log.Printf("migration worker: listening on stream %q (group %q)", StreamName, ConsumerGroup)

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		msgs, err := w.redis.XReadGroup(ctx, &redis.XReadGroupArgs{
			Group:    ConsumerGroup,
			Consumer: consumerName,
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
			log.Printf("migration worker: xreadgroup: %v", err)
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
	attempt, _ := strconv.Atoi(retryStr) // 0 = first try, 1-3 = retries

	var apiSchema schema.APISchema
	if err := json.Unmarshal([]byte(schemaStr), &apiSchema); err != nil {
		log.Printf("migration worker: unparseable schema for %q: %v — discarding", tenantID, err)
		w.ack(ctx, msg.ID)
		return
	}

	pgSchema := "tenant_" + tenantID
	migErr := ApplyTenantMigration(ctx, w.db, pgSchema, &apiSchema)

	if migErr != nil {
		log.Printf("migration worker: apply failed for %q (attempt %d/%d): %v",
			tenantID, attempt+1, maxRetries+1, migErr)

		if attempt >= maxRetries {
			log.Printf("migration worker: giving up on %q after %d attempts", tenantID, maxRetries+1)
			w.ack(ctx, msg.ID)
			w.logMigration(ctx, tenantID, "failed", migErr.Error())
			return
		}

		// Backoff before re-enqueueing: 1× / 2× / 4× BackoffBase.
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
	log.Printf("migration worker: migration ok for tenant %q", tenantID)
}

func (w *MigrationWorker) ack(ctx context.Context, msgID string) {
	if err := w.redis.XAck(ctx, StreamName, ConsumerGroup, msgID).Err(); err != nil {
		log.Printf("migration worker: xack %s: %v", msgID, err)
	}
}

// logMigration inserts a result row in migration_log. Errors are logged, not returned.
func (w *MigrationWorker) logMigration(ctx context.Context, tenantID, status, errMsg string) {
	if _, err := w.db.Exec(ctx, `
		INSERT INTO public.migration_log
		  (tenant_id, status, error, started_at, finished_at)
		VALUES ($1, $2, NULLIF($3,''), now(), now())`,
		tenantID, status, errMsg,
	); err != nil {
		log.Printf("migration worker: insert log for %q: %v", tenantID, err)
	}
}

func isBusyGroup(err error) bool {
	return err != nil && strings.Contains(err.Error(), "BUSYGROUP")
}
