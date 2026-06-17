package schemadiff

import (
	"errors"
	"fmt"
	"sort"
	"strconv"
)

// This file orders a diff Plan into a dependency-safe EXECUTION order. The diff
// (diff.go) emits operations grouped into coarse phases; OrderPlan refines that
// with graph rigor: it builds the foreign-key dependency graph, finds strongly-
// connected components (cycles) with Tarjan, orders the condensation with Kahn,
// and breaks FK cycles by deferring constraints (the DetachCycles pattern).
//
// A key simplification falls out of the model: a foreign key is ALWAYS a separate
// AddForeignKey operation (never inlined into CreateTable), so create-side cycles
// are already broken — every table is created first, every FK added last, once all
// tables exist. Self-referential FKs are the same (added after the table exists),
// so they need no special handling. What ordering genuinely fixes is the DROP side
// (a plain DROP TABLE fails while a referencing table still exists) and drop-side
// cycles (mutually-referencing tables that must have an FK dropped first).

// graph is a directed graph as an adjacency list (node → out-neighbors). An edge
// u→v always means "u must be emitted before v".
type graph map[string][]string

// tarjanSCC returns the strongly-connected components of g over nodes, each as a
// slice of node names. A component of size > 1 (or a single node with a self-loop)
// is a dependency cycle. O(V+E). Recursive DFS — recursion depth is bounded by the
// number of schema objects (small), and the FINAL ordering uses iterative Kahn.
func tarjanSCC(g graph, nodes []string) [][]string {
	index := 0
	idx := make(map[string]int, len(nodes))
	low := make(map[string]int, len(nodes))
	onStack := make(map[string]bool, len(nodes))
	var stack []string
	var sccs [][]string

	var strongconnect func(v string)
	strongconnect = func(v string) {
		idx[v] = index
		low[v] = index
		index++
		stack = append(stack, v)
		onStack[v] = true

		for _, w := range g[v] {
			if _, seen := idx[w]; !seen {
				strongconnect(w)
				low[v] = min(low[v], low[w])
			} else if onStack[w] {
				low[v] = min(low[v], idx[w])
			}
		}

		if low[v] == idx[v] {
			var comp []string
			for {
				w := stack[len(stack)-1]
				stack = stack[:len(stack)-1]
				onStack[w] = false
				comp = append(comp, w)
				if w == v {
					break
				}
			}
			sccs = append(sccs, comp)
		}
	}

	for _, v := range nodes {
		if _, seen := idx[v]; !seen {
			strongconnect(v)
		}
	}
	return sccs
}

// kahnOrder returns a topological order of nodes (edge u→v ⇒ u before v) using
// Kahn's iterative in-degree algorithm — no recursion, so no stack-overflow risk
// on deep graphs. ok is false when a cycle remains (fewer nodes emitted than
// given). The ready queue is kept sorted so the order is deterministic.
func kahnOrder(g graph, nodes []string) (order []string, ok bool) {
	indeg := make(map[string]int, len(nodes))
	for _, n := range nodes {
		indeg[n] = 0
	}
	for _, u := range nodes {
		for _, v := range g[u] {
			if _, known := indeg[v]; known {
				indeg[v]++
			}
		}
	}
	var queue []string
	for _, n := range nodes {
		if indeg[n] == 0 {
			queue = append(queue, n)
		}
	}
	sort.Strings(queue)
	for len(queue) > 0 {
		u := queue[0]
		queue = queue[1:]
		order = append(order, u)
		for _, v := range g[u] {
			if _, known := indeg[v]; !known {
				continue
			}
			indeg[v]--
			if indeg[v] == 0 {
				queue = append(queue, v)
			}
		}
		sort.Strings(queue)
	}
	return order, len(order) == len(nodes)
}

