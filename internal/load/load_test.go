package load

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/bhuneshvar-k/tributary/internal/catalog"
	"github.com/bhuneshvar-k/tributary/internal/graph"
	"github.com/bhuneshvar-k/tributary/internal/subset"
	"github.com/bhuneshvar-k/tributary/pkg/config"
)

// Two Postgres containers — source (seeded from testdata/phase2/source.sql)
// and an empty target — shared across every test in this file. Tables are
// created on target lazily by EnsureSchema as each test needs them, so
// tests share the container but use non-overlapping id ranges per table to
// avoid interfering with each other, matching the pattern established in
// phase 1's (since-removed) closure tests.
//
// If Docker isn't available, TestMain logs why and every test in this file
// skips itself individually rather than failing the whole package.
var (
	sourceConn, targetConn       *pgx.Conn
	sourceConnStr, targetConnStr string
	containerErr                 error
)

func TestMain(m *testing.M) {
	os.Exit(runLoadTests(m))
}

func runLoadTests(m *testing.M) int {
	ctx := context.Background()

	fixturePath, err := filepath.Abs(filepath.Join("..", "..", "testdata", "phase2", "source.sql"))
	if err != nil {
		containerErr = fmt.Errorf("resolve fixture path: %w", err)
		fmt.Fprintln(os.Stderr, containerErr)
		return m.Run()
	}

	waitStrategy := wait.ForLog("database system is ready to accept connections").
		WithOccurrence(2).WithStartupTimeout(60 * time.Second)

	sourceCtr, err := postgres.Run(ctx, "postgres:16-alpine",
		postgres.WithInitScripts(fixturePath),
		postgres.WithDatabase("source"), postgres.WithUsername("postgres"), postgres.WithPassword("postgres"),
		testcontainers.WithWaitStrategy(waitStrategy))
	if err != nil {
		containerErr = fmt.Errorf("start source postgres container (is Docker running?): %w", err)
		fmt.Fprintln(os.Stderr, containerErr)
		return m.Run()
	}
	defer func() { _ = testcontainers.TerminateContainer(sourceCtr) }()

	targetCtr, err := postgres.Run(ctx, "postgres:16-alpine",
		postgres.WithDatabase("target"), postgres.WithUsername("postgres"), postgres.WithPassword("postgres"),
		testcontainers.WithWaitStrategy(waitStrategy))
	if err != nil {
		containerErr = fmt.Errorf("start target postgres container: %w", err)
		fmt.Fprintln(os.Stderr, containerErr)
		return m.Run()
	}
	defer func() { _ = testcontainers.TerminateContainer(targetCtr) }()

	sourceConnStr, err = sourceCtr.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		containerErr = fmt.Errorf("source connection string: %w", err)
		return m.Run()
	}
	targetConnStr, err = targetCtr.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		containerErr = fmt.Errorf("target connection string: %w", err)
		return m.Run()
	}

	sourceConn, err = pgx.Connect(ctx, sourceConnStr)
	if err != nil {
		containerErr = fmt.Errorf("connect to source: %w", err)
		return m.Run()
	}
	defer sourceConn.Close(ctx)

	targetConn, err = pgx.Connect(ctx, targetConnStr)
	if err != nil {
		containerErr = fmt.Errorf("connect to target: %w", err)
		return m.Run()
	}
	defer targetConn.Close(ctx)

	return m.Run()
}

func requireContainers(t *testing.T) {
	t.Helper()
	if containerErr != nil {
		t.Skipf("skipping: postgres testcontainers unavailable: %v", containerErr)
	}
}

func mustExecSource(t *testing.T, ctx context.Context, sql string, args ...any) {
	t.Helper()
	if _, err := sourceConn.Exec(ctx, sql, args...); err != nil {
		t.Fatalf("exec on source %q %v: %v", sql, args, err)
	}
}

func loadSourceSchema(t *testing.T) *catalog.Schema {
	t.Helper()
	schema, err := catalog.Inspect(context.Background(), sourceConnStr)
	if err != nil {
		t.Fatalf("catalog.Inspect(source): %v", err)
	}
	return schema
}

