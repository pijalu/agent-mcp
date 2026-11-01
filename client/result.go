// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package client

import (
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// ToolErrorResult inspects a decoded JSON tool result for the MCP isError
// flag. Non-tool payloads pass through unchanged.
func ToolErrorResult(value any) error {
	object, ok := value.(map[string]any)
	if !ok {
		return nil
	}
	failed, _ := object["isError"].(bool)
	if !failed {
		return nil
	}
	return errToolFailed(ContentText(object["content"]))
}

// CallToolError inspects a typed tool result for the MCP isError flag.
func CallToolError(result *mcp.CallToolResult) error {
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

// ContentText joins the text fields of a decoded MCP content array.
func ContentText(raw any) string {
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
