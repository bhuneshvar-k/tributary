package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func parseSchemaFile(t *testing.T, doc string) (*SchemaFile, error) {
	t.Helper()
	var sf SchemaFile
	if err := yaml.Unmarshal([]byte(doc), &sf); err != nil {
		return nil, err
	}
	return &sf, sf.Validate()
}

func TestColumnRefScalarUnqualified(t *testing.T) {
	sf, err := parseSchemaFile(t, `
relations:
  - from: orders.user_id
    to: users.id
`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	rel := sf.Relations[0]
	if rel.From.Schema != "public" || rel.From.Table != "orders" || rel.From.Columns[0] != "user_id" {
		t.Fatalf("unexpected From: %+v", rel.From)
	}
	if rel.From.TableKey() != "public.orders" {
		t.Fatalf("TableKey() = %q, want %q", rel.From.TableKey(), "public.orders")
	}
}

func TestColumnRefScalarQualified(t *testing.T) {
	sf, err := parseSchemaFile(t, `
relations:
  - from: tenant_a.orders.user_id
    to: tenant_a.users.id
`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	rel := sf.Relations[0]
	if rel.From.Schema != "tenant_a" || rel.From.Table != "orders" || rel.From.Columns[0] != "user_id" {
		t.Fatalf("unexpected From: %+v", rel.From)
	}
}

func TestColumnRefComposite(t *testing.T) {
	sf, err := parseSchemaFile(t, `
relations:
  - from: [line_items.order_id, line_items.tenant_id]
    to: [orders.id, orders.tenant_id]
`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	rel := sf.Relations[0]
	if rel.From.TableKey() != "public.line_items" {
		t.Fatalf("From.TableKey() = %q", rel.From.TableKey())
	}
	if len(rel.From.Columns) != 2 || rel.From.Columns[0] != "order_id" || rel.From.Columns[1] != "tenant_id" {
		t.Fatalf("unexpected From.Columns: %v", rel.From.Columns)
	}
}

func TestColumnRefCompositeMixedTablesRejected(t *testing.T) {
	_, err := parseSchemaFile(t, `
relations:
  - from: [line_items.order_id, other_table.tenant_id]
    to: [orders.id, orders.tenant_id]
`)
	if err == nil {
		t.Fatal("expected error for composite reference spanning two tables, got nil")
	}
}

func TestColumnRefInvalidFormat(t *testing.T) {
	_, err := parseSchemaFile(t, `
relations:
  - from: not_a_valid_ref
    to: users.id
`)
	if err == nil {
		t.Fatal("expected error for malformed table.column reference, got nil")
	}
}

func TestCompositeKeyLengthMismatch(t *testing.T) {
	_, err := parseSchemaFile(t, `
relations:
  - from: [line_items.order_id]
    to: [orders.id, orders.tenant_id]
`)
	if err == nil {
		t.Fatal("expected composite key length mismatch error, got nil")
	}
	if !strings.Contains(err.Error(), "composite key length mismatch") {
		t.Fatalf("error = %q, want it to mention composite key length mismatch", err.Error())
	}
}

func TestPolymorphicRelation(t *testing.T) {
	sf, err := parseSchemaFile(t, `
relations:
  - from: comments.commentable_id
    polymorphic_type: comments.commentable_type
    targets:
      Post: posts.id
      Photo: photos.id
`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	kind, err := sf.Relations[0].Kind()
	if err != nil {
		t.Fatalf("Kind() error: %v", err)
	}
	if kind != KindPolymorphic {
		t.Fatalf("Kind() = %v, want KindPolymorphic", kind)
	}
}

func TestPolymorphicMissingTargets(t *testing.T) {
	_, err := parseSchemaFile(t, `
relations:
  - from: comments.commentable_id
    polymorphic_type: comments.commentable_type
`)
	if err == nil {
		t.Fatal("expected error for polymorphic relation missing targets, got nil")
	}
}

func TestIgnoreRelation(t *testing.T) {
	sf, err := parseSchemaFile(t, `
relations:
  - ignore: audit_logs.actor_id
`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	kind, err := sf.Relations[0].Kind()
	if err != nil {
		t.Fatalf("Kind() error: %v", err)
	}
	if kind != KindIgnore {
		t.Fatalf("Kind() = %v, want KindIgnore", kind)
	}
}

func TestIgnoreCombinedWithOtherFieldsRejected(t *testing.T) {
	_, err := parseSchemaFile(t, `
relations:
  - ignore: audit_logs.actor_id
    from: audit_logs.actor_id
    to: users.id
`)
	if err == nil {
		t.Fatal("expected error for 'ignore' combined with 'from'/'to', got nil")
	}
}

func TestSelfReferentialRelationRejected(t *testing.T) {
	_, err := parseSchemaFile(t, `
relations:
  - from: users.id
    to: users.id
`)
	if err == nil {
		t.Fatal("expected error for a relation referencing itself, got nil")
	}
}

func TestRelationMissingFromOrTo(t *testing.T) {
	_, err := parseSchemaFile(t, `
relations:
  - from: orders.user_id
`)
	if err == nil {
		t.Fatal("expected error for relation missing 'to', got nil")
	}
}

func TestAggregatesMultipleErrors(t *testing.T) {
	_, err := parseSchemaFile(t, `
relations:
  - from: not_valid
    to: users.id
  - from: orders.user_id
  - ignore: also_not_valid
`)
	if err == nil {
		t.Fatal("expected aggregated errors, got nil")
	}
	msg := err.Error()
	for _, want := range []string{"relations[0]", "relations[1]", "relations[2]"} {
		if !strings.Contains(msg, want) {
			t.Errorf("aggregated error %q missing %q", msg, want)
		}
	}
}

func TestDependencyBreaksRequireTableAndColumn(t *testing.T) {
	_, err := parseSchemaFile(t, `
dependency_breaks:
  - table: employees
`)
	if err == nil {
		t.Fatal("expected error for dependency_break missing 'column', got nil")
	}
}

func TestDependencyBreakTableKeyDefaultsSchema(t *testing.T) {
	sf, err := parseSchemaFile(t, `
dependency_breaks:
  - table: employees
    column: manager_id
`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := sf.DependencyBreaks[0].TableKey(); got != "public.employees" {
		t.Fatalf("TableKey() = %q, want %q", got, "public.employees")
	}
}

func TestLoadSchemaFileFromDisk(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tributary.schema.yaml")
	doc := `
relations:
  - from: orders.user_id
    to: users.id
dependency_breaks:
  - table: employees
    column: manager_id
`
	if err := os.WriteFile(path, []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}

	sf, err := LoadSchemaFile(path)
	if err != nil {
		t.Fatalf("LoadSchemaFile: %v", err)
	}
	if len(sf.Relations) != 1 || len(sf.DependencyBreaks) != 1 {
		t.Fatalf("unexpected parsed shape: %+v", sf)
	}
}

func TestLoadSchemaFileMissingFile(t *testing.T) {
	_, err := LoadSchemaFile(filepath.Join(t.TempDir(), "does-not-exist.yaml"))
	if err == nil {
		t.Fatal("expected error for missing file, got nil")
	}
}

func TestExampleSchemaFileParses(t *testing.T) {
	// tributary.schema.example.yaml lives at the repo root; walk up from
	// this package's directory (pkg/config) to find it.
	path := filepath.Join("..", "..", "tributary.schema.example.yaml")
	sf, err := LoadSchemaFile(path)
	if err != nil {
		t.Fatalf("LoadSchemaFile(%s): %v", path, err)
	}
	if len(sf.Relations) != 4 {
		t.Fatalf("got %d relations, want 4 (soft FK, composite, polymorphic, ignore)", len(sf.Relations))
	}
	if len(sf.DependencyBreaks) != 1 {
		t.Fatalf("got %d dependency_breaks, want 1", len(sf.DependencyBreaks))
	}
}
