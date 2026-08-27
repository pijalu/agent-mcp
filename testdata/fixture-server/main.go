// fixture-server is a tiny MCP server over stdio used by E2E tests.
// It exposes one echo tool, one resource, and one prompt.
package main

import (
	"context"
	"fmt"
	"log"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// echoArgs are the arguments of the echo tool.
type echoArgs struct {
	Text string `json:"text"`
}

func main() {
	server := mcp.NewServer(&mcp.Implementation{Name: "fixture", Version: "0.0.1"}, nil)
	mcp.AddTool(server, &mcp.Tool{Name: "echo", Description: "echoes text"},
		func(_ context.Context, _ *mcp.CallToolRequest, args echoArgs) (*mcp.CallToolResult, any, error) {
			return &mcp.CallToolResult{
				Content: []mcp.Content{&mcp.TextContent{Text: args.Text}},
			}, nil, nil
		})
	server.AddResource(&mcp.Resource{URI: "file:///fixture.txt", Name: "fixture"},
		func(_ context.Context, _ *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
			return &mcp.ReadResourceResult{
				Contents: []*mcp.ResourceContents{
					{URI: "file:///fixture.txt", Text: "fixture-body"},
				},
			}, nil
		})
	server.AddPrompt(&mcp.Prompt{Name: "greet"},
		func(_ context.Context, req *mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
			name := req.Params.Arguments["name"]
			return &mcp.GetPromptResult{
				Messages: []*mcp.PromptMessage{
					{Role: "user", Content: &mcp.TextContent{Text: fmt.Sprintf("hello %s", name)}},
				},
			}, nil
		})
	if err := server.Run(context.Background(), &mcp.StdioTransport{}); err != nil {
		log.Fatal(err)
	}
}
