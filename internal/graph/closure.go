package graph

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/bhuneshvar-k/tributary/pkg/config"
)

// RowRef identifies one row: its table and primary key values. Data holds
// the full row once fetched — lazily, on first dequeue during a closure
// walk, since outgoing-edge traversal needs the row's FK column values,
// not just its primary key.
type RowRef struct {
	Table NodeID
	Key   map[string]any
	Data  map[string]any
}

// keyString is a canonical, deterministic encoding of Key (columns sorted
// by name) — used as the closure's visited-set key, and, in phase 4, as
// the stable row identity compared across sync runs to detect rows
// entering or leaving a subset.
func (r RowRef) keyString() string { return encodeKey(r.Key) }

func encodeKey(m map[string]any) string {
	names := make([]string, 0, len(m))
	for k := range m {
		names = append(names, k)
	}
	sort.Strings(names)
	var b strings.Builder
	for i, n := range names {
		if i > 0 {
			b.WriteByte('&')
		}
		fmt.Fprintf(&b, "%s=%v", n, m[n])
	}
	return b.String()
}

// Closure is the phase-1 subset: every row, grouped by table, that must
// travel together to stay referentially valid. Kept as full RowRef sets
// (not just counts) because phase 2's loader needs the actual keys/data to
// SELECT and INSERT rows, and phase 4's incremental sync needs a stable
// row-key set to diff against on later runs.
type Closure struct {
	Rows     map[NodeID]map[string]RowRef // table -> (canonical key -> RowRef)
	Breaks   []AppliedBreak
	Warnings []string
}

// AppliedBreak records one self-referencing/cyclic edge that the walker
// stopped following, either because a configured dependency_breaks entry
// named it (Auto == false) or the walker auto-selected it under the
// default best-effort policy on encountering an unresolved cycle
// (Auto == true).
type AppliedBreak struct {
	Table  NodeID
	Column string
	Auto   bool
}

// UnresolvedCyclePolicy controls what happens when the walker finds a
// cycle-participating edge with no configured dependency_breaks entry
// covering it.
type UnresolvedCyclePolicy int

const (
	// PolicyBestEffort auto-breaks the edge and keeps going — the default,
	// so `tributary plan` always produces output even without a
	// dependency_breaks entry. The break is still surfaced (AppliedBreak,
	// Auto == true), never applied silently.
	PolicyBestEffort UnresolvedCyclePolicy = iota
	// PolicyError fails the whole Closure call instead of guessing which
	// edge to break, naming the unresolved cycle. Selected via
	// --strict-cycles.
	PolicyError
)

// TraversalMode controls whether the walk fans back out through a row that
// was only pulled in to satisfy a referential dependency (a "parent"), or
// stays scoped to what's actually downstream of the seed.
//
// Concretely: seeding one row of a "member" table that references both a
// "user" and a shared "company" pulls in that company (a required parent —
// referential validity needs it to exist). Under ModeFull, the walker then
// also fans back out *from* that company to every other member row
// referencing it, and from there to every other user — the seed's whole
// company, not just the seed. Under ModeDownstreamOnly, the company is
// still fetched (still needed for referential validity), but is not
// itself used as a new fan-out point, so those siblings are excluded.
type TraversalMode int

const (
	// ModeDownstreamOnly (the default) only follows incoming/polymorphic-
	// reverse edges — the direction that fans a row *out* to whatever
	// references it — from the seed itself and rows reached by fanning out
	// from another such row. A row reached purely via an outgoing edge
	// (fetching a required parent) is still fetched, but its own incoming
	// edges aren't walked — unless it turns out to also be reachable via a
	// genuine downstream path, in which case it's promoted and its
	// incoming edges are walked after all (see rowKind).
	ModeDownstreamOnly TraversalMode = iota
	// ModeFull fans out from every row regardless of how it was reached —
	// the original, unconditional bidirectional walk. Selected via
	// --include-upstream.
	ModeFull
)

// ClosureOptions configures the closure walk: cycle handling (self-
// referencing/cyclic outgoing edges) and traversal mode (see
// TraversalMode). Breaks is expected to have already been validated
// against g — Build rejects a dependency_breaks entry naming an edge with
// no cycle — so Closure does not re-validate it.
type ClosureOptions struct {
	Breaks            []config.DependencyBreak
	OnUnresolvedCycle UnresolvedCyclePolicy
	Mode              TraversalMode
}

