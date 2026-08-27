package cli_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/pijalu/agent-mcp/internal/cli"
	"github.com/pijalu/agent-mcp/internal/config"
	"github.com/pijalu/agent-mcp/internal/session"
)

// configType aliases config.Config for factory signatures.
type configType = config.Config

func insecureCreds() credentials.TransportCredentials { return insecure.NewCredentials() }

var (
	fixtureOnce sync.Once
	fixturePath string
)

// buildFixture builds the fixture MCP server binary once per test run.
func buildFixture(t *testing.T) string {
	t.Helper()
	fixtureOnce.Do(func() {
		root := moduleRoot(t)
		dir, err := os.MkdirTemp("", "agent-mcp-fixture")
		if err != nil {
			t.Fatalf("mktemp: %v", err)
		}
		out := filepath.Join(dir, "fixture-server")
		cmd := exec.Command("go", "build", "-o", out, "./testdata/fixture-server")
		cmd.Dir = root
		if b, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("build fixture: %v\n%s", err, b)
		}
		fixturePath = out
	})
	return fixturePath
}

// moduleRoot walks up to the directory containing go.mod.
func moduleRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for dir := wd; ; dir = filepath.Dir(dir) {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		if filepath.Dir(dir) == dir {
			t.Fatal("go.mod not found")
		}
	}
}

// writeConfig writes a config.toml pointing server "fx" at bin.
func writeConfig(t *testing.T, bin string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.toml")
	body := "[mcpServers.fx]\ncommand = \"" + bin + "\"\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// trackingManager wraps fakeManager and counts CloseAll calls.
type trackingManager struct {
	fake       fakeManager
	closeCalls int
}

func (m *trackingManager) CloseAll() int {
	m.closeCalls++
	return 0
}

func (m *trackingManager) ServerStatuses() []session.ServerInfo { return m.fake.ServerStatuses() }

func (m *trackingManager) ListTools(ctx context.Context, s string, p *mcp.ListToolsParams) (*mcp.ListToolsResult, error) {
	return m.fake.ListTools(ctx, s, p)
}

func (m *trackingManager) CallTool(ctx context.Context, s string, p *mcp.CallToolParams) (*mcp.CallToolResult, error) {
	return m.fake.CallTool(ctx, s, p)
}

func (m *trackingManager) ListResources(ctx context.Context, s string, p *mcp.ListResourcesParams) (*mcp.ListResourcesResult, error) {
	return m.fake.ListResources(ctx, s, p)
}

func (m *trackingManager) ReadResource(ctx context.Context, s string, p *mcp.ReadResourceParams) (*mcp.ReadResourceResult, error) {
	return m.fake.ReadResource(ctx, s, p)
}

func (m *trackingManager) ListPrompts(ctx context.Context, s string, p *mcp.ListPromptsParams) (*mcp.ListPromptsResult, error) {
	return m.fake.ListPrompts(ctx, s, p)
}

func (m *trackingManager) GetPrompt(ctx context.Context, s string, p *mcp.GetPromptParams) (*mcp.GetPromptResult, error) {
	return m.fake.GetPrompt(ctx, s, p)
}

var _ cli.ClosableManager = (*trackingManager)(nil)
