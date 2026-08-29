// Command tributary is the CLI entrypoint. Phase 0 shipped `inspect`,
// which connects to a Postgres database and prints its schema graph
// (tables, columns, foreign keys) as JSON. Phase 1 adds `plan`, which
// computes a referentially-consistent subset from a seed table/predicate
// (merging catalog FKs with a declared tributary.schema.yaml, see
// pkg/config and internal/graph) and prints a row-count-per-table report.
//
// Later phases add: `sync run` (phase 2), `sync watch` (phase 4), `branch`
// (phase 6, stretch). See docs/PLAN.md.
//
// This still bootstraps with the stdlib `flag` package rather than cobra.
// The original plan for this file predicted phase 1 would be when cobra
// earns its place, but phase 1 only adds one subcommand (plan, bringing
// the total to two) — still comfortably within a switch + FlagSet per
// command. The actual trigger (nested subcommands like `sync run` /
// `sync watch`, and flags like --dsn shared across nearly every command)
// arrives in phase 2+; deferring the migration until then.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/jackc/pgx/v5"

	"github.com/bhuneshvar-k/tributary/internal/catalog"
	"github.com/bhuneshvar-k/tributary/internal/graph"
	"github.com/bhuneshvar-k/tributary/internal/subset"
	"github.com/bhuneshvar-k/tributary/pkg/config"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(1)
	}

	switch os.Args[1] {
	case "inspect":
		runInspect(os.Args[2:])
	case "plan":
		runPlan(os.Args[2:])
	case "-h", "--help", "help":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n", os.Args[1])
		usage()
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, `tributary — Postgres subsetting & sync

Usage:
  tributary inspect --dsn <connection-string>
      Print the schema graph as JSON.

  tributary plan --dsn <connection-string> --seed-table <table> --seed-predicate <sql>
      Compute a referentially-consistent subset and print a row-count
      report. --seed-predicate is a raw SQL WHERE-clause fragment,
      interpolated directly — tributary is an admin tool, not a web input
      path, so it is not sanitized against injection.
      Optional: --schema-file <path>, --strict-cycles, --format text|json

More commands land as the project plan's phases ship — see docs/PLAN.md.`)
}