func (o ClosureOptions) matchesBreak(table NodeID, column string) bool {
	for _, b := range o.Breaks {
		if NodeID(b.TableKey()) == table && b.Column == column {
			return true
		}
	}
	return false
}

// rowKind records why a row was pulled into the closure, for
// ModeDownstreamOnly's fan-out gating (see TraversalMode). Under ModeFull
// every row behaves as kindDownstream, so kind never restricts anything.
type rowKind int

const (
	kindDownstream rowKind = iota
	kindUpstreamParent
)

// queueItem pairs a discovered row with the kind it was discovered as.
type queueItem struct {
	row  RowRef
	kind rowKind
}

// Querier is the subset of *pgx.Conn (and *pgx.Tx) Closure needs. It
// exists to decouple Closure from which of the two a caller holds — not
// to support a mock: correctness here is only ever tested against real
// Postgres via testcontainers-go (see testdata/README.md).
type Querier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

// ComputeClosure computes the referentially-consistent subset rooted at
// every row of seedTable matching predicate — a raw SQL WHERE-clause
// fragment, interpolated directly (same admin-tool trust model as the rest
// of the CLI: not sanitized against injection, see cmd/tributary).
//
// It walks g from every discovered row: outgoing edges (rows this row's
// table points at — its parents, needed for referential validity, always
// followed) and, per opts.Mode (see TraversalMode), incoming edges (rows
// that point at this row — its children) and polymorphic-reverse edges,
// including polymorphic edges resolved through each row's runtime
// discriminator column value. Self-referencing/cyclic outgoing edges are
// handled per opts — see ClosureOptions and AppliedBreak; incoming
// traversal (when it applies under the current mode) has no special cycle
// handling, since fan-in (e.g. every order for a user) is the intended
// behavior there, and the row-level visited set alone is sufficient to
// terminate even a malformed real cycle in the data.
func ComputeClosure(ctx context.Context, conn Querier, g *Graph, seedTable NodeID, predicate string, opts ClosureOptions) (*Closure, error) {
	seed, ok := g.Nodes[seedTable]
	if !ok {
		return nil, fmt.Errorf("no such table %q in catalog", seedTable)
	}
	if len(seed.PrimaryKey) == 0 {
		return nil, fmt.Errorf("table %q has no primary key; tributary requires one to identify rows", seedTable)
	}

	w := &walker{
		ctx:         ctx,
		conn:        conn,
		g:           g,
		opts:        opts,
		closure:     &Closure{Rows: make(map[NodeID]map[string]RowRef)},
		kindOf:      make(map[NodeID]map[string]rowKind),
		brokenEdges: make(map[string]bool),
	}

	seedRows, err := w.querySeed(seedTable, seed.PrimaryKey, predicate)
	if err != nil {
		return nil, fmt.Errorf("seed query on %q: %w", seedTable, err)
	}

	var queue []queueItem
	for _, r := range seedRows {
		if w.discover(r, kindDownstream) {
			queue = append(queue, queueItem{row: r, kind: kindDownstream})
		}
	}

	for len(queue) > 0 {
		item := queue[0]
		queue = queue[1:]

		row, err := w.fetchData(item.row)
		if err != nil {
			return nil, fmt.Errorf("fetch %s row: %w", row.Table, err)
		}
		w.store(row)

		next, err := w.expand(row, item.kind)
		if err != nil {
			return nil, err
		}
		for _, ni := range next {
			if w.discover(ni.row, ni.kind) {
				queue = append(queue, ni)
			}
		}
	}

	return w.closure, nil
}

// walker holds the mutable state of one Closure call.
type walker struct {
	ctx  context.Context
	conn Querier
	g    *Graph
	opts ClosureOptions

	closure     *Closure
	kindOf      map[NodeID]map[string]rowKind
	brokenEdges map[string]bool // "table.column" -> already broken
}

