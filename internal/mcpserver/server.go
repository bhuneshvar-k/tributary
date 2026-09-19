// Package mcpserver exposes Tributary's core operations as MCP tools so AI
// assistants can inspect databases, preview subsets, and run sync
// operations without shelling out to the CLI.
package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/bhuneshvar-k/tributary/internal/catalog"
	"github.com/bhuneshvar-k/tributary/internal/graph"
	"github.com/bhuneshvar-k/tributary/internal/load"
	"github.com/bhuneshvar-k/tributary/internal/subset"
	"github.com/bhuneshvar-k/tributary/internal/version"
	"github.com/bhuneshvar-k/tributary/pkg/config"
)

// NewServer creates and returns a fully configured MCP server with all
// Tributary tools registered. The caller should serve it over stdio.
func NewServer() *mcp.Server {
	s := mcp.NewServer(
		&mcp.Implementation{
			Name:    "tributary-mcp",
			Version: version.String(),
		},
		nil,
	)

	registerTools(s)
	return s
}

// ── Input types ──────────────────────────────────────────────────

type VersionInput = map[string]any

type InspectSchemaInput struct {
	DSN string `json:"dsn" jsonschema:"description=Postgres connection string (e.g. postgres://user:pass@host:5432/dbname?sslmode=disable)"`
}

type ListTablesInput struct {
	DSN string `json:"dsn" jsonschema:"description=Postgres connection string"`
}

type CheckConnectionInput struct {
	DSN string `json:"dsn" jsonschema:"description=Postgres connection string"`
}

type ValidateConfigInput struct {
	ConfigFile string `json:"config_file" jsonschema:"description=Path to the tributary schema YAML file"`
}

type SubsetPreviewInput struct {
	DSN            string `json:"dsn" jsonschema:"description=Postgres connection string for the SOURCE database"`
	SeedTable      string `json:"seed_table" jsonschema:"description=Fully qualified seed table (e.g. public.users)"`
	SeedPredicate  string `json:"seed_predicate" jsonschema:"description=SQL WHERE clause without WHERE keyword (e.g. \"email = 'admin@example.com'\")"`
	ConfigFile     string `json:"config_file,omitempty" jsonschema:"description=Optional path to a tributary schema file"`
}

type SubsetSyncInput struct {
	SourceDSN      string `json:"source_dsn" jsonschema:"description=Postgres connection string for the SOURCE database"`
	TargetDSN      string `json:"target_dsn" jsonschema:"description=Postgres connection string for the TARGET database"`
	SeedTable      string `json:"seed_table" jsonschema:"description=Fully qualified seed table (e.g. public.users)"`
	SeedPredicate  string `json:"seed_predicate" jsonschema:"description=SQL WHERE clause without WHERE keyword (e.g. \"email = 'admin@example.com'\")"`
	Fresh          bool   `json:"fresh,omitempty" jsonschema:"description=Delete existing subset rows from target before reload (default: false)"`
	ConfigFile     string `json:"config_file,omitempty" jsonschema:"description=Optional path to a tributary schema file"`
	BatchSize      int    `json:"batch_size,omitempty" jsonschema:"description=Rows per batch for backfill/delete (default: 500)"`
}

// ── Tool registration ────────────────────────────────────────────

