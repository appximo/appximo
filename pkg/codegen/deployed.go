package codegen

import (
	"context"

	"github.com/miguelangel/appitools/pkg/schema"
)

// ENG-12 — writing a field the running process was not booted with.
//
// Routes, GraphQL types and /docs are compiled ONCE at boot from the engine's
// schema file. Data, and each tenant's physical columns, are per-tenant: deploying
// a schema to a tenant migrates ITS tables live. That made a freshly deployed column
// READABLE immediately (`SELECT *` returns it) and NOT WRITABLE: the write path
// validated the body against the boot-compiled resource, so a `PATCH` naming the new
// column answered `422 unknown field` until the process restarted. Measured end to
// end in docs/AUTHORING_JOURNEY.md 5-6.
//
// This file closes that gap by letting the write path resolve the resource from the
// TENANT'S DEPLOYED schema — the same schema the read path already reflects —
// falling back to the boot resource whenever there is nothing deployed to use.
//
// Deliberately NOT covered (a restart is still required, and the engine now SAYS so
// rather than answering "unknown field"):
//   - a NEW RESOURCE: its routes, GraphQL type and docs do not exist in this process.
//   - GraphQL mutations: the input types are boot-compiled, so a new field is not an
//     argument the query even parses.
//   - the declarative rules of a new field are compiled when the deployed schema is
//     loaded, not at boot — see the provider in app.go.

// WriteSurface is what the write path needs about a resource: its field set (for
// key/type checking) and its compiled declarative validators. The two travel
// together because they must describe the SAME resource — validating a body against
// one version's rules and one version's fields is how divergences hide.
type WriteSurface struct {
	Res *schema.ResourceSchema
	RV  *schema.ResourceValidator
}

// DeployedProvider resolves the write surface a tenant currently has DEPLOYED.
// Returning nil means "nothing deployed to prefer" and the caller keeps the
// boot-compiled surface — the exact previous behavior.
//
// Implementations must be cheap per call (the write path is hot): resolve from an
// in-memory, invalidation-driven cache, never a query per request.
type DeployedProvider interface {
	WriteSurfaceFor(tenantID, resource string) *WriteSurface
}

type deployedKey struct{}

// WithDeployedProvider puts p in the context so the generated write handlers can
// prefer a tenant's deployed schema. Installed once, in the middleware chain.
func WithDeployedProvider(ctx context.Context, p DeployedProvider) context.Context {
	return context.WithValue(ctx, deployedKey{}, p)
}

// DeployedProviderFromCtx returns the provider installed for this request, if any.
func DeployedProviderFromCtx(ctx context.Context) DeployedProvider {
	p, _ := ctx.Value(deployedKey{}).(DeployedProvider)
	return p
}

// writeSurface picks the surface a write is validated against: the tenant's DEPLOYED
// resource when one is available, else the boot-compiled pair. It is the single
// decision point, so REST create and update can never disagree about which schema
// version they enforce.
func writeSurface(ctx context.Context, tenantID, resource string, bootRes *schema.ResourceSchema, bootRV *schema.ResourceValidator) (*schema.ResourceSchema, *schema.ResourceValidator) {
	p := DeployedProviderFromCtx(ctx)
	if p == nil {
		return bootRes, bootRV
	}
	if ws := p.WriteSurfaceFor(tenantID, resource); ws != nil && ws.Res != nil && ws.RV != nil {
		return ws.Res, ws.RV
	}
	return bootRes, bootRV
}
