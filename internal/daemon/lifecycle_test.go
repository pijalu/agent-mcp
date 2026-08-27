package daemon_test

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"

	"github.com/pijalu/agent-mcp/internal/config"
	"github.com/pijalu/agent-mcp/internal/daemon"
	"github.com/pijalu/agent-mcp/internal/session"
	pb "github.com/pijalu/agent-mcp/proto/agentmcp/v1"
)

type echoArgs struct {
	Text string `json:"text" jsonschema:"text to echo"`
}

// newEchoFixture serves an MCP server with one echo tool over streamable
// HTTP on loopback.
func newEchoFixture(t *testing.T) *httptest.Server {
	t.Helper()
	server := mcp.NewServer(&mcp.Implementation{Name: "fixture-daemon", Version: "v0.0.1"}, nil)
	mcp.AddTool(server, &mcp.Tool{Name: "echo"},
		func(_ context.Context, _ *mcp.CallToolRequest, in echoArgs) (*mcp.CallToolResult, any, error) {
			return &mcp.CallToolResult{
				Content: []mcp.Content{&mcp.TextContent{Text: in.Text}},
			}, nil, nil
		})
	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil)
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return srv
}

// lifecycleDaemon wires a real session.Manager against the fixture into a
// daemon on loopback with auth enabled.
func lifecycleDaemon(t *testing.T, srv *httptest.Server, opts daemon.Options) (pb.AgentMCPClient, *daemon.Daemon) {
	t.Helper()
	mgr := session.NewManager(map[string]config.ServerConfig{
		"fixture": {URL: srv.URL, Transport: "streamable"},
	}, nil)
	d := daemon.New(mgr, opts)
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	serveErr := make(chan error, 1)
	go func() { serveErr <- d.Serve(lis) }()
	t.Cleanup(func() {
		d.Shutdown()
		if err := <-serveErr; err != nil {
			t.Errorf("serve: %v", err)
		}
	})
	conn, err := grpc.NewClient(lis.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	return pb.NewAgentMCPClient(conn), d
}

// TestLifecycleAutoStartToShutdownAll runs the full daemon lifecycle over
// a real MCP session: call a tool through the gRPC surface, observe the
// session in Status, then tear everything down with ShutdownAll.
func TestLifecycleAutoStartToShutdownAll(t *testing.T) {
	fixture := newEchoFixture(t)
	cli, _ := lifecycleDaemon(t, fixture, daemon.Options{APIKeys: []string{"k1"}})
	ctx := ctxWithKey("k1")

	// 1. CallTool lazily dials the fixture MCP server.
	callResp, err := cli.CallTool(ctx, &pb.CallToolRequest{
		Server: "fixture",
		Json:   mustJSON(t, map[string]any{"name": "echo", "arguments": map[string]string{"text": "hello"}}),
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	var result mcp.CallToolResult
	if err := json.Unmarshal(callResp.GetJson(), &result); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	text, ok := result.Content[0].(*mcp.TextContent)
	if !ok || text.Text != "hello" {
		t.Fatalf("echo = %+v, want text %q", result.Content, "hello")
	}

	// 2. Status shows the connected session.
	statusResp, err := cli.Status(ctx, &pb.StatusRequest{})
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if len(statusResp.GetServers()) != 1 {
		t.Fatalf("servers = %d, want 1", len(statusResp.GetServers()))
	}
	server := statusResp.GetServers()[0]
	if server.GetName() != "fixture" || server.GetState() != "connected" {
		t.Errorf("status = %v/%v, want fixture/connected", server.GetName(), server.GetState())
	}
	if statusResp.GetActiveSessions() != 1 {
		t.Errorf("active sessions = %d, want 1", statusResp.GetActiveSessions())
	}

	// 3. ShutdownAll closes the MCP session and stops the daemon.
	shutdownResp, err := cli.ShutdownAll(ctx, &pb.ShutdownAllRequest{})
	if err != nil {
		t.Fatalf("ShutdownAll: %v", err)
	}
	if shutdownResp.GetSessionsClosed() != 1 {
		t.Errorf("sessions closed = %d, want 1", shutdownResp.GetSessionsClosed())
	}
	waitFor(t, "daemon stop", func() bool {
		_, err := cli.Status(ctx, &pb.StatusRequest{})
		return err != nil
	})
}

// TestLifecycleWrongKeyRejected proves auth guards the real stack too.
func TestLifecycleWrongKeyRejected(t *testing.T) {
	fixture := newEchoFixture(t)
	cli, _ := lifecycleDaemon(t, fixture, daemon.Options{APIKeys: []string{"k1"}})
	_, err := cli.Status(ctxWithKey("wrong"), &pb.StatusRequest{})
	if status.Code(err) != codes.Unauthenticated {
		t.Fatalf("code = %v, want Unauthenticated (err=%v)", status.Code(err), err)
	}
}

// TestLifecycleIdleTimeoutAutoStarted: an auto-started daemon with a short
// idle timeout stops on its own; the fixture session is closed.
func TestLifecycleIdleTimeoutAutoStarted(t *testing.T) {
	fixture := newEchoFixture(t)
	stopped := make(chan struct{})
	cli, _ := lifecycleDaemon(t, fixture, daemon.Options{
		IdleTimeout: 100 * time.Millisecond,
		OnShutdown:  func() { close(stopped) },
	})
	// One call to prove the session opens, then let the daemon go idle.
	if _, err := cli.ListServers(context.Background(), &pb.ListServersRequest{}); err != nil {
		t.Fatalf("ListServers: %v", err)
	}
	select {
	case <-stopped:
	case <-time.After(5 * time.Second):
		t.Fatal("daemon did not stop after idle timeout")
	}
	if _, err := cli.Status(context.Background(), &pb.StatusRequest{}); err == nil {
		t.Fatal("Status after idle stop succeeded")
	}
}
