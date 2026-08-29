package subset

import (
	"fmt"
	"sort"

	"github.com/bhuneshvar-k/tributary/internal/graph"
)

// TableOrder returns the tables of g in dependency order — parents before
// children — via Kahn's algorithm over the table-level graph. Self-
// referencing edges (a table pointing at itself) are excluded from
// in-degree counting: they never constrain ordering relative to other
// tables, and counting them would deadlock Kahn's algorithm on that table
// alone.
//
// A cycle spanning more than one table has no true topological order and
// is reported as an error naming the tables still unresolved when the
// algorithm stalls — such a cycle needs a dependency_breaks entry (see
// pkg/config and internal/graph.Build) before a table order can be
// computed at all.
//
// Output is deterministic: ties (multiple tables becoming available at
// once) are broken alphabetically by NodeID.
func TableOrder(g *graph.Graph) ([]graph.NodeID, error) {
	// indegree[n] counts n's own unresolved dependencies: one per distinct
	// parent target across n's outgoing edges (a polymorphic edge with two
	// possible targets counts as two, since either could apply and both
	// must be safely ordered first).
	indegree := make(map[graph.NodeID]int, len(g.Nodes))
	for id := range g.Nodes {
		indegree[id] = 0
	}
	for from, edges := range g.Outgoing {
		for _, e := range edges {
			for _, to := range e.Targets() {
				if to == from {
					continue // self-loop: doesn't constrain ordering
				}
				indegree[from]++
			}
		}
	}

	// childrenOf(parent) is every table with an edge (regular or
	// polymorphic) pointing at parent — i.e. every table whose indegree
	// drops once parent is placed.
	childrenOf := func(parent graph.NodeID) []graph.NodeID {
		children := make([]graph.NodeID, 0, len(g.Incoming[parent])+len(g.PolyReverse[parent]))
		for _, e := range g.Incoming[parent] {
			children = append(children, e.From)
		}
		for _, pe := range g.PolyReverse[parent] {
			children = append(children, pe.Edge.From)
		}
		return children
	}

	var ready []graph.NodeID
	for id, d := range indegree {
		if d == 0 {
			ready = append(ready, id)
		}
	}
	sortNodeIDs(ready)

	order := make([]graph.NodeID, 0, len(g.Nodes))
	for len(ready) > 0 {
		n := ready[0]
		ready = ready[1:]
		order = append(order, n)

		var freed []graph.NodeID
		for _, child := range childrenOf(n) {
			if child == n {
				continue // self-loop
			}
			indegree[child]--
			if indegree[child] == 0 {
				freed = append(freed, child)
			}
		}
		sortNodeIDs(freed)
		ready = append(ready, freed...)
	}

	if len(order) != len(g.Nodes) {
		var stuck []graph.NodeID
		for id, d := range indegree {
			if d > 0 {
				stuck = append(stuck, id)
			}
		}
		sortNodeIDs(stuck)
		return nil, fmt.Errorf(
			"cannot compute a table order: %d table(s) form a cycle with no dependency_breaks entry covering it: %v",
			len(stuck), stuck)
	}

	return order, nil
}

func sortNodeIDs(ids []graph.NodeID) {
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
}
