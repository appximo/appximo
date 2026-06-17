package schemadiff_test

// Structural schema equivalence for the diff tests: two schemas are equivalent
// when they describe the same database structure, IGNORING things a migration does
// not (and cannot cheaply) converge — the schema's own name, physical column ORDER
// (Postgres does not reorder columns), and AUTO-GENERATED constraint/index names
// (a PK keeps its old name after a table rename, etc.). This is the right notion
// of "equal" for convergence; reflect.DeepEqual is too strict here.

import (
	"sort"
	"strings"

	sd "github.com/miguelangel/appitools/pkg/schemadiff"
)

func schemaEquivalent(a, b *sd.Schema) bool {
	if len(a.Tables) != len(b.Tables) || len(a.Enums) != len(b.Enums) {
		return false
	}
	for name, ta := range a.Tables {
		tb, ok := b.Tables[name]
		if !ok || !tableEquivalent(ta, tb) {
			return false
		}
	}
	for name, ea := range a.Enums {
		eb, ok := b.Enums[name]
		if !ok || !eq(ea.Values, eb.Values) {
			return false
		}
	}
	return true
}

func tableEquivalent(a, b *sd.Table) bool {
	if len(a.Columns) != len(b.Columns) {
		return false
	}
	for n, ca := range a.Columns {
		cb, ok := b.Columns[n]
		if !ok || !columnEquivalent(ca, cb) {
			return false
		}
	}
	return pkEquivalent(a.PK, b.PK) &&
		keySetsEqual(fkKeys(a), fkKeys(b)) &&
		keySetsEqual(uniqueKeys(a), uniqueKeys(b)) &&
		keySetsEqual(checkKeys(a), checkKeys(b)) &&
		keySetsEqual(indexKeys(a), indexKeys(b))
}

func columnEquivalent(a, b *sd.Column) bool {
	if a.Type != b.Type || a.NotNull != b.NotNull {
		return false
	}
	if (a.Default == nil) != (b.Default == nil) {
		return false
	}
	if a.Default != nil && a.Default.Raw != b.Default.Raw {
		return false
	}
	if (a.Identity == nil) != (b.Identity == nil) {
		return false
	}
	if a.Identity != nil && a.Identity.Generated != b.Identity.Generated {
		return false
	}
	return true
}

func pkEquivalent(a, b *sd.PrimaryKey) bool {
	if (a == nil) != (b == nil) {
		return false
	}
	if a == nil {
		return true
	}
	return eq(a.Columns, b.Columns) // ignore the auto-derived constraint name
}

// The *Keys helpers build the same structural keys the diff matches on, so
// equivalence and the diff agree on identity (ignoring auto-generated names).

func fkKeys(t *sd.Table) []string {
	out := make([]string, 0, len(t.FKs))
	for _, fk := range t.FKs {
		out = append(out, strings.Join(fk.Columns, ",")+"=>"+fk.RefTable+"("+strings.Join(fk.RefColumns, ",")+")|del="+fk.OnDelete.String()+"|upd="+fk.OnUpdate.String())
	}
	return out
}

func uniqueKeys(t *sd.Table) []string {
	out := make([]string, 0, len(t.Uniques))
	for _, u := range t.Uniques {
		out = append(out, strings.Join(u.Columns, ","))
	}
	return out
}

func checkKeys(t *sd.Table) []string {
	out := make([]string, 0, len(t.Checks))
	for _, c := range t.Checks {
		out = append(out, c.Expression)
	}
	return out
}

func indexKeys(t *sd.Table) []string {
	out := make([]string, 0, len(t.Indexes))
	for _, i := range t.Indexes {
		out = append(out, strings.Join(i.Columns, ",")+"|u="+boolStr(i.Unique)+"|m="+i.Method+"|p="+i.Predicate)
	}
	return out
}

func boolStr(b bool) string {
	if b {
		return "t"
	}
	return "f"
}

func keySetsEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	sort.Strings(a)
	sort.Strings(b)
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// ── schema builders (concise canonical schemas for directed tests) ─────────────

func sch(tables ...*sd.Table) *sd.Schema {
	s := sd.NewSchema("")
	for _, t := range tables {
		s.Tables[t.Name] = t
	}
	return s
}

func tbl(name string, pk []string, cols ...*sd.Column) *sd.Table {
	t := sd.NewTable(name)
	for _, c := range cols {
		t.AddColumn(c)
	}
	if pk != nil {
		t.PK = &sd.PrimaryKey{Name: name + "_pkey", Columns: pk}
	}
	return t
}

func col(name string, base sd.BaseType, notNull bool) *sd.Column {
	return &sd.Column{Name: name, Type: sd.Type{Base: base}, NotNull: notNull}
}

func colT(name string, ty sd.Type, notNull bool) *sd.Column {
	return &sd.Column{Name: name, Type: ty, NotNull: notNull}
}

// ── plan assertion helpers ──────────────────────────────────────────────────────

func hasOp(plan *sd.Plan, pred func(sd.Operation) bool) bool {
	for _, op := range plan.Ops {
		if pred(op) {
			return true
		}
	}
	return false
}

func countOps(plan *sd.Plan, pred func(sd.Operation) bool) int {
	n := 0
	for _, op := range plan.Ops {
		if pred(op) {
			n++
		}
	}
	return n
}
