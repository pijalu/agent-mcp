package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"google.golang.org/grpc"

	"github.com/pijalu/agent-mcp/internal/cli"
	"github.com/pijalu/agent-mcp/internal/daemon"
	"github.com/pijalu/agent-mcp/internal/session"
	pb "github.com/pijalu/agent-mcp/proto/agentmcp/v1"
)

// fakeManager returns canned MCP data for the fake daemon.
type fakeManager struct{}

func (fakeManager) ListTools(context.Context, string, *mcp.ListToolsParams) (*mcp.ListToolsResult, error) {
	return &mcp.ListToolsResult{Tools: []*mcp.Tool{{Name: "echo"}}}, nil
}

func (fakeManager) CallTool(_ context.Context, _ string, params *mcp.CallToolParams) (*mcp.CallToolResult, error) {
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: "ran " + params.Name}},
	}, nil
}

func (fakeManager) ListResources(context.Context, string, *mcp.ListResourcesParams) (*mcp.ListResourcesResult, error) {
	return &mcp.ListResourcesResult{Resources: []*mcp.Resource{{URI: "file:///a", Name: "a"}}}, nil
}

func (fakeManager) ReadResource(_ context.Context, _ string, params *mcp.ReadResourceParams) (*mcp.ReadResourceResult, error) {
	return &mcp.ReadResourceResult{
		Contents: []*mcp.ResourceContents{{URI: params.URI, Text: "body"}},
	}, nil
}

func (fakeManager) ListPrompts(context.Context, string, *mcp.ListPromptsParams) (*mcp.ListPromptsResult, error) {
	return &mcp.ListPromptsResult{Prompts: []*mcp.Prompt{{Name: "greet"}}}, nil
}

func (fakeManager) GetPrompt(_ context.Context, _ string, params *mcp.GetPromptParams) (*mcp.GetPromptResult, error) {
	return &mcp.GetPromptResult{
		Messages: []*mcp.PromptMessage{
			{Role: "user", Content: &mcp.TextContent{Text: "hello " + params.Arguments["name"]}},
		},
	}, nil
}

func (fakeManager) CloseAll() int { return 0 }

func (fakeManager) ServerStatuses() []session.ServerInfo {
	return []session.ServerInfo{{Name: "fake", State: "idle"}}
}

// startFakeDaemon runs a real gRPC daemon on loopback with fakeManager.
func startFakeDaemon(t *testing.T) string {
	t.Helper()
	d := daemon.New(fakeManager{}, daemon.Options{})
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	go func() { _ = d.Serve(lis) }()
	t.Cleanup(func() { d.Shutdown() })
	return lis.Addr().String()
}

// dialReal dials a real gRPC endpoint.
func dialReal(_ context.Context, addr string) (pb.AgentMCPClient, io.Closer, error) {
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecureCreds()))
	if err != nil {
		return nil, nil, err
	}
	return pb.NewAgentMCPClient(conn), conn, nil
}

// runWith executes the CLI against a fake daemon address with no autostart.
func runWith(t *testing.T, addr string, args ...string) (int, string, string) {
	t.Helper()
	deps := cli.Deps{
		Dial: dialReal,
		SpawnDaemon: func(cli.GlobalFlags) (cli.DaemonChild, error) {
			return nil, errors.New("spawn should not be called")
		},
	}
	var stdout, stderr bytes.Buffer
	code := cli.RunWith(deps, append([]string{args[0], "--connect", addr}, args[1:]...), &stdout, &stderr)
	return code, stdout.String(), stderr.String()
}

