package db

import (
	"context"
	"fmt"
	"github.com/appximo/appximo/pkg/observability"
	"log"
	"os"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func NewPool(ctx context.Context, connStr string) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(connStr)
	if err != nil {
		return nil, fmt.Errorf("parse connection string: %w", err)
	}

	// 2vCPU + colocated Postgres: fórmula cores*2+1=5, margen conservador → 10.
	// A bigger pool just adds idle connections (RAM + PG backends) without
	// throughput once the cores are saturated; raise DB_MAX_CONNS only when the
	// DB lives on a separate, larger box.
	maxConns := int32(10)
	if v := os.Getenv("DB_MAX_CONNS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			maxConns = int32(n)
		}
	}
	minConns := int32(max(2, int(maxConns)/2))
	cfg.MaxConns = maxConns
	cfg.MinConns = minConns
	cfg.MaxConnLifetime = time.Hour
	cfg.HealthCheckPeriod = 30 * time.Second

	// DescribeExec instead of the default CacheStatement: the schema is DYNAMIC (a
	// control-plane deploy runs ALTER TABLE ADD COLUMN under live traffic). A cached
	// prepared plan for "SELECT *" is invalidated by the DDL, so the first query
	// after a change fails with "cached plan must not change result type".
	// DescribeExec describes every statement fresh against the server (so it never
	// holds a stale plan AND gets the correct parameter type OIDs — e.g. json for
	// public.tenants.json_schema, which QueryExecModeExec would mis-encode as bytea).
	// pgx pipelines describe+execute, so the overhead is minimal and only hits
	// cache-miss queries (the response cache absorbs most reads).
	cfg.ConnConfig.DefaultQueryExecMode = pgx.QueryExecModeDescribeExec
	// The driver-level tracer notes the exact statement a query FAILED with on
	// the request's trace — for every route, generated or custom — so a 5xx
	// names the query the driver rejected (OBSERVABILIDAD-ERRORES-S1). It stores
	// nothing on success and never records bound values.
	cfg.ConnConfig.Tracer = observability.QueryTracer{}
	log.Printf("db pool: MAX_CONNS=%d MIN_CONNS=%d", maxConns, minConns)

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("create pool: %w", err)
	}

	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping database: %w", err)
	}

	return pool, nil
}