// discover records row's kind and reports whether it should be
// (re)enqueued: true the first time a row is seen, and again if a row
// previously seen only as kindUpstreamParent is now reached as
// kindDownstream — an upgrade, since its incoming edges weren't walked the
// first time (see TraversalMode). Under ModeFull every row is always
// discovered as kindDownstream, so this upgrade path never triggers there.
func (w *walker) discover(r RowRef, kind rowKind) bool {
	set, ok := w.kindOf[r.Table]
	if !ok {
		set = make(map[string]rowKind)
		w.kindOf[r.Table] = set
	}
	k := r.keyString()
	existing, seen := set[k]
	if !seen {
		set[k] = kind
		return true
	}
	if existing == kindUpstreamParent && kind == kindDownstream {
		set[k] = kindDownstream
		return true
	}
	return false
}

func (w *walker) store(row RowRef) {
	m, ok := w.closure.Rows[row.Table]
	if !ok {
		m = make(map[string]RowRef)
		w.closure.Rows[row.Table] = m
	}
	m[row.keyString()] = row
}

func (w *walker) querySeed(id NodeID, pk []string, predicate string) ([]RowRef, error) {
	sql := fmt.Sprintf("SELECT * FROM %s WHERE %s", quotedTable(id), predicate)
	rows, err := w.conn.Query(w.ctx, sql)
	if err != nil {
		return nil, err
	}
	maps, err := pgx.CollectRows(rows, pgx.RowToMap)
	if err != nil {
		return nil, err
	}
	result := make([]RowRef, 0, len(maps))
	for _, m := range maps {
		result = append(result, RowRef{Table: id, Key: extractKey(pk, m), Data: m})
	}
	return result, nil
}

// fetchData populates row.Data by primary key if it isn't already set
// (seed rows arrive already-hydrated; rows discovered via traversal don't).
func (w *walker) fetchData(row RowRef) (RowRef, error) {
	if row.Data != nil {
		return row, nil
	}
	cols := make([]string, 0, len(row.Key))
	args := make([]any, 0, len(row.Key))
	for c, v := range row.Key {
		cols = append(cols, c)
		args = append(args, v)
	}
	data, found, err := w.fetchByColumns(row.Table, cols, args)
	if err != nil {
		return row, err
	}
	if !found {
		return row, fmt.Errorf("row vanished between discovery and fetch (key=%v)", row.Key)
	}
	row.Data = data
	return row, nil
}

func (w *walker) fetchByColumns(table NodeID, columns []string, args []any) (map[string]any, bool, error) {
	where := make([]string, len(columns))
	for i, c := range columns {
		where[i] = fmt.Sprintf("%s = $%d", quotedIdent(c), i+1)
	}
	sql := fmt.Sprintf("SELECT * FROM %s WHERE %s", quotedTable(table), strings.Join(where, " AND "))
	rows, err := w.conn.Query(w.ctx, sql, args...)
	if err != nil {
		return nil, false, err
	}
	data, err := pgx.CollectOneRow(rows, pgx.RowToMap)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, false, nil
		}
		return nil, false, err
	}
	return data, true, nil
}

func (w *walker) fetchManyByColumns(table NodeID, columns []string, args []any) ([]map[string]any, error) {
	where := make([]string, len(columns))
	for i, c := range columns {
		where[i] = fmt.Sprintf("%s = $%d", quotedIdent(c), i+1)
	}
	sql := fmt.Sprintf("SELECT * FROM %s WHERE %s", quotedTable(table), strings.Join(where, " AND "))
	rows, err := w.conn.Query(w.ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowToMap)
}

func extractKey(pk []string, data map[string]any) map[string]any {
	key := make(map[string]any, len(pk))
	for _, c := range pk {
		key[c] = data[c]
	}
	return key
}

