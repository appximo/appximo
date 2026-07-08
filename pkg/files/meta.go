package files

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// tenantRe constrains a tenant id to the same shape TenantMiddleware accepts from
// the Host subdomain: lowercase alnum with interior hyphens, 2–30 chars. It is the
// FIRST line of defence for path safety — a tenant id is the only externally
// derived component of a blob path, and this rejects anything containing "/" or
// ".." before it ever reaches the filesystem. (The blob's other path components
// are the content hash and fixed hex prefixes, never client input.)
var tenantRe = regexp.MustCompile(`^[a-z0-9][a-z0-9\-]{0,28}[a-z0-9]$`)

func validTenant(id string) error {
	if !tenantRe.MatchString(id) {
		return fmt.Errorf("files: invalid tenant id %q", id)
	}
	return nil
}

// metaStore persists file metadata per tenant. Two implementations: pgStore (the
// real per-tenant Postgres table) and memStore (unit tests, no Docker). Both share
// the Local backend's logic — only the storage of the row differs.
type metaStore interface {
	// ensure creates the tenant's files table idempotently (a no-op after the
	// first call per tenant per process).
	ensure(ctx context.Context, tenantID string) error
	// insert stores m (its ID is assigned by the store) and returns the new id.
	insert(ctx context.Context, tenantID string, m Meta) (string, error)
	// get returns the metadata for id, or ErrNotFound.
	get(ctx context.Context, tenantID, id string) (Meta, error)
	// existsSHA reports whether any row for the tenant has this content hash.
	existsSHA(ctx context.Context, tenantID, sha string) (bool, error)
	// del removes id's row and reports whether the blob (sha) is STILL referenced
	// by another row — so the caller deletes the blob only when it is orphaned.
	del(ctx context.Context, tenantID, id string) (sha string, stillReferenced bool, err error)
	// list returns a page of the tenant's file metadata (newest first) plus the
	// total row count. A tenant with no files table yet lists as empty, not an
	// error (same contract as get/existsSHA).
	list(ctx context.Context, tenantID string, limit, offset int) ([]Meta, int, error)
}

// ── in-memory store (unit tests) ────────────────────────────────────────────

type memStore struct {
	mu   sync.Mutex
	rows map[string]map[string]Meta // tenant → id → Meta
}

func newMemStore() *memStore { return &memStore{rows: map[string]map[string]Meta{}} }

func (s *memStore) ensure(_ context.Context, tenant string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.rows[tenant] == nil {
		s.rows[tenant] = map[string]Meta{}
	}
	return nil
}

func (s *memStore) insert(_ context.Context, tenant string, m Meta) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.rows[tenant] == nil {
		s.rows[tenant] = map[string]Meta{}
	}
	m.ID = uuid.NewString()
	if m.CreatedAt.IsZero() {
		m.CreatedAt = time.Now()
	}
	s.rows[tenant][m.ID] = m
	return m.ID, nil
}

func (s *memStore) list(_ context.Context, tenant string, limit, offset int) ([]Meta, int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	all := make([]Meta, 0, len(s.rows[tenant]))
	for _, m := range s.rows[tenant] {
		all = append(all, m)
	}
	sort.Slice(all, func(i, j int) bool {
		if !all[i].CreatedAt.Equal(all[j].CreatedAt) {
			return all[i].CreatedAt.After(all[j].CreatedAt)
		}
		return all[i].ID > all[j].ID
	})
	total := len(all)
	if offset >= total {
		return []Meta{}, total, nil
	}
	end := offset + limit
	if end > total {
		end = total
	}
	return all[offset:end], total, nil
}

func (s *memStore) get(_ context.Context, tenant, id string) (Meta, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	m, ok := s.rows[tenant][id]
	if !ok {
		return Meta{}, ErrNotFound
	}
	return m, nil
}

func (s *memStore) existsSHA(_ context.Context, tenant, sha string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, m := range s.rows[tenant] {
		if m.SHA256 == sha {
			return true, nil
		}
	}
	return false, nil
}

