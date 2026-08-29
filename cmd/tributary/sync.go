package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"text/tabwriter"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/spf13/cobra"

	"github.com/bhuneshvar-k/tributary/internal/catalog"
	"github.com/bhuneshvar-k/tributary/internal/graph"
	"github.com/bhuneshvar-k/tributary/internal/load"
	"github.com/bhuneshvar-k/tributary/internal/state"
	"github.com/bhuneshvar-k/tributary/internal/subset"
	"github.com/bhuneshvar-k/tributary/pkg/config"
)

func newSyncCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "sync",
		Short: "Move data between databases",
	}
	cmd.AddCommand(newSyncRunCmd())
	return cmd
}

func newSyncRunCmd() *cobra.Command {
	var (
		sourceDSN, targetDSN     string
		seedTable, seedPredicate string
		schemaFilePath           string
		strictCycles             bool
		fresh                    bool
		noCreateSchema           bool
		stateDBPath              string
		noResume                 bool
		format                   string
	)

	cmd := &cobra.Command{
		Use:   "run",
		Short: "One-shot export + load: copy the computed subset from source into target",
		Long: `One-shot export + load: copy the computed subset from source into target.

Missing target tables are auto-created from source (columns, types, NOT
NULL, PRIMARY KEY, and real FOREIGN KEY constraints) unless
--no-create-schema is set, in which case a missing table is a preflight
error instead. Re-running is safe and expected: a row that already exists
on target is updated to match source (INSERT ... ON CONFLICT DO UPDATE,
keyed on primary key); a new row is inserted. --fresh instead deletes
exactly this subset's previously-loaded rows (by primary key, never a
TRUNCATE) before reloading — a stronger reset than an upsert, for when
you want target to end up with nothing but exactly today's source data.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if sourceDSN == "" {
				sourceDSN = os.Getenv("TRIBUTARY_SOURCE_DSN")
			}
			if targetDSN == "" {
				targetDSN = os.Getenv("TRIBUTARY_TARGET_DSN")
			}
			if sourceDSN == "" {
				return fmt.Errorf("--source-dsn is required (or set TRIBUTARY_SOURCE_DSN)")
			}
			if targetDSN == "" {
				return fmt.Errorf("--target-dsn is required (or set TRIBUTARY_TARGET_DSN)")
			}
			if seedTable == "" || seedPredicate == "" {
				return fmt.Errorf("--seed-table and --seed-predicate are required")
			}

			return runSyncRun(syncRunOptions{
				sourceDSN: sourceDSN, targetDSN: targetDSN,
				seedTable: seedTable, seedPredicate: seedPredicate,
				schemaFilePath: schemaFilePath, strictCycles: strictCycles,
				fresh: fresh, createSchema: !noCreateSchema,
				stateDBPath: stateDBPath, resume: !noResume,
				format: format,
			})
		},
	}

	cmd.Flags().StringVar(&sourceDSN, "source-dsn", "", "Source Postgres connection string (or set TRIBUTARY_SOURCE_DSN)")
	cmd.Flags().StringVar(&targetDSN, "target-dsn", "", "Target Postgres connection string (or set TRIBUTARY_TARGET_DSN)")
	cmd.Flags().StringVar(&seedTable, "seed-table", "", `Seed table, e.g. "users" or "public.users"`)
	cmd.Flags().StringVar(&seedPredicate, "seed-predicate", "", `Raw SQL WHERE-clause fragment selecting seed rows, e.g. "id = 42"`)
	cmd.Flags().StringVar(&schemaFilePath, "schema-file", "", "Path to a tributary.schema.yaml file (optional)")
	cmd.Flags().BoolVar(&strictCycles, "strict-cycles", false, "Fail on an unresolved FK cycle instead of auto-breaking it")
	cmd.Flags().BoolVar(&fresh, "fresh", false, "Delete this subset's previously-loaded rows from target first (stronger than the default upsert), and abandon any stuck in-progress run for this configuration")
	cmd.Flags().BoolVar(&noCreateSchema, "no-create-schema", false, "Do not auto-create missing target tables; fail preflight instead")
	cmd.Flags().StringVar(&stateDBPath, "state-db", "./.tributary/state.db", "Path to the checkpoint SQLite database")
	cmd.Flags().BoolVar(&noResume, "no-resume", false, "Disable crash-resume checkpointing for this run")
	cmd.Flags().StringVar(&format, "format", "text", `Output format: "text" or "json"`)
	return cmd
}

type syncRunOptions struct {
	sourceDSN, targetDSN     string
	seedTable, seedPredicate string
	schemaFilePath           string
	strictCycles             bool
	fresh                    bool
	createSchema             bool
	stateDBPath              string
	resume                   bool
	format                   string
}

func runSyncRun(opts syncRunOptions) error {
	ctx := context.Background()

	sourceSchema, err := catalog.Inspect(ctx, opts.sourceDSN)
	if err != nil {
		return fmt.Errorf("inspect source: %w", err)
	}

	var sf *config.SchemaFile
	var schemaHash string
	if opts.schemaFilePath != "" {
		sf, err = config.LoadSchemaFile(opts.schemaFilePath)
		if err != nil {
			return err
		}
		if schemaHash, err = fileHash(opts.schemaFilePath); err != nil {
			return err
		}
	}

	g, err := graph.Build(sourceSchema, sf)
	if err != nil {
		return err
	}

	sourceConn, err := pgx.Connect(ctx, opts.sourceDSN)
	if err != nil {
		return fmt.Errorf("connect to source: %w", err)
	}
	defer sourceConn.Close(ctx)

	policy := graph.PolicyBestEffort
	if opts.strictCycles {
		policy = graph.PolicyError
	}
	var breaks []config.DependencyBreak
	if sf != nil {
		breaks = sf.DependencyBreaks
	}

	seedID := graph.NodeID(qualify(opts.seedTable))
	closure, err := graph.ComputeClosure(ctx, sourceConn, g, seedID, opts.seedPredicate, graph.CycleOptions{
		Breaks:            breaks,
		OnUnresolvedCycle: policy,
	})
	if err != nil {
		return err
	}

	order, err := subset.TableOrder(g)
	if err != nil {
		return err
	}

	targetConn, err := pgx.Connect(ctx, opts.targetDSN)
	if err != nil {
		return fmt.Errorf("connect to target: %w", err)
	}
	defer targetConn.Close(ctx)

	loadOpts := load.LoadOptions{
		Fresh:        opts.fresh,
		CreateSchema: opts.createSchema,
		BatchSize:    500,
	}

	var store *state.Store
	var runID string
	if opts.resume {
		if err := os.MkdirAll(filepath.Dir(opts.stateDBPath), 0o755); err != nil {
			return fmt.Errorf("create state db directory: %w", err)
		}
		store, err = state.Open(opts.stateDBPath)
		if err != nil {
			return err
		}
		defer store.Close()

		sourceFP, err := dsnFingerprint(opts.sourceDSN)
		if err != nil {
			return err
		}
		targetFP, err := dsnFingerprint(opts.targetDSN)
		if err != nil {
			return err
		}

		key := state.RunKey{
			SourceFingerprint: sourceFP,
			TargetFingerprint: targetFP,
			SeedTable:         string(seedID),
			SeedPredicate:     opts.seedPredicate,
			SchemaFileHash:    schemaHash,
		}

		// GetOrCreateRun resumes an in-progress (crash-interrupted) run as-is,
		// or resets a completed/failed one for reprocessing — sync run
		// upserts, so re-invoking it is expected to just refresh the target,
		// never blocked on a prior successful run.
		run, err := store.GetOrCreateRun(ctx, key, opts.fresh)
		if err != nil {
			return err
		}

		runID = run.ID
		loadOpts.RunID = runID
		loadOpts.Store = store
	}

	result, err := load.Load(ctx, targetConn, sourceSchema, g, closure, order, loadOpts)
	if err != nil {
		if store != nil && runID != "" {
			_ = store.MarkRunFailed(ctx, runID, err.Error())
		}
		return err
	}

	if store != nil && runID != "" {
		if err := store.MarkRunCompleted(ctx, runID); err != nil {
			return err
		}
	}

	for _, w := range closure.Warnings {
		fmt.Fprintf(os.Stderr, "warning: %s\n", w)
	}

	switch opts.format {
	case "json":
		return printSyncResultJSON(result, runID)
	default:
		printSyncResultText(result, runID)
		return nil
	}
}

func dsnFingerprint(dsn string) (string, error) {
	cfg, err := pgconn.ParseConfig(dsn)
	if err != nil {
		return "", fmt.Errorf("parse DSN: %w", err)
	}
	return fmt.Sprintf("%s:%d/%s", cfg.Host, cfg.Port, cfg.Database), nil
}

func fileHash(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read schema file: %w", err)
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

func printSyncResultText(result *load.LoadResult, runID string) {
	tw := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, "table\tcopied\tbackfilled\tleft_null\tstatus")
	for _, t := range result.Tables {
		status := "loaded"
		if t.SkippedResumed {
			status = "skipped (resumed)"
		}
		fmt.Fprintf(tw, "%s\t%d\t%d\t%d\t%s\n", t.Table, t.RowsCopied, t.RowsBackfilled, t.RowsLeftNull, status)
	}
	tw.Flush()

	fmt.Printf("\ntotal: %d rows loaded across %d tables\n", result.TotalRows, len(result.Tables))
	if runID != "" {
		fmt.Printf("run: %s (completed)\n", runID)
	}
}

type syncTableResult struct {
	Table          string `json:"table"`
	RowsCopied     int64  `json:"rows_copied"`
	RowsBackfilled int64  `json:"rows_backfilled"`
	RowsLeftNull   int64  `json:"rows_left_null"`
	SkippedResumed bool   `json:"skipped_resumed"`
}

type syncResult struct {
	RunID     string            `json:"run_id,omitempty"`
	TotalRows int64             `json:"total_rows"`
	Tables    []syncTableResult `json:"tables"`
}

func printSyncResultJSON(result *load.LoadResult, runID string) error {
	out := syncResult{RunID: runID, TotalRows: result.TotalRows}
	for _, t := range result.Tables {
		out.Tables = append(out.Tables, syncTableResult{
			Table: string(t.Table), RowsCopied: t.RowsCopied, RowsBackfilled: t.RowsBackfilled,
			RowsLeftNull: t.RowsLeftNull, SkippedResumed: t.SkippedResumed,
		})
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}
