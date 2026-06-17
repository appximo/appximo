package schemadiff

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// Querier is the minimal read surface Introspect needs. *pgxpool.Pool, *pgxpool.Conn
// and pgx.Tx all satisfy it. Pass a transaction (REPEATABLE READ) when a snapshot
// consistent across the four catalog queries matters; a pool is fine for a
// quiescent schema.
type Querier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

// Introspect reads the REAL state of the named Postgres schema from pg_catalog
// (never information_schema — pg_catalog is faster and exposes the referential
// actions, identity kind and partial-index predicates information_schema hides)
// and materializes it into the canonical model.
//
// It runs a FIXED four queries regardless of table count — columns, constraints,
// indexes, enums — grouping by table in memory, never one-query-per-table (no
// N+1). Column references in constraints and indexes are resolved structurally
// via attribute numbers, not by parsing DDL text.
//
// Introspect is deterministic: introspecting the same unchanged schema twice
// yields reflect.DeepEqual Schemas — the property the future diff relies on so
// that diff(Introspect(x), Introspect(x)) is empty.
func Introspect(ctx context.Context, q Querier, schemaName string) (*Schema, error) {
	s := NewSchema(schemaName)
	// relid → attnum → column name, built from the columns query and reused to
	// resolve PK / FK / unique / index columns without parsing catalog DDL text.
	attnum := make(map[int64]map[int32]string)

	if err := introspectColumns(ctx, q, schemaName, s, attnum); err != nil {
		return nil, fmt.Errorf("columns: %w", err)
	}
	if err := introspectConstraints(ctx, q, schemaName, s, attnum); err != nil {
		return nil, fmt.Errorf("constraints: %w", err)
	}
	if err := introspectIndexes(ctx, q, schemaName, s, attnum); err != nil {
		return nil, fmt.Errorf("indexes: %w", err)
	}
	if err := introspectEnums(ctx, q, schemaName, s); err != nil {
		return nil, fmt.Errorf("enums: %w", err)
	}
	return s, nil
}

// ── query 1: tables + columns ────────────────────────────────────────────────

const columnsQuery = `
SELECT c.oid::int8                          AS relid,
       c.relname                            AS table_name,
       a.attnum::int                        AS attnum,
       a.attname                            AS column_name,
       format_type(a.atttypid, a.atttypmod) AS data_type,
       a.attnotnull                         AS not_null,
       a.attidentity::text                  AS identity,
       pg_get_expr(ad.adbin, ad.adrelid)    AS default_expr
FROM pg_class c
JOIN pg_namespace n ON n.oid = c.relnamespace
JOIN pg_attribute a ON a.attrelid = c.oid
LEFT JOIN pg_attrdef ad ON ad.adrelid = c.oid AND ad.adnum = a.attnum
WHERE n.nspname = $1
  AND c.relkind IN ('r', 'p')
  AND a.attnum > 0
  AND NOT a.attisdropped
ORDER BY c.relname, a.attnum`

func introspectColumns(ctx context.Context, q Querier, schemaName string, s *Schema, attnum map[int64]map[int32]string) error {
	rows, err := q.Query(ctx, columnsQuery, schemaName)
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		var (
			relid      int64
			table      string
			attn       int32
			colName    string
			dataType   string
			notNull    bool
			identity   string
			defaultExp pgtype.Text
		)
		if err := rows.Scan(&relid, &table, &attn, &colName, &dataType, &notNull, &identity, &defaultExp); err != nil {
			return err
		}

		tbl := s.Tables[table]
		if tbl == nil {
			tbl = NewTable(table)
			s.Tables[table] = tbl
		}

		col := &Column{Name: colName, Type: ParseType(dataType), NotNull: notNull}
		switch identity {
		case "a":
			col.Identity = &Identity{Generated: "always"}
		case "d":
			col.Identity = &Identity{Generated: "by_default"}
		default:
			// Legacy serial: integer column whose default is nextval(...). Model it
			// as Identity "serial"; the nextval default is implied, so we do NOT also
			// record it (keeps a serial a single canonical fact, not two).
			if defaultExp.Valid && strings.HasPrefix(strings.TrimSpace(defaultExp.String), "nextval(") {
				col.Identity = &Identity{Generated: "serial"}
			} else if defaultExp.Valid {
				col.Default = &Expr{Raw: defaultExp.String}
			}
		}
		tbl.AddColumn(col)

		if attnum[relid] == nil {
			attnum[relid] = make(map[int32]string)
		}
		attnum[relid][attn] = colName
	}
	return rows.Err()
}

