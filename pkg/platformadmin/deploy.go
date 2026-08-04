package platformadmin

import (
	"context"

	"github.com/appximo/appximo/pkg/migration"
	"github.com/appximo/appximo/pkg/schema"
)

// Schema deploy surface (UI-F1-S1) — the backend of "deploy from the editor". It
// is a THIN wrapper over the existing control plane (s.cp): the editor is a
// browser SPA served same-origin at /editor and CANNOT reach the internal control
// plane on :9090, so these /admin routes give it the same provisioning + migration
// path the control plane already implements (RegisterTenant for a new tenant via
// CreateTenant; the diff→preview→approved-apply migration engine for an existing
// one). Nothing here reimplements provisioning or migration — it delegates, so the
// engine stays the single authority, gate and all.

// GetTenantSchema returns a tenant's stored schema (for loading it back into the
// visual editor). nil when the tenant exists but has no schema set yet.
func (s *Service) GetTenantSchema(ctx context.Context, id string) (*schema.APISchema, error) {
	return s.cp.GetSchema(ctx, id)
}

// PreviewTenantSchema computes the DRY-RUN migration plan for applying sc to tenant
// id — the safe ops, the data-losing drops with their measured impact (rows lost),
// the drift and the concerns — evaluated against the given destructive-approval set.
// It applies NOTHING (the informed-consent surface the destructive gate needs).
func (s *Service) PreviewTenantSchema(ctx context.Context, id string, sc *schema.APISchema, approved []string) (*migration.Preview, error) {
	return s.cp.PreviewSchema(ctx, id, sc, approved)
}

// ApplyTenantSchema applies sc to tenant id, executing ONLY the destructive drops
// whose key is enumerated in approved (everything else stays gated as drift — the
// fail-safe default). Returns the outcome (applied/gated/unmatched). This is the
// SAME approved-apply path the control plane's PUT /tenants/{id}/schema uses.
func (s *Service) ApplyTenantSchema(ctx context.Context, id string, sc *schema.APISchema, approved []string) (*migration.ApplyOutcome, error) {
	return s.cp.UpdateSchemaApproved(ctx, id, sc, approved)
}
