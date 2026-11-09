package client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/pijalu/agent-mcp/internal/config"
	pb "github.com/pijalu/agent-mcp/proto/agentmcp/v1"
)

// --- InvokeDaemon over canned RPC stubs ---

// invokingDaemonClient runs InvokeDaemon against stub with request r.
func invokingDaemonClient(t *testing.T, r Request, stub pb.AgentMCPClient) (any, error) {
	t.Helper()
	return InvokeDaemon(context.Background(), stub, r)
}

// listStub answers every discovery RPC with the given JSON body.
type listStub struct {
	pb.AgentMCPClient
	body string
}

func (s *listStub) ListTools(_ context.Context, _ *pb.ListToolsRequest, _ ...grpc.CallOption) (*pb.ListToolsResponse, error) {
	return &pb.ListToolsResponse{Json: []byte(s.body)}, nil
}

func (s *listStub) ListResources(_ context.Context, _ *pb.ListResourcesRequest, _ ...grpc.CallOption) (*pb.ListResourcesResponse, error) {
	return &pb.ListResourcesResponse{Json: []byte(s.body)}, nil
}

func (s *listStub) ListPrompts(_ context.Context, _ *pb.ListPromptsRequest, _ ...grpc.CallOption) (*pb.ListPromptsResponse, error) {
	return &pb.ListPromptsResponse{Json: []byte(s.body)}, nil
}

func (s *listStub) ReadResource(_ context.Context, _ *pb.ReadResourceRequest, _ ...grpc.CallOption) (*pb.ReadResourceResponse, error) {
	return &pb.ReadResourceResponse{Json: []byte(s.body)}, nil
}

func (s *listStub) GetPrompt(_ context.Context, _ *pb.GetPromptRequest, _ ...grpc.CallOption) (*pb.GetPromptResponse, error) {
	return &pb.GetPromptResponse{Json: []byte(s.body)}, nil
}

func (s *listStub) CallTool(_ context.Context, _ *pb.CallToolRequest, _ ...grpc.CallOption) (*pb.CallToolResponse, error) {
	return &pb.CallToolResponse{Json: []byte(s.body)}, nil
}

// failStub answers every RPC with a plain (non-gRPC) error.
type failStub struct {
	pb.AgentMCPClient
}

func (failStub) Status(_ context.Context, _ *pb.StatusRequest, _ ...grpc.CallOption) (*pb.StatusResponse, error) {
	return nil, errors.New("kaboom")
}

func (failStub) ListTools(_ context.Context, _ *pb.ListToolsRequest, _ ...grpc.CallOption) (*pb.ListToolsResponse, error) {
	return nil, errors.New("kaboom")
}

// toolErrorStub answers CallTool with an MCP isError result.
type toolErrorStub struct {
	pb.AgentMCPClient
}

func (toolErrorStub) CallTool(_ context.Context, _ *pb.CallToolRequest, _ ...grpc.CallOption) (*pb.CallToolResponse, error) {
	return &pb.CallToolResponse{Json: []byte(`{"isError":true,"content":[{"type":"text","text":"nope"}]}`)}, nil
}

func TestInvokeDaemonEachOpDecodes(t *testing.T) {
	body := `{"items":[{"name":"x"}]}`
	stub := &listStub{body: body}
	cases := []struct {
		r    Request
		kind string
	}{
		{Request{Op: OpListTools, Server: "a"}, "items"},
		{Request{Op: OpListResources, Server: "a"}, "items"},
		{Request{Op: OpListPrompts, Server: "a"}, "items"},
		{Request{Op: OpReadResource, Server: "a", URI: "mem://x"}, "items"},
		{Request{Op: OpGetPrompt, Server: "a", Prompt: "g", Args: []byte(`{"k":"v"}`)}, "items"},
		{Request{Op: OpCallTool, Server: "a", Tool: "echo"}, "items"},
	}
	for _, tc := range cases {
		out, err := invokingDaemonClient(t, tc.r, stub)
		if err != nil {
			t.Errorf("%s: %v", tc.r.Op, err)
			continue
		}
		if !strings.Contains(jsonString(out), tc.kind) {
			t.Errorf("%s = %s, want body", tc.r.Op, jsonString(out))
		}
	}
}

