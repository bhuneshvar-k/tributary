package graph

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/bhuneshvar-k/tributary/internal/catalog"
	"github.com/bhuneshvar-k/tributary/pkg/config"
)

// One Postgres container, loaded once from testdata/fixture.sql, is shared
// across every test in this file — the fixture data is static and every
// test only reads it (closure computation never mutates rows), and each
// test uses its own non-overlapping id range so they don't interfere with
// each other despite sharing state.
//
// If Docker isn't available, TestMain logs why and still runs the suite:
// the pure in-memory tests in merge_test.go and order_test.go don't need a
// container and must keep passing regardless; each test in *this* file
// calls requireContainer to skip itself individually when containerErr is
// set, rather than the whole package failing to even start.
var (
	testConn     *pgx.Conn
	testConnStr  string
	containerErr error
)

func TestMain(m *testing.M) {
	os.Exit(runClosureTests(m))
}

func runClosureTests(m *testing.M) int {
	ctx := context.Background()

	fixturePath, err := filepath.Abs(filepath.Join("..", "..", "testdata", "fixture.sql"))
	if err != nil {
		containerErr = fmt.Errorf("resolve fixture path: %w", err)
		fmt.Fprintln(os.Stderr, containerErr)
		return m.Run()
	}

	ctr, err := postgres.Run(ctx,
		"postgres:16-alpine",
		postgres.WithInitScripts(fixturePath),
		postgres.WithDatabase("tributary_test"),
		postgres.WithUsername("postgres"),
		postgres.WithPassword("postgres"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(60*time.Second),
		),
	)
	if err != nil {
		containerErr = fmt.Errorf("start postgres container (is Docker running?): %w", err)
		fmt.Fprintln(os.Stderr, containerErr)
		return m.Run()
	}
	defer func() {
		if err := testcontainers.TerminateContainer(ctr); err != nil {
			fmt.Fprintln(os.Stderr, "terminate container:", err)
		}
	}()

	connStr, err := ctr.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		containerErr = fmt.Errorf("connection string: %w", err)
		fmt.Fprintln(os.Stderr, containerErr)
		return m.Run()
	}

	conn, err := pgx.Connect(ctx, connStr)
	if err != nil {
		containerErr = fmt.Errorf("connect: %w", err)
		fmt.Fprintln(os.Stderr, containerErr)
		return m.Run()
	}
	defer conn.Close(ctx)

	testConnStr = connStr
	testConn = conn

	return m.Run()
}

// requireContainer skips the calling test if the shared Postgres container
// failed to start (e.g. no Docker daemon available) instead of failing it.
func requireContainer(t *testing.T) {
	t.Helper()
	if containerErr != nil {
		t.Skipf("skipping: postgres testcontainer unavailable: %v", containerErr)
	}
}

func mustExec(t *testing.T, ctx context.Context, sql string, args ...any) {
	t.Helper()
	if _, err := testConn.Exec(ctx, sql, args...); err != nil {
		t.Fatalf("exec %q %v: %v", sql, args, err)
	}
}

func loadSchema(t *testing.T) *catalog.Schema {
	t.Helper()
	schema, err := catalog.Inspect(context.Background(), testConnStr)
	if err != nil {
		t.Fatalf("catalog.Inspect: %v", err)
	}
	return schema
}

func loadRelations(t *testing.T) *config.SchemaFile {
	t.Helper()
	path := filepath.Join("..", "..", "testdata", "relations.yaml")
	sf, err := config.LoadSchemaFile(path)
	if err != nil {
		t.Fatalf("config.LoadSchemaFile(%s): %v", path, err)
	}
	return sf
}

// --- 1. composite FK ---------------------------------------------------

func TestClosureCompositeFK(t *testing.T) {
	requireContainer(t)
	ctx := context.Background()

	mustExec(t, ctx, `insert into tenants (id) values (101), (102)`)
	mustExec(t, ctx, `insert into orders (tenant_id, id, user_id) values (101, 500, 1), (102, 500, 1)`)
	mustExec(t, ctx, `insert into line_items (order_id, tenant_id, sku) values (500, 101, 'A'), (500, 102, 'B')`)

	schema := loadSchema(t)
	g, err := Build(schema, nil)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	closure, err := ComputeClosure(ctx, testConn, g, "public.orders", "tenant_id = 101 AND id = 500", CycleOptions{})
	if err != nil {
		t.Fatalf("ComputeClosure: %v", err)
	}

	items := closure.Rows["public.line_items"]
	if len(items) != 1 {
		t.Fatalf("got %d line_items, want 1 (tenant 101's line item only), rows=%+v", len(items), items)
	}
	for _, r := range items {
		if sku, _ := r.Data["sku"].(string); sku != "A" {
			t.Fatalf("got line_item sku=%v, want %q (tenant 102's line item must not be pulled in)", r.Data["sku"], "A")
		}
	}
}

