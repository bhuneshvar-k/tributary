package graph

import "github.com/bhuneshvar-k/tributary/internal/catalog"

// NodeID identifies one table, schema-qualified ("schema.table"), matching
// the key format internal/catalog uses internally and the format
// pkg/config.ColumnRef.TableKey produces.
type NodeID string

// EdgeSource records where an edge came from.
type EdgeSource int

const (
	EdgeFromCatalog EdgeSource = iota // a real pg_catalog foreign key constraint
	EdgeDeclared                      // a user-declared relation (soft FK)
	EdgePolymorphic                   // a user-declared polymorphic association
)

func (s EdgeSource) String() string {
	switch s {
	case EdgeFromCatalog:
		return "catalog"
	case EdgeDeclared:
		return "declared"
	case EdgePolymorphic:
		return "polymorphic"
	default:
		return "unknown"
	}
}

// PolyTarget is one resolved target of a polymorphic association: the
// table and columns a given discriminator value points at.
type PolyTarget struct {
	To        NodeID
	ToColumns []string
}

// Edge is a directed reference: rows in From point at rows in To via
// FromColumns/ToColumns, paired by index (more than one pair means a
// composite key).
//
// Polymorphic edges (Source == EdgePolymorphic) are the exception: To and
// ToColumns are left zero-valued, since the target isn't fixed — it
// depends on the runtime value of the row's PolyTypeColumn. PolyTargets
// maps each possible discriminator value to its concrete target.
type Edge struct {
	From, To       NodeID
	FromColumns    []string
	ToColumns      []string
	Source         EdgeSource
	ConstraintName string // set for EdgeFromCatalog edges only

	PolyTypeColumn string                // set for EdgePolymorphic edges only
	PolyTargets    map[string]PolyTarget // discriminator value -> target; EdgePolymorphic only
}

// EdgeInCycle reports whether e is one of the edges forming a detected
// cycle — i.e. following it (via any of its FromColumns) can lead back to
// e.From through g.Cycles. Used by the closure algorithm to decide whether
// an edge needs dependency_breaks handling before being followed.
func (g *Graph) EdgeInCycle(e Edge) bool {
	for _, c := range e.FromColumns {
		if g.HasCycleEdge(e.From, c) {
			return true
		}
	}
	return false
}

// Targets returns every table e can point at: a single-element slice
// ([]NodeID{e.To}) for a fixed-target edge, or one element per possible
// target for a polymorphic edge.
func (e Edge) Targets() []NodeID {
	if e.Source != EdgePolymorphic {
		return []NodeID{e.To}
	}
	ts := make([]NodeID, 0, len(e.PolyTargets))
	for _, pt := range e.PolyTargets {
		ts = append(ts, pt.To)
	}
	return ts
}

// PolyReverseEdge lets a closure walk find rows in a polymorphic edge's
// From table that point at a given target table, without a matching (and
// generally wrong, since one edge can resolve to many tables) Incoming
// entry for every possible target.
type PolyReverseEdge struct {
	Edge      Edge   // the originating polymorphic edge
	TypeValue string // the discriminator value that resolves to this target
}

// Graph is the merged FK graph produced by Build: nodes are tables from a
// catalog.Schema, edges are foreign-key-like references merged from real
// pg_catalog constraints and a user-declared relations file.
//
// Both Outgoing and Incoming are materialized so a closure step can walk
// either direction from a table — "rows this table points at" and "rows
// that point at this table" — in O(1) lookup + O(degree) scan, without
// re-deriving one direction by scanning every edge in the graph.
type Graph struct {
	Nodes    map[NodeID]catalog.Table
	Outgoing map[NodeID][]Edge // edges where From == this node
	Incoming map[NodeID][]Edge // edges where To == this node (EdgeFromCatalog / EdgeDeclared only)

	// PolyReverse indexes polymorphic edges by target table, keyed by the
	// table a discriminator value resolves to — see PolyReverseEdge.
	PolyReverse map[NodeID][]PolyReverseEdge

	// Cycles lists every cycle found in the table-level graph (Outgoing
	// edges, including each polymorphic target treated as its own edge),
	// as the sequence of NodeIDs forming the cycle. Computed once by
	// Build; consulted by the closure algorithm's cycle handling and by
	// internal/subset's topological sort.
	Cycles [][]NodeID
}

// HasCycleEdge reports whether the edge (from, column) participates in
// any detected cycle — used to validate that a configured dependency_break
// actually names an edge that needs breaking.
func (g *Graph) HasCycleEdge(from NodeID, column string) bool {
	for _, cycle := range g.Cycles {
		for i, n := range cycle {
			if n != from {
				continue
			}
			next := cycle[(i+1)%len(cycle)]
			for _, e := range g.Outgoing[n] {
				leadsToNext := false
				for _, to := range e.Targets() {
					if to == next {
						leadsToNext = true
						break
					}
				}
				if !leadsToNext {
					continue
				}
				for _, c := range e.FromColumns {
					if c == column {
						return true
					}
				}
			}
		}
	}
	return false
}