func TestInvokeDaemonCursorSent(t *testing.T) {
	var got string
	stub := cursorCapture{onBody: func(cursor string) { got = cursor }}
	_, err := InvokeDaemon(context.Background(), &stub, Request{Op: OpListTools, Server: "a", Cursor: "cur-1"})
	if err != nil {
		t.Fatalf("InvokeDaemon: %v", err)
	}
	if got != "cur-1" {
		t.Errorf("cursor received = %q, want cur-1", got)
	}
}

type cursorCapture struct {
	pb.AgentMCPClient
	onBody func(cursor string)
}

func (c *cursorCapture) ListTools(_ context.Context, req *pb.ListToolsRequest, _ ...grpc.CallOption) (*pb.ListToolsResponse, error) {
	var body struct {
		Cursor string `json:"cursor"`
	}
	_ = json.Unmarshal(req.GetJson(), &body)
	c.onBody(body.Cursor)
	return &pb.ListToolsResponse{Json: []byte(`{"tools":[]}`)}, nil
}

func TestInvokeDaemonRPCErrorsMapThroughRPCFailure(t *testing.T) {
	invalid := &invalidArgStub{}
	_, err := invokingDaemonClient(t, Request{Op: OpListTools, Server: "a"}, invalid)
	if !errors.Is(err, ErrUsage) {
		t.Errorf("invalid-argument error = %v, want ErrUsage", err)
	}
	_, err = invokingDaemonClient(t, Request{Op: OpListTools, Server: "a"}, &failStub{})
	if !errors.Is(err, ErrMCP) {
		t.Errorf("plain error = %v, want ErrMCP", err)
	}
}

type invalidArgStub struct {
	pb.AgentMCPClient
}

func (invalidArgStub) ListTools(_ context.Context, _ *pb.ListToolsRequest, _ ...grpc.CallOption) (*pb.ListToolsResponse, error) {
	return nil, status.Error(codes.InvalidArgument, "bad cursor")
}

func TestInvokeDaemonToolErrorSurfaces(t *testing.T) {
	_, err := invokingDaemonClient(t, Request{Op: OpCallTool, Server: "a", Tool: "fail"}, &toolErrorStub{})
	if !errors.Is(err, ErrMCP) {
		t.Errorf("isError result = %v, want ErrMCP", err)
	}
	if !strings.Contains(err.Error(), "nope") {
		t.Errorf("error = %v, want tool text", err)
	}
}

func TestDecodeRawRejectsGarbage(t *testing.T) {
	_, err := DecodeRaw([]byte("{not json"), nil)
	if !errors.Is(err, ErrInternal) {
		t.Errorf("error = %v, want ErrInternal", err)
	}
}

func TestDecodeRawEmptyBody(t *testing.T) {
	v, err := DecodeRaw(nil, nil)
	if err != nil || v != nil {
		t.Errorf("DecodeRaw(nil, nil) = %v, %v, want nil, nil", v, err)
	}
}

// --- error taxonomy ---

func TestRPCFailureBranches(t *testing.T) {
	if err := RPCFailure(status.Error(codes.InvalidArgument, "session: unknown server \"x\"")); !errors.Is(err, ErrUsage) {
		t.Errorf("invalid arg = %v, want ErrUsage", err)
	}
	if err := RPCFailure(status.Error(codes.NotFound, "session: unknown tool \"y\"")); !errors.Is(err, ErrUsage) {
		t.Errorf("unknown tool = %v, want ErrUsage", err)
	}
	if err := RPCFailure(status.Error(codes.Unavailable, "daemon busy")); !errors.Is(err, ErrMCP) || errors.Is(err, ErrUsage) {
		t.Errorf("unavailable = %v, want plain ErrMCP", err)
	}
	if err := RPCFailure(errors.New("plain")); !errors.Is(err, ErrMCP) {
		t.Errorf("plain = %v, want ErrMCP", err)
	}
}

