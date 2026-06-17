package migration

import (
	"sort"
	"strings"

	"github.com/miguelangel/appitools/pkg/schema"
	"github.com/miguelangel/appitools/pkg/schemadiff"
)

// buildDesiredSchema maps an Appitools API schema (the tenant's JSON, the model
// the engine already parses) onto the canonical schemadiff.Schema — the bridge
// between the engine's schema vocabulary and the migration engine's comparable
// model. It is built so that diffing it against a tenant the historical converger
// provisioned yields an EMPTY plan: a re-provision of an unchanged schema is a
// true no-op (the red-de-seguridad invariant).
//
// It is faithful, table-for-table, to the converger's DDL decisions
// (pkg/migration/runner.go before this session):
//
//   - implicit "id" UUID PRIMARY KEY DEFAULT gen_random_uuid();
//   - a regular field → TypeForAPIType(type); NOT NULL iff required; a UNIQUE
//     constraint iff unique. The field's `default` is DELIBERATELY IGNORED — the
//     converger never emitted a DB default for a regular field (defaults are an
//     app-layer concern applied on create), so modeling one here would make a
//     re-provision try to ADD a DEFAULT to an existing column (a spurious op);
//   - an auto field → TIMESTAMPTZ DEFAULT now(), nullable;
//   - every relation FK column gets a btree index idx_<table>_<col>
//     (ensureRelationIndexes);
//   - each declared `indexes` entry becomes an idx_/uniq_ index
//     (ensureDeclaredIndexes).
//
// It models NO foreign-key constraints and NO check constraints: the converger
// created neither, so adding them to the desired model would diff as "ADD FK /
// ADD CHECK over existing data" on every live tenant — a behavior change and a
// backfill risk, left for a deliberate later increment (the integrity gap in
// docs/MIGRATION_DIAG.md §2). Enum types are likewise not modeled (enum fields
// are TEXT + app-layer validation, exactly as today).
func buildDesiredSchema(pgSchema string, s *schema.APISchema) *schemadiff.Schema {
	ds := schemadiff.NewSchema(pgSchema)

	names := sortedResourceNames(s)
	for _, resName := range names {
		res := s.Resources[resName]
		tbl := schemadiff.NewTable(resName)

		// Implicit id PK — the converger's first column.
		tbl.AddColumn(&schemadiff.Column{
			Name:    "id",
			Type:    schemadiff.Type{Base: schemadiff.BaseUUID},
			NotNull: true,
			Default: &schemadiff.Expr{Raw: "gen_random_uuid()"},
		})
		tbl.PK = &schemadiff.PrimaryKey{Name: resName + "_pkey", Columns: []string{"id"}}

		// Regular fields first (sorted), then auto fields (sorted) — converger order.
		var regular, auto []string
		for name, f := range res.Fields {
			if name == "id" {
				continue
			}
			if f.Auto {
				auto = append(auto, name)
			} else {
				regular = append(regular, name)
			}
		}
		sort.Strings(regular)
		sort.Strings(auto)

		for _, name := range regular {
			f := res.Fields[name]
			tbl.AddColumn(&schemadiff.Column{
				Name:    name,
				Type:    schemadiff.TypeForAPIType(f.Type),
				NotNull: f.Required,
			})
			if f.Unique {
				sym := resName + "_" + name + "_key" // mirrors Postgres' inline-UNIQUE auto-name
				tbl.Uniques[sym] = &schemadiff.UniqueConstraint{Symbol: sym, Columns: []string{name}}
			}
		}
		for _, name := range auto {
			tbl.AddColumn(&schemadiff.Column{
				Name:    name,
				Type:    schemadiff.Type{Base: schemadiff.BaseTimestamptz},
				Default: &schemadiff.Expr{Raw: "now()"},
			})
		}

		ds.Tables[resName] = tbl
	}

	addRelationIndexes(ds, s, names)
	addDeclaredIndexes(ds, s, names)
	return ds
}

// addRelationIndexes mirrors ensureRelationIndexes: a single btree index per
// (table, FK column), deduped globally, added only when the target table and
// column actually exist in the desired model (the converger's "warn and skip" for
// an unknown column/table, expressed structurally here).
func addRelationIndexes(ds *schemadiff.Schema, s *schema.APISchema, names []string) {
	seen := make(map[string]bool)
	for _, resName := range names {
		for _, t := range relationIndexTargets(resName, s.Resources[resName]) {
			key := t.table + "." + t.column
			if seen[key] {
				continue
			}
			seen[key] = true
			tbl := ds.Tables[t.table]
			if tbl == nil {
				continue
			}
			if _, ok := tbl.Columns[t.column]; !ok {
				continue
			}
			idxName := "idx_" + t.table + "_" + t.column
			tbl.Indexes[idxName] = &schemadiff.Index{
				Name: idxName, Columns: []string{t.column}, Method: "btree",
			}
		}
	}
}

// addDeclaredIndexes mirrors ensureDeclaredIndexes: each resource's user-declared
// `indexes` becomes an idx_/uniq_<table>_<cols> btree index, skipped when any
// listed column is absent from the model (parity with the converger's
// information_schema existence check). A name collision with a relation FK index
// (same columns/method/unique) collapses harmlessly in the map — exactly the
// converger's CREATE INDEX IF NOT EXISTS no-op.
func addDeclaredIndexes(ds *schemadiff.Schema, s *schema.APISchema, names []string) {
	for _, resName := range names {
		tbl := ds.Tables[resName]
		if tbl == nil {
			continue
		}
		for _, idx := range s.Resources[resName].Indexes {
			if len(idx.Fields) == 0 {
				continue
			}
			ok := true
			for _, col := range idx.Fields {
				if _, has := tbl.Columns[col]; !has {
					ok = false
					break
				}
			}
			if !ok {
				continue
			}
			prefix := "idx"
			if idx.Unique {
				prefix = "uniq"
			}
			idxName := prefix + "_" + resName + "_" + strings.Join(idx.Fields, "_")
			tbl.Indexes[idxName] = &schemadiff.Index{
				Name:    idxName,
				Columns: append([]string(nil), idx.Fields...),
				Unique:  idx.Unique,
				Method:  "btree",
			}
		}
	}
}

func sortedResourceNames(s *schema.APISchema) []string {
	out := make([]string, 0, len(s.Resources))
	for n := range s.Resources {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}
