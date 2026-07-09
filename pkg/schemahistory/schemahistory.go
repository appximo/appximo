// Package schemahistory is the append-only version history of tenant schemas
// (VERSION-S1) — the base of the productive trust layer. STATE-AUDIT-V1 §2
// established that a deploy OVERWROTE public.tenants.json_schema (the previous
// version was gone); this package records every schema a tenant has run, so the
// history is visible and "roll back to version N" is a re-deploy of a stored
// schema through the EXISTING migration machinery (schemadiff is direction-
// agnostic — the audit's §3 finding).
//
// Invariants:
//   - APPEND-ONLY: a rollback is a NEW version whose content equals an old one
//     ("rollback to v3" creates v6) — the trace is never rewritten.
//   - One row per DISTINCT schema state: appending a schema whose canonical
//     hash equals the LATEST version's is a no-op (re-deploying an unchanged
//     schema — e.g. the resumable fan-out re-run — does not spam the history).
//   - The history mirrors public.tenants.json_schema: every site that persists
//     json_schema appends here in the same flow (register / deploy / rollback /
//     fan-out), so the LATEST version is always the tenant's current schema.
//
// It lives in its own package (not pkg/controlplane) because the fan-out
// orchestrator (pkg/migration) must also append, and controlplane already
// imports migration — this breaks the would-be cycle.
package schemahistory

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Sources — who appended a version. Free-text in the table; these are the
// engine's canonical values.
const (
	SourceRegister = "register" // tenant registration (the first version)
	SourceDeploy   = "deploy"   // control-plane / editor deploy
	SourceRollback = "rollback" // rollback to a prior version (append-only: new version, old content)
	SourceFanout   = "fanout"   // migrate --all-tenants fan-out
	SourceBackfill = "backfill" // pre-versioning schema captured at first boot with this feature
)

// Version is one recorded schema version. SchemaJSON is populated by Get, not
// by List (the listing stays light).
type Version struct {
	Version    int       `json:"version"`
	Hash       string    `json:"hash"` // sha256 hex of the canonical (engine-marshaled) schema JSON
	Source     string    `json:"source"`
	Note       string    `json:"note,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
	Resources  []string  `json:"resources"` // resource names in this version (the timeline summary)
	SchemaJSON []byte    `json:"-"`
}

// Page is one page of a tenant's history, newest first.
type Page struct {
	Versions []Version `json:"versions"`
	Total    int       `json:"total"`
	Page     int       `json:"page"`
	PerPage  int       `json:"per_page"`
}

// ErrVersionNotFound marks a Get/rollback against a version the tenant's
// history does not contain.
var ErrVersionNotFound = errors.New("schema version not found")

// EnsureTable creates public.schema_history idempotently (the outbox pattern —
// existing databases predate the canonical DDL in migrations/001).
func EnsureTable(ctx context.Context, pool *pgxpool.Pool) error {
	_, err := pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS public.schema_history (
			id         BIGSERIAL   PRIMARY KEY,
			tenant_id  TEXT        NOT NULL REFERENCES public.tenants(id) ON DELETE CASCADE,
			version    INT         NOT NULL,
			schema     JSONB       NOT NULL,
			hash       TEXT        NOT NULL,
			source     TEXT        NOT NULL,
			note       TEXT        NOT NULL DEFAULT '',
			created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
			UNIQUE (tenant_id, version)
		);
		CREATE INDEX IF NOT EXISTS idx_schema_history_tenant
			ON public.schema_history (tenant_id, version DESC)`)
	if err != nil {
		return fmt.Errorf("schemahistory: ensure table: %w", err)
	}
	return nil
}

// Hash returns the canonical identity of a schema: sha256 hex over the
// engine-marshaled JSON bytes. Every append site marshals *schema.APISchema*
// with encoding/json (deterministic: sorted map keys, fixed struct order), so
// the same logical schema always hashes identically.
func Hash(schemaJSON []byte) string {
	sum := sha256.Sum256(schemaJSON)
	return hex.EncodeToString(sum[:])
}

