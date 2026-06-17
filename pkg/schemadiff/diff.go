package schemadiff

import (
	"fmt"
	"sort"
	"strings"
)

// ── Operation: one typed schema-modification operator ─────────────────────────
//
// Diff produces a Plan of these, NOT SQL — rendering to safe DDL is a later
// session. Each operation is introspectable by risk: Risk()/Reversible()/
// RequiresBackfill() let a later classification/guard stage reason about a plan
// without re-deriving what each op does. (The taxonomy here is a conservative
// starting point; finer classification + backfill strategy come later.)

// OpKind enumerates the operation kinds (useful for switch-free grouping/tests).
type OpKind int

const (
	OpCreateTable OpKind = iota
	OpDropTable
	OpRenameTable
	OpAddColumn
	OpDropColumn
	OpAlterColumn
	OpRenameColumn
	OpAddPrimaryKey
	OpDropPrimaryKey
	OpAddForeignKey
	OpDropForeignKey
	OpAddUnique
	OpDropUnique
	OpAddCheck
	OpDropCheck
	OpAddIndex
	OpDropIndex
)

// RiskClass is how dangerous an operation is to apply over live data. It is a
// coarse, conservative classification — the detailed risk/backfill-strategy
// engine is a later session; these values exist so a Plan is introspectable now.
type RiskClass int

const (
	// RiskSafe — additive / non-destructive, no table rewrite, no data loss
	// (create table, add nullable column, add/drop index, drop a constraint).
	RiskSafe RiskClass = iota
	// RiskBackfill — needs existing data to already satisfy it (or a backfill) to
	// apply cleanly: add NOT NULL without default, add PK/unique/check on data.
	RiskBackfill
	// RiskDestructive — loses data (drop table, drop column).
	RiskDestructive
	// RiskTransformational — rewrites the data representation (column type change).
	RiskTransformational
)

func (r RiskClass) String() string {
	switch r {
	case RiskBackfill:
		return "backfill"
	case RiskDestructive:
		return "destructive"
	case RiskTransformational:
		return "transformational"
	default:
		return "safe"
	}
}

// Operation is one typed entry in a migration Plan.
type Operation interface {
	Kind() OpKind
	Risk() RiskClass
	// Reversible reports whether rolling the operation back does not, by itself,
	// lose pre-existing data (adding a column is reversible — dropping it only
	// loses data that did not exist before; dropping a column is not).
	Reversible() bool
	// RequiresBackfill reports whether the operation needs a data backfill (or a
	// table rewrite) to be safe over a non-empty table.
	RequiresBackfill() bool
	String() string
}

// ── table operations ──────────────────────────────────────────────────────────

type CreateTable struct{ Table *Table }

func (CreateTable) Kind() OpKind           { return OpCreateTable }
func (CreateTable) Risk() RiskClass        { return RiskSafe }
func (CreateTable) Reversible() bool       { return true }
func (CreateTable) RequiresBackfill() bool { return false }
func (o CreateTable) String() string       { return "CREATE TABLE " + o.Table.Name }

type DropTable struct{ Table *Table }

func (DropTable) Kind() OpKind           { return OpDropTable }
func (DropTable) Risk() RiskClass        { return RiskDestructive }
func (DropTable) Reversible() bool       { return false }
func (DropTable) RequiresBackfill() bool { return false }
func (o DropTable) String() string       { return "DROP TABLE " + o.Table.Name }

// RenameTable preserves data (the whole point — the converger's drop+add loses it).
type RenameTable struct{ From, To string }

func (RenameTable) Kind() OpKind           { return OpRenameTable }
func (RenameTable) Risk() RiskClass        { return RiskSafe }
func (RenameTable) Reversible() bool       { return true }
func (RenameTable) RequiresBackfill() bool { return false }
func (o RenameTable) String() string       { return "RENAME TABLE " + o.From + " -> " + o.To }

// ── column operations ──────────────────────────────────────────────────────────

type AddColumn struct {
	Table  string
	Column *Column
}

func (AddColumn) Kind() OpKind { return OpAddColumn }
func (o AddColumn) Risk() RiskClass {
	// NOT NULL with no default over an existing table needs a backfill (the
	// diagnostic's cases B/C). NOT NULL WITH a default, or a nullable column, is safe.
	if o.Column.NotNull && o.Column.Default == nil && o.Column.Identity == nil {
		return RiskBackfill
	}
	return RiskSafe
}
func (o AddColumn) Reversible() bool { return true }
func (o AddColumn) RequiresBackfill() bool {
	return o.Column.NotNull && o.Column.Default == nil && o.Column.Identity == nil
}
func (o AddColumn) String() string {
	nn := ""
	if o.Column.NotNull {
		nn = " NOT NULL"
	}
	return fmt.Sprintf("ADD COLUMN %s.%s %s%s", o.Table, o.Column.Name, o.Column.Type, nn)
}

