package migration

// Time ranges in the migration engine (MOTOR-AGENDA-S1, ADR-039).
//
// A declared range materializes as (a) a CHECK that start < end (NULL bounds
// allowed — an unscheduled row) and (b), when `no_overlap` is declared, an
// EXCLUDE USING gist constraint over the scope columns (WITH =) and
// tstzrange(start, end, '[)') (WITH &&), partial on the `when` condition.
//
// The EXCLUDE is the one constraint Postgres cannot add NOT VALID: adding it
// scans the table and FAILS if two existing rows already overlap. So the
// runner applies exclusions APART from the rest of the plan, after a
// pre-check that names the colliding pairs — the safe ops land, the exclusion
// is reported as not applied with the exact rows to fix, and the schema is
// NOT persisted over a database that does not enforce it (ENG-13: declared ≠
// applied is a failure in every surface, never a ✓).

import (
	"context"
	"fmt"
	"log"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/appximo/appximo/pkg/schema"
	"github.com/appximo/appximo/pkg/schemadiff"
)

// addRanges models each resource's declared ranges into the desired schema.
func addRanges(ds *schemadiff.Schema, s *schema.APISchema, names []string) {
	for _, resName := range names {
		res := s.Resources[resName]
		if len(res.Ranges) == 0 {
			continue
		}
		tbl := ds.Tables[resName]
		if tbl == nil {
			continue
		}
		for _, rangeName := range res.RangeNames() {
			r := res.Ranges[rangeName]
			if _, ok := tbl.Columns[r.Start]; !ok {
				continue
			}
			if _, ok := tbl.Columns[r.End]; !ok {
				continue
			}
			chk := schema.RangeCheckName(resName, rangeName)
			tbl.Checks[chk] = &schemadiff.Check{Symbol: chk, Expression: schema.RangeCheckExpression(r.Start, r.End)}
			if r.NoOverlap == nil {
				continue
			}
			sym := schema.RangeExclusionName(resName, rangeName, r)
			tbl.Exclusions[sym] = &schemadiff.Exclusion{Symbol: sym, Definition: schema.RangeExclusionDefinition(r, res.Fields)}
		}
	}
}

// ensureRangeExtensions installs btree_gist (trusted in PG13+, so the tenant's
// database owner may install it) when any resource declares a no-overlap rule
// — an EXCLUDE that combines `=` on an id column with `&&` on a range needs
// it. Idempotent; per database, shared by every tenant schema.
func ensureRangeExtensions(ctx context.Context, pool *pgxpool.Pool, s *schema.APISchema) error {
	if !s.HasNoOverlap() {
		return nil
	}
	if _, err := pool.Exec(ctx, "CREATE EXTENSION IF NOT EXISTS btree_gist"); err != nil {
		return fmt.Errorf("btree_gist is required by a no_overlap rule and could not be installed (%v) — as a superuser run: CREATE EXTENSION btree_gist; (scripts/install.sh does it at setup)", err)
	}
	return nil
}

// exclusionApply is one EXCLUDE constraint to put in place, with the drop of
// the rule it supersedes when the definition changed (same table + range, new
// symbol) — applied together so the old rule never lingers alongside the new.
type exclusionApply struct {
	add  schemadiff.AddExclusion
	drop *schemadiff.DropExclusion
}

// splitExclusionOps separates the exclusion work from the rest of a plan and
// pairs each add with the drop of the same range family it replaces.
func splitExclusionOps(plan *schemadiff.Plan) (rest *schemadiff.Plan, excl []exclusionApply) {
	drops := map[string]schemadiff.DropExclusion{}
	for _, op := range plan.Ops {
		if d, ok := op.(schemadiff.DropExclusion); ok {
			drops[d.Table+"."+exclusionFamily(d.Exclusion.Symbol)] = d
		}
	}
	paired := map[string]bool{}
	keep := make([]schemadiff.Operation, 0, len(plan.Ops))
	for _, op := range plan.Ops {
		switch o := op.(type) {
		case schemadiff.AddExclusion:
			item := exclusionApply{add: o}
			key := o.Table + "." + exclusionFamily(o.Exclusion.Symbol)
			if d, ok := drops[key]; ok {
				dd := d
				item.drop = &dd
				paired[key] = true
			}
			excl = append(excl, item)
		case schemadiff.DropExclusion:
			// Handled with its add when paired; otherwise it stays in the plan
			// (where the additive policy has already decided its fate).
			if paired[o.Table+"."+exclusionFamily(o.Exclusion.Symbol)] {
				continue
			}
			// A drop that is paired LATER in the plan (add after drop) is also
			// skipped here — resolved on the second pass below.
			keep = append(keep, op)
		default:
			keep = append(keep, op)
		}
	}
	// Second pass: remove drops whose add came later in the plan order.
	final := keep[:0]
	for _, op := range keep {
		if d, ok := op.(schemadiff.DropExclusion); ok && paired[d.Table+"."+exclusionFamily(d.Exclusion.Symbol)] {
			continue
		}
		final = append(final, op)
	}
	return &schemadiff.Plan{Ops: final}, excl
}

