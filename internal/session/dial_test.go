package session

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/pijalu/agent-mcp/internal/config"
)

// TestDefaultDial exercises the real dialer end-to-end against a streamable
// HTTP fixture: NewManager(nil dial) → NewTransport → mcp.Client.Connect.
func TestDefaultDial(t *testing.T) {
	server := mcp.NewServer(&mcp.Implementation{Name: "fixture-http", Version: "v0.0.1"}, nil)
	mcp.AddTool(server, &mcp.Tool{Name: "echo"},
		func(_ context.Context, _ *mcp.CallToolRequest, in echoArgs) (*mcp.CallToolResult, any, error) {
			return &mcp.CallToolResult{
				Content: []mcp.Content{&mcp.TextContent{Text: in.Text}},
			}, nil, nil
		})
	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil)
	srv := httptest.NewServer(handler)
	defer srv.Close()

	m := NewManager(map[string]config.ServerConfig{
		"http": {URL: srv.URL, Transport: "streamable"},
	}, nil) // nil dial → defaultDial

	result, err := m.CallTool(context.Background(), "http", &mcp.CallToolParams{
		Name:      "echo",
		Arguments: map[string]any{"text": "over-http"},
	})
	if err != nil {
		t.Fatalf("CallTool over default dial: %v", err)
	}
	if got := result.Content[0].(*mcp.TextContent).Text; got != "over-http" {
		t.Errorf("echo = %q, want %q", got, "over-http")
	}
	if got := m.CloseAll(); got != 1 {
		t.Errorf("CloseAll = %d, want 1", got)
	}
}

// TestDefaultDialBadConfig confirms a dial against an unknown transport
// surfaces the factory error.
func TestDefaultDialBadConfig(t *testing.T) {
	m := NewManager(map[string]config.ServerConfig{
		"bad": {URL: "http://localhost:1", Transport: "grpc"},
	}, nil)
	if _, err := m.Open(context.Background(), "bad"); err == nil {
		t.Fatal("Open with unknown transport: want error, got nil")
	}
}