type DropColumn struct {
	Table  string
	Column *Column
}

func (DropColumn) Kind() OpKind           { return OpDropColumn }
func (DropColumn) Risk() RiskClass        { return RiskDestructive }
func (DropColumn) Reversible() bool       { return false }
func (DropColumn) RequiresBackfill() bool { return false }
func (o DropColumn) String() string       { return fmt.Sprintf("DROP COLUMN %s.%s", o.Table, o.Column.Name) }

// AlterColumn changes a column in place. From is the current column, To the desired
// one; the specific sub-changes (type / nullability / default) are derivable.
type AlterColumn struct {
	Table string
	From  *Column
	To    *Column
}

func (AlterColumn) Kind() OpKind               { return OpAlterColumn }
func (o AlterColumn) TypeChanged() bool        { return o.From.Type != o.To.Type }
func (o AlterColumn) NullabilityAdded() bool   { return !o.From.NotNull && o.To.NotNull }
func (o AlterColumn) NullabilityDropped() bool { return o.From.NotNull && !o.To.NotNull }
func (o AlterColumn) DefaultChanged() bool     { return !exprEqual(o.From.Default, o.To.Default) }
func (o AlterColumn) Risk() RiskClass {
	switch {
	case o.TypeChanged():
		return RiskTransformational
	case o.NullabilityAdded():
		return RiskBackfill
	default:
		return RiskSafe
	}
}
func (o AlterColumn) Reversible() bool       { return !o.TypeChanged() }
func (o AlterColumn) RequiresBackfill() bool { return o.TypeChanged() || o.NullabilityAdded() }
func (o AlterColumn) String() string {
	return fmt.Sprintf("ALTER COLUMN %s.%s %s -> %s", o.Table, o.To.Name, o.From.Type, o.To.Type)
}

// RenameColumn preserves data (ALTER … RENAME COLUMN). Table is the CURRENT (post
// table-rename) table name. This is the fix for the diagnostic's worst bug.
type RenameColumn struct {
	Table    string
	From, To string
}

func (RenameColumn) Kind() OpKind           { return OpRenameColumn }
func (RenameColumn) Risk() RiskClass        { return RiskSafe }
func (RenameColumn) Reversible() bool       { return true }
func (RenameColumn) RequiresBackfill() bool { return false }
func (o RenameColumn) String() string {
	return fmt.Sprintf("RENAME COLUMN %s.%s -> %s", o.Table, o.From, o.To)
}

// ── primary key ────────────────────────────────────────────────────────────────

type AddPrimaryKey struct {
	Table string
	PK    *PrimaryKey
}

func (AddPrimaryKey) Kind() OpKind           { return OpAddPrimaryKey }
func (AddPrimaryKey) Risk() RiskClass        { return RiskBackfill } // existing data must be unique+not-null
func (AddPrimaryKey) Reversible() bool       { return true }
func (AddPrimaryKey) RequiresBackfill() bool { return true }
func (o AddPrimaryKey) String() string {
	return fmt.Sprintf("ADD PRIMARY KEY %s (%s)", o.Table, strings.Join(o.PK.Columns, ", "))
}

type DropPrimaryKey struct {
	Table string
	PK    *PrimaryKey
}

func (DropPrimaryKey) Kind() OpKind           { return OpDropPrimaryKey }
func (DropPrimaryKey) Risk() RiskClass        { return RiskSafe }
func (DropPrimaryKey) Reversible() bool       { return true }
func (DropPrimaryKey) RequiresBackfill() bool { return false }
func (o DropPrimaryKey) String() string       { return "DROP PRIMARY KEY " + o.Table }

// ── foreign keys ───────────────────────────────────────────────────────────────

type AddForeignKey struct {
	Table string
	FK    *ForeignKey
}

