package cli_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
)

var chromeCLIBuild struct {
	sync.Once
	path string
	err  error
}

// TestChromeDevToolsGoogle exercises the real Chrome DevTools MCP server.
//
// This test is opt-in because it requires Node.js, npx, Chrome, and network
// access. Set AGENT_MCP_CHROME_E2E=1 to run it.
func TestChromeDevToolsGoogle(t *testing.T) {
	if os.Getenv("AGENT_MCP_CHROME_E2E") != "1" {
		t.Skip("set AGENT_MCP_CHROME_E2E=1 to run Chrome DevTools MCP E2E test")
	}

	cliPath := buildChromeCLI(t)
	configPath := filepath.Join(t.TempDir(), "config.toml")
	config := "[mcpServers.chrome]\n" +
		"command = \"npx\"\n" +
		"args = [\"-y\", \"chrome-devtools-mcp@latest\"]\n"
	if err := os.WriteFile(configPath, []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	addr := freeAddress(t)
	defer func() {
		_, _, _ = runChromeCLI(cliPath, "server", "shutdown", "--connect", addr,
			"--config", configPath, "--timeout", "10s")
	}()

	code, out, errOut := runChromeCLI(cliPath, "call-tool", "--server", "chrome",
		"--tool", "new_page", "--args", `{"url":"https://www.google.com"}`,
		"--connect", addr, "--config", configPath, "--call-timeout", "60s")
	if code != 0 {
		t.Fatalf("new_page exit=%d out=%s err=%s", code, out, errOut)
	}
	pageID, ok := pageIDFromNewPage(out)
	if !ok {
		t.Fatalf("new_page did not return a Google page ID: %s", out)
	}

	code, out, errOut = runChromeCLI(cliPath, "call-tool", "--server", "chrome",
		"--tool", "take_snapshot", "--args", fmt.Sprintf(`{"pageId":%d}`, pageID),
		"--connect", addr, "--config", configPath, "--call-timeout", "60s")
	if code != 0 {
		t.Fatalf("take_snapshot exit=%d out=%s err=%s", code, out, errOut)
	}
	if !hasGoogleSnapshotContent(out) {
		t.Fatalf("Google DOM snapshot has no meaningful content: %s", out)
	}
}

func buildChromeCLI(t *testing.T) string {
	t.Helper()
	chromeCLIBuild.Do(func() {
		dir, err := os.MkdirTemp("", "agent-mcp-chrome-e2e")
		if err != nil {
			chromeCLIBuild.err = err
			return
		}
		chromeCLIBuild.path = filepath.Join(dir, "agent-mcp")
		cmd := exec.Command("go", "build", "-o", chromeCLIBuild.path, "./cmd/agent-mcp")
		cmd.Dir = moduleRoot(t)
		output, err := cmd.CombinedOutput()
		if err != nil {
			chromeCLIBuild.err = fmt.Errorf("build CLI: %w\n%s", err, output)
		}
	})
	if chromeCLIBuild.err != nil {
		t.Fatal(chromeCLIBuild.err)
	}
	return chromeCLIBuild.path
}

func runChromeCLI(path string, args ...string) (int, string, string) {
	cmd := exec.Command(path, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err == nil {
		return 0, stdout.String(), stderr.String()
	}
	if exitErr, ok := err.(*exec.ExitError); ok {
		return exitErr.ExitCode(), stdout.String(), stderr.String()
	}
	return -1, stdout.String(), stderr.String()
}

func freeAddress(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	return addr
}

func pageIDFromNewPage(output string) (int, bool) {
	var envelope struct {
		Data struct {
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
		} `json:"data"`
	}
	if json.Unmarshal([]byte(output), &envelope) != nil {
		return 0, false
	}
	var text strings.Builder
	for _, item := range envelope.Data.Content {
		text.WriteString(item.Text)
	}
	match := regexp.MustCompile(`(?mi)^(\d+): .*google`).FindStringSubmatch(text.String())
	if len(match) != 2 {
		return 0, false
	}
	id, err := strconv.Atoi(match[1])
	return id, err == nil
}

func hasGoogleSnapshotContent(output string) bool {
	var envelope struct {
		Data any `json:"data"`
	}
	if json.Unmarshal([]byte(output), &envelope) != nil || envelope.Data == nil {
		return false
	}
	encoded, err := json.Marshal(envelope.Data)
	if err != nil || strings.TrimSpace(string(encoded)) == "null" || len(encoded) <= 2 {
		return false
	}
	return strings.Contains(strings.ToLower(string(encoded)), "google")
}