// expand finds every row reachable from row in one hop: outgoing edges
// (its parents, including polymorphic — always followed, needed for
// referential validity regardless of mode) and, when kind/opts.Mode allow
// it (see TraversalMode), incoming edges (its children) and polymorphic-
// reverse edges (children of a polymorphic association whose target is
// row's table).
func (w *walker) expand(row RowRef, kind rowKind) ([]queueItem, error) {
	var next []queueItem

	// A parent fetched via an outgoing edge is kindUpstreamParent under
	// ModeDownstreamOnly (it won't itself become a new fan-out point unless
	// later promoted) — or just kindDownstream under ModeFull, where that
	// distinction doesn't exist.
	parentKind := kindDownstream
	if w.opts.Mode == ModeDownstreamOnly {
		parentKind = kindUpstreamParent
	}

	for _, e := range w.g.Outgoing[row.Table] {
		if e.Source == EdgePolymorphic {
			rs, err := w.followPolymorphicOutgoing(row, e)
			if err != nil {
				return nil, err
			}
			for _, r := range rs {
				next = append(next, queueItem{row: r, kind: parentKind})
			}
			continue
		}

		if w.g.EdgeInCycle(e) && edgeHasValue(row, e) {
			broken, err := w.applyBreakIfNeeded(e)
			if err != nil {
				return nil, err
			}
			if broken {
				continue
			}
		}

		r, ok, err := w.followOutgoing(row, e)
		if err != nil {
			return nil, err
		}
		if ok {
			next = append(next, queueItem{row: r, kind: parentKind})
		}
	}

	// Fan-out (incoming / polymorphic-reverse) only applies from a
	// downstream row — the seed itself, or a row reached by fanning out
	// from another downstream row — under ModeDownstreamOnly; under
	// ModeFull it always applies.
	if w.opts.Mode == ModeFull || kind == kindDownstream {
		for _, e := range w.g.Incoming[row.Table] {
			rs, err := w.followIncoming(row, e)
			if err != nil {
				return nil, err
			}
			for _, r := range rs {
				next = append(next, queueItem{row: r, kind: kindDownstream})
			}
		}

		for _, pe := range w.g.PolyReverse[row.Table] {
			rs, err := w.followPolyReverse(row, pe)
			if err != nil {
				return nil, err
			}
			for _, r := range rs {
				next = append(next, queueItem{row: r, kind: kindDownstream})
			}
		}
	}

	return next, nil
}

// edgeHasValue reports whether row has a non-null value on every one of
// e's FromColumns — the same condition followOutgoing itself requires to
// actually traverse the edge. Checked before cycle-break bookkeeping so a
// root row (e.g. a top-level manager with a null manager_id) never "uses
// up" a break for an edge it was never going to follow anyway.
func edgeHasValue(row RowRef, e Edge) bool {
	for _, c := range e.FromColumns {
		if v, ok := row.Data[c]; !ok || v == nil {
			return false
		}
	}
	return true
}

func (w *walker) followOutgoing(row RowRef, e Edge) (RowRef, bool, error) {
	args := make([]any, len(e.FromColumns))
	for i, c := range e.FromColumns {
		v, ok := row.Data[c]
		if !ok || v == nil {
			return RowRef{}, false, nil // nullable FK not set: no parent to follow
		}
		args[i] = v
	}
	data, found, err := w.fetchByColumns(e.To, e.ToColumns, args)
	if err != nil || !found {
		return RowRef{}, false, err
	}
	return RowRef{Table: e.To, Key: extractKey(w.g.Nodes[e.To].PrimaryKey, data), Data: data}, true, nil
}

func (w *walker) followIncoming(row RowRef, e Edge) ([]RowRef, error) {
	args := make([]any, len(e.ToColumns))
	for i, c := range e.ToColumns {
		v, ok := row.Data[c]
		if !ok || v == nil {
			return nil, nil
		}
		args[i] = v
	}
	rowsData, err := w.fetchManyByColumns(e.From, e.FromColumns, args)
	if err != nil {
		return nil, err
	}
	result := make([]RowRef, 0, len(rowsData))
	for _, d := range rowsData {
		result = append(result, RowRef{Table: e.From, Key: extractKey(w.g.Nodes[e.From].PrimaryKey, d), Data: d})
	}
	return result, nil
}