func (AddForeignKey) Kind() OpKind           { return OpAddForeignKey }
func (AddForeignKey) Risk() RiskClass        { return RiskBackfill } // existing rows are validated against the ref
func (AddForeignKey) Reversible() bool       { return true }
func (AddForeignKey) RequiresBackfill() bool { return false }
func (o AddForeignKey) String() string {
	return fmt.Sprintf("ADD FOREIGN KEY %s (%s) -> %s (%s)",
		o.Table, strings.Join(o.FK.Columns, ", "), o.FK.RefTable, strings.Join(o.FK.RefColumns, ", "))
}

type DropForeignKey struct {
	Table string
	FK    *ForeignKey
}

func (DropForeignKey) Kind() OpKind           { return OpDropForeignKey }
func (DropForeignKey) Risk() RiskClass        { return RiskSafe } // removes integrity, no data loss
func (DropForeignKey) Reversible() bool       { return true }
func (DropForeignKey) RequiresBackfill() bool { return false }
func (o DropForeignKey) String() string       { return "DROP FOREIGN KEY " + o.Table + "." + o.FK.Symbol }

// ── unique constraints ───────────────────────────────────────────────────────

type AddUnique struct {
	Table  string
	Unique *UniqueConstraint
}

func (AddUnique) Kind() OpKind           { return OpAddUnique }
func (AddUnique) Risk() RiskClass        { return RiskBackfill } // existing data must already be unique
func (AddUnique) Reversible() bool       { return true }
func (AddUnique) RequiresBackfill() bool { return false }
func (o AddUnique) String() string {
	return fmt.Sprintf("ADD UNIQUE %s (%s)", o.Table, strings.Join(o.Unique.Columns, ", "))
}

type DropUnique struct {
	Table  string
	Unique *UniqueConstraint
}

func (DropUnique) Kind() OpKind           { return OpDropUnique }
func (DropUnique) Risk() RiskClass        { return RiskSafe }
func (DropUnique) Reversible() bool       { return true }
func (DropUnique) RequiresBackfill() bool { return false }
func (o DropUnique) String() string       { return "DROP UNIQUE " + o.Table + "." + o.Unique.Symbol }

// ── check constraints ─────────────────────────────────────────────────────────

type AddCheck struct {
	Table string
	Check *Check
}

func (AddCheck) Kind() OpKind           { return OpAddCheck }
func (AddCheck) Risk() RiskClass        { return RiskBackfill } // existing data must satisfy it
func (AddCheck) Reversible() bool       { return true }
func (AddCheck) RequiresBackfill() bool { return false }
func (o AddCheck) String() string       { return fmt.Sprintf("ADD CHECK %s %s", o.Table, o.Check.Expression) }

type DropCheck struct {
	Table string
	Check *Check
}

func (DropCheck) Kind() OpKind           { return OpDropCheck }
func (DropCheck) Risk() RiskClass        { return RiskSafe }
func (DropCheck) Reversible() bool       { return true }
func (DropCheck) RequiresBackfill() bool { return false }
func (o DropCheck) String() string       { return "DROP CHECK " + o.Table + "." + o.Check.Symbol }

// ── indexes ────────────────────────────────────────────────────────────────────

type AddIndex struct {
	Table string
	Index *Index
}

func (AddIndex) Kind() OpKind           { return OpAddIndex }
func (AddIndex) Risk() RiskClass        { return RiskSafe }
func (AddIndex) Reversible() bool       { return true }
func (AddIndex) RequiresBackfill() bool { return false }
func (o AddIndex) String() string {
	u := ""
	if o.Index.Unique {
		u = "UNIQUE "
	}
	return fmt.Sprintf("ADD %sINDEX %s (%s)", u, o.Index.Name, strings.Join(o.Index.Columns, ", "))
}

type DropIndex struct {
	Table string
	Index *Index
}

func (DropIndex) Kind() OpKind           { return OpDropIndex }
func (DropIndex) Risk() RiskClass        { return RiskSafe }
func (DropIndex) Reversible() bool       { return true }
func (DropIndex) RequiresBackfill() bool { return false }
func (o DropIndex) String() string       { return "DROP INDEX " + o.Index.Name }

// ── Plan ───────────────────────────────────────────────────────────────────────

// Plan is the ordered list of operations that turn the current schema into the
// desired one. Operations are emitted in a coarse, dependency-respecting PHASE
// order (renames → drops → creates → adds, FKs last) that is applicable for the
// common acyclic case; the FINE topological order (inter-table dependency sort +
// FK cycle breaking) is a later session that re-orders this same op list.
type Plan struct {
	Ops []Operation
}

// Empty reports whether the plan has no operations (diff found no changes).
func (p *Plan) Empty() bool { return len(p.Ops) == 0 }

