package subset

import (
	"strings"
	"testing"

	"github.com/bhuneshvar-k/tributary/internal/catalog"
	"github.com/bhuneshvar-k/tributary/internal/graph"
	"github.com/bhuneshvar-k/tributary/pkg/config"
	"gopkg.in/yaml.v3"
)

func parseSchemaFileForOrderTest(t *testing.T, doc string) *config.SchemaFile {
	t.Helper()
	var sf config.SchemaFile
	if err := yaml.Unmarshal([]byte(doc), &sf); err != nil {
		t.Fatalf("yaml.Unmarshal: %v", err)
	}
	return &sf
}

func col(name string) catalog.Column {
	return catalog.Column{Name: name, Type: "text"}
}

func indexOf(order []graph.NodeID, id graph.NodeID) int {
	for i, n := range order {
		if n == id {
			return i
		}
	}
	return -1
}

func TestTableOrderParentsBeforeChildren(t *testing.T) {
	schema := &catalog.Schema{
		Tables: []catalog.Table{
			{Schema: "public", Name: "users", Columns: []catalog.Column{col("id")}},
			{
				Schema:  "public",
				Name:    "orders",
				Columns: []catalog.Column{col("id"), col("user_id")},
				ForeignKeys: []catalog.ForeignKey{{
					ConstraintName: "orders_user_fk", FromTable: "public.orders",
					FromColumns: []string{"user_id"}, ToTable: "public.users", ToColumns: []string{"id"},
				}},
			},
			{
				Schema:  "public",
				Name:    "line_items",
				Columns: []catalog.Column{col("id"), col("order_id")},
				ForeignKeys: []catalog.ForeignKey{{
					ConstraintName: "line_items_order_fk", FromTable: "public.line_items",
					FromColumns: []string{"order_id"}, ToTable: "public.orders", ToColumns: []string{"id"},
				}},
			},
		},
	}

	g, err := graph.Build(schema, nil)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	order, err := TableOrder(g)
	if err != nil {
		t.Fatalf("TableOrder: %v", err)
	}
	if len(order) != 3 {
		t.Fatalf("got %d tables, want 3", len(order))
	}

	users, orders, lineItems := indexOf(order, "public.users"), indexOf(order, "public.orders"), indexOf(order, "public.line_items")
	if !(users < orders && orders < lineItems) {
		t.Fatalf("order = %v, want users before orders before line_items", order)
	}
}

func TestTableOrderSelfReferenceDoesNotBlock(t *testing.T) {
	schema := &catalog.Schema{
		Tables: []catalog.Table{
			{
				Schema:  "public",
				Name:    "employees",
				Columns: []catalog.Column{col("id"), col("manager_id")},
				ForeignKeys: []catalog.ForeignKey{{
					ConstraintName: "employees_manager_fk", FromTable: "public.employees",
					FromColumns: []string{"manager_id"}, ToTable: "public.employees", ToColumns: []string{"id"},
				}},
			},
		},
	}

	g, err := graph.Build(schema, nil)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	order, err := TableOrder(g)
	if err != nil {
		t.Fatalf("TableOrder: %v", err)
	}
	if len(order) != 1 || order[0] != "public.employees" {
		t.Fatalf("order = %v, want [public.employees]", order)
	}
}

func TestTableOrderPolymorphicDependency(t *testing.T) {
	schema := &catalog.Schema{
		Tables: []catalog.Table{
			{Schema: "public", Name: "posts", Columns: []catalog.Column{col("id")}},
			{Schema: "public", Name: "photos", Columns: []catalog.Column{col("id")}},
			{
				Schema:  "public",
				Name:    "comments",
				Columns: []catalog.Column{col("id"), col("commentable_id"), col("commentable_type")},
			},
		},
	}
	sf := parseSchemaFileForOrderTest(t, `
relations:
  - from: comments.commentable_id
    polymorphic_type: comments.commentable_type
    targets:
      Post: posts.id
      Photo: photos.id
`)

	g, err := graph.Build(schema, sf)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	order, err := TableOrder(g)
	if err != nil {
		t.Fatalf("TableOrder: %v", err)
	}
	if len(order) != 3 {
		t.Fatalf("got %d tables, want 3", len(order))
	}
	posts, photos, comments := indexOf(order, "public.posts"), indexOf(order, "public.photos"), indexOf(order, "public.comments")
	if !(posts < comments && photos < comments) {
		t.Fatalf("order = %v, want both posts and photos before comments", order)
	}
}

func TestTableOrderMultiTableCycleErrors(t *testing.T) {
	// a -> b -> a, a genuine multi-table cycle with no self-loop escape.
	schema := &catalog.Schema{
		Tables: []catalog.Table{
			{
				Schema: "public", Name: "a",
				Columns: []catalog.Column{col("id"), col("b_id")},
				ForeignKeys: []catalog.ForeignKey{{
					ConstraintName: "a_b_fk", FromTable: "public.a",
					FromColumns: []string{"b_id"}, ToTable: "public.b", ToColumns: []string{"id"},
				}},
			},
			{
				Schema: "public", Name: "b",
				Columns: []catalog.Column{col("id"), col("a_id")},
				ForeignKeys: []catalog.ForeignKey{{
					ConstraintName: "b_a_fk", FromTable: "public.b",
					FromColumns: []string{"a_id"}, ToTable: "public.a", ToColumns: []string{"id"},
				}},
			},
		},
	}

	g, err := graph.Build(schema, nil)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	_, err = TableOrder(g)
	if err == nil {
		t.Fatal("expected error for a two-table cycle, got nil")
	}
	if !strings.Contains(err.Error(), "public.a") || !strings.Contains(err.Error(), "public.b") {
		t.Fatalf("error = %q, want it to name both stuck tables", err.Error())
	}
}