// --- 2. self-referencing cycle ------------------------------------------

func TestClosureSelfReferencingCycleAutoBreak(t *testing.T) {
	requireContainer(t)
	ctx := context.Background()
	mustExec(t, ctx, `insert into employees (id, manager_id) values (901, null), (902, 901), (903, 902)`)

	schema := loadSchema(t)
	g, err := Build(schema, nil)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	closure, err := ComputeClosure(ctx, testConn, g, "public.employees", "id = 901", CycleOptions{})
	if err != nil {
		t.Fatalf("ComputeClosure: %v", err)
	}

	got := closure.Rows["public.employees"]
	if len(got) != 3 {
		t.Fatalf("got %d employees, want 3 (901, 902, 903 via incoming fan-out), rows=%+v", len(got), got)
	}

	if len(closure.Breaks) != 1 {
		t.Fatalf("got %d applied breaks, want 1, breaks=%+v", len(closure.Breaks), closure.Breaks)
	}
	b := closure.Breaks[0]
	if b.Table != "public.employees" || b.Column != "manager_id" || !b.Auto {
		t.Fatalf("unexpected AppliedBreak: %+v, want {public.employees manager_id Auto:true}", b)
	}
}

func TestClosureSelfReferencingCycleStrictErrors(t *testing.T) {
	requireContainer(t)
	ctx := context.Background()
	mustExec(t, ctx, `insert into employees (id, manager_id) values (911, null), (912, 911)`)

	schema := loadSchema(t)
	g, err := Build(schema, nil)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	_, err = ComputeClosure(ctx, testConn, g, "public.employees", "id = 912", CycleOptions{
		OnUnresolvedCycle: PolicyError,
	})
	if err == nil {
		t.Fatal("expected an error under PolicyError for an unresolved cycle, got nil")
	}
}

func TestClosureSelfReferencingCycleExplicitBreak(t *testing.T) {
	requireContainer(t)
	ctx := context.Background()
	mustExec(t, ctx, `insert into employees (id, manager_id) values (921, null), (922, 921), (923, 922)`)

	schema := loadSchema(t)
	g, err := Build(schema, nil)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	closure, err := ComputeClosure(ctx, testConn, g, "public.employees", "id = 922", CycleOptions{
		Breaks: []config.DependencyBreak{{Table: "employees", Column: "manager_id"}},
	})
	if err != nil {
		t.Fatalf("ComputeClosure: %v", err)
	}

	got := closure.Rows["public.employees"]
	// 922 (seed) and 923 (reports to 922, via incoming fan-out) — but not
	// 921, since the outgoing edge from 922 to its own manager was broken.
	if len(got) != 2 {
		t.Fatalf("got %d employees, want 2 (922, 923), rows=%+v", len(got), got)
	}
	for _, r := range got {
		if fmt.Sprintf("%v", r.Data["id"]) == "921" {
			t.Fatalf("employee 921 should not be in the closure (edge to it was broken), rows=%+v", got)
		}
	}

	if len(closure.Breaks) != 1 || closure.Breaks[0].Auto {
		t.Fatalf("expected exactly one non-auto AppliedBreak, got %+v", closure.Breaks)
	}
}

// --- 3. soft FK (declared relation, no DB constraint) -------------------

func TestClosureSoftFK(t *testing.T) {
	requireContainer(t)
	ctx := context.Background()
	mustExec(t, ctx, `insert into users (id) values (201)`)
	mustExec(t, ctx, `insert into orders (tenant_id, id, user_id) values (201, 201, 201)`)

	schema := loadSchema(t)
	sf := loadRelations(t)

	t.Run("with relations file, order is pulled in", func(t *testing.T) {
		g, err := Build(schema, sf)
		if err != nil {
			t.Fatalf("Build: %v", err)
		}
		closure, err := ComputeClosure(ctx, testConn, g, "public.users", "id = 201", CycleOptions{})
		if err != nil {
			t.Fatalf("ComputeClosure: %v", err)
		}
		if len(closure.Rows["public.orders"]) != 1 {
			t.Fatalf("got %d orders, want 1 (soft FK should have pulled it in), rows=%+v", len(closure.Rows["public.orders"]), closure.Rows["public.orders"])
		}
	})

	t.Run("without relations file, order is not pulled in", func(t *testing.T) {
		g, err := Build(schema, nil)
		if err != nil {
			t.Fatalf("Build: %v", err)
		}
		closure, err := ComputeClosure(ctx, testConn, g, "public.users", "id = 201", CycleOptions{})
		if err != nil {
			t.Fatalf("ComputeClosure: %v", err)
		}
		if len(closure.Rows["public.orders"]) != 0 {
			t.Fatalf("got %d orders, want 0 (no catalog FK, no declared relation), rows=%+v", len(closure.Rows["public.orders"]), closure.Rows["public.orders"])
		}
	})
}