func (s *memStore) del(_ context.Context, tenant, id string) (string, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	m, ok := s.rows[tenant][id]
	if !ok {
		return "", false, ErrNotFound
	}
	delete(s.rows[tenant], id)
	for _, other := range s.rows[tenant] {
		if other.SHA256 == m.SHA256 {
			return m.SHA256, true, nil
		}
	}
	return m.SHA256, false, nil
}

// ── Postgres store (per-tenant files table) ─────────────────────────────────

// pgStore keeps file metadata in a "files" table inside each tenant's schema
// (tenant_<id>.files), the same schema-per-tenant isolation the engine uses for
// resource tables. It is safe for the engine (its pool) and the worker (its own
// pool) to share the same logical store: both reach the same rows.
type pgStore struct {
	pool    *pgxpool.Pool
	ensured sync.Map // tenant → struct{}: ensure() runs the DDL once per process
}

// NewPGStore builds the Postgres-backed metadata store. The pool may be the
// engine's shared pool or a worker-owned pool.
func NewPGStore(pool *pgxpool.Pool) metaStore { return &pgStore{pool: pool} }

// schemaFor returns the validated tenant schema name (tenant_<id>).
func schemaFor(tenant string) (string, error) {
	if err := validTenant(tenant); err != nil {
		return "", err
	}
	return "tenant_" + tenant, nil
}

func (s *pgStore) table(tenant string) (string, error) {
	sch, err := schemaFor(tenant)
	if err != nil {
		return "", err
	}
	return pgx.Identifier{sch, "files"}.Sanitize(), nil
}

func (s *pgStore) ensure(ctx context.Context, tenant string) error {
	if _, done := s.ensured.Load(tenant); done {
		return nil
	}
	if err := EnsureMetaTable(ctx, s.pool, tenant); err != nil {
		return err
	}
	s.ensured.Store(tenant, struct{}{})
	return nil
}

// EnsureMetaTable creates the tenant's files metadata table idempotently — the
// SAME DDL the store runs lazily on first upload. Exported (FILES-LINK-S1) so
// the migration engine can guarantee the table exists BEFORE adding a file
// field's foreign key to it (a schema may declare a `file` field for a tenant
// that has never uploaded anything).
func EnsureMetaTable(ctx context.Context, pool *pgxpool.Pool, tenant string) error {
	sch, err := schemaFor(tenant)
	if err != nil {
		return err
	}
	tbl := pgx.Identifier{sch, "files"}.Sanitize()
	ddl := fmt.Sprintf(`
CREATE TABLE IF NOT EXISTS %s (
    id            UUID        DEFAULT gen_random_uuid() PRIMARY KEY,
    sha256        TEXT        NOT NULL,
    size          BIGINT      NOT NULL,
    content_type  TEXT,
    original_name TEXT,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_files_sha256_%s ON %s (sha256);`,
		tbl, tenant, tbl)
	if _, err := pool.Exec(ctx, ddl); err != nil {
		return fmt.Errorf("files: ensure table: %w", err)
	}
	return nil
}

func (s *pgStore) insert(ctx context.Context, tenant string, m Meta) (string, error) {
	tbl, err := s.table(tenant)
	if err != nil {
		return "", err
	}
	var id string
	q := fmt.Sprintf(
		`INSERT INTO %s (sha256, size, content_type, original_name) VALUES ($1,$2,$3,$4) RETURNING id::text`, tbl)
	if err := s.pool.QueryRow(ctx, q, m.SHA256, m.Size, nullable(m.ContentType), nullable(m.OriginalName)).Scan(&id); err != nil {
		return "", fmt.Errorf("files: insert metadata: %w", err)
	}
	return id, nil
}

func (s *pgStore) get(ctx context.Context, tenant, id string) (Meta, error) {
	tbl, err := s.table(tenant)
	if err != nil {
		return Meta{}, err
	}
	q := fmt.Sprintf(
		`SELECT id::text, sha256, size, content_type, original_name, created_at FROM %s WHERE id = $1`, tbl)
	var m Meta
	var ct, on *string
	err = s.pool.QueryRow(ctx, q, id).Scan(&m.ID, &m.SHA256, &m.Size, &ct, &on, &m.CreatedAt)
	if err != nil {
		if isNoFileRows(err) {
			return Meta{}, ErrNotFound
		}
		return Meta{}, fmt.Errorf("files: get metadata: %w", err)
	}
	m.ContentType, m.OriginalName = deref(ct), deref(on)
	return m, nil
}

