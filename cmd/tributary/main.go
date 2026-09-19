// Command tributary is the CLI entrypoint.
//
// Phase 0 shipped `inspect`, which connects to a Postgres database and
// prints its schema graph (tables, columns, foreign keys) as JSON. Phase 1
// added `plan`, which computes a referentially-consistent subset from a
// seed table/predicate (merging catalog FKs with a declared
// tributary.schema.yaml, see pkg/config and internal/graph) and prints a
// row-count-per-table report. Phase 2 adds `sync run`, which actually
// copies that subset into a target database (see internal/load,
// internal/state).
//
// Later phases add: `sync watch` (phase 4), `branch` (phase 6, stretch).
// See docs/PLAN.md.
//
// This migrated from stdlib flag to cobra in phase 2, confirming what
// phase 1's doc comment predicted: `sync run` brought both trigger
// conditions phase 1 named — a nested subcommand (`sync run`, later `sync
// watch`) and a DSN-shaped flag needed more than once per invocation
// (--source-dsn/--target-dsn).
package main

import (
	"fmt"
	"os"

	"github.com/bhuneshvar-k/tributary/internal/version"
	"github.com/spf13/cobra"
)

func main() {
	if err := newRootCmd().Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func newRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:           "tributary",
		Short:         "Postgres subsetting & sync",
		SilenceUsage:  true,
		SilenceErrors: true,
		Version:       version.Short(),
		PersistentPreRun: func(cmd *cobra.Command, args []string) {
			// Skip version check for certain commands
			if cmd.Name() == "version" || cmd.Name() == "update" || cmd.Name() == "help" {
				return
			}

			// Check for updates (non-blocking, cached daily)
			if updateMsg, hasUpdate := version.CheckForUpdate(version.Version()); hasUpdate {
				fmt.Fprintf(os.Stderr, "ℹ️  %s\n", updateMsg)
			}
		},
	}
	root.AddCommand(newInspectCmd())
	root.AddCommand(newPlanCmd())
	root.AddCommand(newSyncCmd())
	root.AddCommand(newUpdateCmd())
	return root
}