func TestDaemonListTools(t *testing.T) {
	addr := startFakeDaemon(t)
	code, out, _ := runWith(t, addr, "list-tools", "--server", "fake")
	if code != 0 {
		t.Fatalf("exit = %d, out %s", code, out)
	}
	var env struct {
		Data struct {
			Tools []struct {
				Name string `json:"name"`
			} `json:"tools"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(env.Data.Tools) != 1 || env.Data.Tools[0].Name != "echo" {
		t.Errorf("tools: %+v", env.Data.Tools)
	}
}

func TestDaemonCallTool(t *testing.T) {
	addr := startFakeDaemon(t)
	code, out, _ := runWith(t, addr, "call-tool", "--server", "fake", "--tool", "echo", "--args", `{"text":"hi"}`)
	if code != 0 {
		t.Fatalf("exit = %d, out %s", code, out)
	}
	if !strings.Contains(out, "ran echo") {
		t.Errorf("out: %s", out)
	}
}

// TestDaemonListServers covers D-21: list-servers is a config read, so a
// running daemon is not consulted (and never receives the command) — the
// daemon's own server list must not leak into the answer.
func TestDaemonListServers(t *testing.T) {
	addr := startFakeDaemon(t)
	cfgPath := writeConfig(t, buildFixture(t))
	code, out, errOut := runWith(t, addr, "list-servers", "--config", cfgPath)
	if code != 0 {
		t.Fatalf("exit = %d, out %s err %s", code, out, errOut)
	}
	if !strings.Contains(out, `"fx"`) {
		t.Errorf("out must list the configured server: %s", out)
	}
	if strings.Contains(out, `"fake"`) {
		t.Errorf("out must not carry the daemon's servers: %s", out)
	}
	if strings.Contains(errOut, "auto-starting") {
		t.Errorf("stderr must stay quiet: %q", errOut)
	}
}

func TestDaemonReadResource(t *testing.T) {
	addr := startFakeDaemon(t)
	code, out, _ := runWith(t, addr, "read-resource", "--server", "fake", "--uri", "file:///a")
	if code != 0 {
		t.Fatalf("exit = %d, out %s", code, out)
	}
	if !strings.Contains(out, "body") {
		t.Errorf("out: %s", out)
	}
}

func TestDaemonListPromptsAndGet(t *testing.T) {
	addr := startFakeDaemon(t)
	code, out, _ := runWith(t, addr, "list-prompts", "--server", "fake")
	if code != 0 || !strings.Contains(out, "greet") {
		t.Fatalf("list-prompts exit=%d out=%s", code, out)
	}
	code, out, _ = runWith(t, addr, "get-prompt", "--server", "fake", "--prompt", "greet", "--args", `{"name":"bob"}`)
	if code != 0 {
		t.Fatalf("get-prompt exit=%d out=%s", code, out)
	}
	if !strings.Contains(out, "hello bob") {
		t.Errorf("out: %s", out)
	}
}

func TestDaemonListResources(t *testing.T) {
	addr := startFakeDaemon(t)
	code, out, _ := runWith(t, addr, "list-resources", "--server", "fake")
	if code != 0 || !strings.Contains(out, "file:///a") {
		t.Fatalf("exit=%d out=%s", code, out)
	}
}

// flakyDial fails once then succeeds, to assert auto-start is triggered.
type flakyDial struct {
	addr   string
	failed bool
}

func (f *flakyDial) dial(_ context.Context, addr string) (pb.AgentMCPClient, io.Closer, error) {
	if !f.failed {
		f.failed = true
		return pb.NewAgentMCPClient(mustConn(f.addr)), nopCloser{}, errors.New("probe: connection refused")
	}
	return dialReal(context.Background(), f.addr)
}

type nopCloser struct{}

func (nopCloser) Close() error { return nil }

func mustConn(addr string) grpc.ClientConnInterface {
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecureCreds()))
	if err != nil {
		panic(err)
	}
	return conn
}

func TestAutoStartOnUnreachableDaemon(t *testing.T) {
	addr := startFakeDaemon(t)
	spawned := false
	f := &flakyDial{addr: addr}
	deps := cli.Deps{
		Dial: f.dial,
		SpawnDaemon: func(cli.GlobalFlags) (cli.DaemonChild, error) {
			spawned = true
			return &fakeChild{}, nil
		},
	}
	var stdout, stderr bytes.Buffer
	code := cli.RunWith(deps, []string{"list-tools", "--server", "fake", "--connect", addr}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit = %d, out %s err %s", code, stdout.String(), stderr.String())
	}
	if !spawned {
		t.Error("SpawnDaemon was not called after failed probe")
	}
	if !strings.Contains(stderr.String(), "auto-starting") {
		t.Errorf("stderr: %q", stderr.String())
	}
	if !strings.Contains(stderr.String(), "server shutdown") {
		t.Errorf("stderr must remind the caller to stop the daemon: %q", stderr.String())
	}
}

// TestAutoStartHintOnEveryDiscoveryCommand covers the D-21 stderr contract: the
// discovery commands that still auto-start say so and point at the teardown
// command, because the daemon they start outlives them.
func TestAutoStartHintOnEveryDiscoveryCommand(t *testing.T) {
	for _, command := range [][]string{
		{"list-tools", "--server", "fake"},
		{"list-resources", "--server", "fake"},
		{"list-prompts", "--server", "fake"},
	} {
		t.Run(command[0], func(t *testing.T) {
			deps := cli.Deps{
				Dial: func(context.Context, string) (pb.AgentMCPClient, io.Closer, error) {
					return nil, nil, errors.New("probe: connection refused")
				},
				SpawnDaemon: func(cli.GlobalFlags) (cli.DaemonChild, error) {
					return nil, errors.New("spawn failed")
				},
			}
			var stdout, stderr bytes.Buffer
			code := cli.RunWith(deps, append([]string{}, append(command, "--connect", "127.0.0.1:1")...), &stdout, &stderr)
			if code == 0 {
				t.Fatalf("exit = 0, want a failure (out %s)", stdout.String())
			}
			out := stderr.String()
			if !strings.Contains(out, "auto-starting") {
				t.Errorf("stderr must announce the auto-start: %q", out)
			}
			if !strings.Contains(out, "server shutdown") {
				t.Errorf("stderr must carry the shutdown reminder: %q", out)
			}
		})
	}

	t.Run("list-servers stays quiet", func(t *testing.T) {
		cfgPath := writeConfig(t, buildFixture(t))
		deps := cli.Deps{
			Dial: func(context.Context, string) (pb.AgentMCPClient, io.Closer, error) {
				t.Error("list-servers must not dial a daemon")
				return nil, nil, errors.New("unreachable")
			},
			SpawnDaemon: func(cli.GlobalFlags) (cli.DaemonChild, error) {
				t.Error("list-servers must not spawn a daemon")
				return nil, errors.New("spawn failed")
			},
		}
		var stdout, stderr bytes.Buffer
		code := cli.RunWith(deps, []string{"list-servers", "--connect", "127.0.0.1:1", "--config", cfgPath}, &stdout, &stderr)
		if code != 0 {
			t.Fatalf("exit = %d, want 0 (out %s err %s)", code, stdout.String(), stderr.String())
		}
		if stderr.Len() != 0 {
			t.Errorf("stderr must stay empty: %q", stderr.String())
		}
	})
}

func TestOneShotWithFixture(t *testing.T) {
	bin := buildFixture(t)
	cfgPath := writeConfig(t, bin)
	deps := cli.Deps{
		SpawnDaemon: func(cli.GlobalFlags) (cli.DaemonChild, error) {
			return nil, errors.New("no spawn expected")
		},
	}
	var stdout, stderr bytes.Buffer
	args := []string{"call-tool", "--server", "fx", "--tool", "echo",
		"--args", `{"text":"one-shot"}`, "--no-server", "--config", cfgPath}
	code := cli.RunWith(deps, args, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit = %d, out %s err %s", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "one-shot") {
		t.Errorf("out: %s", stdout.String())
	}
}

func TestOneShotClosesManager(t *testing.T) {
	cfgPath := writeConfig(t, buildFixture(t))
	mgr := &trackingManager{fake: fakeManager{}}
	deps := cli.Deps{
		NewManager: func(*configType) cli.ClosableManager { return mgr },
	}
	var stdout, stderr bytes.Buffer
	args := []string{"list-tools", "--server", "fx", "--no-server", "--config", cfgPath}
	code := cli.RunWith(deps, args, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit = %d err %s", code, stderr.String())
	}
	if mgr.closeCalls != 1 {
		t.Errorf("CloseAll called %d times, want 1", mgr.closeCalls)
	}
}

func TestStatusUnreachableDaemon(t *testing.T) {
	deps := cli.Deps{Dial: dialReal}
	var stdout, stderr bytes.Buffer
	code := cli.RunWith(deps, []string{"server", "status", "--connect", "127.0.0.1:1"}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("exit = %d, want 1 (out %s)", code, stdout.String())
	}
}

func TestShutdownNoDaemonIdempotent(t *testing.T) {
	deps := cli.Deps{Dial: dialReal}
	var stdout, stderr bytes.Buffer
	code := cli.RunWith(deps, []string{"server", "shutdown", "--connect", "127.0.0.1:1"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (out %s)", code, stdout.String())
	}
	if !strings.Contains(stdout.String(), `"shutdown":false`) {
		t.Errorf("out: %s", stdout.String())
	}
}

func TestShutdownRealDaemon(t *testing.T) {
	addr := startFakeDaemon(t)
	deps := cli.Deps{Dial: dialReal}
	var stdout, stderr bytes.Buffer
	code := cli.RunWith(deps, []string{"server", "shutdown", "--connect", addr, "--timeout", "3s"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit = %d, out %s err %s", code, stdout.String(), stderr.String())
	}
	out := stdout.String()
	if !strings.Contains(out, `"shutdown":true`) || !strings.Contains(out, `"sessions_closed":0`) {
		t.Errorf("out: %s", out)
	}
	// Daemon should be gone: a follow-up shutdown reports false.
	var out2 bytes.Buffer
	code = cli.RunWith(deps, []string{"server", "shutdown", "--connect", addr, "--timeout", "500ms"}, &out2, &stderr)
	if code != 0 || !strings.Contains(out2.String(), `"shutdown":false`) {
		t.Errorf("second shutdown exit=%d out=%s", code, out2.String())
	}
}

func TestStatusRealDaemon(t *testing.T) {
	addr := startFakeDaemon(t)
	code, out, _ := runWith2(t, addr, "server", "status", "--connect", addr)
	if code != 0 {
		t.Fatalf("exit = %d, out %s", code, out)
	}
	if !strings.Contains(out, `"uptime_seconds"`) || !strings.Contains(out, `"fake"`) {
		t.Errorf("out: %s", out)
	}
}

// runWith2 is runWith without injecting --connect (args carry it already).
func runWith2(t *testing.T, _ string, args ...string) (int, string, string) {
	t.Helper()
	deps := cli.Deps{Dial: dialReal}
	var stdout, stderr bytes.Buffer
	code := cli.RunWith(deps, args, &stdout, &stderr)
	return code, stdout.String(), stderr.String()
}

var _ = time.Second // keep time import if unused in refactor

// startFakeDaemonWithKeys starts an in-process daemon that requires API keys.
func startFakeDaemonWithKeys(t *testing.T, keys []string) string {
	t.Helper()
	d := daemon.New(fakeManager{}, daemon.Options{APIKeys: keys})
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	go func() { _ = d.Serve(lis) }()
	t.Cleanup(func() { d.Shutdown() })
	return lis.Addr().String()
}

// TestAuthDaemonNotMisdetectedAsUnreachable covers the trap where the
// reachability probe omitted the API key, so every authenticated call
// reported "daemon not reachable" and attempted a duplicate daemon spawn.
// runWith's SpawnDaemon errors, so any spawn attempt also flips the exit code.
func TestAuthDaemonNotMisdetectedAsUnreachable(t *testing.T) {
	addr := startFakeDaemonWithKeys(t, []string{"s3cret"})

	t.Run("correct key", func(t *testing.T) {
		code, out, errOut := runWith(t, addr, "list-tools", "--server", "fake", "--api-key", "s3cret")
		if code != 0 {
			t.Fatalf("exit = %d, out %s, err %s", code, out, errOut)
		}
		if strings.Contains(errOut, "auto-starting") {
			t.Errorf("authenticated daemon misreported as unreachable: %s", errOut)
		}
	})

	t.Run("wrong key", func(t *testing.T) {
		code, out, errOut := runWith(t, addr, "list-tools", "--server", "fake", "--api-key", "wrong")
		if code != 1 {
			t.Fatalf("exit = %d, want 1 (out %s, err %s)", code, out, errOut)
		}
		if !strings.Contains(out, "API key") {
			t.Errorf("out must explain the key was rejected: %s", out)
		}
		if !strings.Contains(out, `"code":"mcp"`) {
			t.Errorf("out must keep the mcp error code: %s", out)
		}
		if strings.Contains(errOut, "auto-starting") {
			t.Errorf("must not auto-start on an auth failure: %s", errOut)
		}
	})
}
