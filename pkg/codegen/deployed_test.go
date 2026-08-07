package codegen

import (
	"context"
	"testing"

	"github.com/appximo/appximo/pkg/schema"
)

// fakeProvider returns a fixed surface for one (tenant, resource) pair.
type fakeProvider struct {
	tenant, resource string
	ws               *WriteSurface
}

func (f *fakeProvider) WriteSurfaceFor(tenantID, resource string) *WriteSurface {
	if tenantID == f.tenant && resource == f.resource {
		return f.ws
	}
	return nil
}

// TestReadSurface pins the M1 contract: the read path's field universe comes
// from the SAME deployed-surface seam as the write path (ENG-12), with the boot
// resource as the only fallback — so a hot-migrated column is filterable exactly
// when it is writable, per tenant.
func TestReadSurface(t *testing.T) {
	boot := &schema.ResourceSchema{Fields: map[string]schema.FieldDef{
		"title": {Type: "string"},
	}}
	deployed := &schema.ResourceSchema{Fields: map[string]schema.FieldDef{
		"title":        {Type: "string"},
		"codigo_socio": {Type: "string"}, // the hot-migrated field of the field report
	}}

	// No provider in ctx → boot.
	if got := readSurface(context.Background(), "acme", "miembros", boot); got != boot {
		t.Fatal("no provider: must fall back to boot")
	}

	p := &fakeProvider{tenant: "acme", resource: "miembros",
		ws: &WriteSurface{Res: deployed, RV: schema.CompileRules(deployed)}}
	ctx := WithDeployedProvider(context.Background(), p)

	// Provider hit → the deployed surface (the new field is in the universe).
	got := readSurface(ctx, "acme", "miembros", boot)
	if got != deployed {
		t.Fatal("provider hit: must return the deployed surface")
	}
	if _, ok := got.Fields["codigo_socio"]; !ok {
		t.Fatal("deployed surface must carry the hot-migrated field")
	}

	// Another tenant on the same process → ITS state, i.e. boot (per-tenant truth).
	if got := readSurface(ctx, "otro", "miembros", boot); got != boot {
		t.Fatal("tenant without a deployed schema must keep boot")
	}
	// A surface with nil Res (negative cache) → boot.
	p2 := &fakeProvider{tenant: "acme", resource: "miembros", ws: &WriteSurface{}}
	if got := readSurface(WithDeployedProvider(context.Background(), p2), "acme", "miembros", boot); got != boot {
		t.Fatal("nil deployed Res must fall back to boot")
	}

	// And the write path agrees with the read path about which surface is
	// current — the whole point of one seam.
	wres, _ := writeSurface(ctx, "acme", "miembros", boot, nil)
	if wres != readSurface(ctx, "acme", "miembros", boot) {
		t.Fatal("write and read surfaces must resolve identically")
	}
}
