package graph

import (
	"errors"
	"fmt"

	"github.com/bhuneshvar-k/tributary/internal/catalog"
	"github.com/bhuneshvar-k/tributary/pkg/config"
)

// Build merges a catalog.Schema (real pg_catalog constraints) with a
// user-declared config.SchemaFile (soft FKs, polymorphic associations,
// ignores) into a single Graph. sf may be nil, in which case the graph is
// built from catalog edges alone.
//
// Every relation is validated against the actual catalog — an unknown
// table/column reference is an error naming the exact offending relation
// and field, and every such problem found is aggregated (via errors.Join)
// rather than stopping at the first, matching config.SchemaFile.Validate's
// own aggregation.
func Build(schema *catalog.Schema, sf *config.SchemaFile) (*Graph, error) {
	if sf != nil {
		if err := sf.Validate(); err != nil {
			return nil, fmt.Errorf("invalid schema file: %w", err)
		}
	}

	g := &Graph{
		Nodes:       make(map[NodeID]catalog.Table, len(schema.Tables)),
		Outgoing:    make(map[NodeID][]Edge),
		Incoming:    make(map[NodeID][]Edge),
		PolyReverse: make(map[NodeID][]PolyReverseEdge),
	}

	for _, t := range schema.Tables {
		id := NodeID(t.Schema + "." + t.Name)
		g.Nodes[id] = t
		for _, fk := range t.ForeignKeys {
			e := Edge{
				From:           NodeID(fk.FromTable),
				To:             NodeID(fk.ToTable),
				FromColumns:    fk.FromColumns,
				ToColumns:      fk.ToColumns,
				Source:         EdgeFromCatalog,
				ConstraintName: fk.ConstraintName,
			}
			g.addEdge(e)
		}
	}

	var errs []error
	if sf != nil {
		for i, r := range sf.Relations {
			if err := g.mergeRelation(r); err != nil {
				errs = append(errs, fmt.Errorf("relations[%d]: %w", i, err))
			}
		}
	}
	if err := errors.Join(errs...); err != nil {
		return nil, err
	}

	g.Cycles = detectCycles(g.Nodes, g.Outgoing)

	if sf != nil {
		var breakErrs []error
		for i, b := range sf.DependencyBreaks {
			if !g.HasCycleEdge(NodeID(b.TableKey()), b.Column) {
				breakErrs = append(breakErrs, fmt.Errorf(
					"dependency_breaks[%d]: %s.%s does not participate in any detected cycle — remove it or fix the table/column",
					i, b.TableKey(), b.Column))
			}
		}
		if err := errors.Join(breakErrs...); err != nil {
			return nil, err
		}
	}

	return g, nil
}

// addEdge records e in both Outgoing[e.From] and, for edges with a fixed
// target (i.e. not polymorphic), Incoming[e.To].
func (g *Graph) addEdge(e Edge) {
	g.Outgoing[e.From] = append(g.Outgoing[e.From], e)
	if e.Source != EdgePolymorphic {
		g.Incoming[e.To] = append(g.Incoming[e.To], e)
	}
}

func (g *Graph) mergeRelation(r config.Relation) error {
	kind, err := r.Kind()
	if err != nil {
		return err
	}

	switch kind {
	case config.KindForeignKey:
		return g.mergeForeignKey(r)
	case config.KindPolymorphic:
		return g.mergePolymorphic(r)
	case config.KindIgnore:
		return g.mergeIgnore(r)
	default:
		return fmt.Errorf("unknown relation kind %v", kind)
	}
}

func (g *Graph) mergeForeignKey(r config.Relation) error {
	fromID := NodeID(r.From.TableKey())
	toID := NodeID(r.To.TableKey())

	fromTable, ok := g.Nodes[fromID]
	if !ok {
		return fmt.Errorf("from=%s: no such table %q in catalog", r.From, fromID)
	}
	toTable, ok := g.Nodes[toID]
	if !ok {
		return fmt.Errorf("to=%s: no such table %q in catalog", r.To, toID)
	}

	for _, c := range r.From.Columns {
		if !hasColumn(fromTable, c) {
			return fmt.Errorf("from=%s: no such column %q on table %q", r.From, c, fromID)
		}
	}
	for _, c := range r.To.Columns {
		if !hasColumn(toTable, c) {
			return fmt.Errorf("to=%s: no such column %q on table %q", r.To, c, toID)
		}
	}

	g.addEdge(Edge{
		From:        fromID,
		To:          toID,
		FromColumns: r.From.Columns,
		ToColumns:   r.To.Columns,
		Source:      EdgeDeclared,
	})
	return nil
}

