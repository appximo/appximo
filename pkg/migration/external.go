package migration

import (
	"context"
	"encoding/json"
	"log"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/miguelangel/appitools/pkg/schemadiff"
)

// ENG-9 (CONSUMER-PATH-S1) — consumer-owned database objects must never be
// PROPOSED as destructive drops.
//
// The diff alone cannot tell "a field the operator removed from the schema"
// (a legitimate, approvable drop) from "a column the schema NEVER declared" (a
// consumer's own DDL — a generated column, a side table created via
// Config.BeforeStart / OnTenantProvisioned). Both look identical against the
// live database. Measured consequence on the 58: EVERY dry-run proposed
// `productos.attr_marca — 12 of 12 rows lost` forever, a chronic false alarm on
// the engine's only destructive gate — and an operator who "approves what the
// tool suggests" would have destroyed a column the app needs.
//
// The tiebreaker the diff lacks is HISTORY, and the engine already keeps it:
// public.schema_history is append-only over every schema the tenant ever
// deployed (backfilled at boot for pre-versioning tenants), and
// public.tenants.json_schema is the current one. The rule:
//
//	an object some deployed schema version DECLARED  → engine-managed:
//	    its removal is a real, approvable destructive drop (unchanged);
//	an object NO deployed schema version ever declared → EXTERNAL:
//	    reported as consumer-owned drift, NEVER proposed, NEVER approvable.
//
// When neither the tenant row nor any history exists (standalone use of the
// migration engine, unit tests, a database without the control plane) the
// classifier reports "unknown" and the behavior is EXACTLY the previous one —
// fail-open to the safe side, because everything stays gated anyway.

// ownedObjects is the set of tables/columns any deployed schema version declared.
type ownedObjects struct {
	tables  map[string]bool
	columns map[string]map[string]bool // table → column set
}

func (o *ownedObjects) ownsTable(t string) bool { return o.tables[t] }
func (o *ownedObjects) ownsColumn(t, c string) bool {
	return o.columns[t] != nil && o.columns[t][c]
}

// loadOwnedObjects builds the ownership set for pgSchema from the tenant's
// current schema + its whole version history. ok=false means no control-plane
// record exists for this schema (standalone use) — callers keep legacy behavior.
func loadOwnedObjects(ctx context.Context, pool *pgxpool.Pool, pgSchema string) (*ownedObjects, bool) {
	var tenantID string
	var current []byte
	err := pool.QueryRow(ctx,
		`SELECT id, COALESCE(json_schema, 'null'::jsonb)::text
		   FROM public.tenants WHERE pg_schema = $1`, pgSchema,
	).Scan(&tenantID, &current)
	if err != nil {
		return nil, false // no tenant row (or no control plane at all)
	}

	o := &ownedObjects{tables: map[string]bool{}, columns: map[string]map[string]bool{}}
	found := false
	if o.addSchemaJSON(current) {
		found = true
	}
	rows, err := pool.Query(ctx,
		`SELECT schema::text FROM public.schema_history WHERE tenant_id = $1`, tenantID)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var raw []byte
			if rows.Scan(&raw) == nil && o.addSchemaJSON(raw) {
				found = true
			}
		}
	}
	if !found {
		return nil, false // a tenant row with no readable schema — stay legacy
	}
	return o, true
}

// addSchemaJSON folds one schema document into the ownership set. Tolerant by
// design: history entries are parsed structurally (resources → fields), never
// validated — an old version that today's validator would reject still counts
// for ownership. Returns whether anything was extracted.
func (o *ownedObjects) addSchemaJSON(raw []byte) bool {
	var doc struct {
		Resources map[string]struct {
			RenamedFrom string `json:"renamed_from"`
			Fields      map[string]struct {
				RenamedFrom string `json:"renamed_from"`
			} `json:"fields"`
		} `json:"resources"`
	}
	if json.Unmarshal(raw, &doc) != nil || len(doc.Resources) == 0 {
		return false
	}
	for rname, r := range doc.Resources {
		names := []string{rname}
		if r.RenamedFrom != "" {
			names = append(names, r.RenamedFrom)
		}
		for _, tn := range names {
			o.tables[tn] = true
			cols := o.columns[tn]
			if cols == nil {
				cols = map[string]bool{}
				o.columns[tn] = cols
			}
			cols["id"] = true // the implicit primary key
			for fname, f := range r.Fields {
				cols[fname] = true
				if f.RenamedFrom != "" {
					cols[f.RenamedFrom] = true
				}
			}
		}
	}
	return true
}

// splitExternalDrops removes from plan every DESTRUCTIVE drop whose object no
// deployed schema version ever declared, returning them separately as external
// (consumer-owned) drift. Non-drop ops and engine-owned drops pass through
// untouched. With owned == nil the plan is returned as-is (legacy behavior).
func splitExternalDrops(plan *schemadiff.Plan, owned *ownedObjects) (*schemadiff.Plan, []schemadiff.Operation) {
	if owned == nil {
		return plan, nil
	}
	keep := make([]schemadiff.Operation, 0, len(plan.Ops))
	var external []schemadiff.Operation
	for _, op := range plan.Ops {
		switch o := op.(type) {
		case schemadiff.DropTable:
			if !owned.ownsTable(o.Table.Name) {
				external = append(external, op)
				continue
			}
		case schemadiff.DropColumn:
			if !owned.ownsColumn(o.Table, o.Column.Name) {
				external = append(external, op)
				continue
			}
		}
		keep = append(keep, op)
	}
	return &schemadiff.Plan{Ops: keep}, external
}

// logExternal reports each consumer-owned object left untouched — once per
// apply, so the operator knows WHY it is not in the plan (it is not forgotten,
// it is out of the schema's jurisdiction).
func logExternal(pgSchema string, external []schemadiff.Operation) {
	for _, op := range external {
		log.Printf("migration[%s]: EXTERNAL (consumer-owned, no schema version ever declared it) — out of migration scope, left untouched: %s", pgSchema, op.String())
	}
}
