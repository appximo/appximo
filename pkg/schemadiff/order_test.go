package schemadiff_test

import (
	"testing"

	sd "github.com/miguelangel/appitools/pkg/schemadiff"
)

// linkFK adds a single-column FK (referencing the target's id) to a built table.
func linkFK(t *sd.Table, symbol, col, refTable string, onDelete sd.RefAction) {
	t.FKs[symbol] = &sd.ForeignKey{
		Symbol: symbol, Columns: []string{col},
		RefTable: refTable, RefColumns: []string{"id"}, OnDelete: onDelete,
	}
}

func dropTableOrder(plan *sd.Plan) []string {
	var out []string
	for _, op := range plan.Ops {
		if dt, ok := op.(sd.DropTable); ok {
			out = append(out, dt.Table.Name)
		}
	}
	return out
}

func indexOf(ss []string, v string) int {
	for i, s := range ss {
		if s == v {
			return i
		}
	}
	return -1
}

// ── mutual FK cycle (A↔B): create and drop both must apply ──────────────────────

func TestOrder_MutualFKCycle(t *testing.T) {
	build := func() *sd.Schema {
		a := tbl("ta", []string{"id"}, col("id", sd.BaseUUID, true), col("b_ref", sd.BaseUUID, false))
		linkFK(a, "fk_ta_tb", "b_ref", "tb", sd.NoAction)
		b := tbl("tb", []string{"id"}, col("id", sd.BaseUUID, true), col("a_ref", sd.BaseUUID, false))
		linkFK(b, "fk_tb_ta", "a_ref", "ta", sd.NoAction)
		return sch(a, b)
	}

	// create both (deferred FKs break the create cycle)
	assertConverges(t, sch(), build())
	// drop both (the drop cycle must be broken with DropForeignKey first)
	assertConverges(t, build(), sch())

	// the ordered DROP plan must drop the cyclic FKs before the tables (pure check)
	plan, err := sd.Diff(sch(), build())
	if err != nil {
		t.Fatalf("diff: %v", err)
	}
	ordered, err := sd.OrderPlan(plan)
	if err != nil {
		t.Fatalf("order: %v", err)
	}
	if countOps(ordered, func(o sd.Operation) bool { _, ok := o.(sd.DropForeignKey); return ok }) != 2 {
		t.Fatalf("expected 2 cycle-breaking DropForeignKey, got plan:\n%s", ordered)
	}
	firstDropTable, lastDropFK := -1, -1
	for i, op := range ordered.Ops {
		switch op.(type) {
		case sd.DropForeignKey:
			lastDropFK = i
		case sd.DropTable:
			if firstDropTable == -1 {
				firstDropTable = i
			}
		}
	}
	if !(lastDropFK < firstDropTable) {
		t.Errorf("DropForeignKey must precede DropTable in a drop cycle:\n%s", ordered)
	}
}

// ── self-referential FK: inline-equivalent, no special handling, applies ────────

func TestOrder_SelfReferentialFK(t *testing.T) {
	build := func() *sd.Schema {
		n := tbl("node", []string{"id"}, col("id", sd.BaseUUID, true), col("parent_id", sd.BaseUUID, false))
		linkFK(n, "fk_node_parent", "parent_id", "node", sd.NoAction)
		return sch(n)
	}
	assertConverges(t, sch(), build()) // create
	assertConverges(t, build(), sch()) // drop

	// a self-ref FK is NOT a drop cycle → no cycle-breaking DropForeignKey emitted.
	plan, _ := sd.Diff(sch(), build())
	ordered, _ := sd.OrderPlan(plan)
	if n := countOps(ordered, func(o sd.Operation) bool { _, ok := o.(sd.DropForeignKey); return ok }); n != 0 {
		t.Errorf("self-ref drop should need no DropForeignKey, got %d:\n%s", n, ordered)
	}
}

// ── 3-table cycle A→B→C→A ───────────────────────────────────────────────────────

func TestOrder_ThreeTableCycle(t *testing.T) {
	build := func() *sd.Schema {
		a := tbl("ta", []string{"id"}, col("id", sd.BaseUUID, true), col("ref", sd.BaseUUID, false))
		linkFK(a, "fk_ta", "ref", "tb", sd.NoAction)
		b := tbl("tb", []string{"id"}, col("id", sd.BaseUUID, true), col("ref", sd.BaseUUID, false))
		linkFK(b, "fk_tb", "ref", "tc", sd.NoAction)
		c := tbl("tc", []string{"id"}, col("id", sd.BaseUUID, true), col("ref", sd.BaseUUID, false))
		linkFK(c, "fk_tc", "ref", "ta", sd.NoAction)
		return sch(a, b, c)
	}
	assertConverges(t, sch(), build()) // create
	assertConverges(t, build(), sch()) // drop (3-cycle broken)
}

// ── acyclic drop chain: tc→tb→ta must drop in reverse order (referencing first) ──

func TestOrder_DropChainReverseOrder(t *testing.T) {
	a := tbl("ta", []string{"id"}, col("id", sd.BaseUUID, true))
	b := tbl("tb", []string{"id"}, col("id", sd.BaseUUID, true), col("a_ref", sd.BaseUUID, false))
	linkFK(b, "fk_tb_ta", "a_ref", "ta", sd.NoAction)
	c := tbl("tc", []string{"id"}, col("id", sd.BaseUUID, true), col("b_ref", sd.BaseUUID, false))
	linkFK(c, "fk_tc_tb", "b_ref", "tb", sd.NoAction)
	current := sch(a, b, c)

	// applies cleanly only because drops are reverse-topologically ordered
	assertConverges(t, current, sch())

	// verify the order explicitly: tc before tb before ta (referencing first), and
	// NO cycle-breaking DropForeignKey (the chain is acyclic).
	plan, _ := sd.Diff(sch(), current)
	ordered, err := sd.OrderPlan(plan)
	if err != nil {
		t.Fatalf("order: %v", err)
	}
	if n := countOps(ordered, func(o sd.Operation) bool { _, ok := o.(sd.DropForeignKey); return ok }); n != 0 {
		t.Errorf("acyclic drop should need no cycle-breaking DropForeignKey, got %d", n)
	}
	order := dropTableOrder(ordered)
	if !(indexOf(order, "tc") < indexOf(order, "tb") && indexOf(order, "tb") < indexOf(order, "ta")) {
		t.Errorf("drop order must be tc, tb, ta (referencing first), got %v", order)
	}
}

// ── create order: a referenced table is created before its referencer ───────────

func TestOrder_CreateReferencedFirst(t *testing.T) {
	parent := tbl("p", []string{"id"}, col("id", sd.BaseUUID, true))
	child := tbl("c", []string{"id"}, col("id", sd.BaseUUID, true), col("p_ref", sd.BaseUUID, false))
	linkFK(child, "fk_c_p", "p_ref", "p", sd.NoAction)
	desired := sch(parent, child)

	assertConverges(t, sch(), desired)

	plan, _ := sd.Diff(desired, sch())
	ordered, err := sd.OrderPlan(plan)
	if err != nil {
		t.Fatalf("order: %v", err)
	}
	var createOrder []string
	for _, op := range ordered.Ops {
		if ct, ok := op.(sd.CreateTable); ok {
			createOrder = append(createOrder, ct.Table.Name)
		}
	}
	if !(indexOf(createOrder, "p") < indexOf(createOrder, "c")) {
		t.Errorf("referenced table p must be created before referencer c, got %v", createOrder)
	}
}
