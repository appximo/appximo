package schemadiff_test

// Property-based correctness tests for the diff engine — the "gate" of this work.
// A hand-rolled deterministic generator (stdlib math/rand, no new dependency —
// matching the repo's minimal-dependency ethos) produces VALID random schemas; a
// failure prints the seed and the rendered schemas so any case is reproducible.
//
// Properties:
//   - diff(A,A) = ∅            (pure, no DB; the canonicalization/matching guardrail)
//   - convergence + idempotence: apply(diff(current→desired)) yields a schema
//     EQUIVALENT to desired, and re-diffing is empty (verified against real
//     Postgres inside assertConverges).
//
// Generator scope: tables (id uuid PK + 0..4 columns from a cast-closed type
// universe, random nullability), non-unique standalone indexes, and unique
// constraints — names derived deterministically from structure, no renames, no
// defaults. That exercises create/drop/alter(type,nullability) of tables and
// columns plus index/unique add/drop across random shapes. Renames (data
// preservation), FK referential actions, checks, defaults, and PK add/drop are
// covered with precise control in the directed tests (diff_test.go).

import (
	"fmt"
	"math/rand"
	"sort"
	"strings"
	"testing"

	sd "github.com/appximo/appximo/pkg/schemadiff"
)

var (
	genTypes = []sd.Type{
		{Base: sd.BaseText},
		{Base: sd.BaseInteger},
		{Base: sd.BaseBigint},
		{Base: sd.BaseNumeric, Prec: 10, Scale: 2},
		{Base: sd.BaseDouble},
		{Base: sd.BaseVarchar, Size: 255},
	}
	genTableNames = []string{"alpha", "beta", "gamma"}
	genColNames   = []string{"c1", "c2", "c3", "c4"}
)

// pickSubset returns a deterministic, sorted subset of pool of size k.
func pickSubset(rng *rand.Rand, pool []string, k int) []string {
	cp := append([]string(nil), pool...)
	rng.Shuffle(len(cp), func(i, j int) { cp[i], cp[j] = cp[j], cp[i] })
	if k > len(cp) {
		k = len(cp)
	}
	out := append([]string(nil), cp[:k]...)
	sort.Strings(out)
	return out
}

// genSchema builds a valid random schema (see the file comment for scope).
func genSchema(rng *rand.Rand) *sd.Schema {
	s := sd.NewSchema("")
	tnames := pickSubset(rng, genTableNames, 1+rng.Intn(len(genTableNames)))
	for _, tn := range tnames {
		tb := sd.NewTable(tn)
		tb.AddColumn(&sd.Column{Name: "id", Type: sd.Type{Base: sd.BaseUUID}, NotNull: true})
		tb.PK = &sd.PrimaryKey{Name: tn + "_pkey", Columns: []string{"id"}}

		cnames := pickSubset(rng, genColNames, rng.Intn(len(genColNames)+1))
		for _, cn := range cnames {
			tb.AddColumn(&sd.Column{
				Name:    cn,
				Type:    genTypes[rng.Intn(len(genTypes))],
				NotNull: rng.Intn(2) == 0,
			})
		}

		if len(cnames) > 0 {
			for j := 0; j < rng.Intn(3); j++ { // 0..2 non-unique indexes
				idxCols := pickSubset(rng, cnames, 1+rng.Intn(min(2, len(cnames))))
				name := "idx_" + tn + "_" + strings.Join(idxCols, "_")
				tb.Indexes[name] = &sd.Index{Name: name, Columns: idxCols, Method: "btree"}
			}
			if rng.Intn(2) == 0 { // maybe one unique constraint
				uc := cnames[rng.Intn(len(cnames))]
				name := "uq_" + tn + "_" + uc
				tb.Uniques[name] = &sd.UniqueConstraint{Symbol: name, Columns: []string{uc}}
			}
		}
		s.Tables[tn] = tb
	}
	return s
}

// TestProperty_DiffSelfEmpty: diff(A,A) = ∅ for any generated schema. Pure (no DB),
// so it runs even in -short — the canonicalization/matching guardrail.
func TestProperty_DiffSelfEmpty(t *testing.T) {
	const base, n = int64(424242), 400
	for i := 0; i < n; i++ {
		seed := base + int64(i)
		s := genSchema(rand.New(rand.NewSource(seed)))
		plan, err := sd.Diff(s, s)
		if err != nil {
			t.Fatalf("seed %d: diff(A,A) error: %v", seed, err)
		}
		if !plan.Empty() {
			t.Fatalf("seed %d: diff(A,A) is not empty:\n%s\n-- schema --\n%s", seed, plan, render(s))
		}
	}
}

// TestProperty_Convergence: for random (current, desired) pairs, applying the diff
// turns current into a schema equivalent to desired, and re-diffing is empty
// (idempotence). Verified against real Postgres (assertConverges).
func TestProperty_Convergence(t *testing.T) {
	requireDB(t) // skips in -short / no DB
	const base, n = int64(20260617), 50
	for i := 0; i < n; i++ {
		seed := base + int64(i)
		rng := rand.New(rand.NewSource(seed))
		current := genSchema(rng)
		desired := genSchema(rng)
		t.Run(fmt.Sprintf("seed_%d", seed), func(t *testing.T) {
			assertConverges(t, current, desired)
		})
	}
}
