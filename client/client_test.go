package client

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/pijalu/agent-mcp/internal/config"
	"github.com/pijalu/agent-mcp/internal/session"
	pb "github.com/pijalu/agent-mcp/proto/agentmcp/v1"
)

type echoArgs struct {
	Text string `json:"text"`
}

// newFixtureServer returns an in-process MCP server with one echo tool, one
// resource, and one prompt, plus the dial side of an in-memory pipe.
func newFixtureServer(t *testing.T) (*mcp.Server, *mcp.InMemoryTransport) {
	t.Helper()
	server := mcp.NewServer(&mcp.Implementation{Name: "fixture", Version: "v0.0.1"}, nil)
	mcp.AddTool(server, &mcp.Tool{Name: "echo", Description: "echoes text"},
		func(_ context.Context, _ *mcp.CallToolRequest, in echoArgs) (*mcp.CallToolResult, any, error) {
			return &mcp.CallToolResult{
				Content: []mcp.Content{&mcp.TextContent{Text: in.Text}},
			}, nil, nil
		})
	mcp.AddTool(server, &mcp.Tool{Name: "fail", Description: "always fails"},
		func(_ context.Context, _ *mcp.CallToolRequest, _ echoArgs) (*mcp.CallToolResult, any, error) {
			return &mcp.CallToolResult{
				Content: []mcp.Content{&mcp.TextContent{Text: "boom"}},
				IsError: true,
			}, nil, nil
		})
	server.AddResource(&mcp.Resource{URI: "mem://hello", Name: "hello"},
		func(_ context.Context, _ *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
			return &mcp.ReadResourceResult{
				Contents: []*mcp.ResourceContents{{URI: "mem://hello", Text: "world"}},
			}, nil
		})
	server.AddPrompt(&mcp.Prompt{Name: "greet"},
		func(_ context.Context, req *mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
			return &mcp.GetPromptResult{
				Messages: []*mcp.PromptMessage{{
					Role:    "user",
					Content: &mcp.TextContent{Text: "hello " + req.Params.Arguments["name"]},
				}},
			}, nil
		})

	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	if _, err := server.Connect(context.Background(), serverTransport, nil); err != nil {
		t.Fatalf("fixture connect: %v", err)
	}
	return server, clientTransport
}

// fixtureDialer returns a DialSession-style func for the fixture server.
func fixtureDialer(t *testing.T) func(context.Context, string, config.ServerConfig) (*mcp.ClientSession, error) {
	t.Helper()
	return func(ctx context.Context, _ string, _ config.ServerConfig) (*mcp.ClientSession, error) {
		_, clientTransport := newFixtureServer(t)
		cl := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "v0.0.1"}, nil)
		return cl.Connect(ctx, clientTransport, nil)
	}
}

// noServerClient builds a NoServer Client wired to the in-memory fixture:
// the manager hook keeps the fixture config from spawning real servers.
func noServerClient(t *testing.T) *Client {
	t.Helper()
	return fixtureClient(t, func(cfg *config.Config) (ClosableManager, error) {
		return sessionManagerForFixture(t), nil
	})
}