// Append records schemaJSON as the tenant's next version. If the latest
// version already holds the same canonical hash it appends NOTHING and returns
// that version with appended=false (re-deploying an unchanged schema is a
// history no-op, mirroring the idempotent migration diff).
func Append(ctx context.Context, pool *pgxpool.Pool, tenantID string, schemaJSON []byte, source, note string) (version int, appended bool, err error) {
	h := Hash(schemaJSON)

	// Concurrency: two simultaneous deploys could race MAX(version)+1; the
	// UNIQUE (tenant_id, version) makes the loser retry. Deploys are already
	// serialized per tenant by the migration advisory lock, so >1 retry is
	// vanishingly rare — 3 attempts is a formality, not a load-bearing loop.
	for attempt := 0; attempt < 3; attempt++ {
		var latest int
		var latestHash string
		err = pool.QueryRow(ctx, `
			SELECT version, hash FROM public.schema_history
			WHERE tenant_id = $1 ORDER BY version DESC LIMIT 1`, tenantID).
			Scan(&latest, &latestHash)
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			latest, latestHash = 0, ""
		case err != nil:
			return 0, false, fmt.Errorf("schemahistory: read latest: %w", err)
		}
		if latestHash == h {
			return latest, false, nil // unchanged schema — no new version
		}

		_, err = pool.Exec(ctx, `
			INSERT INTO public.schema_history (tenant_id, version, schema, hash, source, note)
			VALUES ($1, $2, $3, $4, $5, $6)`,
			tenantID, latest+1, schemaJSON, h, source, note)
		if err == nil {
			return latest + 1, true, nil
		}
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			continue // lost the version race — re-read latest and retry
		}
		return 0, false, fmt.Errorf("schemahistory: append: %w", err)
	}
	return 0, false, fmt.Errorf("schemahistory: append for %q kept losing the version race: %w", tenantID, err)
}

// List returns one page of a tenant's history, newest first. Resource names
// are extracted from the stored JSONB so the timeline can summarize each
// version without shipping full schemas.
func List(ctx context.Context, pool *pgxpool.Pool, tenantID string, page, perPage int) (*Page, error) {
	if page < 1 {
		page = 1
	}
	if perPage < 1 || perPage > 200 {
		perPage = 50
	}
	out := &Page{Versions: []Version{}, Page: page, PerPage: perPage}
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM public.schema_history WHERE tenant_id = $1`, tenantID).
		Scan(&out.Total); err != nil {
		return nil, fmt.Errorf("schemahistory: count: %w", err)
	}
	rows, err := pool.Query(ctx, `
		SELECT version, hash, source, note, created_at,
		       COALESCE((SELECT array_agg(k ORDER BY k)
		                 FROM jsonb_object_keys(schema->'resources') AS k), '{}')
		FROM public.schema_history
		WHERE tenant_id = $1
		ORDER BY version DESC
		LIMIT $2 OFFSET $3`,
		tenantID, perPage, (page-1)*perPage)
	if err != nil {
		return nil, fmt.Errorf("schemahistory: list: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var v Version
		if err := rows.Scan(&v.Version, &v.Hash, &v.Source, &v.Note, &v.CreatedAt, &v.Resources); err != nil {
			return nil, fmt.Errorf("schemahistory: scan: %w", err)
		}
		out.Versions = append(out.Versions, v)
	}
	return out, rows.Err()
}

// Get returns one version WITH its full schema JSON. ErrVersionNotFound when
// the tenant's history has no such version.
func Get(ctx context.Context, pool *pgxpool.Pool, tenantID string, version int) (*Version, error) {
	var v Version
	v.Version = version
	err := pool.QueryRow(ctx, `
		SELECT hash, source, note, created_at, schema,
		       COALESCE((SELECT array_agg(k ORDER BY k)
		                 FROM jsonb_object_keys(schema->'resources') AS k), '{}')
		FROM public.schema_history
		WHERE tenant_id = $1 AND version = $2`,
		tenantID, version).
		Scan(&v.Hash, &v.Source, &v.Note, &v.CreatedAt, &v.SchemaJSON, &v.Resources)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("tenant %q version %d: %w", tenantID, version, ErrVersionNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("schemahistory: get: %w", err)
	}
	return &v, nil
}

// TenantsNeedingBackfill returns the ids of tenants that HAVE a stored schema
// but NO history rows — pre-versioning tenants whose current schema should be
// captured as v1 at upgrade (the caller re-marshals through schema.APISchema so
// the hash is canonical; raw jsonb text would hash differently than the
// engine's own marshaling and break the dedup invariant).
func TenantsNeedingBackfill(ctx context.Context, pool *pgxpool.Pool) ([]string, error) {
	rows, err := pool.Query(ctx, `
		SELECT t.id FROM public.tenants t
		WHERE t.json_schema IS NOT NULL
		  AND NOT EXISTS (SELECT 1 FROM public.schema_history h WHERE h.tenant_id = t.id)
		ORDER BY t.id`)
	if err != nil {
		return nil, fmt.Errorf("schemahistory: backfill scan: %w", err)
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}
