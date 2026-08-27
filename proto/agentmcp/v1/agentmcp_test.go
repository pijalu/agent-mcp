package agentmcpv1

import (
	"testing"

	"google.golang.org/protobuf/proto"
)

// roundTrip marshals then unmarshals a message and reports failure on mismatch.
func roundTrip(t *testing.T, m proto.Message) {
	t.Helper()
	data, err := proto.Marshal(m)
	if err != nil {
		t.Fatalf("marshal %T: %v", m, err)
	}
	out := m.ProtoReflect().New().Interface()
	if err := proto.Unmarshal(data, out); err != nil {
		t.Fatalf("unmarshal %T: %v", m, err)
	}
	if !proto.Equal(m, out) {
		t.Fatalf("round-trip mismatch for %T:\n got: %v\nwant: %v", m, out, m)
	}
}

func TestMessageRoundTrip(t *testing.T) {
	payload := []byte(`{"key":"value"}`)

	msgs := []proto.Message{
		&ListServersRequest{Json: payload},
		&ListServersResponse{Json: payload},
		&ListToolsRequest{Server: "fs", Json: payload},
		&ListToolsResponse{Json: payload},
		&CallToolRequest{Server: "fs", Json: payload},
		&CallToolResponse{Json: payload},
		&ListResourcesRequest{Server: "fs", Json: payload},
		&ListResourcesResponse{Json: payload},
		&ReadResourceRequest{Server: "fs", Json: payload},
		&ReadResourceResponse{Json: payload},
		&ListPromptsRequest{Server: "fs", Json: payload},
		&ListPromptsResponse{Json: payload},
		&GetPromptRequest{Server: "fs", Json: payload},
		&GetPromptResponse{Json: payload},
		&StatusRequest{Json: payload},
		&ServerStatus{
			Name:           "fs",
			State:          "ready",
			ActiveSessions: 2,
			LastError:      "",
			Json:           payload,
		},
		&StatusResponse{
			UptimeSeconds:  3600,
			ActiveSessions: 3,
			Servers: []*ServerStatus{
				{Name: "fs", State: "ready", ActiveSessions: 2, Json: payload},
				{Name: "web", State: "failed", LastError: "boom"},
			},
		},
		&ShutdownAllRequest{Json: payload},
		&ShutdownAllResponse{SessionsClosed: 4},
	}

	for _, m := range msgs {
		roundTrip(t, m)
	}
}

func TestServiceRegistered(t *testing.T) {
	if AgentMCP_ServiceDesc.ServiceName != "agentmcp.v1.AgentMCP" {
		t.Fatalf("service name = %q, want agentmcp.v1.AgentMCP", AgentMCP_ServiceDesc.ServiceName)
	}
	want := []string{
		"ListServers", "ListTools", "CallTool", "ListResources", "ReadResource",
		"ListPrompts", "GetPrompt", "Status", "ShutdownAll",
	}
	if len(AgentMCP_ServiceDesc.Methods) != len(want) {
		t.Fatalf("method count = %d, want %d", len(AgentMCP_ServiceDesc.Methods), len(want))
	}
	for i, name := range want {
		if AgentMCP_ServiceDesc.Methods[i].MethodName != name {
			t.Errorf("method[%d] = %q, want %q", i, AgentMCP_ServiceDesc.Methods[i].MethodName, name)
		}
	}
}
