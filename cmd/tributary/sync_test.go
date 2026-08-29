package main

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
)

// Same two-container pattern as internal/load's tests: a source seeded
// from testdata/phase2/source.sql, and an empty target. Skips gracefully
// (see requireContainers) rather than failing the package when Docker
// isn't available.
var (
	sourceConn                   *pgx.Conn
	sourceConnStr, targetConnStr string
	containerErr                 error
)

func TestMain(m *testing.M) {
	os.Exit(runSyncCLITests(m))
}

func runSyncCLITests(m *testing.M) int {
	ctx := context.Background()

	fixturePath, err := filepath.Abs(filepath.Join("..", "..", "testdata", "phase2", "source.sql"))
	if err != nil {
		containerErr = fmt.Errorf("resolve fixture path: %w", err)
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

	return m.Run()
}

func requireContainers(t *testing.T) {
	t.Helper()
	if containerErr != nil {
		t.Skipf("skipping: postgres testcontainers unavailable: %v", containerErr)
	}
}

// Re-running sync run is expected to succeed and refresh the target
// (upsert), not error — this specifically covers the case that used to be
// a hard "already synced" error before upsert became the default.
func TestSyncRun_Rerun_UpsertsChangedRowInPlace(t *testing.T) {
	requireContainers(t)
	ctx := context.Background()

	if _, err := sourceConn.Exec(ctx, `INSERT INTO parent_table (id, name) VALUES (5001, 'p5001')`); err != nil {
		t.Fatalf("seed source: %v", err)
	}
	if _, err := sourceConn.Exec(ctx, `INSERT INTO child_table (id, parent_id, name) VALUES (5001, 5001, 'original name')`); err != nil {
		t.Fatalf("seed source: %v", err)
	}

	stateDB := filepath.Join(t.TempDir(), "state.db")
	opts := syncRunOptions{
		sourceDSN: sourceConnStr, targetDSN: targetConnStr,
		seedTable: "public.parent_table", seedPredicate: "id = 5001",
		createSchema: true, stateDBPath: stateDB, resume: true, format: "text",
	}

	if err := runSyncRun(opts); err != nil {
		t.Fatalf("first sync run: %v", err)
	}
	if n := targetChildRowCount(t, 5001); n != 1 {
		t.Fatalf("target child_table rows for 5001 = %d, want 1 after first run", n)
	}
	if name := targetChildName(t, 5001); name != "original name" {
		t.Fatalf("target child_table.name for 5001 = %q, want %q", name, "original name")
	}

	// Change the source row, then re-run with identical args (no --fresh).
	if _, err := sourceConn.Exec(ctx, `UPDATE child_table SET name = 'updated name' WHERE id = 5001`); err != nil {
		t.Fatalf("update source: %v", err)
	}
	if err := runSyncRun(opts); err != nil {
		t.Fatalf("second sync run (rerun): %v", err)
	}

	if n := targetChildRowCount(t, 5001); n != 1 {
		t.Fatalf("target child_table rows for 5001 = %d, want 1 after rerun (not duplicated)", n)
	}
	if name := targetChildName(t, 5001); name != "updated name" {
		t.Fatalf("target child_table.name for 5001 = %q, want %q (upsert should have updated it)", name, "updated name")
	}
}

func targetChildName(t *testing.T, id int) string {
	t.Helper()
	conn, err := pgx.Connect(context.Background(), targetConnStr)
	if err != nil {
		t.Fatalf("connect to target: %v", err)
	}
	defer conn.Close(context.Background())

	var name string
	if err := conn.QueryRow(context.Background(), `SELECT name FROM child_table WHERE id = $1`, id).Scan(&name); err != nil {
		t.Fatalf("query child_table.name: %v", err)
	}
	return name
}

func targetChildRowCount(t *testing.T, id int) int {
	t.Helper()
	conn, err := pgx.Connect(context.Background(), targetConnStr)
	if err != nil {
		t.Fatalf("connect to target: %v", err)
	}
	defer conn.Close(context.Background())

	var n int
	if err := conn.QueryRow(context.Background(), `SELECT count(*) FROM child_table WHERE id = $1`, id).Scan(&n); err != nil {
		t.Fatalf("count child_table: %v", err)
	}
	return n
}