func loadPhase2Relations(t *testing.T) *config.SchemaFile {
	t.Helper()
	path := filepath.Join("..", "..", "testdata", "phase2", "relations.yaml")
	sf, err := config.LoadSchemaFile(path)
	if err != nil {
		t.Fatalf("config.LoadSchemaFile(%s): %v", path, err)
	}
	return sf
}

// runClosureAndOrder is the shared "plan" half of the pipeline: build the
// graph, compute the closure from source, and get a table load order —
// exactly what cmd/tributary's sync run does before calling Load.
func runClosureAndOrder(t *testing.T, sf *config.SchemaFile, seedTable, predicate string) (*graph.Graph, *graph.Closure, []graph.NodeID) {
	t.Helper()
	ctx := context.Background()

	schema := loadSourceSchema(t)
	g, err := graph.Build(schema, sf)
	if err != nil {
		t.Fatalf("graph.Build: %v", err)
	}

	closure, err := graph.ComputeClosure(ctx, sourceConn, g, graph.NodeID(seedTable), predicate, graph.CycleOptions{})
	if err != nil {
		t.Fatalf("graph.ComputeClosure: %v", err)
	}

	order, err := subset.TableOrder(g)
	if err != nil {
		t.Fatalf("subset.TableOrder: %v", err)
	}

	return g, closure, order
}

func targetRowCount(t *testing.T, table string, where string, args ...any) int {
	t.Helper()
	var n int
	sql := fmt.Sprintf("SELECT count(*) FROM %s", table)
	if where != "" {
		sql += " WHERE " + where
	}
	if err := targetConn.QueryRow(context.Background(), sql, args...).Scan(&n); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	return n
}

// --- rows 1-2: leaf table + single-FK table, target missing entirely -----

func TestLoad_LeafAndSingleFKTable(t *testing.T) {
	requireContainers(t)
	ctx := context.Background()

	mustExecSource(t, ctx, `INSERT INTO parent_table (id, name) VALUES (101, 'p101')`)
	mustExecSource(t, ctx, `INSERT INTO child_table (id, parent_id, name) VALUES (101, 101, 'c101')`)

	sourceSchema := loadSourceSchema(t)
	g, closure, order := runClosureAndOrder(t, nil, "public.parent_table", "id = 101")

	result, err := Load(ctx, targetConn, sourceSchema, g, closure, order, LoadOptions{CreateSchema: true})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if result.TotalRows != 2 {
		t.Fatalf("TotalRows = %d, want 2", result.TotalRows)
	}
	if n := targetRowCount(t, "public.child_table", "id = 101"); n != 1 {
		t.Fatalf("target child_table rows = %d, want 1", n)
	}

	// The FK constraint must actually have been created — inserting a
	// child that references a nonexistent parent should now fail on target.
	_, err = targetConn.Exec(ctx, `INSERT INTO child_table (id, parent_id, name) VALUES (999901, 999999, 'orphan')`)
	if err == nil {
		t.Fatal("expected the auto-created FK constraint to reject a child referencing a nonexistent parent")
	}
}

// --- row 3: composite FK, tests correct column pairing --------------------

func TestLoad_CompositeFK_CorrectColumnPairing(t *testing.T) {
	requireContainers(t)
	ctx := context.Background()

	mustExecSource(t, ctx, `INSERT INTO tenant_table (id) VALUES (201), (202)`)
	mustExecSource(t, ctx, `INSERT INTO composite_parent_table (tenant_id, id, name) VALUES (201, 500, 'p-201-500'), (202, 500, 'p-202-500')`)
	mustExecSource(t, ctx, `INSERT INTO composite_child_table (id, parent_id, tenant_id, sku) VALUES
		(201, 500, 201, 'sku-A'), (202, 500, 202, 'sku-B')`)

	sourceSchema := loadSourceSchema(t)
	g, closure, order := runClosureAndOrder(t, nil, "public.composite_parent_table", "tenant_id = 201 AND id = 500")

	items := closure.Rows["public.composite_child_table"]
	if len(items) != 1 {
		t.Fatalf("closure has %d composite_child_table rows, want 1 (tenant 201's only)", len(items))
	}

	result, err := Load(ctx, targetConn, sourceSchema, g, closure, order, LoadOptions{CreateSchema: true})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if result.TotalRows != 3 { // tenant, composite_parent, composite_child
		t.Fatalf("TotalRows = %d, want 3", result.TotalRows)
	}
	if n := targetRowCount(t, "public.composite_child_table", "sku = 'sku-A'"); n != 1 {
		t.Fatalf("expected sku-A on target, got count=%d", n)
	}
	if n := targetRowCount(t, "public.composite_child_table", "sku = 'sku-B'"); n != 0 {
		t.Fatalf("sku-B (tenant 202's row) must not have been loaded, got count=%d", n)
	}
}