// orderNodes returns a topological ordering of nodes that respects every edge
// (u→v ⇒ u before v), tolerating cycles: it finds SCCs with Tarjan, orders the
// SCC condensation (a DAG) with Kahn, and expands each SCC (sorted) in place.
// Returns the SCCs too, so a caller can break cycles. err is non-nil only on the
// impossible case of a cyclic condensation.
func orderNodes(g graph, nodes []string) (order []string, sccs [][]string, err error) {
	sccs = tarjanSCC(g, nodes)
	sccOf := make(map[string]int, len(nodes))
	for i, comp := range sccs {
		for _, n := range comp {
			sccOf[n] = i
		}
	}

	condNodes := make([]string, len(sccs))
	for i := range sccs {
		condNodes[i] = strconv.Itoa(i)
	}
	cond := graph{}
	seenEdge := map[string]bool{}
	for u, neigh := range g {
		for _, v := range neigh {
			su, sv := sccOf[u], sccOf[v]
			if su == sv {
				continue // intra-SCC edge collapses away in the condensation
			}
			key := strconv.Itoa(su) + ">" + strconv.Itoa(sv)
			if seenEdge[key] {
				continue
			}
			seenEdge[key] = true
			cu := strconv.Itoa(su)
			cond[cu] = append(cond[cu], strconv.Itoa(sv))
		}
	}

	condOrder, ok := kahnOrder(cond, condNodes)
	if !ok {
		return nil, sccs, errors.New("schemadiff: cyclic SCC condensation (internal error)")
	}
	for _, cid := range condOrder {
		i, _ := strconv.Atoi(cid)
		comp := append([]string(nil), sccs[i]...)
		sort.Strings(comp)
		order = append(order, comp...)
	}
	return order, sccs, nil
}

// OrderPlan reorders a diff Plan into a dependency-safe execution order:
//
//   - CreateTable ops are ordered topologically (a referenced table before a
//     referencing one). Because FKs are deferred to AddForeignKey at the end, this
//     is for cleanliness; create-side cycles are already broken by that deferral.
//   - DropTable ops are ordered in REVERSE (a referencing table before the one it
//     references), so a plain DROP TABLE never hits a still-referencing table.
//   - Drop-side cycles (mutually-referencing tables both being dropped) are broken
//     by emitting DropForeignKey for the cycle-internal constraints BEFORE the
//     DropTables (DetachCycles).
//
// Every other operation keeps its phase and relative order. The result is a Plan
// whose operations can be executed top-to-bottom without a dependency error.
func OrderPlan(p *Plan) (*Plan, error) {
	var (
		renameTable, renameColumn                                               []Operation
		dropFK, dropPK, dropUnique, dropCheck, dropIndex, dropColumn, dropTable []Operation
		createTable, addColumn, alterColumn                                     []Operation
		addPK, addUnique, addCheck, addIndex, addFK                             []Operation
	)
	for _, op := range p.Ops {
		switch op.(type) {
		case RenameTable:
			renameTable = append(renameTable, op)
		case RenameColumn:
			renameColumn = append(renameColumn, op)
		case DropForeignKey:
			dropFK = append(dropFK, op)
		case DropPrimaryKey:
			dropPK = append(dropPK, op)
		case DropUnique:
			dropUnique = append(dropUnique, op)
		case DropCheck:
			dropCheck = append(dropCheck, op)
		case DropIndex:
			dropIndex = append(dropIndex, op)
		case DropColumn:
			dropColumn = append(dropColumn, op)
		case DropTable:
			dropTable = append(dropTable, op)
		case CreateTable:
			createTable = append(createTable, op)
		case AddColumn:
			addColumn = append(addColumn, op)
		case AlterColumn:
			alterColumn = append(alterColumn, op)
		case AddPrimaryKey:
			addPK = append(addPK, op)
		case AddUnique:
			addUnique = append(addUnique, op)
		case AddCheck:
			addCheck = append(addCheck, op)
		case AddIndex:
			addIndex = append(addIndex, op)
		case AddForeignKey:
			addFK = append(addFK, op)
		default:
			return nil, fmt.Errorf("schemadiff: OrderPlan: unhandled operation %T", op)
		}
	}

	orderedCreates, err := orderCreates(createTable)
	if err != nil {
		return nil, err
	}
	cycleDropFK, orderedDrops, err := orderDrops(dropTable)
	if err != nil {
		return nil, err
	}
	dropFK = append(dropFK, cycleDropFK...)

	var ops []Operation
	for _, phase := range [][]Operation{
		renameTable, renameColumn,
		dropFK, dropPK, dropUnique, dropCheck, dropIndex, dropColumn, orderedDrops,
		orderedCreates, addColumn, alterColumn,
		addPK, addUnique, addCheck, addIndex, addFK,
	} {
		ops = append(ops, phase...)
	}
	return &Plan{Ops: ops}, nil
}

