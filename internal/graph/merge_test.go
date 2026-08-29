package graph

import (
	"strings"
	"testing"

	"github.com/bhuneshvar-k/tributary/internal/catalog"
	"github.com/bhuneshvar-k/tributary/pkg/config"
	"gopkg.in/yaml.v3"
)

func col(name string) catalog.Column {
	return catalog.Column{Name: name, Type: "text"}
}

// baseSchema is a small hand-built catalog.Schema covering: a plain FK
// (orders -> users, via catalog), a composite FK (line_items -> orders),
// a self-referencing table with no catalog FK broken out (employees), and
// tables with no catalog FKs at all for declared/polymorphic relations to
// attach to (posts, photos, comments, audit_logs).
func baseSchema() *catalog.Schema {
	return &catalog.Schema{
		Tables: []catalog.Table{
			{
				Schema:  "public",
				Name:    "users",
				Columns: []catalog.Column{col("id")},
			},
			{
				Schema:  "public",
				Name:    "orders",
				Columns: []catalog.Column{col("id"), col("user_id"), col("tenant_id")},
			},
			{
				Schema:  "public",
				Name:    "line_items",
				Columns: []catalog.Column{col("order_id"), col("tenant_id"), col("sku")},
				ForeignKeys: []catalog.ForeignKey{
					{
						ConstraintName: "line_items_order_fk",
						FromTable:      "public.line_items",
						FromColumns:    []string{"order_id", "tenant_id"},
						ToTable:        "public.orders",
						ToColumns:      []string{"id", "tenant_id"},
					},
				},
			},
			{
				Schema:  "public",
				Name:    "employees",
				Columns: []catalog.Column{col("id"), col("manager_id")},
				ForeignKeys: []catalog.ForeignKey{
					{
						ConstraintName: "employees_manager_fk",
						FromTable:      "public.employees",
						FromColumns:    []string{"manager_id"},
						ToTable:        "public.employees",
						ToColumns:      []string{"id"},
					},
				},
			},
			{
				Schema:  "public",
				Name:    "posts",
				Columns: []catalog.Column{col("id")},
			},
			{
				Schema:  "public",
				Name:    "photos",
				Columns: []catalog.Column{col("id")},
			},
			{
				Schema:  "public",
				Name:    "comments",
				Columns: []catalog.Column{col("id"), col("commentable_id"), col("commentable_type")},
			},
			{
				Schema:  "public",
				Name:    "audit_logs",
				Columns: []catalog.Column{col("id"), col("actor_id")},
				ForeignKeys: []catalog.ForeignKey{
					{
						ConstraintName: "audit_logs_actor_fk",
						FromTable:      "public.audit_logs",
						FromColumns:    []string{"actor_id"},
						ToTable:        "public.users",
						ToColumns:      []string{"id"},
					},
				},
			},
		},
	}
}

func parseSchemaFile(t *testing.T, doc string) *config.SchemaFile {
	t.Helper()
	var sf config.SchemaFile
	if err := yaml.Unmarshal([]byte(doc), &sf); err != nil {
		t.Fatalf("yaml.Unmarshal: %v", err)
	}
	return &sf
}

func TestBuildCatalogOnly(t *testing.T) {
	g, err := Build(baseSchema(), nil)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	lineItems := g.Outgoing["public.line_items"]
	if len(lineItems) != 1 {
		t.Fatalf("got %d outgoing edges from line_items, want 1", len(lineItems))
	}
	e := lineItems[0]
	if e.To != "public.orders" || len(e.FromColumns) != 2 || len(e.ToColumns) != 2 {
		t.Fatalf("unexpected composite edge: %+v", e)
	}

	ordersIncoming := g.Incoming["public.orders"]
	if len(ordersIncoming) != 1 || ordersIncoming[0].From != "public.line_items" {
		t.Fatalf("unexpected incoming edges on orders: %+v", ordersIncoming)
	}
}

func TestBuildDeclaredSoftFK(t *testing.T) {
	sf := parseSchemaFile(t, `
relations:
  - from: orders.user_id
    to: users.id
`)
	g, err := Build(baseSchema(), sf)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	found := false
	for _, e := range g.Outgoing["public.orders"] {
		if e.To == "public.users" && e.Source == EdgeDeclared {
			found = true
		}
	}
	if !found {
		t.Fatal("expected a declared edge orders -> users, not found")
	}
}

func TestBuildDeclaredUnknownTable(t *testing.T) {
	sf := parseSchemaFile(t, `
relations:
  - from: nonexistent.user_id
    to: users.id
`)
	_, err := Build(baseSchema(), sf)
	if err == nil {
		t.Fatal("expected error for relation referencing an unknown table, got nil")
	}
	if !strings.Contains(err.Error(), "nonexistent") {
		t.Fatalf("error = %q, want it to name the unknown table", err.Error())
	}
}

func TestBuildDeclaredUnknownColumn(t *testing.T) {
	sf := parseSchemaFile(t, `
relations:
  - from: orders.no_such_column
    to: users.id
`)
	_, err := Build(baseSchema(), sf)
	if err == nil {
		t.Fatal("expected error for relation referencing an unknown column, got nil")
	}
	if !strings.Contains(err.Error(), "no_such_column") {
		t.Fatalf("error = %q, want it to name the unknown column", err.Error())
	}
}