// ── query 2: constraints (PK / FK / UNIQUE / CHECK) ──────────────────────────

const constraintsQuery = `
SELECT con.conname                          AS name,
       con.contype::text                    AS contype,
       conrel.relname                       AS table_name,
       conrel.oid::int8                     AS conrelid,
       COALESCE(con.conkey, '{}')::int[]    AS conkey,
       COALESCE(confrel.relname, '')        AS ref_table,
       COALESCE(confrel.oid, 0)::int8       AS confrelid,
       COALESCE(con.confkey, '{}')::int[]   AS confkey,
       con.confdeltype::text                AS confdeltype,
       con.confupdtype::text                AS confupdtype,
       pg_get_constraintdef(con.oid)        AS def
FROM pg_constraint con
JOIN pg_class conrel ON conrel.oid = con.conrelid
JOIN pg_namespace n ON n.oid = conrel.relnamespace
LEFT JOIN pg_class confrel ON confrel.oid = con.confrelid
WHERE n.nspname = $1
  AND con.contype IN ('p', 'f', 'u', 'c')
ORDER BY conrel.relname, con.conname`

func introspectConstraints(ctx context.Context, q Querier, schemaName string, s *Schema, attnum map[int64]map[int32]string) error {
	rows, err := q.Query(ctx, constraintsQuery, schemaName)
	if err != nil {
		return err
	}
	defer rows.Close()

	// resolve maps attribute numbers on relid to column names via the columns-query
	// map. ok is false if any attnum is unknown (e.g. a cross-schema referenced
	// table this introspection did not load) so the caller can fall back to def text.
	resolve := func(relid int64, keys []int32) (cols []string, ok bool) {
		m := attnum[relid]
		cols = make([]string, 0, len(keys))
		for _, k := range keys {
			name, found := m[k]
			if !found {
				return nil, false
			}
			cols = append(cols, name)
		}
		return cols, true
	}

	for rows.Next() {
		var (
			name, contype, table, refTable, confdel, confupd, def string
			conrelid, confrelid                                   int64
			conkey, confkey                                       []int32
		)
		if err := rows.Scan(&name, &contype, &table, &conrelid, &conkey, &refTable, &confrelid, &confkey, &confdel, &confupd, &def); err != nil {
			return err
		}
		tbl := s.Tables[table]
		if tbl == nil {
			continue // table not in the columns result (should not happen for r/p)
		}

		switch contype {
		case "p":
			cols, ok := resolve(conrelid, conkey)
			if !ok {
				cols = colsInParens(def)
			}
			tbl.PK = &PrimaryKey{Name: name, Columns: cols}
		case "u":
			cols, ok := resolve(conrelid, conkey)
			if !ok {
				cols = colsInParens(def)
			}
			tbl.Uniques[name] = &UniqueConstraint{Symbol: name, Columns: cols}
		case "f":
			cols, ok := resolve(conrelid, conkey)
			if !ok {
				cols = colsInParens(def) // first "(...)" group = local columns
			}
			refCols, ok := resolve(confrelid, confkey)
			if !ok {
				refCols = refColsInDef(def) // REFERENCES tbl(...) — handles cross-schema refs
			}
			tbl.FKs[name] = &ForeignKey{
				Symbol:     name,
				Columns:    cols,
				RefTable:   refTable,
				RefColumns: refCols,
				OnDelete:   parseRefAction(confdel),
				OnUpdate:   parseRefAction(confupd),
			}
		case "c":
			tbl.Checks[name] = &Check{Symbol: name, Expression: normalizeCheck(def)}
		}
	}
	return rows.Err()
}