func (s *pgStore) existsSHA(ctx context.Context, tenant, sha string) (bool, error) {
	tbl, err := s.table(tenant)
	if err != nil {
		return false, err
	}
	var ok bool
	q := fmt.Sprintf(`SELECT EXISTS(SELECT 1 FROM %s WHERE sha256 = $1)`, tbl)
	if err := s.pool.QueryRow(ctx, q, sha).Scan(&ok); err != nil {
		if isMissingTable(err) {
			return false, nil
		}
		return false, fmt.Errorf("files: exists sha: %w", err)
	}
	return ok, nil
}

func (s *pgStore) del(ctx context.Context, tenant, id string) (string, bool, error) {
	tbl, err := s.table(tenant)
	if err != nil {
		return "", false, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return "", false, fmt.Errorf("files: begin delete: %w", err)
	}
	defer tx.Rollback(context.Background()) //nolint:errcheck

	var sha string
	delQ := fmt.Sprintf(`DELETE FROM %s WHERE id = $1 RETURNING sha256`, tbl)
	if err := tx.QueryRow(ctx, delQ, id).Scan(&sha); err != nil {
		if isNoFileRows(err) {
			return "", false, ErrNotFound
		}
		return "", false, fmt.Errorf("files: delete metadata: %w", err)
	}
	var stillRef bool
	refQ := fmt.Sprintf(`SELECT EXISTS(SELECT 1 FROM %s WHERE sha256 = $1)`, tbl)
	if err := tx.QueryRow(ctx, refQ, sha).Scan(&stillRef); err != nil {
		return "", false, fmt.Errorf("files: ref check: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return "", false, fmt.Errorf("files: commit delete: %w", err)
	}
	return sha, stillRef, nil
}

func (s *pgStore) list(ctx context.Context, tenant string, limit, offset int) ([]Meta, int, error) {
	tbl, err := s.table(tenant)
	if err != nil {
		return nil, 0, err
	}
	var total int
	if err := s.pool.QueryRow(ctx, fmt.Sprintf(`SELECT COUNT(*) FROM %s`, tbl)).Scan(&total); err != nil {
		if isMissingTable(err) {
			return []Meta{}, 0, nil // tenant has no files yet — an empty listing, not an error
		}
		return nil, 0, fmt.Errorf("files: count metadata: %w", err)
	}
	q := fmt.Sprintf(
		`SELECT id::text, sha256, size, content_type, original_name, created_at
		   FROM %s ORDER BY created_at DESC, id DESC LIMIT $1 OFFSET $2`, tbl)
	rows, err := s.pool.Query(ctx, q, limit, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("files: list metadata: %w", err)
	}
	defer rows.Close()
	out := []Meta{}
	for rows.Next() {
		var m Meta
		var ct, on *string
		if err := rows.Scan(&m.ID, &m.SHA256, &m.Size, &ct, &on, &m.CreatedAt); err != nil {
			return nil, 0, fmt.Errorf("files: scan metadata: %w", err)
		}
		m.ContentType, m.OriginalName = deref(ct), deref(on)
		out = append(out, m)
	}
	return out, total, rows.Err()
}

// NewMemStore returns the in-memory metadata store — for TESTS and ephemeral
// tooling only (nothing survives the process). Exported so other packages'
// tests can build a files.Store without Postgres; production always uses
// NewPGStore.
func NewMemStore() metaStore { return newMemStore() } //nolint:revive // opaque handle by design

func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func deref(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// isNoFileRows reports a "no rows" result OR a not-yet-created tenant files table
// (42P01) / missing schema (3F000) — both mean "this tenant has no such file",
// which maps to ErrNotFound rather than a 500.
func isNoFileRows(err error) bool {
	return errors.Is(err, pgx.ErrNoRows) || isMissingTable(err)
}

func isMissingTable(err error) bool {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code == "42P01" || pgErr.Code == "3F000"
	}
	return false
}
