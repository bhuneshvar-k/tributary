package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/jackc/pgx/v5"
	"github.com/spf13/cobra"

	"github.com/bhuneshvar-k/tributary/internal/catalog"
	"github.com/bhuneshvar-k/tributary/internal/graph"
	"github.com/bhuneshvar-k/tributary/internal/subset"
	"github.com/bhuneshvar-k/tributary/pkg/config"
	"github.com/bhuneshvar-k/tributary/pkg/userconfig"
)

func newPlanCmd() *cobra.Command {
	var (
		dsn             string
		seedTable       string
		seedPredicate   string
		schemaFilePath  string
		strictCycles    bool
		includeUpstream bool
		format          string
	)

	cmd := &cobra.Command{
		Use:   "plan",
		Short: "Compute a referentially-consistent subset and print a row-count report",
		Long: `Compute a referentially-consistent subset and print a row-count report.

--seed-predicate is a raw SQL WHERE-clause fragment, interpolated
directly — tributary is an admin tool, not a web input path, so it is not
sanitized against injection.

By default the subset is scoped downstream of the seed: a required parent
row (e.g. the company a seeded user's membership references) is always
included, but isn't itself used to fan back out to its other members —
only what's actually reachable by fanning out from the seed is. Pass
--include-upstream to fan out from every row regardless of how it was
reached (the seed's whole company, not just the seed).`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if dsn == "" {
				dsn = os.Getenv("TRIBUTARY_DSN")
			}
			if dsn == "" {
				if userCfg, err := userconfig.Load(); err == nil {
					dsn = userCfg.DSN
				}
			}
			if dsn == "" {
				return fmt.Errorf("--dsn is required (or set TRIBUTARY_DSN, or use 'tributary config set dsn <dsn>')")
			}
			if seedTable == "" {
				if userCfg, err := userconfig.Load(); err == nil && userCfg.SeedTable != "" {
					seedTable = userCfg.SeedTable
				}
			}
			if seedPredicate == "" {
				if userCfg, err := userconfig.Load(); err == nil && userCfg.SeedPredicate != "" {
					seedPredicate = userCfg.SeedPredicate
				}
			}
			if seedTable == "" || seedPredicate == "" {
				return fmt.Errorf("--seed-table and --seed-predicate are required (or set via 'tributary config set')")
			}
			if schemaFilePath == "" {
				if userCfg, err := userconfig.Load(); err == nil && userCfg.SchemaFile != "" {
					schemaFilePath = userCfg.SchemaFile
				}
			}

			ctx := context.Background()

			schema, err := catalog.Inspect(ctx, dsn)
			if err != nil {
				return err
			}

			var sf *config.SchemaFile
			if schemaFilePath != "" {
				sf, err = config.LoadSchemaFile(schemaFilePath)
				if err != nil {
					return err
				}
			}

			g, err := graph.Build(schema, sf)
			if err != nil {
				return err
			}

			conn, err := pgx.Connect(ctx, dsn)
			if err != nil {
				return err
			}
			defer conn.Close(ctx)

			policy := graph.PolicyBestEffort
			if strictCycles {
				policy = graph.PolicyError
			}
			var breaks []config.DependencyBreak
			if sf != nil {
				breaks = sf.DependencyBreaks
			}

			mode := graph.ModeDownstreamOnly
			if includeUpstream {
				mode = graph.ModeFull
			}

			seedID := graph.NodeID(qualify(seedTable))
			closure, err := graph.ComputeClosure(ctx, conn, g, seedID, seedPredicate, graph.ClosureOptions{
				Breaks:            breaks,
				OnUnresolvedCycle: policy,
				Mode:              mode,
			})
			if err != nil {
				return err
			}

			order, err := subset.TableOrder(g)
			if err != nil {
				return err
			}

			switch format {
			case "json":
				if err := printPlanJSON(g, closure, order, seedID); err != nil {
					return err
				}
			default:
				printPlanText(g, closure, order, seedID)
			}

			for _, w := range closure.Warnings {
				fmt.Fprintf(os.Stderr, "warning: %s\n", w)
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&dsn, "dsn", "", "Postgres connection string (or set TRIBUTARY_DSN)")
	cmd.Flags().StringVar(&seedTable, "seed-table", "", `Seed table, e.g. "users" or "public.users"`)
	cmd.Flags().StringVar(&seedPredicate, "seed-predicate", "", `Raw SQL WHERE-clause fragment selecting seed rows, e.g. "id = 42"`)
	cmd.Flags().StringVar(&schemaFilePath, "schema-file", "", "Path to a tributary.schema.yaml file (optional; omit for a catalog-only graph)")
	cmd.Flags().BoolVar(&strictCycles, "strict-cycles", false, "Fail on an unresolved FK cycle instead of auto-breaking it (best-effort is the default)")
	cmd.Flags().BoolVar(&includeUpstream, "include-upstream", false, "Also fan out from required-parent rows (e.g. every other member of the seed's company), not just the seed's own downstream data")
	cmd.Flags().StringVar(&format, "format", "text", `Output format: "text" or "json"`)
	return cmd
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

func printPlanJSON(g *graph.Graph, closure *graph.Closure, order []graph.NodeID, seedTable graph.NodeID) error {
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
	return enc.Encode(out)
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