// orderCreates topologically orders CreateTable ops (referenced table first). FKs
// to a table not being created, and self-references, impose no ordering.
func orderCreates(creates []Operation) ([]Operation, error) {
	if len(creates) <= 1 {
		return creates, nil
	}
	names := make([]string, 0, len(creates))
	byName := make(map[string]Operation, len(creates))
	created := make(map[string]bool, len(creates))
	for _, op := range creates {
		ct := op.(CreateTable)
		names = append(names, ct.Table.Name)
		byName[ct.Table.Name] = op
		created[ct.Table.Name] = true
	}
	sort.Strings(names)

	g := graph{}
	for _, op := range creates {
		ct := op.(CreateTable)
		for _, fk := range sortedFKs(ct.Table) {
			if fk.RefTable == ct.Table.Name || !created[fk.RefTable] {
				continue
			}
			g[fk.RefTable] = appendUnique(g[fk.RefTable], ct.Table.Name) // ref before referencing
		}
	}
	order, _, err := orderNodes(g, names)
	if err != nil {
		return nil, err
	}
	out := make([]Operation, 0, len(creates))
	for _, n := range order {
		out = append(out, byName[n])
	}
	return out, nil
}

// orderDrops orders DropTable ops in reverse dependency order (a referencing table
// before the one it references) and returns the DropForeignKey ops needed to break
// any drop-side cycle first.
func orderDrops(drops []Operation) (cycleDropFK, ordered []Operation, err error) {
	if len(drops) == 0 {
		return nil, drops, nil
	}
	names := make([]string, 0, len(drops))
	byName := make(map[string]Operation, len(drops))
	tableOf := make(map[string]*Table, len(drops))
	dropped := make(map[string]bool, len(drops))
	for _, op := range drops {
		dt := op.(DropTable)
		names = append(names, dt.Table.Name)
		byName[dt.Table.Name] = op
		tableOf[dt.Table.Name] = dt.Table
		dropped[dt.Table.Name] = true
	}
	sort.Strings(names)

	g := graph{}
	for _, n := range names {
		for _, fk := range sortedFKs(tableOf[n]) {
			if fk.RefTable == n || !dropped[fk.RefTable] {
				continue
			}
			g[n] = appendUnique(g[n], fk.RefTable) // referencing before referenced
		}
	}

	order, sccs, err := orderNodes(g, names)
	if err != nil {
		return nil, nil, err
	}

	// Break each drop-side cycle: drop every constraint internal to the SCC before
	// the tables themselves, so the cyclic DROP TABLEs can proceed in any order.
	for _, comp := range sccs {
		if len(comp) <= 1 {
			continue
		}
		inSCC := make(map[string]bool, len(comp))
		for _, n := range comp {
			inSCC[n] = true
		}
		sorted := append([]string(nil), comp...)
		sort.Strings(sorted)
		for _, n := range sorted {
			for _, fk := range sortedFKs(tableOf[n]) {
				if fk.RefTable != n && inSCC[fk.RefTable] {
					cycleDropFK = append(cycleDropFK, DropForeignKey{Table: n, FK: fk})
				}
			}
		}
	}

	for _, n := range order {
		ordered = append(ordered, byName[n])
	}
	return cycleDropFK, ordered, nil
}

// sortedFKs returns a table's foreign keys ordered by symbol (deterministic).
func sortedFKs(t *Table) []*ForeignKey {
	keys := make([]string, 0, len(t.FKs))
	for k := range t.FKs {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]*ForeignKey, 0, len(keys))
	for _, k := range keys {
		out = append(out, t.FKs[k])
	}
	return out
}

func appendUnique(s []string, v string) []string {
	for _, x := range s {
		if x == v {
			return s
		}
	}
	return append(s, v)
}
