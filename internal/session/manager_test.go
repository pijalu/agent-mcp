package session

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/pijalu/agent-mcp/internal/config"
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

// fixtureDialer returns a DialFunc that connects to the fixture server. Each
// call needs a fresh pipe, so the fixture is re-created per dial.
func fixtureDialer(t *testing.T) DialFunc {
	t.Helper()
	return func(ctx context.Context, _ string, _ config.ServerConfig) (*mcp.ClientSession, error) {
		_, clientTransport := newFixtureServer(t)
		client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "v0.0.1"}, nil)
		return client.Connect(ctx, clientTransport, nil)
	}
}

func testConfigs() map[string]config.ServerConfig {
	return map[string]config.ServerConfig{
		"alpha": {Command: "alpha-cmd"},
		"beta":  {URL: "http://localhost:1/mcp", Transport: "streamable"},
	}
}

func TestManagerOpenDedupes(t *testing.T) {
	var dials atomic.Int32
	dial := func(ctx context.Context, name string, cfg config.ServerConfig) (*mcp.ClientSession, error) {
		dials.Add(1)
		return fixtureDialer(t)(ctx, name, cfg)
	}
	m := NewManager(testConfigs(), dial)

	s1, err := m.Open(context.Background(), "alpha")
	if err != nil {
		t.Fatalf("Open alpha: %v", err)
	}
	s2, err := m.Open(context.Background(), "alpha")
	if err != nil {
		t.Fatalf("Open alpha again: %v", err)
	}
	if s1 != s2 {
		t.Error("second Open returned a different session")
	}
	if got := dials.Load(); got != 1 {
		t.Errorf("dial count = %d, want 1", got)
	}
}

func TestManagerOpenUnknownServer(t *testing.T) {
	m := NewManager(testConfigs(), fixtureDialer(t))
	if _, err := m.Open(context.Background(), "ghost"); err == nil {
		t.Fatal("Open(ghost): want error, got nil")
	}
}

func TestManagerDialErrorPropagates(t *testing.T) {
	want := errors.New("boom")
	m := NewManager(testConfigs(), func(context.Context, string, config.ServerConfig) (*mcp.ClientSession, error) {
		return nil, want
	})
	_, err := m.Open(context.Background(), "alpha")
	if !errors.Is(err, want) {
		t.Fatalf("Open error = %v, want %v", err, want)
	}
}