func registerTools(s *mcp.Server) {
	// ── version ──────────────────────────────────────────────
	type versionOutput struct {
		Version string `json:"version"`
		Commit  string `json:"commit"`
		Date    string `json:"date"`
	}

	mcp.AddTool(s, &mcp.Tool{
		Name:        "tributary_version",
		Description: "Return the Tributary version, commit, and build date.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, _ VersionInput) (*mcp.CallToolResult, versionOutput, error) {
		return nil, versionOutput{
			Version: version.String(),
			Commit:  version.Commit(),
			Date:    version.Date(),
		}, nil
	})

	// ── inspect schema ───────────────────────────────────────
	type inspectOutput = *catalog.Schema

	mcp.AddTool(s, &mcp.Tool{
		Name:        "tributary_inspect_schema",
		Description: "Inspect a Postgres database schema: tables, columns, types, primary keys, and foreign keys. Returns structured JSON useful for understanding what tables exist and how they relate.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, input InspectSchemaInput) (*mcp.CallToolResult, inspectOutput, error) {
		conn, err := pgx.Connect(ctx, input.DSN)
		if err != nil {
			return nil, nil, fmt.Errorf("connect to database: %w", err)
		}
		defer conn.Close(ctx)

		schema, err := catalog.InspectConn(ctx, conn)
		if err != nil {
			return nil, nil, fmt.Errorf("inspect schema: %w", err)
		}

		return nil, schema, nil
	})

	// ── list tables ──────────────────────────────────────────
	type tableSummary struct {
		Name        string   `json:"name"`
		Schema      string   `json:"schema"`
		Columns     int      `json:"columns"`
		PrimaryKey  []string `json:"primary_key,omitempty"`
		ForeignKeys int      `json:"foreign_keys"`
	}
	type listTablesOutput struct {
		Total  int            `json:"total"`
		Tables []tableSummary `json:"tables"`
	}

	mcp.AddTool(s, &mcp.Tool{
		Name:        "tributary_list_tables",
		Description: "List all user tables in a Postgres database with their column count, PK columns, and FK count. Quick overview of database structure.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, input ListTablesInput) (*mcp.CallToolResult, listTablesOutput, error) {
		conn, err := pgx.Connect(ctx, input.DSN)
		if err != nil {
			return nil, listTablesOutput{}, fmt.Errorf("connect to database: %w", err)
		}
		defer conn.Close(ctx)

		schema, err := catalog.InspectConn(ctx, conn)
		if err != nil {
			return nil, listTablesOutput{}, fmt.Errorf("inspect schema: %w", err)
		}

		var tables []tableSummary
		for _, t := range schema.Tables {
			tables = append(tables, tableSummary{
				Name:        t.Name,
				Schema:      t.Schema,
				Columns:     len(t.Columns),
				PrimaryKey:  t.PrimaryKey,
				ForeignKeys: len(t.ForeignKeys),
			})
		}

		return nil, listTablesOutput{Total: len(tables), Tables: tables}, nil
	})

	// ── check connection ─────────────────────────────────────
	type checkConnOutput struct {
		Status  string `json:"status"`
		Version string `json:"version"`
		Uptime  string `json:"uptime,omitempty"`
	}

	mcp.AddTool(s, &mcp.Tool{
		Name:        "tributary_check_connection",
		Description: "Test connectivity to a Postgres database and return the server version. Useful for verifying DSN strings before running sync.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, input CheckConnectionInput) (*mcp.CallToolResult, checkConnOutput, error) {
		conn, err := pgx.Connect(ctx, input.DSN)
		if err != nil {
			return nil, checkConnOutput{Status: "error"}, fmt.Errorf("connect: %w", err)
		}
		defer conn.Close(ctx)

		var ver string
		if err := conn.QueryRow(ctx, "SELECT version()").Scan(&ver); err != nil {
			return nil, checkConnOutput{Status: "error"}, fmt.Errorf("query version: %w", err)
		}

		var uptime string
		_ = conn.QueryRow(ctx, "SELECT now() - pg_postmaster_start_time()::timestamp").Scan(&uptime)

		return nil, checkConnOutput{Status: "connected", Version: ver, Uptime: uptime}, nil
	})

	// ── validate config ──────────────────────────────────────
	type validateConfigOutput struct {
		Status    string            `json:"status"`
		File      string            `json:"file"`
		Relations []config.Relation `json:"relations,omitempty"`
	}

	mcp.AddTool(s, &mcp.Tool{
		Name:        "tributary_validate_config",
		Description: "Validate a tributary schema YAML file. Returns parsed relationships, ignored tables, and any validation errors.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, input ValidateConfigInput) (*mcp.CallToolResult, validateConfigOutput, error) {
		sf, err := config.LoadSchemaFile(input.ConfigFile)
		if err != nil {
			return nil, validateConfigOutput{}, fmt.Errorf("validate config: %w", err)
		}

		return nil, validateConfigOutput{
			Status:    "valid",
			File:      input.ConfigFile,
			Relations: sf.Relations,
		}, nil
	})

	// ── subset preview ───────────────────────────────────────
	type tablePreview struct {
		TableName string   `json:"table"`
		RowKeys   []string `json:"row_keys"`
		RowCount  int      `json:"row_count"`
	}
	type previewOutput struct {
		SeedTable      string         `json:"seed_table"`
		SeedPredicate  string         `json:"seed_predicate"`
		TotalTables    int            `json:"total_tables"`
		TotalRows      int            `json:"total_rows"`
		LoadOrder      []string       `json:"load_order"`
		Tables         []tablePreview `json:"tables"`
	}

	mcp.AddTool(s, &mcp.Tool{
		Name:        "tributary_subset_preview",
		Description: "Preview which tables and rows would be included in a subset sync WITHOUT actually loading anything. Returns the closure (table → row keys), FK graph, and load order.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, input SubsetPreviewInput) (*mcp.CallToolResult, previewOutput, error) {
		conn, err := pgx.Connect(ctx, input.DSN)
		if err != nil {
			return nil, previewOutput{}, fmt.Errorf("connect to database: %w", err)
		}
		defer conn.Close(ctx)

		schema, err := catalog.InspectConn(ctx, conn)
		if err != nil {
			return nil, previewOutput{}, fmt.Errorf("inspect schema: %w", err)
		}

		var sf *config.SchemaFile
		if input.ConfigFile != "" {
			sf, err = config.LoadSchemaFile(input.ConfigFile)
			if err != nil {
				return nil, previewOutput{}, fmt.Errorf("load config file: %w", err)
			}
		}

		g, err := graph.Build(schema, sf)
		if err != nil {
			return nil, previewOutput{}, fmt.Errorf("build FK graph: %w", err)
		}

		closure, err := graph.ComputeClosure(ctx, conn, g, graph.NodeID(input.SeedTable), input.SeedPredicate, graph.ClosureOptions{})
		if err != nil {
			return nil, previewOutput{}, fmt.Errorf("compute closure: %w", err)
		}

		order, err := subset.TableOrder(g)
		if err != nil {
			return nil, previewOutput{}, fmt.Errorf("compute load order: %w", err)
		}

		var previews []tablePreview
		for _, t := range order {
			rows := closure.Rows[t]
			keys := make([]string, 0, len(rows))
			for k := range rows {
				keys = append(keys, k)
			}
			previews = append(previews, tablePreview{
				TableName: string(t),
				RowKeys:   keys,
				RowCount:  len(rows),
			})
		}

		orderStrs := make([]string, len(order))
		for i, o := range order {
			orderStrs[i] = string(o)
		}

		return nil, previewOutput{
			SeedTable:     input.SeedTable,
			SeedPredicate: input.SeedPredicate,
			TotalTables:   len(previews),
			TotalRows:     totalRows(closure),
			LoadOrder:     orderStrs,
			Tables:        previews,
		}, nil
	})

	// ── subset sync ──────────────────────────────────────────
	type tableResult struct {
		Table          string `json:"table"`
		RowsCopied     int64  `json:"rows_copied"`
		RowsBackfilled int64  `json:"rows_backfilled"`
		SkippedResumed bool   `json:"skipped_resumed"`
	}
	type syncOutput struct {
		Status     string       `json:"status"`
		TotalRows  int64        `json:"total_rows"`
		Tables     []tableResult `json:"tables"`
		Message    string       `json:"message,omitempty"`
	}

	mcp.AddTool(s, &mcp.Tool{
		Name:        "tributary_subset_sync",
		Description: "Run a full subset sync from source to target Postgres. Creates missing tables, loads data with upsert semantics. Use tributary_subset_preview first to verify what will be synced.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, input SubsetSyncInput) (*mcp.CallToolResult, syncOutput, error) {
		// Connect
		srcConn, err := pgx.Connect(ctx, input.SourceDSN)
		if err != nil {
			return nil, syncOutput{}, fmt.Errorf("connect to source: %w", err)
		}
		defer srcConn.Close(ctx)

		tgtConn, err := pgx.Connect(ctx, input.TargetDSN)
		if err != nil {
			return nil, syncOutput{}, fmt.Errorf("connect to target: %w", err)
		}
		defer tgtConn.Close(ctx)

		// Inspect source
		schema, err := catalog.InspectConn(ctx, srcConn)
		if err != nil {
			return nil, syncOutput{}, fmt.Errorf("inspect source schema: %w", err)
		}

		var sf *config.SchemaFile
		if input.ConfigFile != "" {
			sf, err = config.LoadSchemaFile(input.ConfigFile)
			if err != nil {
				return nil, syncOutput{}, fmt.Errorf("load config file: %w", err)
			}
		}

		// Build graph + closure
		g, err := graph.Build(schema, sf)
		if err != nil {
			return nil, syncOutput{}, fmt.Errorf("build FK graph: %w", err)
		}

		closure, err := graph.ComputeClosure(ctx, srcConn, g, graph.NodeID(input.SeedTable), input.SeedPredicate, graph.ClosureOptions{})
		if err != nil {
			return nil, syncOutput{}, fmt.Errorf("compute closure: %w", err)
		}

		order, err := subset.TableOrder(g)
		if err != nil {
			return nil, syncOutput{}, fmt.Errorf("compute load order: %w", err)
		}

		if totalRows(closure) == 0 {
			return nil, syncOutput{
				Status:    "no_rows",
				Message:   "Seed query matched no rows. Nothing to sync.",
				TotalRows: 0,
			}, nil
		}

		batchSize := input.BatchSize
		if batchSize <= 0 {
			batchSize = 500
		}

		// Run sync
		result, err := load.Load(ctx, tgtConn, schema, g, closure, order, load.LoadOptions{
			Fresh:        input.Fresh,
			CreateSchema: true,
			BatchSize:    batchSize,
		})
		if err != nil {
			return nil, syncOutput{}, fmt.Errorf("load: %w", err)
		}

		var tables []tableResult
		for _, t := range result.Tables {
			tables = append(tables, tableResult{
				Table:          string(t.Table),
				RowsCopied:     t.RowsCopied,
				RowsBackfilled: t.RowsBackfilled,
				SkippedResumed: t.SkippedResumed,
			})
		}

		return nil, syncOutput{
			Status:    "completed",
			TotalRows: result.TotalRows,
			Tables:    tables,
		}, nil
	})
}

// Suppress unused import warnings.
var _ = json.Marshal

// totalRows counts the total number of rows across all tables in a closure.
func totalRows(c *graph.Closure) int {
	n := 0
	for _, rows := range c.Rows {
		n += len(rows)
	}
	return n
}
