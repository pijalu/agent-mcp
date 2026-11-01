package cli

import (
	"github.com/pijalu/agent-mcp/client"
)

// toolErrorResult inspects a decoded JSON tool result for the MCP isError flag.
// Non-tool payloads pass through unchanged.
func toolErrorResult(value any) error {
	return client.ToolErrorResult(value)
}
