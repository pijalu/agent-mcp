package cli_test

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/pijalu/agent-mcp/internal/cli"
)

// erroringManager reports a tool-level MCP failure (isError) for every call.
type erroringManager struct{ trackingManager }

func (m *erroringManager) CallTool(context.Context, string, *mcp.CallToolParams) (*mcp.CallToolResult, error) {
	return &mcp.CallToolResult{
		IsError: true,
		Content: []mcp.Content{&mcp.TextContent{Text: "boom: bad arguments"}},
	}, nil
}

var _ cli.ClosableManager = (*erroringManager)(nil)

// TestOneShotToolErrorExitsNonZero covers the trap where a tool-reported
// failure exited 0 with the failure buried inside .data.
func TestOneShotToolErrorExitsNonZero(t *testing.T) {
	cfgPath := writeConfig(t, buildFixture(t))
	mgr := &erroringManager{}
	deps := cli.Deps{NewManager: func(*configType) cli.ClosableManager { return mgr }}
	var stdout, stderr bytes.Buffer
	args := []string{"call-tool", "--server", "fx", "--tool", "echo",
		"--no-server", "--config", cfgPath}
	code := cli.RunWith(deps, args, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("exit = %d, want 1 (out %s)", code, stdout.String())
	}
	out := stdout.String()
	if strings.Contains(out, `"data"`) {
		t.Errorf("a failure must not carry .data: %s", out)
	}
	errObj, ok := envelopeOf(t, out)["error"].(map[string]any)
	if !ok {
		t.Fatalf("missing error object: %s", out)
	}
	if errObj["code"] != "mcp" {
		t.Errorf("code = %v, want mcp", errObj["code"])
	}
	if msg, _ := errObj["message"].(string); !strings.Contains(msg, "boom") {
		t.Errorf("message %q must carry the server text", msg)
	}
}

// TestOneShotUnknownServerIsUsage covers bad parameters reported as transient
// MCP failures with the internal layering exposed.
func TestOneShotUnknownServerIsUsage(t *testing.T) {
	cfgPath := writeConfig(t, buildFixture(t))
	var stdout, stderr bytes.Buffer
	args := []string{"list-tools", "--server", "nosuch", "--no-server", "--config", cfgPath}
	code := cli.RunWith(cli.Deps{}, args, &stdout, &stderr)
	if code != 2 {
		t.Fatalf("exit = %d, want 2 (out %s)", code, stdout.String())
	}
	out := stdout.String()
	if strings.Contains(out, "rpc error") || strings.Contains(out, "session:") {
		t.Errorf("internal layering leaked: %s", out)
	}
	if !strings.Contains(out, "unknown server") {
		t.Errorf("out: %s", out)
	}
}

// TestMissingServerNamesCommand covers the literal "command requires --server"
// text that made the error uncorrectable without re-reading --help.
func TestMissingServerNamesCommand(t *testing.T) {
	for _, cmd := range []string{"list-tools", "list-resources", "list-prompts"} {
		t.Run(cmd, func(t *testing.T) {
			code, out := run(t, cmd, "--no-server")
			if code != 2 {
				t.Fatalf("exit = %d, want 2", code)
			}
			if !strings.Contains(out, cmd+" requires --server") {
				t.Errorf("out: %s", out)
			}
		})
	}
}
