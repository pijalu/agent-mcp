package daemon_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/pijalu/agent-mcp/internal/daemon"
	pb "github.com/pijalu/agent-mcp/proto/agentmcp/v1"
)

// rpcCase describes one delegating RPC round-trip: invoke it through the
// real gRPC client, then check the decoded result.
type rpcCase struct {
	name   string
	server string
	invoke func(t *testing.T, cli pb.AgentMCPClient) json.RawMessage
	check  func(t *testing.T, raw json.RawMessage)
}

func invokeListTools(t *testing.T, cli pb.AgentMCPClient) json.RawMessage {
	resp, err := cli.ListTools(context.Background(), &pb.ListToolsRequest{
		Server: "alpha",
		Json:   mustJSON(t, map[string]string{"cursor": "c1"}),
	})
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	return resp.GetJson()
}

func checkListTools(t *testing.T, raw json.RawMessage) {
	var result mcp.ListToolsResult
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(result.Tools) != 2 || result.Tools[0].Name != "t1" {
		t.Errorf("unexpected tools: %+v", result.Tools)
	}
}

func invokeCallTool(t *testing.T, cli pb.AgentMCPClient) json.RawMessage {
	resp, err := cli.CallTool(context.Background(), &pb.CallToolRequest{
		Server: "beta",
		Json:   mustJSON(t, map[string]any{"name": "echo", "arguments": map[string]string{"x": "1"}}),
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	return resp.GetJson()
}

func checkCallTool(t *testing.T, raw json.RawMessage) {
	var result mcp.CallToolResult
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(result.Content) != 1 {
		t.Errorf("unexpected content: %+v", result.Content)
	}
}

func invokeListResources(t *testing.T, cli pb.AgentMCPClient) json.RawMessage {
	resp, err := cli.ListResources(context.Background(), &pb.ListResourcesRequest{Server: "alpha"})
	if err != nil {
		t.Fatalf("ListResources: %v", err)
	}
	return resp.GetJson()
}

func checkListResources(t *testing.T, raw json.RawMessage) {
	var result mcp.ListResourcesResult
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(result.Resources) != 1 || result.Resources[0].URI != "file:///a" {
		t.Errorf("unexpected resources: %+v", result.Resources)
	}
}

func invokeReadResource(t *testing.T, cli pb.AgentMCPClient) json.RawMessage {
	resp, err := cli.ReadResource(context.Background(), &pb.ReadResourceRequest{
		Server: "alpha",
		Json:   mustJSON(t, map[string]string{"uri": "file:///a"}),
	})
	if err != nil {
		t.Fatalf("ReadResource: %v", err)
	}
	return resp.GetJson()
}

func checkReadResource(t *testing.T, raw json.RawMessage) {
	var result mcp.ReadResourceResult
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(result.Contents) != 1 || result.Contents[0].Text != "body" {
		t.Errorf("unexpected contents: %+v", result.Contents)
	}
}

func invokeListPrompts(t *testing.T, cli pb.AgentMCPClient) json.RawMessage {
	resp, err := cli.ListPrompts(context.Background(), &pb.ListPromptsRequest{Server: "alpha"})
	if err != nil {
		t.Fatalf("ListPrompts: %v", err)
	}
	return resp.GetJson()
}

func checkListPrompts(t *testing.T, raw json.RawMessage) {
	var result mcp.ListPromptsResult
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(result.Prompts) != 1 || result.Prompts[0].Name != "p1" {
		t.Errorf("unexpected prompts: %+v", result.Prompts)
	}
}

func invokeGetPrompt(t *testing.T, cli pb.AgentMCPClient) json.RawMessage {
	resp, err := cli.GetPrompt(context.Background(), &pb.GetPromptRequest{
		Server: "alpha",
		Json:   mustJSON(t, map[string]string{"name": "p1"}),
	})
	if err != nil {
		t.Fatalf("GetPrompt: %v", err)
	}
	return resp.GetJson()
}

func checkGetPrompt(t *testing.T, raw json.RawMessage) {
	var result mcp.GetPromptResult
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
}

// delegationCases covers the six MCP-mirroring RPCs.
func delegationCases() []rpcCase {
	return []rpcCase{
		{name: "ListTools", server: "alpha", invoke: invokeListTools, check: checkListTools},
		{name: "CallTool", server: "beta", invoke: invokeCallTool, check: checkCallTool},
		{name: "ListResources", server: "alpha", invoke: invokeListResources, check: checkListResources},
		{name: "ReadResource", server: "alpha", invoke: invokeReadResource, check: checkReadResource},
		{name: "ListPrompts", server: "alpha", invoke: invokeListPrompts, check: checkListPrompts},
		{name: "GetPrompt", server: "alpha", invoke: invokeGetPrompt, check: checkGetPrompt},
	}
}

// TestRPCDelegation drives every MCP-mirroring RPC end to end: request
// JSON is decoded into MCP params, the fake manager receives the server
// name, and the result round-trips as JSON.
func TestRPCDelegation(t *testing.T) {
	td := startDaemon(t, daemon.Options{})
	for _, tc := range delegationCases() {
		t.Run(tc.name, func(t *testing.T) {
			raw := tc.invoke(t, td.cli)
			tc.check(t, raw)
			if td.mgr.lastServer != tc.server {
				t.Errorf("manager saw server %q, want %q", td.mgr.lastServer, tc.server)
			}
		})
	}
}

func TestBadJSONIsInvalidArgument(t *testing.T) {
	td := startDaemon(t, daemon.Options{})
	_, err := td.cli.ListTools(context.Background(), &pb.ListToolsRequest{
		Server: "alpha",
		Json:   []byte("{not json"),
	})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("code = %v, want InvalidArgument (err=%v)", status.Code(err), err)
	}
}

func TestManagerErrorIsInternal(t *testing.T) {
	td := startDaemon(t, daemon.Options{})
	td.mgr.callErr = errFake
	_, err := td.cli.CallTool(context.Background(), &pb.CallToolRequest{
		Server: "alpha",
		Json:   mustJSON(t, map[string]string{"name": "x"}),
	})
	if status.Code(err) != codes.Internal {
		t.Fatalf("code = %v, want Internal (err=%v)", status.Code(err), err)
	}
}