// --- 4. polymorphic association ------------------------------------------

func TestClosurePolymorphic(t *testing.T) {
	requireContainer(t)
	ctx := context.Background()
	mustExec(t, ctx, `insert into posts (id) values (301)`)
	mustExec(t, ctx, `insert into photos (id) values (302)`)
	mustExec(t, ctx, `insert into comments (id, commentable_type, commentable_id) values
		(303, 'Post', 301), (304, 'Photo', 302), (305, 'Bogus', 999)`)

	schema := loadSchema(t)
	sf := loadRelations(t)
	g, err := Build(schema, sf)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	t.Run("forward: comment resolves to its Post target", func(t *testing.T) {
		closure, err := ComputeClosure(ctx, testConn, g, "public.comments", "id = 303", CycleOptions{})
		if err != nil {
			t.Fatalf("ComputeClosure: %v", err)
		}
		if len(closure.Rows["public.posts"]) != 1 {
			t.Fatalf("got %d posts, want 1, rows=%+v", len(closure.Rows["public.posts"]), closure.Rows["public.posts"])
		}
	})

	t.Run("reverse: seeding the target finds only the matching-type comment", func(t *testing.T) {
		closure, err := ComputeClosure(ctx, testConn, g, "public.posts", "id = 301", CycleOptions{})
		if err != nil {
			t.Fatalf("ComputeClosure: %v", err)
		}
		comments := closure.Rows["public.comments"]
		if len(comments) != 1 {
			t.Fatalf("got %d comments, want 1 (only the Post-typed one), rows=%+v", len(comments), comments)
		}
		for _, r := range comments {
			if fmt.Sprintf("%v", r.Data["id"]) != "303" {
				t.Fatalf("got comment id=%v, want 303", r.Data["id"])
			}
		}
	})

	t.Run("unrecognized discriminator value warns instead of failing", func(t *testing.T) {
		closure, err := ComputeClosure(ctx, testConn, g, "public.comments", "id = 305", CycleOptions{})
		if err != nil {
			t.Fatalf("ComputeClosure: %v", err)
		}
		if len(closure.Rows["public.comments"]) != 1 {
			t.Fatalf("expected the seed comment itself to still be in the closure")
		}
		if len(closure.Warnings) == 0 {
			t.Fatal("expected a warning about the unrecognized polymorphic type value, got none")
		}
	})
}

// --- 5. ignored real FK ---------------------------------------------------

func TestClosureIgnore(t *testing.T) {
	requireContainer(t)
	ctx := context.Background()
	mustExec(t, ctx, `insert into users (id) values (401)`)
	mustExec(t, ctx, `insert into audit_logs (id, actor_id) values (401, 401)`)

	schema := loadSchema(t)
	sf := loadRelations(t)

	t.Run("with ignore, audit_logs is not pulled in", func(t *testing.T) {
		g, err := Build(schema, sf)
		if err != nil {
			t.Fatalf("Build: %v", err)
		}
		closure, err := ComputeClosure(ctx, testConn, g, "public.users", "id = 401", CycleOptions{})
		if err != nil {
			t.Fatalf("ComputeClosure: %v", err)
		}
		if len(closure.Rows["public.audit_logs"]) != 0 {
			t.Fatalf("got %d audit_logs, want 0 (ignored), rows=%+v", len(closure.Rows["public.audit_logs"]), closure.Rows["public.audit_logs"])
		}
	})

	t.Run("without ignore, audit_logs is pulled in via the real FK", func(t *testing.T) {
		g, err := Build(schema, nil)
		if err != nil {
			t.Fatalf("Build: %v", err)
		}
		closure, err := ComputeClosure(ctx, testConn, g, "public.users", "id = 401", CycleOptions{})
		if err != nil {
			t.Fatalf("ComputeClosure: %v", err)
		}
		if len(closure.Rows["public.audit_logs"]) != 1 {
			t.Fatalf("got %d audit_logs, want 1, rows=%+v", len(closure.Rows["public.audit_logs"]), closure.Rows["public.audit_logs"])
		}
	})
}