func (p *Plan) String() string {
	if p.Empty() {
		return "(empty plan)"
	}
	var b strings.Builder
	for i, op := range p.Ops {
		fmt.Fprintf(&b, "%2d. [%s] %s\n", i+1, op.Risk(), op.String())
	}
	return b.String()
}

// ── Diff ─────────────────────────────────────────────────────────────────────

// Diff computes the typed Plan that turns current into desired, in O(N+M) over the
// total number of schema objects (tables, columns, constraints, indexes) — every
// level is matched by NAME (or by STRUCTURE for auto-named constraints/indexes)
// through the model's indexed maps, never an O(N·M) pairwise scan.
//
// RENAMES ARE BY EXPLICIT INTENT (the model's RenamedFrom), never a heuristic, and
// are resolved FIRST so a renamed table/column is matched to its old self and
// emitted as a RENAME — never the converger's drop+add that strands the data
// (docs/MIGRATION_DIAG.md case F). A malformed rename intent (RenamedFrom naming a
// table/column that does not exist in current) is an error, not a silent drop+add.
func Diff(desired, current *Schema) (*Plan, error) {
	b := &planBuilder{}

	consumedCurrent := map[string]bool{} // current tables consumed by a rename
	handledDesired := map[string]bool{}  // desired tables already emitted (rename)
	matchedCurrent := map[string]bool{}  // current tables matched by name

	// Pass 1 — table renames (consume names on both sides before drop/add matching).
	for _, name := range sortedTableNames(desired) {
		dt := desired.Tables[name]
		if dt.RenamedFrom == "" {
			continue
		}
		ct, ok := current.Tables[dt.RenamedFrom]
		if !ok {
			return nil, fmt.Errorf("schemadiff: table %q declares RenamedFrom %q, which does not exist in the current schema", dt.Name, dt.RenamedFrom)
		}
		if consumedCurrent[dt.RenamedFrom] {
			return nil, fmt.Errorf("schemadiff: current table %q is the rename source of more than one desired table", dt.RenamedFrom)
		}
		b.renameTable = append(b.renameTable, RenameTable{From: dt.RenamedFrom, To: dt.Name})
		consumedCurrent[dt.RenamedFrom] = true
		handledDesired[dt.Name] = true
		if err := diffTableContents(b, dt, ct); err != nil {
			return nil, err
		}
	}

	// Pass 2 — matched-by-name tables + creates.
	for _, name := range sortedTableNames(desired) {
		if handledDesired[name] {
			continue
		}
		dt := desired.Tables[name]
		if ct, ok := current.Tables[name]; ok && !consumedCurrent[name] {
			matchedCurrent[name] = true
			if err := diffTableContents(b, dt, ct); err != nil {
				return nil, err
			}
		} else {
			b.createTable = append(b.createTable, CreateTable{Table: dt})
			// A brand-new table's columns + PK ride the CreateTable; its
			// constraints/indexes/FKs are emitted as adds (FKs land in the last
			// phase, after every table exists).
			diffConstraints(b, dt, nil)
		}
	}

	// Pass 3 — drops (current tables neither renamed away nor matched).
	for _, name := range sortedTableNames(current) {
		if consumedCurrent[name] || matchedCurrent[name] {
			continue
		}
		b.dropTable = append(b.dropTable, DropTable{Table: current.Tables[name]})
	}

	return b.plan(), nil
}

// diffTableContents diffs a matched (or renamed) table pair: columns, PK, then the
// structurally-matched constraints/indexes. The table identifier used in emitted
// ops is the DESIRED (post-rename) name.
func diffTableContents(b *planBuilder, dt, ct *Table) error {
	if err := diffColumns(b, dt, ct); err != nil {
		return err
	}
	diffPK(b, dt, ct)
	diffConstraints(b, dt, ct)
	return nil
}