// --- row 4: missing enum type on target is auto-created --------------------

func TestEnsureSchema_MissingEnumType_AutoCreated(t *testing.T) {
	requireContainers(t)
	ctx := context.Background()

	mustExecSource(t, ctx, `INSERT INTO enum_table (id, status) VALUES (301, 'active')`)

	sourceSchema := loadSourceSchema(t)
	_, closure, _ := runClosureAndOrder(t, nil, "public.enum_table", "id = 301")

	report, err := EnsureSchema(ctx, targetConn, sourceSchema, closure, true)
	if err != nil {
		t.Fatalf("EnsureSchema: %v", err)
	}
	found := false
	for _, name := range report.TypesCreated {
		if name == "enum_status" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected enum_status in TypesCreated, got %+v", report.TypesCreated)
	}

	var status string
	if err := targetConn.QueryRow(ctx, `SELECT status FROM enum_table WHERE id = 301`).Scan(&status); err != nil {
		t.Fatalf("query loaded row: %v", err)
	}
	if status != "active" {
		t.Fatalf("enum_table.status = %q, want %q", status, "active")
	}
}

// A USER-DEFINED type that isn't an enum (a domain, here) is still a hard,
// named error — Tributary only auto-creates enum type definitions.
func TestEnsureSchema_NonEnumCustomType_StillErrors(t *testing.T) {
	requireContainers(t)
	ctx := context.Background()

	mustExecSource(t, ctx, `INSERT INTO domain_table (id, amount) VALUES (302, 5)`)

	sourceSchema := loadSourceSchema(t)
	_, closure, _ := runClosureAndOrder(t, nil, "public.domain_table", "id = 302")

	_, err := EnsureSchema(ctx, targetConn, sourceSchema, closure, true)
	if err == nil {
		t.Fatal("expected an error for a column using a non-enum custom type not present on target")
	}
	if !strings.Contains(err.Error(), "positive_int") || !strings.Contains(err.Error(), "recognized enum") {
		t.Fatalf("error %q should name the type and mention it isn't a recognized enum", err.Error())
	}
	if n := targetRowCount(t, "information_schema.tables", "table_name = 'domain_table'"); n != 0 {
		t.Fatalf("domain_table should not have been created on target after a failed preflight, count=%d", n)
	}
}

// --- row 8: self-referencing, full DAG within the subset ------------------

func TestLoad_SelfReferencing_FullDAG_BackfillsCompletely(t *testing.T) {
	requireContainers(t)
	ctx := context.Background()

	mustExecSource(t, ctx, `INSERT INTO self_ref_table (id, next_id) VALUES (601, NULL), (602, 601), (603, 602)`)

	sourceSchema := loadSourceSchema(t)
	g, closure, order := runClosureAndOrder(t, nil, "public.self_ref_table", "id = 601")

	rows := closure.Rows["public.self_ref_table"]
	if len(rows) != 3 {
		t.Fatalf("closure has %d self_ref_table rows, want 3 (601,602,603 via incoming fan-out)", len(rows))
	}

	result, err := Load(ctx, targetConn, sourceSchema, g, closure, order, LoadOptions{CreateSchema: true})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	var tr TableResult
	for _, r := range result.Tables {
		if r.Table == "public.self_ref_table" {
			tr = r
		}
	}
	if tr.RowsBackfilled != 2 || tr.RowsLeftNull != 0 {
		t.Fatalf("self_ref_table result = %+v, want RowsBackfilled=2 RowsLeftNull=0", tr)
	}

	var nextID int
	if err := targetConn.QueryRow(ctx, `SELECT next_id FROM self_ref_table WHERE id = 602`).Scan(&nextID); err != nil {
		t.Fatalf("query backfilled row: %v", err)
	}
	if nextID != 601 {
		t.Fatalf("self_ref_table.next_id for 602 = %d, want 601 (backfilled)", nextID)
	}
}

