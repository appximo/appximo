package migration

import (
	"sort"
	"strings"

	"github.com/appximo/appximo/pkg/schema"
	"github.com/appximo/appximo/pkg/schemadiff"
)

// buildDesiredSchema maps an Appximo API schema (the tenant's JSON, the model
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
		// Rename intent (MIG-F1-S2): a table whose schema declares renamed_from is
		// matched to its old self so the diff emits ALTER TABLE … RENAME (data
		// preserving), not drop+add. The intent is resolved against the live DB in
		// diffTenant (resolveRenames) so an already-applied rename is inert.
		tbl.RenamedFrom = res.RenamedFrom

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
				Name:        name,
				Type:        schemadiff.TypeForAPIType(f.Type),
				NotNull:     f.Required,
				RenamedFrom: f.RenamedFrom, // MIG-F1-S2: ALTER RENAME COLUMN, not drop+add
			})
			if f.Unique {
				sym := resName + "_" + name + "_key" // mirrors Postgres' inline-UNIQUE auto-name
				tbl.Uniques[sym] = &schemadiff.UniqueConstraint{Symbol: sym, Columns: []string{name}}
			}
		}
		for _, name := range auto {
			tbl.AddColumn(&schemadiff.Column{
				Name:        name,
				Type:        schemadiff.Type{Base: schemadiff.BaseTimestamptz},
				Default:     &schemadiff.Expr{Raw: "now()"},
				RenamedFrom: res.Fields[name].RenamedFrom,
			})
		}

		ds.Tables[resName] = tbl
	}

	addForeignKeys(ds, s, names)
	addCompositeForeignKeys(ds, s, names)
	addRelationIndexes(ds, s, names)
	addDeclaredIndexes(ds, s, names)
	return ds
}

// addCompositeForeignKeys models each resource-level COMPOSITE foreign key
// (MIG-F1-S5) — a multi-column constraint that does not fit the field-level
// `relation` (one column = one column). Each ForeignKeyDef becomes a real FK over
// (Columns) → Target(RefColumns) with its declared on_delete/on_update, and the
// source columns are indexed (a composite btree) so the referential check is an
// index lookup, exactly like the single-column FKs. The constraint is matched by
// STRUCTURE in the diff, so a re-provision of an unchanged composite FK is a no-op.
func addCompositeForeignKeys(ds *schemadiff.Schema, s *schema.APISchema, names []string) {
	for _, resName := range names {
		tbl := ds.Tables[resName]
		if tbl == nil {
			continue
		}
		for _, fk := range s.Resources[resName].ForeignKeys {
			if _, ok := ds.Tables[fk.Target]; !ok {
				continue // target not a managed resource (validated away at load; defensive)
			}
			sym := compositeFKConstraintName(resName, fk.Columns)
			tbl.FKs[sym] = &schemadiff.ForeignKey{
				Symbol:     sym,
				Columns:    append([]string(nil), fk.Columns...),
				RefTable:   fk.Target,
				RefColumns: append([]string(nil), fk.RefColumns...),
				OnDelete:   refActionForOnDelete(fk.OnDelete),
				OnUpdate:   refActionForOnUpdate(fk.OnUpdate),
			}
			idxName := "idx_" + resName + "_" + strings.Join(fk.Columns, "_")
			if _, exists := tbl.Indexes[idxName]; !exists {
				tbl.Indexes[idxName] = &schemadiff.Index{
					Name: idxName, Columns: append([]string(nil), fk.Columns...), Method: "btree",
				}
			}
		}
	}
}

// compositeFKConstraintName derives a deterministic symbol for a composite FK. The
// diff matches by structure, so the name only has to be a stable valid identifier.
func compositeFKConstraintName(table string, columns []string) string {
	return "fk_" + table + "_" + strings.Join(columns, "_")
}

// refColumnOrID returns the FK target column, defaulting to the implicit "id" when
// `references` is unset (retrocompat: every pre-S5 FK points at id).
func refColumnOrID(references string) string {
	if references == "" {
		return "id"
	}
	return references
}

// refActionForOnUpdate maps a schema `on_update` string to the canonical RefAction.
// Empty (unset) yields NO ACTION — the Postgres default an FK created without ON
// UPDATE already carries — so adding on_update to a schema never churns existing FKs
// (the no-churn default, the deliberate asymmetry with refActionForOnDelete's RESTRICT).
func refActionForOnUpdate(onUpdate string) schemadiff.RefAction {
	switch onUpdate {
	case schema.OnUpdateCascade:
		return schemadiff.Cascade
	case schema.OnUpdateSetNull:
		return schemadiff.SetNull
	case schema.OnUpdateRestrict:
		return schemadiff.Restrict
	default:
		return schemadiff.NoAction
	}
}

