package daemon_test

import (
	"context"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/pijalu/agent-mcp/internal/daemon"
	pb "github.com/pijalu/agent-mcp/proto/agentmcp/v1"
)

// TestServeRegistersAgentMCP is the reflection-free connectivity check:
// a real gRPC client reaches the server and gets a handler response.
func TestServeRegistersAgentMCP(t *testing.T) {
	td := startDaemon(t, daemon.Options{})
	resp, err := td.cli.ListServers(context.Background(), &pb.ListServersRequest{})
	if err != nil {
		t.Fatalf("ListServers: %v", err)
	}
	if len(resp.GetJson()) == 0 {
		t.Fatal("ListServers returned empty payload")
	}
}

func TestAuthInterceptor(t *testing.T) {
	cases := []struct {
		name    string
		keys    []string
		header  string
		wantErr codes.Code
	}{
		{name: "no keys configured allows all", keys: nil, header: "", wantErr: codes.OK},
		{name: "valid key passes", keys: []string{"s3cret"}, header: "s3cret", wantErr: codes.OK},
		{name: "valid among many passes", keys: []string{"a", "b", "s3cret"}, header: "s3cret", wantErr: codes.OK},
		{name: "missing key rejected", keys: []string{"s3cret"}, header: "", wantErr: codes.Unauthenticated},
		{name: "wrong key rejected", keys: []string{"s3cret"}, header: "nope", wantErr: codes.Unauthenticated},
		{name: "prefix key rejected", keys: []string{"s3cret"}, header: "s3cr", wantErr: codes.Unauthenticated},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			td := startDaemon(t, daemon.Options{APIKeys: tc.keys})
			ctx := context.Background()
			if tc.header != "" {
				ctx = ctxWithKey(tc.header)
			}
			_, err := td.cli.ListServers(ctx, &pb.ListServersRequest{})
			if got := status.Code(err); got != tc.wantErr {
				t.Fatalf("code = %v, want %v (err=%v)", got, tc.wantErr, err)
			}
		})
	}
}

func TestStatusRPC(t *testing.T) {
	td := startDaemon(t, daemon.Options{})
	resp, err := td.cli.Status(context.Background(), &pb.StatusRequest{})
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if len(resp.GetServers()) != 2 {
		t.Fatalf("servers = %d, want 2", len(resp.GetServers()))
	}
	alpha := resp.GetServers()[0]
	if alpha.GetName() != "alpha" || alpha.GetState() != "connected" {
		t.Errorf("unexpected first status: %v", alpha)
	}
	if resp.GetActiveSessions() != 1 {
		t.Errorf("active sessions = %d, want 1", resp.GetActiveSessions())
	}
	if resp.GetUptimeSeconds() != 0 {
		t.Errorf("uptime = %d, want 0 just after start", resp.GetUptimeSeconds())
	}
	if resp.GetConfigPath() != "" {
		t.Errorf("config path = %q, want empty when the daemon was not told", resp.GetConfigPath())
	}
}

// TestStatusReportsConfigPath covers clients detecting that --config names a
// different config than the running daemon serves.
func TestStatusReportsConfigPath(t *testing.T) {
	td := startDaemon(t, daemon.Options{ConfigPath: "/etc/agent-mcp/daemon.toml"})
	resp, err := td.cli.Status(context.Background(), &pb.StatusRequest{})
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if got := resp.GetConfigPath(); got != "/etc/agent-mcp/daemon.toml" {
		t.Errorf("config path = %q, want the loaded config", got)
	}
}

func TestUptimeCountsAfterStart(t *testing.T) {
	td := startDaemon(t, daemon.Options{})
	// First Status just after start is 0; sleep past one second to see it move.
	time.Sleep(1100 * time.Millisecond)
	resp, err := td.cli.Status(context.Background(), &pb.StatusRequest{})
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if resp.GetUptimeSeconds() < 1 {
		t.Errorf("uptime = %d, want >= 1", resp.GetUptimeSeconds())
	}
}