func diffColumns(b *planBuilder, dt, ct *Table) error {
	table := dt.Name
	consumed := map[string]bool{} // current columns consumed by a rename
	handled := map[string]bool{}  // desired columns already emitted (rename)
	matched := map[string]bool{}

	// Pass 1 — column renames.
	for _, name := range sortedColumnNames(dt) {
		dc := dt.Columns[name]
		if dc.RenamedFrom == "" {
			continue
		}
		cc, ok := ct.Columns[dc.RenamedFrom]
		if !ok {
			return fmt.Errorf("schemadiff: column %s.%s declares RenamedFrom %q, which does not exist in the current table", table, dc.Name, dc.RenamedFrom)
		}
		if consumed[dc.RenamedFrom] {
			return fmt.Errorf("schemadiff: current column %s.%s is the rename source of more than one desired column", table, dc.RenamedFrom)
		}
		b.renameColumn = append(b.renameColumn, RenameColumn{Table: table, From: dc.RenamedFrom, To: dc.Name})
		consumed[dc.RenamedFrom] = true
		handled[dc.Name] = true
		if !columnAttrsEqual(cc, dc) {
			b.alterColumn = append(b.alterColumn, AlterColumn{Table: table, From: cc, To: dc})
		}
	}

	// Pass 2 — matched-by-name columns + adds.
	for _, name := range sortedColumnNames(dt) {
		if handled[name] {
			continue
		}
		dc := dt.Columns[name]
		if cc, ok := ct.Columns[name]; ok && !consumed[name] {
			matched[name] = true
			if !columnAttrsEqual(cc, dc) {
				b.alterColumn = append(b.alterColumn, AlterColumn{Table: table, From: cc, To: dc})
			}
		} else {
			b.addColumn = append(b.addColumn, AddColumn{Table: table, Column: dc})
		}
	}

	// Pass 3 — drops.
	for _, name := range sortedColumnNames(ct) {
		if consumed[name] || matched[name] {
			continue
		}
		b.dropColumn = append(b.dropColumn, DropColumn{Table: table, Column: ct.Columns[name]})
	}
	return nil
}

func diffPK(b *planBuilder, dt, ct *Table) {
	table := dt.Name
	dp, cp := dt.PK, ct.PK
	switch {
	case dp == nil && cp == nil:
		return
	case cp == nil: // add
		b.addPK = append(b.addPK, AddPrimaryKey{Table: table, PK: dp})
	case dp == nil: // drop
		b.dropPK = append(b.dropPK, DropPrimaryKey{Table: table, PK: cp})
	case !stringsEqual(dp.Columns, cp.Columns): // replace (compare by columns; the
		// constraint NAME is auto-derived and not semantically meaningful)
		b.dropPK = append(b.dropPK, DropPrimaryKey{Table: table, PK: cp})
		b.addPK = append(b.addPK, AddPrimaryKey{Table: table, PK: dp})
	}
}

// diffConstraints diffs FKs, unique constraints, checks and standalone indexes by
// STRUCTURE (not by auto-generated symbol/name), so a renamed-but-identical
// constraint is not churned as drop+add. ct may be nil (a brand-new table: every
// constraint is an add).
func diffConstraints(b *planBuilder, dt, ct *Table) {
	table := dt.Name

	// Foreign keys — match on (columns, ref table, ref columns); a changed
	// referential action (ON DELETE/UPDATE) is a drop + add of the FK.
	curFK := map[string]*ForeignKey{}
	if ct != nil {
		for _, fk := range ct.FKs {
			curFK[fkKey(fk)] = fk
		}
	}
	desFK := map[string]*ForeignKey{}
	for _, fk := range dt.FKs {
		desFK[fkKey(fk)] = fk
	}
	for _, k := range sortedKeysOf(desFK) {
		dfk := desFK[k]
		cfk, ok := curFK[k]
		if !ok {
			b.addFK = append(b.addFK, AddForeignKey{Table: table, FK: dfk})
		} else if cfk.OnDelete != dfk.OnDelete || cfk.OnUpdate != dfk.OnUpdate {
			b.dropFK = append(b.dropFK, DropForeignKey{Table: table, FK: cfk})
			b.addFK = append(b.addFK, AddForeignKey{Table: table, FK: dfk})
		}
	}
	for _, k := range sortedKeysOf(curFK) {
		if _, ok := desFK[k]; !ok {
			b.dropFK = append(b.dropFK, DropForeignKey{Table: table, FK: curFK[k]})
		}
	}

	// Unique constraints — match on column list.
	curU := map[string]*UniqueConstraint{}
	if ct != nil {
		for _, u := range ct.Uniques {
			curU[uniqueKey(u)] = u
		}
	}
	desU := map[string]*UniqueConstraint{}
	for _, u := range dt.Uniques {
		desU[uniqueKey(u)] = u
	}
	for _, k := range sortedKeysOf(desU) {
		if _, ok := curU[k]; !ok {
			b.addUnique = append(b.addUnique, AddUnique{Table: table, Unique: desU[k]})
		}
	}
	for _, k := range sortedKeysOf(curU) {
		if _, ok := desU[k]; !ok {
			b.dropUnique = append(b.dropUnique, DropUnique{Table: table, Unique: curU[k]})
		}
	}

	// Check constraints — match on predicate text.
	curC := map[string]*Check{}
	if ct != nil {
		for _, c := range ct.Checks {
			curC[c.Expression] = c
		}
	}
	desC := map[string]*Check{}
	for _, c := range dt.Checks {
		desC[c.Expression] = c
	}
	for _, k := range sortedKeysOf(desC) {
		if _, ok := curC[k]; !ok {
			b.addCheck = append(b.addCheck, AddCheck{Table: table, Check: desC[k]})
		}
	}
	for _, k := range sortedKeysOf(curC) {
		if _, ok := desC[k]; !ok {
			b.dropCheck = append(b.dropCheck, DropCheck{Table: table, Check: curC[k]})
		}
	}

	// Standalone indexes — match on (columns, unique, method, predicate). A change
	// to any of those is a different index → drop old + add new.
	curI := map[string]*Index{}
	if ct != nil {
		for _, idx := range ct.Indexes {
			curI[indexKey(idx)] = idx
		}
	}
	desI := map[string]*Index{}
	for _, idx := range dt.Indexes {
		desI[indexKey(idx)] = idx
	}
	for _, k := range sortedKeysOf(desI) {
		if _, ok := curI[k]; !ok {
			b.addIndex = append(b.addIndex, AddIndex{Table: table, Index: desI[k]})
		}
	}
	for _, k := range sortedKeysOf(curI) {
		if _, ok := desI[k]; !ok {
			b.dropIndex = append(b.dropIndex, DropIndex{Table: table, Index: curI[k]})
		}
	}
}