func runInspect(args []string) {
	fs := flag.NewFlagSet("inspect", flag.ExitOnError)
	dsn := fs.String("dsn", os.Getenv("TRIBUTARY_DSN"), "Postgres connection string (or set TRIBUTARY_DSN)")
	fs.Parse(args)

	if *dsn == "" {
		fmt.Fprintln(os.Stderr, "error: --dsn is required (or set TRIBUTARY_DSN)")
		os.Exit(1)
	}

	schema, err := catalog.Inspect(context.Background(), *dsn)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(schema); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func runPlan(args []string) {
	fs := flag.NewFlagSet("plan", flag.ExitOnError)
	dsn := fs.String("dsn", os.Getenv("TRIBUTARY_DSN"), "Postgres connection string (or set TRIBUTARY_DSN)")
	seedTable := fs.String("seed-table", "", `Seed table, e.g. "users" or "public.users"`)
	seedPredicate := fs.String("seed-predicate", "", `Raw SQL WHERE-clause fragment selecting seed rows, e.g. "id = 42" -- interpolated directly, not sanitized: tributary is an admin tool, not a web input path`)
	schemaFilePath := fs.String("schema-file", "", "Path to a tributary.schema.yaml file (optional; omit for a catalog-only graph)")
	strictCycles := fs.Bool("strict-cycles", false, "Fail on an unresolved FK cycle instead of auto-breaking it (best-effort is the default)")
	format := fs.String("format", "text", `Output format: "text" or "json"`)
	fs.Parse(args)

	if *dsn == "" {
		fmt.Fprintln(os.Stderr, "error: --dsn is required (or set TRIBUTARY_DSN)")
		os.Exit(1)
	}
	if *seedTable == "" || *seedPredicate == "" {
		fmt.Fprintln(os.Stderr, "error: --seed-table and --seed-predicate are required")
		os.Exit(1)
	}

	ctx := context.Background()

	schema, err := catalog.Inspect(ctx, *dsn)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	var sf *config.SchemaFile
	if *schemaFilePath != "" {
		sf, err = config.LoadSchemaFile(*schemaFilePath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
	}

	g, err := graph.Build(schema, sf)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	conn, err := pgx.Connect(ctx, *dsn)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	defer conn.Close(ctx)

	policy := graph.PolicyBestEffort
	if *strictCycles {
		policy = graph.PolicyError
	}
	var breaks []config.DependencyBreak
	if sf != nil {
		breaks = sf.DependencyBreaks
	}

	seedID := graph.NodeID(qualify(*seedTable))
	closure, err := graph.ComputeClosure(ctx, conn, g, seedID, *seedPredicate, graph.CycleOptions{
		Breaks:            breaks,
		OnUnresolvedCycle: policy,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	order, err := subset.TableOrder(g)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	switch *format {
	case "json":
		printPlanJSON(g, closure, order, seedID)
	default:
		printPlanText(g, closure, order, seedID)
	}

	for _, w := range closure.Warnings {
		fmt.Fprintf(os.Stderr, "warning: %s\n", w)
	}
}

// qualify defaults an unqualified table name to the "public" schema,
// matching the convention pkg/config.ColumnRef uses for table.column
// references.
func qualify(table string) string {
	if strings.Contains(table, ".") {
		return table
	}
	return "public." + table
}

func printPlanText(g *graph.Graph, closure *graph.Closure, order []graph.NodeID, seedTable graph.NodeID) {
	tw := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, "table\trows\tvia")

	total, tables := 0, 0
	for _, table := range order {
		rows := closure.Rows[table]
		if len(rows) == 0 {
			continue
		}
		total += len(rows)
		tables++
		fmt.Fprintf(tw, "%s\t%d\t%s\n", table, len(rows), viaDescription(g, closure, table, seedTable)+breakNote(closure, table))
	}
	tw.Flush()

	fmt.Printf("\ntotal: %d rows across %d tables\n", total, tables)
}

type tableCount struct {
	Table        string `json:"table"`
	Rows         int    `json:"rows"`
	Via          string `json:"via"`
	BreakApplied bool   `json:"break_applied"`
	BreakAuto    bool   `json:"break_auto"`
}

func printPlanJSON(g *graph.Graph, closure *graph.Closure, order []graph.NodeID, seedTable graph.NodeID) {
	out := make([]tableCount, 0, len(order))
	for _, table := range order {
		rows := closure.Rows[table]
		if len(rows) == 0 {
			continue
		}
		applied, auto := breakStatus(closure, table)
		out = append(out, tableCount{
			Table:        string(table),
			Rows:         len(rows),
			Via:          viaDescription(g, closure, table, seedTable),
			BreakApplied: applied,
			BreakAuto:    auto,
		})
	}

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(out); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func breakStatus(closure *graph.Closure, table graph.NodeID) (applied, auto bool) {
	for _, b := range closure.Breaks {
		if b.Table == table {
			return true, b.Auto
		}
	}
	return false, false
}

func breakNote(closure *graph.Closure, table graph.NodeID) string {
	applied, auto := breakStatus(closure, table)
	if !applied {
		return ""
	}
	if auto {
		return " (cycle, auto-break applied — add a dependency_breaks entry to control this)"
	}
	return " (cycle, dependency_breaks applied)"
}

// viaDescription explains which edge pulled table into the subset, by
// finding an edge in g connecting it to another table already present in
// closure — a table-level (not per-row) explanation, sufficient for a
// human-readable report.
func viaDescription(g *graph.Graph, closure *graph.Closure, table, seedTable graph.NodeID) string {
	if table == seedTable {
		return "seed"
	}

	for _, e := range g.Incoming[table] {
		if _, ok := closure.Rows[e.From]; ok {
			return fmt.Sprintf("%s.%s -> %s.%s", e.From, strings.Join(e.FromColumns, ","), table, strings.Join(e.ToColumns, ","))
		}
	}

	for _, pe := range g.PolyReverse[table] {
		if _, ok := closure.Rows[pe.Edge.From]; ok {
			return fmt.Sprintf("%s.%s -> %s (polymorphic: %s)", pe.Edge.From, pe.Edge.PolyTypeColumn, table, pe.TypeValue)
		}
	}

	for _, e := range g.Outgoing[table] {
		if e.Source == graph.EdgePolymorphic {
			for value, pt := range e.PolyTargets {
				if _, ok := closure.Rows[pt.To]; ok {
					return fmt.Sprintf("%s.%s -> %s.%s (polymorphic: %s)", table, e.PolyTypeColumn, pt.To, strings.Join(pt.ToColumns, ","), value)
				}
			}
			continue
		}
		if _, ok := closure.Rows[e.To]; ok {
			suffix := ""
			if len(e.FromColumns) > 1 {
				suffix = " (composite)"
			}
			return fmt.Sprintf("%s.%s -> %s.%s%s", table, strings.Join(e.FromColumns, ","), e.To, strings.Join(e.ToColumns, ","), suffix)
		}
	}

	return "?"
}