func TestBuildPolymorphic(t *testing.T) {
	sf := parseSchemaFile(t, `
relations:
  - from: comments.commentable_id
    polymorphic_type: comments.commentable_type
    targets:
      Post: posts.id
      Photo: photos.id
`)
	g, err := Build(baseSchema(), sf)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	edges := g.Outgoing["public.comments"]
	if len(edges) != 1 || edges[0].Source != EdgePolymorphic {
		t.Fatalf("expected one polymorphic outgoing edge on comments, got %+v", edges)
	}
	e := edges[0]
	if e.PolyTypeColumn != "commentable_type" {
		t.Fatalf("PolyTypeColumn = %q, want commentable_type", e.PolyTypeColumn)
	}
	if pt, ok := e.PolyTargets["Post"]; !ok || pt.To != "public.posts" {
		t.Fatalf("PolyTargets[Post] = %+v, want public.posts", pt)
	}

	// Reverse index lets a walk *into* posts discover the comments edge.
	rev := g.PolyReverse["public.posts"]
	if len(rev) != 1 || rev[0].TypeValue != "Post" {
		t.Fatalf("PolyReverse[public.posts] = %+v", rev)
	}

	// Polymorphic edges must not create a (false) Incoming entry.
	if len(g.Incoming["public.posts"]) != 0 {
		t.Fatalf("expected no Incoming edges on posts from a polymorphic relation, got %+v", g.Incoming["public.posts"])
	}
}

func TestBuildPolymorphicUnknownTarget(t *testing.T) {
	sf := parseSchemaFile(t, `
relations:
  - from: comments.commentable_id
    polymorphic_type: comments.commentable_type
    targets:
      Post: nonexistent.id
`)
	_, err := Build(baseSchema(), sf)
	if err == nil {
		t.Fatal("expected error for polymorphic target referencing an unknown table, got nil")
	}
}

func TestBuildIgnoreRemovesEdge(t *testing.T) {
	sf := parseSchemaFile(t, `
relations:
  - ignore: audit_logs.actor_id
`)
	g, err := Build(baseSchema(), sf)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if len(g.Outgoing["public.audit_logs"]) != 0 {
		t.Fatalf("expected audit_logs outgoing edge to be removed, got %+v", g.Outgoing["public.audit_logs"])
	}
	if len(g.Incoming["public.users"]) != 0 {
		t.Fatalf("expected users incoming edge from audit_logs to be removed, got %+v", g.Incoming["public.users"])
	}
}

func TestBuildIgnoreNoMatch(t *testing.T) {
	sf := parseSchemaFile(t, `
relations:
  - ignore: audit_logs.no_such_column
`)
	_, err := Build(baseSchema(), sf)
	if err == nil {
		t.Fatal("expected error for ignore referencing a column with no catalog FK, got nil")
	}
}

func TestDetectCyclesSelfReferencing(t *testing.T) {
	g, err := Build(baseSchema(), nil)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if len(g.Cycles) != 1 {
		t.Fatalf("got %d cycles, want 1 (employees self-reference), cycles=%+v", len(g.Cycles), g.Cycles)
	}
	cycle := g.Cycles[0]
	if len(cycle) != 1 || cycle[0] != "public.employees" {
		t.Fatalf("unexpected cycle: %+v", cycle)
	}
}

func TestHasCycleEdge(t *testing.T) {
	g, err := Build(baseSchema(), nil)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if !g.HasCycleEdge("public.employees", "manager_id") {
		t.Fatal("expected HasCycleEdge(employees, manager_id) to be true")
	}
	if g.HasCycleEdge("public.employees", "id") {
		t.Fatal("expected HasCycleEdge(employees, id) to be false — id is not the cycle-forming column")
	}
}

func TestBuildDependencyBreakMustNameACycle(t *testing.T) {
	sf := parseSchemaFile(t, `
dependency_breaks:
  - table: orders
    column: user_id
`)
	_, err := Build(baseSchema(), sf)
	if err == nil {
		t.Fatal("expected error for dependency_break naming an edge with no cycle, got nil")
	}
}

func TestBuildDependencyBreakOnRealCycleAccepted(t *testing.T) {
	sf := parseSchemaFile(t, `
dependency_breaks:
  - table: employees
    column: manager_id
`)
	if _, err := Build(baseSchema(), sf); err != nil {
		t.Fatalf("Build: %v", err)
	}
}

func TestBuildAggregatesMultipleErrors(t *testing.T) {
	sf := parseSchemaFile(t, `
relations:
  - from: nonexistent.user_id
    to: users.id
  - from: orders.no_such_column
    to: users.id
`)
	_, err := Build(baseSchema(), sf)
	if err == nil {
		t.Fatal("expected aggregated errors, got nil")
	}
	msg := err.Error()
	if !strings.Contains(msg, "relations[0]") || !strings.Contains(msg, "relations[1]") {
		t.Fatalf("aggregated error %q missing per-relation indices", msg)
	}
}