func TestSessionFailureBranches(t *testing.T) {
	if err := SessionFailure(fmt.Errorf("session: %w", ErrUnknownServer)); !errors.Is(err, ErrUsage) {
		t.Errorf("unknown server = %v, want ErrUsage", err)
	}
	if err := SessionFailure(errors.New("session: unknown tool \"z\"")); !errors.Is(err, ErrUsage) {
		t.Errorf("unknown tool = %v, want ErrUsage", err)
	}
	if err := SessionFailure(errors.New("dial refused")); !errors.Is(err, ErrMCP) {
		t.Errorf("other = %v, want ErrMCP", err)
	}
}

func TestIsUnknownTarget(t *testing.T) {
	if !IsUnknownTarget(`no such server: unknown server "x"`) {
		t.Error("unknown server not detected")
	}
	if IsUnknownTarget("all good") {
		t.Error("false positive on clean message")
	}
}

// --- daemon helpers ---

func TestTransportReasonStripsWrapper(t *testing.T) {
	err := status.Error(codes.Unavailable,
		`connection error: desc = "transport: Error while dialing: dial tcp 1.2.3.4:1: connect: connection refused"`)
	got := TransportReason(err)
	if got != "dial tcp 1.2.3.4:1: connect: connection refused" {
		t.Errorf("transportReason = %q", got)
	}
}

func TestChildDetailJoinsAndUnwraps(t *testing.T) {
	raw := "{\"error\":{\"message\":\"config: bad\"}}\nplain line\n\n"
	got := ChildDetail(raw)
	if got != "config: bad; plain line" {
		t.Errorf("childDetail = %q", got)
	}
	if got := ChildDetail(""); got != "(no output)" {
		t.Errorf("empty = %q", got)
	}
}

func TestDaemonStartupErrorKinds(t *testing.T) {
	if err := DaemonStartupError("a:1", 2, "boom"); !errors.Is(err, ErrUsage) {
		t.Errorf("exit 2 = %v, want ErrUsage", err)
	}
	if err := DaemonStartupError("a:1", 3, "boom"); !errors.Is(err, ErrInternal) {
		t.Errorf("exit 3 = %v, want ErrInternal", err)
	}
	if err := DaemonStartupError("a:1", 1, "boom"); !errors.Is(err, ErrMCP) {
		t.Errorf("exit 1 = %v, want ErrMCP", err)
	}
}

func TestWaitReadyReportsExitingChild(t *testing.T) {
	child := &stubChild{exited: true, code: 2, out: "usage error: config: nope\n"}
	err := WaitReady("127.0.0.1:1", child, 400*time.Millisecond)
	if !errors.Is(err, ErrUsage) {
		t.Errorf("error = %v, want ErrUsage", err)
	}
}

func TestWaitReadyTimesOut(t *testing.T) {
	start := time.Now()
	err := WaitReady("127.0.0.1:1", nil, 150*time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "not ready within") {
		t.Errorf("error = %v, want readiness timeout", err)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("waitReady took %s, want fast", elapsed)
	}
}

func TestWaitReadySucceedsOnListener(t *testing.T) {
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer lis.Close()
	if err := WaitReady(lis.Addr().String(), nil, time.Second); err != nil {
		t.Errorf("WaitReady on listener = %v, want nil", err)
	}
}

type stubChild struct {
	exited bool
	code   int
	out    string
}

func (c *stubChild) Exited() (int, bool) { return c.code, c.exited }
func (c *stubChild) Output() string      { return c.out }
func (*stubChild) Cleanup()              {}

