// Command tributary is the CLI entrypoint. Phase 0 ships a single command,
// `inspect`, that connects to a Postgres database and prints its schema
// graph (tables, columns, foreign keys) as JSON.
//
// Later phases add: `plan` (phase 1, subset closure), `sync run` (phase 2),
// `sync watch` (phase 4), `branch` (phase 6, stretch). See docs/PLAN.md.
//
// This bootstraps with the stdlib `flag` package rather than cobra, on
// purpose — one command doesn't earn a CLI framework. Cobra goes in once
// phase 1 adds enough subcommands (plan, sync, branch) to need real
// subcommand routing and shared flags.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"github.com/bhuneshvar-k/tributary/internal/catalog"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(1)
	}

	switch os.Args[1] {
	case "inspect":
		runInspect(os.Args[2:])
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
  tributary inspect --dsn <connection-string>   Print the schema graph as JSON

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
