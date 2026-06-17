package schemadiff

import (
	"sort"
	"testing"
)

// normalizeSCCs sorts within each component and orders components by their first
// element, so SCC sets can be compared regardless of discovery order.
func normalizeSCCs(sccs [][]string) [][]string {
	out := make([][]string, 0, len(sccs))
	for _, comp := range sccs {
		c := append([]string(nil), comp...)
		sort.Strings(c)
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i][0] < out[j][0] })
	return out
}

func sameSCCs(a, b [][]string) bool {
	a, b = normalizeSCCs(a), normalizeSCCs(b)
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if len(a[i]) != len(b[i]) {
			return false
		}
		for j := range a[i] {
			if a[i][j] != b[i][j] {
				return false
			}
		}
	}
	return true
}

func TestTarjanSCC(t *testing.T) {
	cases := []struct {
		name  string
		g     graph
		nodes []string
		want  [][]string
	}{
		{
			name:  "dag, no cycle — each node its own component",
			g:     graph{"a": {"b"}, "b": {"c"}},
			nodes: []string{"a", "b", "c"},
			want:  [][]string{{"a"}, {"b"}, {"c"}},
		},
		{
			name:  "simple 2-cycle",
			g:     graph{"a": {"b"}, "b": {"a"}},
			nodes: []string{"a", "b"},
			want:  [][]string{{"a", "b"}},
		},
		{
			name:  "self loop",
			g:     graph{"a": {"a"}},
			nodes: []string{"a"},
			want:  [][]string{{"a"}},
		},
		{
			name:  "3-cycle",
			g:     graph{"a": {"b"}, "b": {"c"}, "c": {"a"}},
			nodes: []string{"a", "b", "c"},
			want:  [][]string{{"a", "b", "c"}},
		},
		{
			name:  "multiple components: one cycle, two singletons",
			g:     graph{"a": {"b"}, "b": {"a"}, "c": {"d"}},
			nodes: []string{"a", "b", "c", "d"},
			want:  [][]string{{"a", "b"}, {"c"}, {"d"}},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := tarjanSCC(c.g, c.nodes)
			if !sameSCCs(got, c.want) {
				t.Errorf("tarjanSCC = %v, want %v", normalizeSCCs(got), normalizeSCCs(c.want))
			}
		})
	}
}

func TestKahnOrder(t *testing.T) {
	t.Run("dag yields a valid topological order", func(t *testing.T) {
		// edges: a→b, a→c, b→d, c→d  (a first, d last)
		g := graph{"a": {"b", "c"}, "b": {"d"}, "c": {"d"}}
		nodes := []string{"a", "b", "c", "d"}
		order, ok := kahnOrder(g, nodes)
		if !ok {
			t.Fatalf("expected a valid order for a DAG, got cycle")
		}
		pos := map[string]int{}
		for i, n := range order {
			pos[n] = i
		}
		for u, neigh := range g {
			for _, v := range neigh {
				if pos[u] >= pos[v] {
					t.Errorf("edge %s→%s violated: %s at %d not before %s at %d", u, v, u, pos[u], v, pos[v])
				}
			}
		}
	})

	t.Run("deterministic order (sorted ready set)", func(t *testing.T) {
		g := graph{"a": {"c"}, "b": {"c"}}
		nodes := []string{"a", "b", "c"}
		o1, _ := kahnOrder(g, nodes)
		o2, _ := kahnOrder(g, nodes)
		want := []string{"a", "b", "c"}
		for i := range want {
			if o1[i] != want[i] || o2[i] != want[i] {
				t.Fatalf("non-deterministic/unsorted order: %v / %v, want %v", o1, o2, want)
			}
		}
	})

	t.Run("cycle is detected (ok=false)", func(t *testing.T) {
		g := graph{"a": {"b"}, "b": {"a"}}
		if _, ok := kahnOrder(g, []string{"a", "b"}); ok {
			t.Error("expected ok=false for a cyclic graph")
		}
	})
}

// TestOrderNodes_CyclesTolerated: orderNodes returns a full ordering even with a
// cycle (the SCC is collapsed), placing a dependency component before its dependents.
func TestOrderNodes_CyclesTolerated(t *testing.T) {
	// a↔b form a cycle; c depends on the cycle (edge a→c so a before c).
	g := graph{"a": {"b", "c"}, "b": {"a"}}
	order, sccs, err := orderNodes(g, []string{"a", "b", "c"})
	if err != nil {
		t.Fatalf("orderNodes: %v", err)
	}
	if len(order) != 3 {
		t.Fatalf("expected all 3 nodes ordered, got %v", order)
	}
	// the {a,b} cycle must be reported as one SCC
	foundCycle := false
	for _, comp := range sccs {
		if len(comp) == 2 {
			foundCycle = true
		}
	}
	if !foundCycle {
		t.Errorf("expected a 2-node SCC for the a↔b cycle, got %v", normalizeSCCs(sccs))
	}
	// c (dependent of the cycle) comes last
	if order[len(order)-1] != "c" {
		t.Errorf("expected c last (it depends on the cycle), got order %v", order)
	}
}