func TestChildProcessLifecycle(t *testing.T) {
	if testing.Short() {
		t.Skip("spawns a real process")
	}
	log, err := os.CreateTemp("", "client-child-*.log")
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("sh", "-c", "echo hello; exit 7")
	cmd.Stdout = log
	cmd.Stderr = log
	child := &childProcess{cmd: cmd, logPath: log.Name()}
	if err := cmd.Start(); err != nil {
		_ = log.Close()
		_ = os.Remove(log.Name())
		t.Fatal(err)
	}
	_ = log.Close() // the child holds its own descriptor
	go child.watch()
	code, exited := child.Exited()
	if exited {
		t.Fatal("child reported exited before Wait")
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if code, exited = child.Exited(); exited {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if !exited || code != 7 {
		t.Fatalf("Exited = (%d, %v), want (7, true)", code, exited)
	}
	if out := child.Output(); !strings.Contains(out, "hello") {
		t.Errorf("Output = %q, want captured echo", out)
	}
	child.Cleanup()
	child.Cleanup() // idempotent
	if _, err := os.Stat(log.Name()); !os.IsNotExist(err) {
		t.Error("Cleanup did not remove the capture file")
	}
}

func TestSpawnDaemonRefusesTestBinary(t *testing.T) {
	if _, err := SpawnDaemon(Options{}); err == nil {
		t.Fatal("SpawnDaemon from test binary: want error, got nil")
	} else if !strings.Contains(err.Error(), "refusing to auto-start") {
		t.Errorf("error = %v, want test-binary guard", err)
	}
}

func TestDaemonArgs(t *testing.T) {
	o := Options{ConfigPath: "c.toml", ConnectAddr: "h:1", APIKey: "k"}
	wantFlags := []string{"--server-idle-time", "5m", "--config", "c.toml", "--listen", "h:1", "--api-key", "k"}
	for _, tc := range []struct {
		name    string
		command []string
	}{
		{name: "agent-mcp CLI default", command: []string{"server", "start"}},
		{name: "Goa embedded daemon", command: []string{"mcp", "daemon"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			want := append(append([]string(nil), tc.command...), wantFlags...)
			if got := daemonArgs(o, tc.command); !reflect.DeepEqual(got, want) {
				t.Fatalf("daemonArgs() = %q, want %q", got, want)
			}
		})
	}
}

func TestSpawnDaemonWithCommandRequiresExecutable(t *testing.T) {
	_, err := SpawnDaemonWithCommand(Options{}, filepath.Join(t.TempDir(), "missing"), "mcp", "daemon")
	if err == nil || !strings.Contains(err.Error(), "spawn daemon") {
		t.Fatalf("SpawnDaemonWithCommand() error = %v, want spawn failure", err)
	}
}

// --- ShutdownDaemon ---

func TestShutdownDaemonAbsentIsIdempotent(t *testing.T) {
	c, err := New(Options{
		// Port 1 is reserved: the dial cannot succeed.
		Dial: downDialer(),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer c.Close()
	res, err := c.ShutdownDaemon(context.Background(), 200*time.Millisecond)
	if err != nil {
		t.Fatalf("ShutdownDaemon: %v", err)
	}
	if res.Shutdown {
		t.Errorf("result = %+v, want shutdown false", res)
	}
}

func TestShutdownDaemonRefusedWhenPortStaysOpen(t *testing.T) {
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer lis.Close()
	c, err := New(Options{ConnectAddr: lis.Addr().String()})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer c.Close()
	// The RPC fails (stub), and the port stays open: the daemon refuses
	// shutdown.
	c.opts.Dial = func(context.Context, string) (pb.AgentMCPClient, io.Closer, error) {
		return &shutdownFailStub{}, noopCloser{}, nil
	}
	_, err = c.ShutdownDaemon(context.Background(), 300*time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "refused shutdown") {
		t.Errorf("error = %v, want refused shutdown", err)
	}
}

type shutdownFailStub struct {
	pb.AgentMCPClient
}

func (shutdownFailStub) ShutdownAll(_ context.Context, _ *pb.ShutdownAllRequest, _ ...grpc.CallOption) (*pb.ShutdownAllResponse, error) {
	return nil, errors.New("not permitted")
}

func TestShutdownDaemonReportsSessionsClosed(t *testing.T) {
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer lis.Close()
	addr := lis.Addr().String()
	c, err := New(Options{ConnectAddr: addr})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer c.Close()
	go func() {
		// Keep accepting until the test closes the listener, so the
		// gone-check sees the port close only when we want it to.
		for {
			conn, err := lis.Accept()
			if err != nil {
				return
			}
			_ = conn.Close()
		}
	}()
	// The stub answers ShutdownAll; the listener closes right after, so
	// waitDaemonGone observes the port going down.
	shutdownStub := &shutdownAllStub{onCall: func() { _ = lis.Close() }}
	c.opts.Dial = func(context.Context, string) (pb.AgentMCPClient, io.Closer, error) {
		return shutdownStub, noopCloser{}, nil
	}
	res, err := c.ShutdownDaemon(context.Background(), 2*time.Second)
	if err != nil {
		t.Fatalf("ShutdownDaemon: %v", err)
	}
	if !res.Shutdown || res.SessionsClosed != 3 {
		t.Errorf("result = %+v, want shutdown true, 3 closed", res)
	}
}

type shutdownAllStub struct {
	pb.AgentMCPClient
	onCall func()
}

func (s *shutdownAllStub) ShutdownAll(_ context.Context, _ *pb.ShutdownAllRequest, _ ...grpc.CallOption) (*pb.ShutdownAllResponse, error) {
	if s.onCall != nil {
		s.onCall()
	}
	return &pb.ShutdownAllResponse{SessionsClosed: 3}, nil
}

// --- misc uncovered branches ---

func TestGRPCDialBuildsClient(t *testing.T) {
	cl, closer, err := GRPCDial(context.Background(), "127.0.0.1:1")
	if err != nil {
		t.Fatalf("GRPCDial: %v", err)
	}
	if cl == nil || closer == nil {
		t.Fatal("GRPCDial returned nil client or closer")
	}
	_ = closer.Close()
}

func TestShapeEncodeFailureSurfaces(t *testing.T) {
	// A channel cannot marshal: Shape surfaces the filter's raw encode error
	// (CLI parity — the reducer does not wrap it).
	_, err := Shape(map[string]any{"tools": []any{make(chan int)}}, Request{Op: OpListTools})
	if err == nil {
		t.Fatal("Shape with unencodable payload: want error, got nil")
	}
	if errors.Is(err, ErrUsage) {
		t.Errorf("error = %v, must not be a usage failure", err)
	}
}

func TestUpsertServerLoadFailureWrapsUsage(t *testing.T) {
	// A directory in place of the config file fails at load, mapped to the
	// CLI's usage exit code.
	dir := filepath.Join(t.TempDir(), "config.toml")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	_, err := UpsertServer(dir, "alpha", config.ServerConfig{Command: "x"})
	if !errors.Is(err, ErrUsage) {
		t.Errorf("error = %v, want ErrUsage", err)
	}
}

func TestClientDefaultsApplied(t *testing.T) {
	c, err := New(Options{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer c.Close()
	if c.addr() != DefaultConnectAddr {
		t.Errorf("addr = %q, want default", c.addr())
	}
	if c.callTimeoutValue() != DefaultCallTimeout {
		t.Errorf("call timeout = %s, want default", c.callTimeoutValue())
	}
	if c.connectTimeoutValue() != DefaultConnectTimeout {
		t.Errorf("connect timeout = %s, want default", c.connectTimeoutValue())
	}
}