func (w *walker) followPolymorphicOutgoing(row RowRef, e Edge) ([]RowRef, error) {
	typeVal, ok := row.Data[e.PolyTypeColumn]
	if !ok || typeVal == nil {
		return nil, nil
	}
	discriminator := fmt.Sprintf("%v", typeVal)
	pt, ok := e.PolyTargets[discriminator]
	if !ok {
		w.closure.Warnings = append(w.closure.Warnings, fmt.Sprintf(
			"%s.%s: unrecognized polymorphic type value %q, no matching target in schema file — skipped",
			row.Table, e.PolyTypeColumn, discriminator))
		return nil, nil
	}

	args := make([]any, len(e.FromColumns))
	for i, c := range e.FromColumns {
		v, ok := row.Data[c]
		if !ok || v == nil {
			return nil, nil
		}
		args[i] = v
	}
	data, found, err := w.fetchByColumns(pt.To, pt.ToColumns, args)
	if err != nil || !found {
		return nil, err
	}
	return []RowRef{{Table: pt.To, Key: extractKey(w.g.Nodes[pt.To].PrimaryKey, data), Data: data}}, nil
}

func (w *walker) followPolyReverse(row RowRef, pe PolyReverseEdge) ([]RowRef, error) {
	pt, ok := pe.Edge.PolyTargets[pe.TypeValue]
	if !ok {
		return nil, nil
	}

	args := make([]any, len(pt.ToColumns))
	for i, c := range pt.ToColumns {
		v, ok := row.Data[c]
		if !ok || v == nil {
			return nil, nil
		}
		args[i] = v
	}

	where := make([]string, 0, len(pe.Edge.FromColumns)+1)
	queryArgs := make([]any, 0, len(args)+1)
	for i, c := range pe.Edge.FromColumns {
		where = append(where, fmt.Sprintf("%s = $%d", quotedIdent(c), i+1))
		queryArgs = append(queryArgs, args[i])
	}
	where = append(where, fmt.Sprintf("%s = $%d", quotedIdent(pe.Edge.PolyTypeColumn), len(where)+1))
	queryArgs = append(queryArgs, pe.TypeValue)

	sql := fmt.Sprintf("SELECT * FROM %s WHERE %s", quotedTable(pe.Edge.From), strings.Join(where, " AND "))
	rows, err := w.conn.Query(w.ctx, sql, queryArgs...)
	if err != nil {
		return nil, err
	}
	maps, err := pgx.CollectRows(rows, pgx.RowToMap)
	if err != nil {
		return nil, err
	}
	result := make([]RowRef, 0, len(maps))
	for _, d := range maps {
		result = append(result, RowRef{Table: pe.Edge.From, Key: extractKey(w.g.Nodes[pe.Edge.From].PrimaryKey, d), Data: d})
	}
	return result, nil
}

// applyBreakIfNeeded decides whether e (already known to participate in a
// cycle) should stop being followed. An edge already broken (explicitly or
// automatically) stays broken for the rest of the walk — once applied, a
// dependency_break is global to the edge, not re-evaluated per row: the
// point is to stop an unbounded chain (e.g. climbing a management
// hierarchy), not to allow exactly one hop per row encountered.
func (w *walker) applyBreakIfNeeded(e Edge) (bool, error) {
	for _, c := range e.FromColumns {
		if w.brokenEdges[string(e.From)+"."+c] {
			return true, nil
		}
	}

	var explicit bool
	var brokenColumn string
	for _, c := range e.FromColumns {
		if w.opts.matchesBreak(e.From, c) {
			explicit = true
			brokenColumn = c
			break
		}
	}

	if !explicit {
		if w.opts.OnUnresolvedCycle == PolicyError {
			return false, fmt.Errorf(
				"unresolved cycle: %s.%s participates in a cycle with no dependency_breaks entry (add one, or drop --strict-cycles for best-effort)",
				e.From, strings.Join(e.FromColumns, ","))
		}
		brokenColumn = e.FromColumns[0]
	}

	for _, c := range e.FromColumns {
		w.brokenEdges[string(e.From)+"."+c] = true
	}
	w.closure.Breaks = append(w.closure.Breaks, AppliedBreak{
		Table:  e.From,
		Column: brokenColumn,
		Auto:   !explicit,
	})
	return true, nil
}

func quotedTable(id NodeID) string {
	parts := strings.SplitN(string(id), ".", 2)
	if len(parts) != 2 {
		return pgx.Identifier{string(id)}.Sanitize()
	}
	return pgx.Identifier{parts[0], parts[1]}.Sanitize()
}

func quotedIdent(name string) string {
	return pgx.Identifier{name}.Sanitize()
}
