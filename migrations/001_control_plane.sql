-- Control plane tables for Appitools multi-tenant platform.
-- Apply once against the shared PostgreSQL instance before starting the server.

-- ── Tenants ──────────────────────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS public.tenants (
    id           TEXT        PRIMARY KEY,
    pg_schema    TEXT        NOT NULL UNIQUE,
    display_name TEXT        NOT NULL,
    email        TEXT        NOT NULL,
    plan         TEXT        NOT NULL DEFAULT 'free',
    json_schema  JSONB,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- ── Per-tenant RBAC policies ──────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS public.tenant_policies (
    tenant_id  TEXT        PRIMARY KEY REFERENCES public.tenants(id) ON DELETE CASCADE,
    policy     JSONB       NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- ── Migration log ─────────────────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS public.migration_log (
    id          BIGSERIAL   PRIMARY KEY,
    tenant_id   TEXT        NOT NULL REFERENCES public.tenants(id) ON DELETE CASCADE,
    status      TEXT        NOT NULL CHECK (status IN ('pending', 'running', 'ok', 'failed')),
    changes     TEXT,
    error       TEXT,
    started_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    finished_at TIMESTAMPTZ
);

-- ── Schema version history (VERSION-S1) ───────────────────────────────────────
-- Append-only: every persisted tenant schema (register / deploy / rollback /
-- fan-out) is recorded as a version. A rollback appends a NEW version whose
-- content equals an old one — the trace is never rewritten. The engine also
-- ensures this table at boot (pkg/schemahistory.EnsureTable) for databases that
-- predate it.
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
    ON public.schema_history (tenant_id, version DESC);

-- ── Notify listeners when a tenant's JSON schema is updated ───────────────────
CREATE OR REPLACE FUNCTION public.notify_schema_updated()
RETURNS TRIGGER LANGUAGE plpgsql AS $$
BEGIN
    PERFORM pg_notify('schema_updated', NEW.id);
    RETURN NEW;
END;
$$;

DROP TRIGGER IF EXISTS schema_updated_trigger ON public.tenants;

CREATE TRIGGER schema_updated_trigger
    AFTER UPDATE OF json_schema ON public.tenants
    FOR EACH ROW
    EXECUTE FUNCTION public.notify_schema_updated();