// ── query 3: standalone indexes ──────────────────────────────────────────────
//
// Excludes any index that backs a constraint (con.conindid) — PK and unique
// constraints are modeled as PK / Uniques, so only standalone CREATE INDEX /
// CREATE UNIQUE INDEX definitions remain here. indkey is rendered to a space-
// separated text then to int[] for clean scanning; a 0 entry is an expression key.
const indexesQuery = `
SELECT t.relname                                   AS table_name,
       t.oid::int8                                 AS relid,
       ic.relname                                  AS index_name,
       i.indisunique                               AS is_unique,
       am.amname                                   AS method,
       string_to_array(i.indkey::text, ' ')::int[] AS keyattnums,
       pg_get_expr(i.indpred, i.indrelid)          AS predicate
FROM pg_index i
JOIN pg_class ic ON ic.oid = i.indexrelid
JOIN pg_class t ON t.oid = i.indrelid
JOIN pg_namespace n ON n.oid = t.relnamespace
JOIN pg_am am ON am.oid = ic.relam
WHERE n.nspname = $1
  AND t.relkind IN ('r', 'p')
  AND NOT EXISTS (SELECT 1 FROM pg_constraint con WHERE con.conindid = i.indexrelid)
ORDER BY t.relname, ic.relname`

func introspectIndexes(ctx context.Context, q Querier, schemaName string, s *Schema, attnum map[int64]map[int32]string) error {
	rows, err := q.Query(ctx, indexesQuery, schemaName)
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		var (
			table, idxName, method string
			relid                  int64
			isUnique               bool
			keyattnums             []int32
			predicate              pgtype.Text
		)
		if err := rows.Scan(&table, &relid, &idxName, &isUnique, &method, &keyattnums, &predicate); err != nil {
			return err
		}
		tbl := s.Tables[table]
		if tbl == nil {
			continue
		}
		m := attnum[relid]
		cols := make([]string, 0, len(keyattnums))
		for _, k := range keyattnums {
			if k == 0 {
				cols = append(cols, "(expression)") // expression index key
				continue
			}
			if n, ok := m[k]; ok {
				cols = append(cols, n)
			} else {
				cols = append(cols, fmt.Sprintf("att%d", k))
			}
		}
		idx := &Index{Name: idxName, Columns: cols, Unique: isUnique, Method: method}
		if predicate.Valid {
			idx.Predicate = predicate.String
		}
		tbl.Indexes[idxName] = idx
	}
	return rows.Err()
}

// ── query 4: enum types ──────────────────────────────────────────────────────

const enumsQuery = `
SELECT typ.typname AS enum_name, e.enumlabel AS label
FROM pg_type typ
JOIN pg_enum e ON e.enumtypid = typ.oid
JOIN pg_namespace n ON n.oid = typ.typnamespace
WHERE n.nspname = $1
ORDER BY typ.typname, e.enumsortorder`

func introspectEnums(ctx context.Context, q Querier, schemaName string, s *Schema) error {
	rows, err := q.Query(ctx, enumsQuery, schemaName)
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		var enumName, label string
		if err := rows.Scan(&enumName, &label); err != nil {
			return err
		}
		et := s.Enums[enumName]
		if et == nil {
			et = &EnumType{Name: enumName}
			s.Enums[enumName] = et
		}
		et.Values = append(et.Values, label)
	}
	return rows.Err()
}

// ── DDL-text fallbacks (used only when structural attnum resolution can't, e.g.
//    a foreign key whose referenced table lives in another, un-introspected
//    schema). Our identifiers match ^[a-z][a-z0-9_]*$, so comma-splitting is safe.

var reReferences = regexp.MustCompile(`REFERENCES\s+[^(]+\(([^)]*)\)`)

// colsInParens returns the columns inside the FIRST "(...)" group of a constraint
// definition (PRIMARY KEY (a, b) → [a b]; FOREIGN KEY (a, b) … → [a b]).
func colsInParens(def string) []string {
	i := strings.Index(def, "(")
	if i < 0 {
		return nil
	}
	j := strings.Index(def[i:], ")")
	if j < 0 {
		return nil
	}
	return splitCols(def[i+1 : i+j])
}

// refColsInDef returns the referenced columns from a FK definition's
// "REFERENCES tbl(x, y)" clause.
func refColsInDef(def string) []string {
	m := reReferences.FindStringSubmatch(def)
	if len(m) < 2 {
		return nil
	}
	return splitCols(m[1])
}

func splitCols(s string) []string {
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		p = strings.Trim(p, `"`)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// normalizeCheck strips the leading "CHECK " keyword from a check-constraint
// definition, leaving the predicate text (e.g. "CHECK ((qty >= 0))" → "((qty >= 0))").
func normalizeCheck(def string) string {
	return strings.TrimSpace(strings.TrimPrefix(def, "CHECK"))
}
