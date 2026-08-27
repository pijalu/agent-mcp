package session

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/pijalu/agent-mcp/internal/config"
)

// TestUnknownServerErrorListsConfiguredServers covers the unhelpful-error
// defect: a wrong server name must name the configured ones.
func TestUnknownServerErrorListsConfiguredServers(t *testing.T) {
	m := NewManager(testConfigs(), nil)
	ctx := context.Background()
	err := m.call(ctx, "gamma", func(*mcp.ClientSession) error { return nil })
	if !errors.Is(err, ErrUnknownServer) {
		t.Fatalf("err = %v, want ErrUnknownServer", err)
	}
	if !strings.Contains(err.Error(), "configured servers: alpha, beta") {
		t.Errorf("err = %q, want the configured names", err)
	}
	if cerr := m.Close("gamma"); !strings.Contains(cerr.Error(), "configured servers: alpha, beta") {
		t.Errorf("Close err = %q, want the configured names", cerr)
	}
}

// TestUnknownToolErrorListsTools covers the same defect for tools: the failure
// names what the server exposes, without the caller probing for it.
func TestUnknownToolErrorListsTools(t *testing.T) {
	m := NewManager(map[string]config.ServerConfig{"fx": {}}, fixtureDialer(t))
	result, err := m.CallTool(context.Background(), "fx", &mcp.CallToolParams{Name: "nope"})
	if err == nil {
		t.Fatalf("result = %v, want an unknown-tool error", result)
	}
	if !strings.Contains(err.Error(), "unknown tool") {
		t.Fatalf("err = %q, want an unknown-tool failure", err)
	}
	if !strings.Contains(err.Error(), "available tools: echo") {
		t.Errorf("err = %q, want the available tool names", err)
	}
}

// TestUnknownToolHintIsBestEffort pins that a probe failure never hides the
// original failure.
func TestUnknownToolHintIsBestEffort(t *testing.T) {
	m := NewManager(testConfigs(), nil)
	ctx := context.Background()
	plain := errors.New("boom")
	if got := m.unknownToolHint(ctx, "alpha", plain); got != plain {
		t.Errorf("non-matching error was rewritten: %v", got)
	}
	unknown := errors.New(`calling "tools/call": unknown tool "nope"`)
	if got := m.unknownToolHint(ctx, "nosuch", unknown); got != unknown {
		t.Errorf("failed probe rewrote the error: %v", got)
	}
}

// TestUnknownToolHintSkipsEmptyToolList pins the empty-probe branch.
func TestUnknownToolHintSkipsEmptyToolList(t *testing.T) {
	server := mcp.NewServer(&mcp.Implementation{Name: "toolless", Version: "v0.0.1"}, nil)
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	if _, err := server.Connect(context.Background(), serverTransport, nil); err != nil {
		t.Fatalf("connect: %v", err)
	}
	m := NewManager(map[string]config.ServerConfig{"fx": {}}, func(context.Context, string, config.ServerConfig) (*mcp.ClientSession, error) {
		client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "v0.0.1"}, nil)
		return client.Connect(context.Background(), clientTransport, nil)
	})
	unknown := errors.New(`unknown tool "nope"`)
	if got := m.unknownToolHint(context.Background(), "fx", unknown); got != unknown {
		t.Errorf("empty tool list rewrote the error: %v", got)
	}
}

// TestCandidateHint covers the rendering: nothing to suggest, a short list, and
// a list long enough to be summarised.
func TestCandidateHint(t *testing.T) {
	if got := candidateHint("configured servers", nil); got != "" {
		t.Errorf("candidateHint(nil) = %q, want empty", got)
	}
	if got := candidateHint("configured servers", []string{"a", "b"}); got != "; configured servers: a, b" {
		t.Errorf("candidateHint = %q", got)
	}
	names := make([]string, 0, maxCandidates+5)
	for i := 0; i < maxCandidates+5; i++ {
		names = append(names, fmt.Sprintf("s%02d", i))
	}
	got := candidateHint("configured servers", names)
	if !strings.HasSuffix(got, ", +5 more") {
		t.Errorf("candidateHint = %q, want a summary of the remainder", got)
	}
	if strings.Contains(got, names[maxCandidates]) {
		t.Errorf("candidateHint = %q, want the list capped at %d names", got, maxCandidates)
	}
}

// TestWithHintEmptyKeepsError pins that an empty hint returns the error itself.
func TestWithHintEmptyKeepsError(t *testing.T) {
	err := errors.New("boom")
	if got := withHint(err, ""); got != err {
		t.Errorf("withHint(err, \"\") = %v, want the original error", got)
	}
	if got := (hintedError{err: err, hint: "; x"}).Unwrap(); got != err {
		t.Errorf("Unwrap = %v, want the wrapped error", got)
	}
}
