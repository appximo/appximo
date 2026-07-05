// Package migrations exposes the canonical control-plane bootstrap DDL as an
// embedded string, so Go callers (the fleet orchestrator's per-app database
// bootstrap) apply THE file — not a drifting copy. docker-compose.yml still
// inlines it for the clone-free quickstart; this embed and that inline both
// mirror 001_control_plane.sql (the single source of truth).
package migrations

import _ "embed"

// ControlPlane is 001_control_plane.sql verbatim: public.tenants,
// public.tenant_policies, public.migration_log and the schema_updated NOTIFY
// trigger. Idempotent (IF NOT EXISTS / OR REPLACE) — safe to apply on every
// boot of a fresh or existing database.
//
//go:embed 001_control_plane.sql
var ControlPlane string
