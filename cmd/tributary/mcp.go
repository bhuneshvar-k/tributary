package main

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/spf13/cobra"

	"github.com/bhuneshvar-k/tributary/internal/mcpserver"
)

func newMCPCmd() *cobra.Command {
	return &cobra.Command{
		Use:    "mcp",
		Short:  "Start the MCP server for AI integration",
		Hidden: true,
		Long: `Start a Model Context Protocol (MCP) server over stdio.

This exposes Tributary's operations as tools that AI assistants
(Claude Desktop, OpenCode, Cursor, Windsurf, etc.) can call directly.

Configure in your AI client:

  {
    "mcpServers": {
      "tributary": {
        "command": ["tributary", "mcp"]
      }
    }
  }

Available tools:
  tributary_version          - Version info
  tributary_check_connection - Test DB connectivity
  tributary_inspect_schema   - Full schema inspection
  tributary_list_tables      - Quick table overview
  tributary_validate_config  - Validate schema YAML
  tributary_subset_preview   - Dry-run subset computation
  tributary_subset_sync      - Run full subset sync`,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			server := mcpserver.NewServer()
			transport := &mcp.StdioTransport{}
			return server.Run(ctx, transport)
		},
	}
}