// exclusionFamily strips the definition hash from an engine-generated
// exclusion symbol (excl_<table>_<range>_<8hex> → excl_<table>_<range>), so a
// changed rule over the same range is recognized as a replacement.
func exclusionFamily(symbol string) string {
	i := strings.LastIndex(symbol, "_")
	if i > 0 && len(symbol)-i-1 == 8 {
		return symbol[:i]
	}
	return symbol
}

// isExclusionReplacement reports whether a DropExclusion in plan is the drop
// half of a changed rule (an AddExclusion over the same family exists).
func isExclusionReplacement(plan *schemadiff.Plan, d schemadiff.DropExclusion) bool {
	fam := d.Table + "." + exclusionFamily(d.Exclusion.Symbol)
	for _, op := range plan.Ops {
		if a, ok := op.(schemadiff.AddExclusion); ok && a.Table+"."+exclusionFamily(a.Exclusion.Symbol) == fam {
			return true
		}
	}
	return false
}

// rangeOf resolves an exclusion symbol back to the (resource, range) that
// generated it, for the pre-check query.
func rangeOf(s *schema.APISchema, table, symbol string) (schema.RangeDef, string, bool) {
	res, ok := s.Resources[table]
	if !ok {
		return schema.RangeDef{}, "", false
	}
	for _, name := range res.RangeNames() {
		r := res.Ranges[name]
		if r.NoOverlap != nil && schema.RangeExclusionName(table, name, r) == symbol {
			return r, name, true
		}
	}
	return schema.RangeDef{}, "", false
}

// OverlapPair is one pair of existing rows that already violate a no-overlap
// rule about to be added.
type OverlapPair struct {
	A, B string
}

// existingOverlaps lists (up to limit) pairs of rows of pgSchema.table that
// overlap under the rule, plus the total count. This is what the dry-run
// prints and what blocks the ADD — the same predicate the constraint enforces.
func existingOverlaps(ctx context.Context, q interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}, pgSchema, table string, r schema.RangeDef, fields map[string]schema.FieldDef, limit int) (pairs []OverlapPair, total int64, err error) {
	tbl := pgx.Identifier{pgSchema, table}.Sanitize()
	q1 := func(col string) string { return "a." + pgx.Identifier{col}.Sanitize() }
	q2 := func(col string) string { return "b." + pgx.Identifier{col}.Sanitize() }
	conds := []string{
		"a.id < b.id",
		q1(r.Start) + " IS NOT NULL", q1(r.End) + " IS NOT NULL",
		q2(r.Start) + " IS NOT NULL", q2(r.End) + " IS NOT NULL",
		schema.RangeExprSQL(q1(r.Start), q1(r.End)) + " && " + schema.RangeExprSQL(q2(r.Start), q2(r.End)),
	}
	if r.NoOverlap != nil {
		for _, c := range r.NoOverlap.Scope {
			conds = append(conds, q1(c)+" = "+q2(c))
		}
		if w := r.NoOverlap.When; w != nil {
			conds = append(conds, schema.WhenSQLPrefixed(*w, fields, "a."), schema.WhenSQLPrefixed(*w, fields, "b."))
		}
	}
	where := strings.Join(conds, " AND ")
	base := " FROM " + tbl + " a JOIN " + tbl + " b ON " + where
	if err = q.QueryRow(ctx, "SELECT count(*)"+base).Scan(&total); err != nil {
		return nil, 0, err
	}
	if total == 0 {
		return nil, 0, nil
	}
	rows, err := q.Query(ctx, "SELECT a.id::text, b.id::text"+base+" ORDER BY a.id, b.id LIMIT "+fmt.Sprint(limit))
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	for rows.Next() {
		var p OverlapPair
		if err := rows.Scan(&p.A, &p.B); err != nil {
			return nil, 0, err
		}
		pairs = append(pairs, p)
	}
	return pairs, total, rows.Err()
}

