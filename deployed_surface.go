package appximo

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"strings"
	"sync"

	"github.com/appximo/appximo/pkg/codegen"
	"github.com/appximo/appximo/pkg/schema"
	"github.com/appximo/appximo/pkg/tenant"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ENG-12 — the engine-side provider that makes a freshly deployed column WRITABLE
// without a restart.
//
// The read path already reflects a deploy immediately (the migration adds the column
// and `SELECT *` returns it). The write path validated against the BOOT-compiled
// resource, so `PATCH {"weight_kg": 8.2}` answered `422 unknown field` until the
// process restarted — deploy a field, then restart before you can use it.
//
// This provider resolves, per tenant, the WRITE SURFACE from the schema the tenant
// actually has deployed, and hands it to the generated handlers through the request
// context (codegen.DeployedProvider).
//
// Three properties make it safe on a hot path:
//
//   - It is a MERGE, never a replacement: a field is writable if the BOOT schema has
//     it OR the deployed one does. A deploy can only ever ADD to what was accepted
//     before, so a tenant whose record lags the boot file (registered but not yet
//     re-deployed) behaves exactly as it did.
//   - Everything expensive happens ONCE per tenant per invalidation: the JSON is read
//     from public.tenants, parsed and compiled into validators, then cached. A request
//     costs one RWMutex read and two map lookups.
//   - It is driven by the SAME invalidation the response cache uses (pg_notify
//     `schema_updated`, fired by every persist path), so a deploy takes effect on the
//     next write with no polling and no restart.
//
// What still needs a restart is unchanged and now SAID OUT LOUD rather than answered
// with "unknown field": a NEW RESOURCE (its routes/GraphQL type/docs are boot-built),
// GraphQL mutation inputs, and hooks.
type deployedSurfaces struct {
	pool *pgxpool.Pool
	sc   *tenant.SchemaCache
	boot *schema.APISchema

	mu sync.RWMutex
	// byTenant maps tenant → resource → merged write surface. A tenant present with
	// a nil resource entry means "resolved, nothing to prefer" — a negative cache, so
	// a tenant with no deployed schema does not re-query on every write.
	byTenant map[string]map[string]*codegen.WriteSurface
}

func newDeployedSurfaces(pool *pgxpool.Pool, sc *tenant.SchemaCache, boot *schema.APISchema) *deployedSurfaces {
	return &deployedSurfaces{
		pool: pool, sc: sc, boot: boot,
		byTenant: make(map[string]map[string]*codegen.WriteSurface),
	}
}

// WriteSurfaceFor implements codegen.DeployedProvider.
func (d *deployedSurfaces) WriteSurfaceFor(tenantID, resource string) *codegen.WriteSurface {
	d.mu.RLock()
	byRes, ok := d.byTenant[tenantID]
	d.mu.RUnlock()
	if ok {
		return byRes[resource] // nil for a resource with nothing deployed to prefer
	}
	return d.load(tenantID)[resource]
}

// Invalidate drops a tenant's compiled surfaces so the next write recompiles them
// from the newly deployed schema. Called from the pg_notify invalidator, next to the
// response-cache and schema-cache invalidations.
func (d *deployedSurfaces) Invalidate(tenantID string) {
	d.mu.Lock()
	delete(d.byTenant, tenantID)
	d.mu.Unlock()
}

// load resolves and caches a tenant's write surfaces. On any failure it caches an
// EMPTY set — the boot surface then applies, which is the previous behavior, and the
// failure is not retried on every single write.
func (d *deployedSurfaces) load(tenantID string) map[string]*codegen.WriteSurface {
	out := make(map[string]*codegen.WriteSurface)
	if s := d.deployedSchema(tenantID); s != nil {
		for name, bootRes := range d.boot.Resources {
			depRes, has := s.Resources[name]
			if !has {
				continue // not deployed here — the boot surface already governs it
			}
			merged := mergeWritableFields(bootRes, depRes)
			out[name] = &codegen.WriteSurface{Res: merged, RV: schema.CompileRules(merged)}
		}
	}
	d.mu.Lock()
	d.byTenant[tenantID] = out
	d.mu.Unlock()
	return out
}