func TestManagerCallToolPassthrough(t *testing.T) {
	m := NewManager(testConfigs(), fixtureDialer(t))
	result, err := m.CallTool(context.Background(), "alpha", &mcp.CallToolParams{
		Name:      "echo",
		Arguments: map[string]any{"text": "ping"},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if len(result.Content) != 1 {
		t.Fatalf("CallTool returned %d content items, want 1", len(result.Content))
	}
	text, ok := result.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("content type = %T, want *mcp.TextContent", result.Content[0])
	}
	if text.Text != "ping" {
		t.Errorf("echo = %q, want %q", text.Text, "ping")
	}
}

func TestManagerListTools(t *testing.T) {
	m := NewManager(testConfigs(), fixtureDialer(t))
	result, err := m.ListTools(context.Background(), "alpha", &mcp.ListToolsParams{Cursor: ""})
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	if len(result.Tools) != 1 || result.Tools[0].Name != "echo" {
		t.Errorf("ListTools = %+v, want single echo tool", result.Tools)
	}
}

func TestManagerResources(t *testing.T) {
	ctx := context.Background()
	m := NewManager(testConfigs(), fixtureDialer(t))

	resources, err := m.ListResources(ctx, "alpha", &mcp.ListResourcesParams{})
	if err != nil {
		t.Fatalf("ListResources: %v", err)
	}
	if len(resources.Resources) != 1 || resources.Resources[0].URI != "mem://hello" {
		t.Errorf("ListResources = %+v", resources.Resources)
	}

	read, err := m.ReadResource(ctx, "alpha", &mcp.ReadResourceParams{URI: "mem://hello"})
	if err != nil {
		t.Fatalf("ReadResource: %v", err)
	}
	if len(read.Contents) != 1 || read.Contents[0].Text != "world" {
		t.Errorf("ReadResource = %+v", read.Contents)
	}
}

func TestManagerPrompts(t *testing.T) {
	ctx := context.Background()
	m := NewManager(testConfigs(), fixtureDialer(t))

	prompts, err := m.ListPrompts(ctx, "alpha", &mcp.ListPromptsParams{})
	if err != nil {
		t.Fatalf("ListPrompts: %v", err)
	}
	if len(prompts.Prompts) != 1 || prompts.Prompts[0].Name != "greet" {
		t.Errorf("ListPrompts = %+v", prompts.Prompts)
	}

	prompt, err := m.GetPrompt(ctx, "alpha", &mcp.GetPromptParams{
		Name:      "greet",
		Arguments: map[string]string{"name": "agent"},
	})
	if err != nil {
		t.Fatalf("GetPrompt: %v", err)
	}
	if len(prompt.Messages) != 1 {
		t.Fatalf("GetPrompt returned %d messages, want 1", len(prompt.Messages))
	}
	if got := prompt.Messages[0].Content.(*mcp.TextContent).Text; got != "hello agent" {
		t.Errorf("prompt message = %q, want %q", got, "hello agent")
	}
}

func TestManagerClose(t *testing.T) {
	ctx := context.Background()
	m := NewManager(testConfigs(), fixtureDialer(t))

	if err := m.Close("ghost"); !errors.Is(err, ErrUnknownServer) {
		t.Fatalf("Close(ghost) = %v, want ErrUnknownServer", err)
	}
	if _, err := m.Open(ctx, "alpha"); err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := m.Close("alpha"); err != nil {
		t.Fatalf("Close: %v", err)
	}
	// Entry is closed now; Open must fail fast rather than redial.
	if _, err := m.Open(ctx, "alpha"); err == nil {
		t.Fatal("Open after Close: want error, got nil")
	}
	if err := m.Close("alpha"); err != nil {
		t.Fatalf("double Close must not fail: %v", err)
	}
	if _, err := m.CallTool(ctx, "alpha", &mcp.CallToolParams{Name: "echo"}); err == nil {
		t.Fatal("CallTool after Close: want error, got nil")
	}
}

func TestManagerCloseAll(t *testing.T) {
	ctx := context.Background()
	m := NewManager(testConfigs(), fixtureDialer(t))
	for _, name := range []string{"alpha", "beta"} {
		if _, err := m.Open(ctx, name); err != nil {
			t.Fatalf("Open %s: %v", name, err)
		}
	}
	if got := m.CloseAll(); got != 2 {
		t.Errorf("CloseAll = %d, want 2", got)
	}
	if got := m.CloseAll(); got != 0 {
		t.Errorf("second CloseAll = %d, want 0", got)
	}
	if _, err := m.Open(ctx, "alpha"); err == nil {
		t.Fatal("Open after CloseAll: want error, got nil")
	}
}

// TestManagerRace hammers Open/CallTool/Close/CloseAll concurrently. Run
// with -race; the assertion is just that nothing panics or corrupts state.
func TestManagerRace(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	m := NewManager(testConfigs(), fixtureDialer(t))
	names := []string{"alpha", "beta"}

	var wg sync.WaitGroup
	for i := range 8 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			name := names[i%len(names)]
			for range 20 {
				if _, err := m.Open(ctx, name); err != nil {
					return // entry closed concurrently; that path is legal
				}
				_, _ = m.CallTool(ctx, name, &mcp.CallToolParams{
					Name:      "echo",
					Arguments: map[string]any{"text": fmt.Sprint(i)},
				})
			}
		}(i)
	}
	for i := range 4 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			name := names[i%len(names)]
			for range 10 {
				_ = m.Close(name)
				m.CloseAll()
			}
		}(i)
	}
	wg.Wait()
	m.CloseAll()
}
