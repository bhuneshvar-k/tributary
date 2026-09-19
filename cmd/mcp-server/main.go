// mcp-server is the Tributary MCP server binary. It runs over stdio and
// exposes Tributary's core operations (schema inspection, subset preview,
// subset sync) as MCP tools that any compatible AI client can call.
//
// Usage:
//
//	tributary-mcp-server          # starts the server on stdin/stdout
//
// Configure in opencode.json:
//
//	{
//	  "mcp": {
//	    "servers": {
//	      "tributary": {
//	        "command": ["tributary-mcp-server"]
//	      }
//	    }
//	  }
//	}
//
// Or in Claude Desktop's claude_desktop_config.json:
//
//	{
//	  "mcpServers": {
//	    "tributary": {
//	      "command": "tributary-mcp-server"
//	    }
//	  }
//	}
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/bhuneshvar-k/tributary/internal/mcpserver"
)

func main() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	server := mcpserver.NewServer()

	transport := &mcp.StdioTransport{}
	if err := server.Run(ctx, transport); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}