// describeOverlapBlock words the reason an exclusion cannot be added.
func describeOverlapBlock(table, rangeName string, pairs []OverlapPair, total int64) string {
	var names []string
	for _, p := range pairs {
		names = append(names, p.A+" × "+p.B)
	}
	more := ""
	if int64(len(pairs)) < total {
		more = fmt.Sprintf(" (+%d more)", total-int64(len(pairs)))
	}
	return fmt.Sprintf("no_overlap %q on %s: %d existing row pair(s) already overlap — %s%s; the rule cannot be enforced until they are resolved (move, cancel or mark them as not blocking), nothing was half-applied",
		rangeName, table, total, strings.Join(names, ", "), more)
}

// applyExclusions puts each EXCLUDE constraint in place, one transaction per
// rule (with the drop of the rule it replaces in the same transaction). Before
// the ADD it runs the pre-check; a rule whose existing rows collide is NOT
// attempted and comes back in the returned list, worded with the colliding
// pairs. A rule that fails anyway (a concurrent write between check and add)
// is reported the same way.
func applyExclusions(ctx context.Context, pool *pgxpool.Pool, ex *schemadiff.Executor, pgSchema string, s *schema.APISchema, items []exclusionApply) (blocked []string) {
	for _, item := range items {
		op := item.add
		r, rangeName, ok := rangeOf(s, op.Table, op.Exclusion.Symbol)
		if ok {
			pairs, total, err := existingOverlaps(ctx, pool, pgSchema, op.Table, r, s.Resources[op.Table].Fields, 5)
			if err != nil {
				log.Printf("migration[%s]: overlap pre-check could not run for %s: %v (attempting the constraint anyway)", pgSchema, op.String(), err)
			} else if total > 0 {
				msg := describeOverlapBlock(op.Table, rangeName, pairs, total)
				log.Printf("migration[%s]: NOT APPLIED — %s", pgSchema, msg)
				blocked = append(blocked, msg)
				continue
			}
		}
		stmts, err := schemadiff.Render(&schemadiff.Plan{Ops: []schemadiff.Operation{op}})
		if err != nil || len(stmts) == 0 {
			log.Printf("migration[%s]: exclusion render failed, skipped: %s: %v", pgSchema, op.String(), err)
			blocked = append(blocked, op.String()+": could not be rendered")
			continue
		}
		batch := []string{stmts[0].SQL}
		if item.drop != nil {
			dropStmts, derr := schemadiff.Render(&schemadiff.Plan{Ops: []schemadiff.Operation{*item.drop}})
			if derr != nil || len(dropStmts) == 0 {
				log.Printf("migration[%s]: exclusion replace render failed, skipped: %s: %v", pgSchema, item.drop.String(), derr)
				blocked = append(blocked, op.String()+": replacement could not be rendered")
				continue
			}
			batch = append([]string{dropStmts[0].SQL}, batch...)
			log.Printf("migration[%s]: REPLACING no-overlap rule (its definition changed — old constraint dropped and new one added atomically): %s", pgSchema, op.String())
		}
		if err := ex.ExecBatch(ctx, batch...); err != nil {
			msg := fmt.Sprintf("%s: the database refused the no-overlap rule (%s) — existing rows overlap; nothing was half-applied", op.String(), safeErr(err))
			log.Printf("migration[%s]: NOT APPLIED — %s", pgSchema, msg)
			blocked = append(blocked, msg)
		}
	}
	return blocked
}

// safeErr keeps only the first line of a database error for a report line.
func safeErr(err error) string {
	s := err.Error()
	if i := strings.IndexByte(s, '\n'); i > 0 {
		s = s[:i]
	}
	return s
}

// exclusionConcerns computes, for a dry-run, the pre-check concerns of every
// exclusion the plan would add on an EXISTING table: which pairs already
// overlap. A brand-new table has no rows and no concern.
func exclusionConcerns(ctx context.Context, pool *pgxpool.Pool, pgSchema string, s *schema.APISchema, plan *schemadiff.Plan) []string {
	created := map[string]bool{}
	for _, op := range plan.Ops {
		if c, ok := op.(schemadiff.CreateTable); ok {
			created[c.Table.Name] = true
		}
	}
	var out []string
	for _, op := range plan.Ops {
		a, ok := op.(schemadiff.AddExclusion)
		if !ok || created[a.Table] {
			continue
		}
		r, rangeName, ok := rangeOf(s, a.Table, a.Exclusion.Symbol)
		if !ok {
			continue
		}
		pairs, total, err := existingOverlaps(ctx, pool, pgSchema, a.Table, r, s.Resources[a.Table].Fields, 5)
		if err != nil {
			out = append(out, fmt.Sprintf("[unknown] no_overlap %q on %s: the overlap pre-check could not run (%s)", rangeName, a.Table, safeErr(err)))
			continue
		}
		if total > 0 {
			out = append(out, "[blocked] "+describeOverlapBlock(a.Table, rangeName, pairs, total))
		}
	}
	return out
}