// deployedSchema returns the tenant's deployed schema from the in-memory cache,
// reading and validating public.tenants.json_schema on a miss. nil when there is
// none (or it cannot be trusted) — the caller then keeps the boot surface.
func (d *deployedSurfaces) deployedSchema(tenantID string) *schema.APISchema {
	if s, ok := d.sc.Get(tenantID); ok {
		return s
	}
	if d.pool == nil {
		return nil
	}
	var raw []byte
	err := d.pool.QueryRow(context.Background(),
		`SELECT json_schema FROM public.tenants WHERE id = $1`, tenantID).Scan(&raw)
	if err != nil || len(raw) == 0 {
		return nil
	}
	s, err := schema.LoadFromBytes(raw)
	if err != nil {
		log.Printf("deployed schema for tenant %q is unreadable (%v) — writes keep using the boot schema", tenantID, err)
		return nil
	}
	if errs := schema.Validate(s); len(errs) > 0 {
		log.Printf("deployed schema for tenant %q does not validate (%d error(s)) — writes keep using the boot schema", tenantID, len(errs))
		return nil
	}
	d.sc.Set(tenantID, s)
	return s
}

// mergeWritableFields returns a resource whose field set is the UNION of the boot
// and deployed field sets, with the deployed definition winning where both declare a
// field (the deployed one describes the column that is actually in the table).
//
// Everything else — hooks, relations, events, indexes — is taken from the BOOT
// resource: those are compiled into the router, the hook runner and the GraphQL
// types at boot, so pretending a deployed change to them is live would be the same
// class of lie this session is removing.
func mergeWritableFields(bootRes, depRes schema.ResourceSchema) *schema.ResourceSchema {
	merged := bootRes
	fields := make(map[string]schema.FieldDef, len(bootRes.Fields)+len(depRes.Fields))
	for k, v := range bootRes.Fields {
		fields[k] = v
	}
	for k, v := range depRes.Fields {
		fields[k] = v
	}
	merged.Fields = fields
	return &merged
}

// apiNotFound answers a request no route matched. For an /api/<name> path whose
// <name> the tenant HAS deployed but this process was not booted with, it says so
// instead of a bare "not found": the resource exists, its tables exist, and what is
// missing is a reload of the server. That is a different problem with a different
// fix, and the owner cannot tell them apart from a 404.
func (a *App) apiNotFound(w http.ResponseWriter, r *http.Request) {
	name := apiSegment(r.URL.Path)
	tc := tenant.FromCtx(r.Context())
	if name == "" || tc == nil || a.deployed == nil {
		http.NotFound(w, r)
		return
	}
	deployed := false
	for _, n := range a.deployed.deployedOnlyResources(tc.ID) {
		if n == name {
			deployed = true
			break
		}
	}
	if !deployed {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusNotFound)
	_ = json.NewEncoder(w).Encode(map[string]string{
		"error": "resource_not_loaded",
		"message": "\"" + name + "\" was deployed to this tenant and its table exists, but this server was started " +
			"before it and does not serve it yet. Restart the server (Studio's deploy screen has a \"Restart engine now\" " +
			"button) and it will answer. Fields added to an EXISTING resource do not need this.",
		"resource": name,
	})
}

// apiSegment returns the resource segment of an /api/<name>[/...] path, or "".
func apiSegment(path string) string {
	const p = "/api/"
	if !strings.HasPrefix(path, p) {
		return ""
	}
	rest := path[len(p):]
	if i := strings.IndexByte(rest, '/'); i >= 0 {
		rest = rest[:i]
	}
	return rest
}

// deployedOnlyResources returns the resources a tenant has DEPLOYED that this
// process was not booted with — the ones whose routes genuinely do not exist yet.
// It is what lets a 404 on /api/<name> say "deployed, needs a restart" instead of
// leaving the caller to guess (ENG-12's other half).
func (d *deployedSurfaces) deployedOnlyResources(tenantID string) []string {
	s := d.deployedSchema(tenantID)
	if s == nil {
		return nil
	}
	var out []string
	for name := range s.Resources {
		if _, booted := d.boot.Resources[name]; !booted {
			out = append(out, name)
		}
	}
	return out
}