// addForeignKeys models a REAL foreign-key constraint for every field that
// declares a `relation` (MIG-F1-S1) — the column IS the FK, like `unique`/
// `required` are field-level. Each FK references the target table's implicit `id`
// PK, carrying the field's `on_delete` action (default RESTRICT — the safe choice
// that closes the silent-orphan integrity bug). It ALSO ensures the FK column is
// indexed, so the referential check Postgres runs on a RESTRICT delete (and the
// embed/join paths) is an index lookup, never a sequential scan.
//
// The field-level `relation` is the COMPLETE, unambiguous source of every physical
// FK: a relations-block `belongs_to`/`many_to_many` FK column is also declared as a
// field-level relation, while the inverse `has_many` is a read-embed view of the
// SAME constraint (so deriving FKs from the relations block would double-count). The
// junction columns of a many_to_many are likewise field-level relations on the
// through table, so their integrity comes for free.
func addForeignKeys(ds *schemadiff.Schema, s *schema.APISchema, names []string) {
	for _, resName := range names {
		tbl := ds.Tables[resName]
		if tbl == nil {
			continue
		}
		for _, fieldName := range sortedFieldNames(s.Resources[resName]) {
			f := s.Resources[resName].Fields[fieldName]
			// A file field (FILES-LINK-S1) is a FIXED relation to the engine's
			// per-tenant files table: same FK machinery (NOT VALID/VALIDATE,
			// auto-index, on_delete — restrict default, set_null allowed, cascade
			// rejected at load), target hardcoded to files(id). The files table is
			// engine-managed (never in ds.Tables — isEngineManagedTable excludes it
			// from the diffed subset), so the FK references it by name and the
			// runner ensures it exists before the FK lands (ensureFilesTable).
			if f.Type == "file" {
				sym := fkConstraintName(resName, fieldName)
				tbl.FKs[sym] = &schemadiff.ForeignKey{
					Symbol:     sym,
					Columns:    []string{fieldName},
					RefTable:   "files",
					RefColumns: []string{"id"},
					OnDelete:   refActionForOnDelete(f.OnDelete),
					OnUpdate:   schemadiff.NoAction, // a file id never changes; on_update rejected at load
				}
				idxName := "idx_" + resName + "_" + fieldName
				if _, exists := tbl.Indexes[idxName]; !exists {
					tbl.Indexes[idxName] = &schemadiff.Index{
						Name: idxName, Columns: []string{fieldName}, Method: "btree",
					}
				}
				continue
			}
			if f.Relation == "" {
				continue
			}
			if _, ok := ds.Tables[f.Relation]; !ok {
				continue // target not a managed resource (validated away at load; defensive)
			}
			sym := fkConstraintName(resName, fieldName)
			tbl.FKs[sym] = &schemadiff.ForeignKey{
				Symbol:  sym,
				Columns: []string{fieldName},
				// references the target's id by default, or an explicit unique column
				// (MIG-F1-S5) when the field declares `references` (validated unique at load).
				RefTable:   f.Relation,
				RefColumns: []string{refColumnOrID(f.References)},
				OnDelete:   refActionForOnDelete(f.OnDelete),
				// on_update defaults to NO ACTION (MIG-F1-S5) — what an FK created without
				// ON UPDATE already carries, so existing FKs do not churn.
				OnUpdate: refActionForOnUpdate(f.OnUpdate),
			}
			// Index the FK column so the RESTRICT referential check is an index
			// lookup (deduped by name with the relation/declared index adders).
			idxName := "idx_" + resName + "_" + fieldName
			if _, exists := tbl.Indexes[idxName]; !exists {
				tbl.Indexes[idxName] = &schemadiff.Index{
					Name: idxName, Columns: []string{fieldName}, Method: "btree",
				}
			}
		}
	}
}

// fkConstraintName derives a deterministic FK constraint symbol. The diff matches
// FKs by STRUCTURE (columns + referenced table), not by symbol, so the exact name
// only has to be a valid, stable identifier for the emitted DDL (Postgres truncates
// at 63 bytes; our resource/field names are short).
func fkConstraintName(table, column string) string {
	return "fk_" + table + "_" + column
}

// refActionForOnDelete maps a schema `on_delete` string to the canonical RefAction.
// Empty (unset) and "restrict" both yield RESTRICT — the safe default.
func refActionForOnDelete(onDelete string) schemadiff.RefAction {
	switch onDelete {
	case schema.OnDeleteCascade:
		return schemadiff.Cascade
	case schema.OnDeleteSetNull:
		return schemadiff.SetNull
	default:
		return schemadiff.Restrict
	}
}

// sortedFieldNames returns a resource's field names sorted, for deterministic FK
// emission order.
func sortedFieldNames(res schema.ResourceSchema) []string {
	out := make([]string, 0, len(res.Fields))
	for n := range res.Fields {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
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
			// The access method (LIBRARY-GAPS-S1): empty means btree, so an index
			// declared before `method` existed diffs byte-identical. The opclass
			// (e.g. jsonb_path_ops) is rendered into the CREATE but excluded from
			// the diff key — the introspector cannot read one back.
			method := idx.Method
			if method == "" {
				method = schema.IndexMethodBtree
			}
			idxName := prefix + "_" + resName + "_" + strings.Join(idx.Fields, "_")
			if method != schema.IndexMethodBtree {
				// Suffix non-btree indexes so a gin index and a btree index over the
				// SAME column are two distinct indexes (different name, different diff
				// key) instead of colliding in the map. btree names are unsuffixed, so
				// every pre-existing index keeps its exact name — zero churn.
				idxName += "_" + method
			}
			tbl.Indexes[idxName] = &schemadiff.Index{
				Name:    idxName,
				Columns: append([]string(nil), idx.Fields...),
				Unique:  idx.Unique,
				Method:  method,
				Opclass: idx.Opclass,
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
