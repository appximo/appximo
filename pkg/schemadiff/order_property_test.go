package schemadiff_test

// Property-based test for the ORDERING: a generator that produces schemas WITH
// foreign keys (freely cross-referencing, so cycles arise naturally) and verifies
// that the ORDERED plan is EXECUTABLE against real Postgres (apply never hits a
// dependency error) and still converges. This exercises Tarjan/Kahn ordering and
// FK cycle-breaking across random shapes. A failure prints the seed.

import (
	"fmt"
	"math/rand"
	"testing"

	sd "github.com/miguelangel/appitools/pkg/schemadiff"
)

var fkActions = []sd.RefAction{sd.NoAction, sd.Cascade, sd.SetNull}

// genSchemaFK builds a valid random schema with FKs. Every table has an id uuid
// PK; FK columns are nullable uuids referencing some table's id (possibly its own,
// possibly forming a cycle). FK symbols are derived from table+column so a changed
// target re-uses the name (drop+add). No defaults/checks (focus: FK ordering).
func genSchemaFK(rng *rand.Rand) *sd.Schema {
	s := sd.NewSchema("")
	tnames := pickSubset(rng, genTableNames, 1+rng.Intn(len(genTableNames)))
	for _, tn := range tnames {
		tb := sd.NewTable(tn)
		tb.AddColumn(&sd.Column{Name: "id", Type: sd.Type{Base: sd.BaseUUID}, NotNull: true})
		tb.PK = &sd.PrimaryKey{Name: tn + "_pkey", Columns: []string{"id"}}

		for _, cn := range pickSubset(rng, []string{"c1", "c2"}, rng.Intn(3)) {
			tb.AddColumn(&sd.Column{Name: cn, Type: genTypes[rng.Intn(len(genTypes))], NotNull: rng.Intn(2) == 0})
		}

		for i := 0; i < rng.Intn(3); i++ { // 0..2 FK columns
			target := tnames[rng.Intn(len(tnames))]
			colName := fmt.Sprintf("fk%d_id", i)
			tb.AddColumn(&sd.Column{Name: colName, Type: sd.Type{Base: sd.BaseUUID}, NotNull: false})
			sym := "fk_" + tn + "_" + colName
			tb.FKs[sym] = &sd.ForeignKey{
				Symbol: sym, Columns: []string{colName},
				RefTable: target, RefColumns: []string{"id"},
				OnDelete: fkActions[rng.Intn(len(fkActions))],
			}
		}
		s.Tables[tn] = tb
	}
	return s
}

// TestProperty_DiffSelfEmpty_FK extends the diff(A,A)=∅ guardrail to FK-bearing
// schemas (pure, no DB).
func TestProperty_DiffSelfEmpty_FK(t *testing.T) {
	const base, n = int64(99999), 300
	for i := 0; i < n; i++ {
		seed := base + int64(i)
		s := genSchemaFK(rand.New(rand.NewSource(seed)))
		plan, err := sd.Diff(s, s)
		if err != nil {
			t.Fatalf("seed %d: diff(A,A): %v", seed, err)
		}
		if !plan.Empty() {
			t.Fatalf("seed %d: diff(A,A) not empty:\n%s\n-- schema --\n%s", seed, plan, render(s))
		}
		// ordering an empty plan is a no-op and must not error
		if op, err := sd.OrderPlan(plan); err != nil || !op.Empty() {
			t.Fatalf("seed %d: OrderPlan(∅) = (%v, %v), want (empty, nil)", seed, op, err)
		}
	}
}

// TestProperty_OrderedExecutable: for random FK-bearing (current, desired) pairs,
// the ORDERED plan applies against real Postgres without a dependency error and
// converges (assertConverges materializes, diffs, OrderPlans, applies, and checks
// equivalence + idempotence).
func TestProperty_OrderedExecutable(t *testing.T) {
	requireDB(t)
	const base, n = int64(31415926), 60
	for i := 0; i < n; i++ {
		seed := base + int64(i)
		rng := rand.New(rand.NewSource(seed))
		current := genSchemaFK(rng)
		desired := genSchemaFK(rng)
		t.Run(fmt.Sprintf("seed_%d", seed), func(t *testing.T) {
			assertConverges(t, current, desired)
		})
	}
}
