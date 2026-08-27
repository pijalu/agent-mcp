package daemon_test

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"

	"github.com/pijalu/agent-mcp/internal/daemon"
	"github.com/pijalu/agent-mcp/internal/session"
	pb "github.com/pijalu/agent-mcp/proto/agentmcp/v1"
)

// fakeMgr implements daemon.SessionManager with canned results.
type fakeMgr struct {
	mu            sync.Mutex
	lastServer    string
	closeAllCount int
	callErr       error
	statuses      []session.ServerInfo
}

func (f *fakeMgr) note(name string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lastServer = name
}

func (f *fakeMgr) CallTool(ctx context.Context, name string, params *mcp.CallToolParams) (*mcp.CallToolResult, error) {
	f.note(name)
	if f.callErr != nil {
		return nil, f.callErr
	}
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "ok:" + params.Name}}}, nil
}

func (f *fakeMgr) ListTools(ctx context.Context, name string, params *mcp.ListToolsParams) (*mcp.ListToolsResult, error) {
	f.note(name)
	return &mcp.ListToolsResult{Tools: []*mcp.Tool{{Name: "t1"}, {Name: "t2"}}}, nil
}

func (f *fakeMgr) ListResources(ctx context.Context, name string, params *mcp.ListResourcesParams) (*mcp.ListResourcesResult, error) {
	f.note(name)
	return &mcp.ListResourcesResult{Resources: []*mcp.Resource{{URI: "file:///a"}}}, nil
}

func (f *fakeMgr) ReadResource(ctx context.Context, name string, params *mcp.ReadResourceParams) (*mcp.ReadResourceResult, error) {
	f.note(name)
	return &mcp.ReadResourceResult{Contents: []*mcp.ResourceContents{{URI: params.URI, Text: "body"}}}, nil
}

func (f *fakeMgr) ListPrompts(ctx context.Context, name string, params *mcp.ListPromptsParams) (*mcp.ListPromptsResult, error) {
	f.note(name)
	return &mcp.ListPromptsResult{Prompts: []*mcp.Prompt{{Name: "p1"}}}, nil
}

func (f *fakeMgr) GetPrompt(ctx context.Context, name string, params *mcp.GetPromptParams) (*mcp.GetPromptResult, error) {
	f.note(name)
	return &mcp.GetPromptResult{}, nil
}

func (f *fakeMgr) CloseAll() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closeAllCount++
	return 2
}

func (f *fakeMgr) ServerStatuses() []session.ServerInfo {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.statuses
}

// fakeTimer records Reset calls; firing is driven by the test via fire().
type fakeTimer struct {
	mu     sync.Mutex
	fn     func()
	resets int
}

func (t *fakeTimer) Stop() bool { return true }

func (t *fakeTimer) Reset(d time.Duration) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.resets++
	return true
}

func (t *fakeTimer) fire() {
	t.mu.Lock()
	fn := t.fn
	t.mu.Unlock()
	if fn != nil {
		fn()
	}
}

// fakeClock hands out a fakeTimer for the idle reaper and real timers for
// everything else, keeping drain deadlines honest in tests.
type fakeClock struct {
	mu      sync.Mutex
	idle    *fakeTimer
	pending map[*fakeTimer]struct{}
}

func newFakeClock() *fakeClock { return &fakeClock{pending: map[*fakeTimer]struct{}{}} }

func (c *fakeClock) Now() time.Time { return time.Now() }

func (c *fakeClock) AfterFunc(d time.Duration, fn func()) daemon.Timer {
	if d >= time.Minute {
		c.mu.Lock()
		defer c.mu.Unlock()
		t := &fakeTimer{fn: fn}
		c.pending[t] = struct{}{}
		c.idle = t
		return t
	}
	return time.AfterFunc(d, fn)
}

func (c *fakeClock) fireIdle() {
	c.mu.Lock()
	t := c.idle
	c.mu.Unlock()
	if t != nil {
		t.fire()
	}
}

// waitArmed blocks until the daemon has armed the idle timer. Serve runs in
// its own goroutine and grpc.NewClient does not connect eagerly, so a test
// that fires immediately after startDaemon would otherwise race the first
// touch() call and miss a not-yet-created timer.
func (c *fakeClock) waitArmed(t *testing.T) {
	t.Helper()
	waitFor(t, "idle timer armed", func() bool {
		c.mu.Lock()
		defer c.mu.Unlock()
		return c.idle != nil
	})
}

func (c *fakeClock) idleResets() int {
	c.mu.Lock()
	t := c.idle
	c.mu.Unlock()
	if t == nil {
		return 0
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.resets
}

// testDaemon bundles a running daemon with its client connection.
type testDaemon struct {
	t    *testing.T
	d    *daemon.Daemon
	mgr  *fakeMgr
	conn *grpc.ClientConn
	cli  pb.AgentMCPClient
	addr string
}

// startDaemon serves a daemon on loopback and dials it. apiKey, when
// non-empty, is attached to every client call as metadata.
func startDaemon(t *testing.T, opts daemon.Options) *testDaemon {
	t.Helper()
	mgr := &fakeMgr{}
	if mgr.statuses == nil {
		mgr.statuses = []session.ServerInfo{
			{Name: "alpha", State: "connected", ActiveSessions: 1},
			{Name: "beta", State: "idle"},
		}
	}
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
	return &testDaemon{t: t, d: d, mgr: mgr, conn: conn, cli: pb.NewAgentMCPClient(conn), addr: lis.Addr().String()}
}

// ctxWithKey returns a context carrying the API-key metadata header.
func ctxWithKey(key string) context.Context {
	return metadata.AppendToOutgoingContext(context.Background(), "x-agent-mcp-api-key", key)
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return raw
}

var errFake = errors.New("fake manager failure")

// slowMgr blocks inside CallTool until released, for drain tests.
type slowMgr struct {
	fakeMgr
	started  chan struct{}
	release  chan struct{}
	inflight atomic.Int32
}

func (s *slowMgr) CallTool(ctx context.Context, name string, params *mcp.CallToolParams) (*mcp.CallToolResult, error) {
	s.inflight.Add(1)
	defer s.inflight.Add(-1)
	close(s.started)
	<-s.release
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "slow"}}}, nil
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}