// --- row 7: self-referencing, broken edge, true parent excluded -----------

func TestLoad_SelfReferencing_BrokenEdge_ParentExcluded_StaysNull(t *testing.T) {
	requireContainers(t)
	ctx := context.Background()

	// 702 is a valid root on source (satisfies the real FK for 701's
	// next_id), but nothing in the subset ever reaches it: 701 is seeded
	// alone, and Outgoing traversal toward 702 gets broken (best-effort
	// auto-break) rather than followed.
	mustExecSource(t, ctx, `INSERT INTO self_ref_table (id, next_id) VALUES (702, NULL), (701, 702)`)

	sourceSchema := loadSourceSchema(t)
	g, closure, order := runClosureAndOrder(t, nil, "public.self_ref_table", "id = 701")

	rows := closure.Rows["public.self_ref_table"]
	if len(rows) != 1 {
		t.Fatalf("closure has %d self_ref_table rows, want 1 (701 only — 702 must not be pulled in)", len(rows))
	}
	if len(closure.Breaks) != 1 || !closure.Breaks[0].Auto {
		t.Fatalf("expected exactly one auto-applied break, got %+v", closure.Breaks)
	}

	result, err := Load(ctx, targetConn, sourceSchema, g, closure, order, LoadOptions{CreateSchema: true})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	var tr TableResult
	for _, r := range result.Tables {
		if r.Table == "public.self_ref_table" {
			tr = r
		}
	}
	if tr.RowsBackfilled != 0 || tr.RowsLeftNull != 1 {
		t.Fatalf("self_ref_table result = %+v, want RowsBackfilled=0 RowsLeftNull=1", tr)
	}

	var nextID *int
	if err := targetConn.QueryRow(ctx, `SELECT next_id FROM self_ref_table WHERE id = 701`).Scan(&nextID); err != nil {
		t.Fatalf("query row: %v", err)
	}
	if nextID != nil {
		t.Fatalf("self_ref_table.next_id for 701 = %v, want NULL (true parent 702 was excluded from the subset)", *nextID)
	}
}

// --- row 11: NOT NULL self-referencing column ------------------------------

func TestLoad_SelfReferencingNotNull_PreflightErrorNamesColumn(t *testing.T) {
	requireContainers(t)
	ctx := context.Background()

	mustExecSource(t, ctx, `INSERT INTO self_ref_strict_table (id, next_id) VALUES (801, 801)`)

	sourceSchema := loadSourceSchema(t)
	g, closure, order := runClosureAndOrder(t, nil, "public.self_ref_strict_table", "id = 801")

	_, err := Load(ctx, targetConn, sourceSchema, g, closure, order, LoadOptions{CreateSchema: true})
	if err == nil {
		t.Fatal("expected a hard error for a NOT NULL self-referencing column")
	}
	if got := err.Error(); !strings.Contains(got, "next_id") || !strings.Contains(got, "NOT NULL") {
		t.Fatalf("error %q should name the column and mention NOT NULL", got)
	}
}

// --- polymorphic target, no FK enforcement needed --------------------------

func TestLoad_PolymorphicTarget(t *testing.T) {
	requireContainers(t)
	ctx := context.Background()

	mustExecSource(t, ctx, `INSERT INTO poly_target_a (id, name) VALUES (901, 'a901')`)
	mustExecSource(t, ctx, `INSERT INTO poly_source_table (id, target_type, target_id) VALUES (901, 'A', 901)`)

	sf := loadPhase2Relations(t)
	sourceSchema := loadSourceSchema(t)
	g, closure, order := runClosureAndOrder(t, sf, "public.poly_source_table", "id = 901")

	if len(closure.Rows["public.poly_target_a"]) != 1 {
		t.Fatalf("expected poly_target_a to be pulled in via the polymorphic association")
	}

	result, err := Load(ctx, targetConn, sourceSchema, g, closure, order, LoadOptions{CreateSchema: true})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if result.TotalRows != 2 {
		t.Fatalf("TotalRows = %d, want 2", result.TotalRows)
	}
}