// fixtureClient builds a NoServer Client around a custom manager factory.
func fixtureClient(t *testing.T, factory func(*config.Config) (ClosableManager, error)) *Client {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("[mcpServers.alpha]\ncommand = \"alpha-cmd\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := New(Options{NoServer: true, ConfigPath: path, NewManager: factory})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

// sessionManagerForFixture builds a session.Manager over one fake "alpha"
// server whose sessions come from the in-memory fixture.
func sessionManagerForFixture(t *testing.T) *session.Manager {
	t.Helper()
	return session.NewManager(map[string]config.ServerConfig{
		"alpha": {Command: "alpha-cmd"},
	}, fixtureDialer(t))
}

func TestClientDoRoundsTripThroughFixture(t *testing.T) {
	c := noServerClient(t)
	ctx := context.Background()

	out, err := c.Do(ctx, Request{Op: OpCallTool, Server: "alpha", Tool: "echo",
		Args: []byte(`{"text":"hi"}`)})
	if err != nil {
		t.Fatalf("call-tool echo: %v", err)
	}
	text := ContentText(out.(map[string]any)["content"])
	if text != "hi" {
		t.Errorf("echo content = %q, want %q", text, "hi")
	}

	out, err = c.Do(ctx, Request{Op: OpReadResource, Server: "alpha", URI: "mem://hello"})
	if err != nil {
		t.Fatalf("read-resource: %v", err)
	}
	if !strings.Contains(jsonString(out), `"world"`) {
		t.Errorf("read-resource = %s, want mem://hello body", jsonString(out))
	}

	out, err = c.Do(ctx, Request{Op: OpGetPrompt, Server: "alpha", Prompt: "greet",
		Args: []byte(`{"name":"go"}`)})
	if err != nil {
		t.Fatalf("get-prompt: %v", err)
	}
	if !strings.Contains(jsonString(out), "hello go") {
		t.Errorf("get-prompt = %s, want greeting", jsonString(out))
	}

	out, err = c.Do(ctx, Request{Op: OpListTools, Server: "alpha"})
	if err != nil {
		t.Fatalf("list-tools: %v", err)
	}
	if !strings.Contains(jsonString(out), `"echo"`) {
		t.Errorf("list-tools = %s, want echo entry", jsonString(out))
	}
}

// jsonString renders a shaped payload compactly for substring assertions.
func jsonString(v any) string {
	raw, _ := json.Marshal(v)
	return string(raw)
}

func TestClientDoListServersLocalEvenInDaemonMode(t *testing.T) {
	// Daemon mode (NoServer false) but no daemon reachable and no spawn hook
	// wired: list-servers must answer from the config instead of dialing.
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(path, []byte("[mcpServers.alpha]\ncommand = \"alpha-cmd\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := New(Options{ConfigPath: path})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer c.Close()
	out, err := c.Do(context.Background(), Request{Op: OpListServers})
	if err != nil {
		t.Fatalf("list-servers: %v", err)
	}
	if !strings.Contains(jsonString(out), `"alpha"`) {
		t.Errorf("list-servers = %s, want alpha entry", jsonString(out))
	}
}

func TestClientDoToolErrorMapsToMCPClass(t *testing.T) {
	c := noServerClient(t)
	_, err := c.Do(context.Background(), Request{Op: OpCallTool, Server: "alpha", Tool: "fail"})
	if err == nil {
		t.Fatal("call-tool fail: want error, got nil")
	}
	if !errors.Is(err, ErrMCP) {
		t.Errorf("error = %v, want ErrMCP class", err)
	}
}

func TestClientDoUnknownServerMapsToUsage(t *testing.T) {
	c := noServerClient(t)
	_, err := c.Do(context.Background(), Request{Op: OpCallTool, Server: "ghost", Tool: "echo"})
	if err == nil {
		t.Fatal("call-tool ghost: want error, got nil")
	}
	if !errors.Is(err, ErrUsage) {
		t.Errorf("error = %v, want ErrUsage class", err)
	}
}

func TestClientDoUnknownToolMapsToUsage(t *testing.T) {
	c := noServerClient(t)
	_, err := c.Do(context.Background(), Request{Op: OpCallTool, Server: "alpha", Tool: "ghost"})
	if err == nil {
		t.Fatal("call-tool ghost tool: want error, got nil")
	}
	if !errors.Is(err, ErrUsage) {
		t.Errorf("error = %v, want ErrUsage class", err)
	}
}

func TestClientDoValidationErrors(t *testing.T) {
	c := noServerClient(t)
	cases := []Request{
		{Op: OpCallTool},
		{Op: OpCallTool, Server: "alpha"},
		{Op: OpListTools},
		{Op: OpReadResource, Server: "alpha"},
		{Op: OpGetPrompt, Server: "alpha"},
		{Op: "nope"},
	}
	for i, r := range cases {
		if _, err := c.Do(context.Background(), r); err == nil {
			t.Errorf("case %d: want error, got nil", i)
		} else if !errors.Is(err, ErrUsage) {
			t.Errorf("case %d: error = %v, want ErrUsage", i, err)
		}
	}
}

func TestClientShapeTotalsAndMetadata(t *testing.T) {
	result := map[string]any{
		"tools": []any{
			map[string]any{"name": "a", "_meta": map[string]any{"x": 1}},
			map[string]any{"name": "b"},
			map[string]any{"name": "c"},
		},
	}
	out, err := Shape(result, Request{Op: OpListTools, Search: "a"})
	if err != nil {
		t.Fatalf("Shape: %v", err)
	}
	object := out.(map[string]any)
	if got := object["total"]; got != 1 {
		t.Errorf("total = %v, want 1", got)
	}
	kept := object["tools"].([]any)
	if len(kept) != 1 {
		t.Fatalf("kept = %v, want one entry", kept)
	}
	if _, has := kept[0].(map[string]any)["_meta"]; has {
		t.Error("_meta survived shaping")
	}
}

func TestClientShapeRawPassesThrough(t *testing.T) {
	result := []any{map[string]any{"name": "alpha"}}
	out, err := Shape(result, Request{Op: OpListServers, Raw: true})
	if err != nil {
		t.Fatalf("Shape: %v", err)
	}
	if !reflect.DeepEqual(out, result) {
		t.Errorf("raw shape = %v, want passthrough", out)
	}
}

func TestClientNoServerBadConfigFailsAtNew(t *testing.T) {
	c, err := New(Options{NoServer: true, ConfigPath: filepath.Join(t.TempDir(), "absent.toml")})
	if err == nil {
		c.Close()
		t.Fatal("New with missing config: want error, got nil")
	}
	if !errors.Is(err, ErrUsage) {
		t.Errorf("error = %v, want ErrUsage", err)
	}
}

func TestClientWarmSessionsReusedAcrossDo(t *testing.T) {
	var opens int
	c := fixtureClient(t, func(cfg *config.Config) (ClosableManager, error) {
		opens++
		return sessionManagerForFixture(t), nil
	})
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		if _, err := c.Do(ctx, Request{Op: OpListTools, Server: "alpha"}); err != nil {
			t.Fatalf("Do %d: %v", i, err)
		}
	}
	if opens != 1 {
		t.Errorf("manager built %d times, want 1", opens)
	}
}

func TestClientCloseIdempotentAndNilSafe(t *testing.T) {
	c := noServerClient(t)
	if _, err := c.Do(context.Background(), Request{Op: OpListTools, Server: "alpha"}); err != nil {
		t.Fatalf("Do: %v", err)
	}
	if err := c.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}
	if err := c.Close(); err != nil {
		t.Errorf("second Close: %v", err)
	}
}

func TestClientAutoStartFlow(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(path, []byte(""), 0o600); err != nil {
		t.Fatal(err)
	}
	// A real listener stands in for the spawned daemon: WaitReady probes the
	// address over TCP before the first RPC is issued.
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer lis.Close()
	var spawned int
	var notice bool
	c, err := New(Options{
		ConfigPath:     path,
		ConnectAddr:    lis.Addr().String(),
		ConnectTimeout: 2 * time.Second,
		CallTimeout:    2 * time.Second,
		Stderr:         &noticeWriter{t: t, seen: &notice},
		SpawnDaemon: func(o Options) (DaemonChild, error) {
			spawned++
			if o.ConnectAddr != lis.Addr().String() {
				t.Errorf("spawn options addr = %q, want %q", o.ConnectAddr, lis.Addr().String())
			}
			return &fakeChild{}, nil
		},
		Dial: fakeDaemonDialer(t, &spawned),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer c.Close()
	out, err := c.Do(context.Background(), Request{Op: OpListTools, Server: "alpha"})
	if err != nil {
		t.Fatalf("Do with auto-start: %v", err)
	}
	if !strings.Contains(jsonString(out), `"echo"`) {
		t.Errorf("list-tools = %s, want daemon payload", jsonString(out))
	}
	if spawned != 1 {
		t.Errorf("spawn count = %d, want 1", spawned)
	}
	if !notice {
		t.Error("auto-start notice not written to stderr")
	}
	// Second Do reuses the daemon connection: no second spawn.
	if _, err := c.Do(context.Background(), Request{Op: OpListTools, Server: "alpha"}); err != nil {
		t.Fatalf("second Do: %v", err)
	}
	if spawned != 1 {
		t.Errorf("spawn count after reuse = %d, want 1", spawned)
	}
}

func TestClientAutoStartRechecksDaemonReadiness(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := New(Options{
		ConfigPath:     path,
		ConnectAddr:    listener.Addr().String(),
		ConnectTimeout: 200 * time.Millisecond,
		SpawnDaemon: func(Options) (DaemonChild, error) {
			return &stubChild{exited: true, code: 1, out: "server: listen: address already in use"}, nil
		},
		Dial: func(context.Context, string) (pb.AgentMCPClient, io.Closer, error) {
			return &failStub{}, noopCloser{}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	_, err = c.Do(context.Background(), Request{Op: OpListTools, Server: "alpha"})
	if err == nil || !strings.Contains(err.Error(), "address already in use") {
		t.Fatalf("Do error = %v, want child startup diagnostic", err)
	}
}

func TestClientAutoStartSpawnFailureMapsToInternal(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(path, []byte(""), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := New(Options{
		ConfigPath:     path,
		ConnectTimeout: 2 * time.Second,
		SpawnDaemon: func(Options) (DaemonChild, error) {
			return nil, errors.New("refused")
		},
		Dial: func(context.Context, string) (pb.AgentMCPClient, io.Closer, error) {
			return nil, nil, errors.New("down")
		},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer c.Close()
	_, err = c.Do(context.Background(), Request{Op: OpListTools, Server: "alpha"})
	if !errors.Is(err, ErrInternal) {
		t.Errorf("error = %v, want ErrInternal", err)
	}
}

func TestClientDaemonOnlyNeverSpawns(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(path, []byte(""), 0o600); err != nil {
		t.Fatal(err)
	}
	var spawned int
	c, err := New(Options{
		ConfigPath:     path,
		ConnectTimeout: 2 * time.Second,
		DaemonOnly:     true,
		SpawnDaemon: func(Options) (DaemonChild, error) {
			spawned++
			return &fakeChild{}, nil
		},
		Dial: downDialer(),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer c.Close()
	_, err = c.Do(context.Background(), Request{Op: OpListTools, Server: "alpha"})
	if !errors.Is(err, ErrMCP) {
		t.Errorf("error = %v, want ErrMCP", err)
	}
	if spawned != 0 {
		t.Errorf("spawn count = %d, want 0", spawned)
	}
}

func TestClientConfigMismatchRejected(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "mine.toml")
	if err := os.WriteFile(path, []byte(""), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := New(Options{
		ConfigPath:     path,
		ConnectTimeout: 2 * time.Second,
		Dial: func(context.Context, string) (pb.AgentMCPClient, io.Closer, error) {
			return &statusStub{configPath: "/other/config.toml"}, noopCloser{}, nil
		},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer c.Close()
	_, err = c.Do(context.Background(), Request{Op: OpListTools, Server: "alpha"})
	if err == nil {
		t.Fatal("Do with mismatching daemon: want error, got nil")
	}
	if !strings.Contains(err.Error(), "config mismatch") {
		t.Errorf("error = %v, want config mismatch", err)
	}
	if !errors.Is(err, ErrMCP) {
		t.Errorf("error = %v, want ErrMCP", err)
	}
}

func TestClientUnauthenticatedDaemonNotAutoStartedAround(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(path, []byte(""), 0o600); err != nil {
		t.Fatal(err)
	}
	var spawned int
	c, err := New(Options{
		ConfigPath:     path,
		ConnectTimeout: 2 * time.Second,
		SpawnDaemon: func(Options) (DaemonChild, error) {
			spawned++
			return &fakeChild{}, nil
		},
		Dial: func(context.Context, string) (pb.AgentMCPClient, io.Closer, error) {
			return &authStub{}, noopCloser{}, nil
		},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer c.Close()
	_, err = c.Do(context.Background(), Request{Op: OpListTools, Server: "alpha"})
	if err == nil {
		t.Fatal("Do against key-rejecting daemon: want error, got nil")
	}
	if !strings.Contains(err.Error(), "rejected the API key") {
		t.Errorf("error = %v, want API-key message", err)
	}
	if spawned != 0 {
		t.Errorf("spawn count = %d, want 0", spawned)
	}
}

func TestUpsertServerAddedThenUpdated(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	res, err := UpsertServer(path, "alpha", config.ServerConfig{Command: "run"})
	if err != nil {
		t.Fatalf("UpsertServer: %v", err)
	}
	if res.Action != "added" || res.Name != "alpha" || res.Path != path {
		t.Errorf("result = %+v, want added/%s/%s", res, "alpha", path)
	}
	res, err = UpsertServer(path, "alpha", config.ServerConfig{Command: "other"})
	if err != nil {
		t.Fatalf("UpsertServer again: %v", err)
	}
	if res.Action != "updated" {
		t.Errorf("second action = %q, want updated", res.Action)
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := cfg.MCPServers["alpha"].Command; got != "other" {
		t.Errorf("saved command = %q, want other", got)
	}
}

func TestUpsertServerInvalidEntryRejected(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	_, err := UpsertServer(path, "alpha", config.ServerConfig{})
	if !errors.Is(err, ErrUsage) {
		t.Errorf("error = %v, want ErrUsage", err)
	}
	if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
		t.Error("invalid upsert created a config file")
	}
}

func TestClientReconnectsCachedDeadDaemonBeforeRequest(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	first := &restartableRPC{}
	second := &restartableRPC{}
	dials := 0
	c, err := New(Options{
		ConfigPath:     path,
		ConnectTimeout: time.Second,
		CallTimeout:    time.Second,
		Dial: func(context.Context, string) (pb.AgentMCPClient, io.Closer, error) {
			dials++
			if dials == 1 {
				return first, &first.closer, nil
			}
			return second, &second.closer, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	if _, err := c.Do(context.Background(), Request{Op: OpListTools, Server: "alpha"}); err != nil {
		t.Fatalf("first Do: %v", err)
	}
	first.stale = true
	if _, err := c.Do(context.Background(), Request{Op: OpListTools, Server: "alpha"}); err != nil {
		t.Fatalf("Do after daemon restart: %v", err)
	}
	if dials != 2 {
		t.Errorf("dial count = %d, want reconnect through a second dial", dials)
	}
	if !first.closer.closed {
		t.Error("stale daemon connection was not closed")
	}
	if first.listCalls != 1 || second.listCalls != 1 {
		t.Errorf("ListTools calls = first %d, second %d; want one on each daemon", first.listCalls, second.listCalls)
	}
}

func TestClientReconnectsBeforeCallToolWithoutReplaying(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	first := &restartableRPC{}
	second := &restartableRPC{}
	dials := 0
	c, err := New(Options{
		ConfigPath:     path,
		ConnectTimeout: time.Second,
		CallTimeout:    time.Second,
		Dial: func(context.Context, string) (pb.AgentMCPClient, io.Closer, error) {
			dials++
			if dials == 1 {
				return first, &first.closer, nil
			}
			return second, &second.closer, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	if _, err := c.Do(context.Background(), Request{Op: OpListTools, Server: "alpha"}); err != nil {
		t.Fatalf("initial Do: %v", err)
	}
	first.stale = true
	request := Request{Op: OpCallTool, Server: "alpha", Tool: "echo", Args: []byte(`{"text":"once"}`)}
	if _, err := c.Do(context.Background(), request); err != nil {
		t.Fatalf("CallTool after daemon restart: %v", err)
	}
	if first.callCalls != 0 || second.callCalls != 1 {
		t.Errorf("CallTool calls = first %d, second %d; want no stale call and exactly one live call", first.callCalls, second.callCalls)
	}
}

// --- test doubles ---

type noopCloser struct{}

func (noopCloser) Close() error { return nil }

// noticeWriter captures the auto-start notice.
type noticeWriter struct {
	t    *testing.T
	seen *bool
}

func (w *noticeWriter) Write(p []byte) (int, error) {
	*w.seen = true
	return len(p), nil
}

// fakeChild is a DaemonChild that never exits.
type fakeChild struct{}

func (*fakeChild) Exited() (int, bool) { return 0, false }
func (*fakeChild) Output() string      { return "" }
func (*fakeChild) Cleanup()            {}

// fakeDaemonDialer returns a Dial that succeeds once a spawn happened and
// serves canned discovery payloads.
func fakeDaemonDialer(t *testing.T, spawned *int) ProbeDial {
	t.Helper()
	return func(ctx context.Context, addr string) (pb.AgentMCPClient, io.Closer, error) {
		if *spawned == 0 {
			return nil, nil, errors.New("dial " + addr + ": down")
		}
		return &rpcStub{}, noopCloser{}, nil
	}
}

func downDialer() ProbeDial {
	return func(context.Context, string) (pb.AgentMCPClient, io.Closer, error) {
		return nil, nil, errors.New("down")
	}
}

type recordingCloser struct{ closed bool }

func (c *recordingCloser) Close() error {
	c.closed = true
	return nil
}

// restartableRPC simulates one daemon connection becoming stale after a
// daemon restart. Requests on the stale connection must never reach a server.
type restartableRPC struct {
	pb.AgentMCPClient
	closer    recordingCloser
	stale     bool
	listCalls int
	callCalls int
}

func (s *restartableRPC) Status(context.Context, *pb.StatusRequest, ...grpc.CallOption) (*pb.StatusResponse, error) {
	if s.stale {
		return nil, status.Error(codes.Unavailable, "connection refused")
	}
	return &pb.StatusResponse{}, nil
}

func (s *restartableRPC) ListTools(context.Context, *pb.ListToolsRequest, ...grpc.CallOption) (*pb.ListToolsResponse, error) {
	s.listCalls++
	if s.stale {
		return nil, status.Error(codes.Unavailable, "connection refused")
	}
	return &pb.ListToolsResponse{Json: []byte(`{"tools":[{"name":"echo"}]}`)}, nil
}

func (s *restartableRPC) CallTool(context.Context, *pb.CallToolRequest, ...grpc.CallOption) (*pb.CallToolResponse, error) {
	s.callCalls++
	if s.stale {
		return nil, status.Error(codes.Unavailable, "connection refused")
	}
	return &pb.CallToolResponse{Json: []byte(`{"content":[{"type":"text","text":"once"}]}`)}, nil
}

// rpcStub answers Status and the discovery RPCs with canned payloads.
type rpcStub struct {
	pb.AgentMCPClient
}

func (s *rpcStub) Status(_ context.Context, _ *pb.StatusRequest, _ ...grpc.CallOption) (*pb.StatusResponse, error) {
	return &pb.StatusResponse{}, nil
}

func (s *rpcStub) ListTools(_ context.Context, _ *pb.ListToolsRequest, _ ...grpc.CallOption) (*pb.ListToolsResponse, error) {
	return &pb.ListToolsResponse{Json: []byte(`{"tools":[{"name":"echo"}]}`)}, nil
}

// statusStub serves a Status with a foreign config path.
type statusStub struct {
	pb.AgentMCPClient
	configPath string
}

func (s *statusStub) Status(_ context.Context, _ *pb.StatusRequest, _ ...grpc.CallOption) (*pb.StatusResponse, error) {
	return &pb.StatusResponse{ConfigPath: s.configPath}, nil
}

// authStub rejects every call as unauthenticated.
type authStub struct {
	pb.AgentMCPClient
}

func (authStub) Status(_ context.Context, _ *pb.StatusRequest, _ ...grpc.CallOption) (*pb.StatusResponse, error) {
	return nil, status.Error(codes.Unauthenticated, "missing api key")
}
