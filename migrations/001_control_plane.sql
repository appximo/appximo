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