func (g *Graph) mergePolymorphic(r config.Relation) error {
	fromID := NodeID(r.From.TableKey())
	fromTable, ok := g.Nodes[fromID]
	if !ok {
		return fmt.Errorf("from=%s: no such table %q in catalog", r.From, fromID)
	}
	for _, c := range r.From.Columns {
		if !hasColumn(fromTable, c) {
			return fmt.Errorf("from=%s: no such column %q on table %q", r.From, c, fromID)
		}
	}

	typeSchema, typeTable, typeColumn, err := config.ParseTableColumn(r.PolymorphicType)
	if err != nil {
		return fmt.Errorf("polymorphic_type: %w", err)
	}
	if NodeID(typeSchema+"."+typeTable) != fromID {
		return fmt.Errorf("polymorphic_type %s must be a column on the same table as from (%s)", r.PolymorphicType, fromID)
	}
	if !hasColumn(fromTable, typeColumn) {
		return fmt.Errorf("polymorphic_type: no such column %q on table %q", typeColumn, fromID)
	}

	e := Edge{
		From:           fromID,
		Source:         EdgePolymorphic,
		FromColumns:    r.From.Columns,
		PolyTypeColumn: typeColumn,
		PolyTargets:    make(map[string]PolyTarget, len(r.Targets)),
	}

	for typeValue, target := range r.Targets {
		tSchema, tTable, tColumn, err := config.ParseTableColumn(target)
		if err != nil {
			return fmt.Errorf("targets[%q]=%q: %w", typeValue, target, err)
		}
		targetID := NodeID(tSchema + "." + tTable)
		targetTable, ok := g.Nodes[targetID]
		if !ok {
			return fmt.Errorf("targets[%q]=%q: no such table %q in catalog", typeValue, target, targetID)
		}
		if !hasColumn(targetTable, tColumn) {
			return fmt.Errorf("targets[%q]=%q: no such column %q on table %q", typeValue, target, tColumn, targetID)
		}
		e.PolyTargets[typeValue] = PolyTarget{To: targetID, ToColumns: []string{tColumn}}
	}

	g.addEdge(e)
	for typeValue, pt := range e.PolyTargets {
		g.PolyReverse[pt.To] = append(g.PolyReverse[pt.To], PolyReverseEdge{Edge: e, TypeValue: typeValue})
	}
	return nil
}

func (g *Graph) mergeIgnore(r config.Relation) error {
	schema, table, column, err := config.ParseTableColumn(r.Ignore)
	if err != nil {
		return fmt.Errorf("ignore: %w", err)
	}
	id := NodeID(schema + "." + table)
	if _, ok := g.Nodes[id]; !ok {
		return fmt.Errorf("ignore=%s: no such table %q in catalog", r.Ignore, id)
	}

	var matches []Edge
	for _, e := range g.Outgoing[id] {
		if e.Source != EdgeFromCatalog {
			continue
		}
		for _, c := range e.FromColumns {
			if c == column {
				matches = append(matches, e)
				break
			}
		}
	}

	switch len(matches) {
	case 0:
		return fmt.Errorf("ignore=%s: no catalog foreign key on %q uses column %q", r.Ignore, id, column)
	case 1:
		g.removeEdge(id, matches[0])
		return nil
	default:
		return fmt.Errorf("ignore=%s: ambiguous — %d distinct foreign key constraints on %q use column %q, name the constraint more specifically",
			r.Ignore, len(matches), id, column)
	}
}

// removeEdge drops e (matched by ConstraintName) from Outgoing[from] and
// from Incoming[e.To].
func (g *Graph) removeEdge(from NodeID, e Edge) {
	out := g.Outgoing[from]
	for i, o := range out {
		if o.ConstraintName == e.ConstraintName {
			g.Outgoing[from] = append(out[:i], out[i+1:]...)
			break
		}
	}
	in := g.Incoming[e.To]
	for i, o := range in {
		if o.ConstraintName == e.ConstraintName && o.From == from {
			g.Incoming[e.To] = append(in[:i], in[i+1:]...)
			break
		}
	}
}

func hasColumn(t catalog.Table, name string) bool {
	for _, c := range t.Columns {
		if c.Name == name {
			return true
		}
	}
	return false
}

// detectCycles finds cycles in the table-level graph via DFS with an
// explicit recursion stack: when a traversal reaches a node still on the
// stack, the stack slice from that node's position onward is one cycle.
// Polymorphic edges are expanded into one traversal edge per possible
// target, since a single Edge doesn't have a fixed To.
func detectCycles(nodes map[NodeID]catalog.Table, outgoing map[NodeID][]Edge) [][]NodeID {
	const (
		white = iota
		gray
		black
	)
	color := make(map[NodeID]int, len(nodes))
	var stack []NodeID
	var cycles [][]NodeID

	var visit func(n NodeID)
	visit = func(n NodeID) {
		color[n] = gray
		stack = append(stack, n)

		for _, e := range outgoing[n] {
			for _, to := range e.Targets() {
				switch color[to] {
				case white:
					visit(to)
				case gray:
					for i, s := range stack {
						if s == to {
							cycle := make([]NodeID, len(stack)-i)
							copy(cycle, stack[i:])
							cycles = append(cycles, cycle)
							break
						}
					}
				case black:
					// already fully explored; no new cycle via this edge
				}
			}
		}

		stack = stack[:len(stack)-1]
		color[n] = black
	}

	for n := range nodes {
		if color[n] == white {
			visit(n)
		}
	}
	return cycles
}
