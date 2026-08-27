package cli

import (
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/pijalu/agent-mcp/internal/output"
)

// errToolFailed marks a tool result that the MCP server flagged as an error.
// The call reached the server, but reporting it as success would let agents
// act on a failure, so it maps to the MCP exit code.
func errToolFailed(text string) error {
	if text == "" {
		return fmt.Errorf("%w: tool reported an error", output.ErrMCP)
	}
	return fmt.Errorf("%w: %s", output.ErrMCP, text)
}

// toolErrorResult inspects a decoded JSON tool result for the MCP isError flag.
// Non-tool payloads pass through unchanged.
func toolErrorResult(value any) error {
	object, ok := value.(map[string]any)
	if !ok {
		return nil
	}
	failed, _ := object["isError"].(bool)
	if !failed {
		return nil
	}
	return errToolFailed(contentText(object["content"]))
}

// callToolError inspects a typed tool result for the MCP isError flag.
func callToolError(result *mcp.CallToolResult) error {
	if result == nil || !result.IsError {
		return nil
	}
	var parts []string
	for _, item := range result.Content {
		if text, ok := item.(*mcp.TextContent); ok && text.Text != "" {
			parts = append(parts, text.Text)
		}
	}
	return errToolFailed(strings.Join(parts, "\n"))
}

// contentText joins the text fields of a decoded MCP content array.
func contentText(raw any) string {
	items, ok := raw.([]any)
	if !ok {
		return ""
	}
	var parts []string
	for _, item := range items {
		entry, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if text, ok := entry["text"].(string); ok && text != "" {
			parts = append(parts, text)
		}
	}
	return strings.Join(parts, "\n")
}