// ── planBuilder: collects ops per phase, assembles in a safe default order ──────

type planBuilder struct {
	renameTable, renameColumn                   []Operation
	dropFK, dropPK, dropUnique, dropCheck       []Operation
	dropIndex, dropColumn, dropTable            []Operation
	createTable, addColumn, alterColumn         []Operation
	addPK, addUnique, addCheck, addIndex, addFK []Operation
}

// plan concatenates the phases in dependency-respecting order: renames first (free
// up names, preserve data), then drops (constraints before the columns/tables they
// depend on), then creates, then adds (FKs last, once every table exists).
func (b *planBuilder) plan() *Plan {
	var ops []Operation
	for _, phase := range [][]Operation{
		b.renameTable, b.renameColumn,
		b.dropFK, b.dropPK, b.dropUnique, b.dropCheck, b.dropIndex, b.dropColumn, b.dropTable,
		b.createTable, b.addColumn, b.alterColumn,
		b.addPK, b.addUnique, b.addCheck, b.addIndex, b.addFK,
	} {
		ops = append(ops, phase...)
	}
	return &Plan{Ops: ops}
}

// ── matching keys + equality helpers ───────────────────────────────────────────

func fkKey(fk *ForeignKey) string {
	return strings.Join(fk.Columns, ",") + "=>" + fk.RefTable + "(" + strings.Join(fk.RefColumns, ",") + ")"
}

func uniqueKey(u *UniqueConstraint) string { return strings.Join(u.Columns, ",") }

func indexKey(i *Index) string {
	return fmt.Sprintf("%s|u=%t|m=%s|p=%s", strings.Join(i.Columns, ","), i.Unique, i.Method, i.Predicate)
}

// columnAttrsEqual compares everything that defines a column EXCEPT its name and
// rename intent (those are handled by the rename pass) — so a matched/renamed
// column with identical type/nullability/default/identity yields no AlterColumn.
func columnAttrsEqual(a, b *Column) bool {
	return a.Type == b.Type &&
		a.NotNull == b.NotNull &&
		exprEqual(a.Default, b.Default) &&
		identityEqual(a.Identity, b.Identity)
}

func exprEqual(a, b *Expr) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.Raw == b.Raw
}

func identityEqual(a, b *Identity) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.Generated == b.Generated
}

func stringsEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// ── sorted iteration (deterministic plans) ──────────────────────────────────────

func sortedTableNames(s *Schema) []string {
	out := make([]string, 0, len(s.Tables))
	for n := range s.Tables {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

func sortedColumnNames(t *Table) []string {
	out := make([]string, 0, len(t.Columns))
	for n := range t.Columns {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

func sortedKeysOf[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
