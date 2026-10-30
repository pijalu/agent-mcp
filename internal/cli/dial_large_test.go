package cli

import (
	"context"
	"encoding/json"
	"net"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/pijalu/agent-mcp/internal/daemon"
	"github.com/pijalu/agent-mcp/internal/session"
	pb "github.com/pijalu/agent-mcp/proto/agentmcp/v1"
)

// largePayloadManager returns a large CallTool result for the "big" tool and a
// tiny one otherwise, so the test can isolate each direction's message cap.
type largePayloadManager struct {
	text string
}

func (m largePayloadManager) CallTool(_ context.Context, _ string, params *mcp.CallToolParams) (*mcp.CallToolResult, error) {
	text := "ok"
	if params.Name == "big" {
		text = m.text
	}
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: text}}}, nil
}

func (largePayloadManager) ListTools(context.Context, string, *mcp.ListToolsParams) (*mcp.ListToolsResult, error) {
	return &mcp.ListToolsResult{}, nil
}

func (largePayloadManager) ListResources(context.Context, string, *mcp.ListResourcesParams) (*mcp.ListResourcesResult, error) {
	return &mcp.ListResourcesResult{}, nil
}

func (largePayloadManager) ReadResource(context.Context, string, *mcp.ReadResourceParams) (*mcp.ReadResourceResult, error) {
	return &mcp.ReadResourceResult{}, nil
}

func (largePayloadManager) ListPrompts(context.Context, string, *mcp.ListPromptsParams) (*mcp.ListPromptsResult, error) {
	return &mcp.ListPromptsResult{}, nil
}

func (largePayloadManager) GetPrompt(context.Context, string, *mcp.GetPromptParams) (*mcp.GetPromptResult, error) {
	return &mcp.GetPromptResult{}, nil
}

func (largePayloadManager) CloseAll() int { return 0 }

func (largePayloadManager) ServerStatuses() []session.ServerInfo { return nil }

// TestGRPCDialExceedsDefaultMessageSize pins the fix for payloads above gRPC's
// 4 MiB default: MCP servers such as blender-mcp return screenshots, docs and
// renders that large, and the daemon hop used to fail with a bare
// ResourceExhausted. Both directions are covered because the server and client
// caps are configured separately.
func TestGRPCDialExceedsDefaultMessageSize(t *testing.T) {
	client := dialLargePayloadDaemon(t)

	t.Run("large response", func(t *testing.T) {
		req := &pb.CallToolRequest{Server: "big", Json: mustJSON(t, map[string]any{"name": "big"})}
		resp, err := client.CallTool(context.Background(), req)
		if err != nil {
			t.Fatalf("CallTool: %v", err)
		}
		assertTextPayload(t, resp.GetJson(), payloadBytes)
	})

	t.Run("large request", func(t *testing.T) {
		req := &pb.CallToolRequest{Server: "big", Json: mustJSON(t, map[string]any{
			"name":      "small",
			"arguments": map[string]any{"blob": strings.Repeat("y", payloadBytes)},
		})}
		resp, err := client.CallTool(context.Background(), req)
		if err != nil {
			t.Fatalf("CallTool: %v", err)
		}
		if len(resp.GetJson()) >= payloadBytes {
			t.Errorf("response = %d bytes, want a small echo", len(resp.GetJson()))
		}
	})
}

// payloadBytes is comfortably above gRPC's 4 MiB default message size.
const payloadBytes = 6 << 20

// dialLargePayloadDaemon serves a daemon whose "big" tool answers with a
// payloadBytes-sized result and dials it through the production dialer.
func dialLargePayloadDaemon(t *testing.T) pb.AgentMCPClient {
	t.Helper()
	d := daemon.New(largePayloadManager{text: strings.Repeat("x", payloadBytes)}, daemon.Options{})
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	go func() { _ = d.Serve(lis) }()
	t.Cleanup(func() { d.Shutdown() })

	client, closer, err := grpcDial(context.Background(), lis.Addr().String())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = closer.Close() })
	return client
}

// assertTextPayload checks that raw is a CallToolResult carrying one text block
// of exactly want bytes.
func assertTextPayload(t *testing.T, raw []byte, want int) {
	t.Helper()
	var result mcp.CallToolResult
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(result.Content) != 1 {
		t.Fatalf("content = %d, want 1", len(result.Content))
	}
	text, ok := result.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("content type = %T, want *mcp.TextContent", result.Content[0])
	}
	if len(text.Text) != want {
		t.Errorf("payload = %d bytes, want %d", len(text.Text), want)
	}
}

// mustJSON encodes v as JSON or fails the test.
func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return raw
}
