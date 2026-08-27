// agent-mcp: CLI bridge between agent skills and MCP servers.
package main

import (
	"os"

	"github.com/pijalu/agent-mcp/internal/cli"
)

func main() { os.Exit(cli.Run(os.Args[1:], os.Stdout, os.Stderr)) }
