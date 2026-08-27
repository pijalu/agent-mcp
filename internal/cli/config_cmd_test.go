package cli

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"strings"
	"testing"

	"github.com/pijalu/agent-mcp/internal/config"
)

// runConfigAdd runs `agent-mcp config add` against a temp config file.
func runConfigAdd(t *testing.T, path string, extra ...string) (int, string) {
	t.Helper()
	args := append([]string{"config", "add", "--config", path, "--format", "json"}, extra...)
	var stdout, stderr bytes.Buffer
	code := RunWith(Deps{}, args, &stdout, &stderr)
	return code, stdout.String()
}

func TestConfigAddStdioServer(t *testing.T) {
	path := t.TempDir() + "/nested/config.toml"
	code, out := runConfigAdd(t, path,
		"--name", "chrome-devtools",
		"--command", "npx",
		"--arg", "-y", "--arg", "chrome-devtools-mcp@latest",
		"--env", "LOG_LEVEL=debug",
	)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, out)
	}
	for _, want := range []string{`"name":"chrome-devtools"`, `"action":"added"`} {
		if !strings.Contains(out, want) {
			t.Errorf("output %q missing %s", out, want)
		}
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("load written config: %v", err)
	}
	srv := cfg.MCPServers["chrome-devtools"]
	if srv.Command != "npx" || srv.Transport != "stdio" {
		t.Errorf("got %+v", srv)
	}
	if got := strings.Join(srv.Args, " "); got != "-y chrome-devtools-mcp@latest" {
		t.Errorf("args = %q", got)
	}
	if srv.Env["LOG_LEVEL"] != "debug" {
		t.Errorf("env = %v", srv.Env)
	}
}

func TestConfigAddURLServer(t *testing.T) {
	path := t.TempDir() + "/config.toml"
	code, out := runConfigAdd(t, path,
		"--name", "remote",
		"--url", "https://example.com/mcp",
		"--header", "Authorization=Bearer token",
	)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, out)
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("load written config: %v", err)
	}
	srv := cfg.MCPServers["remote"]
	if srv.Transport != "streamable" {
		t.Errorf("transport = %q, want streamable default", srv.Transport)
	}
	if srv.Headers["Authorization"] != "Bearer token" {
		t.Errorf("headers = %v", srv.Headers)
	}
}

func TestConfigAddUpdatesExistingAndPreservesOthers(t *testing.T) {
	path := t.TempDir() + "/config.toml"
	if code, out := runConfigAdd(t, path, "--name", "keep", "--command", "ls"); code != 0 {
		t.Fatalf("seed exit %d: %s", code, out)
	}
	if code, out := runConfigAdd(t, path, "--name", "srv", "--command", "old"); code != 0 {
		t.Fatalf("first exit %d: %s", code, out)
	}
	code, out := runConfigAdd(t, path, "--name", "srv", "--command", "new", "--arg", "-l")
	if code != 0 {
		t.Fatalf("update exit %d: %s", code, out)
	}
	if !strings.Contains(out, `"action":"updated"`) {
		t.Errorf("output %q missing updated action", out)
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("load written config: %v", err)
	}
	if cfg.MCPServers["srv"].Command != "new" {
		t.Errorf("srv not updated: %+v", cfg.MCPServers["srv"])
	}
	if cfg.MCPServers["keep"].Command != "ls" {
		t.Errorf("keep entry lost: %+v", cfg.MCPServers)
	}
}

func TestConfigAddUsageErrors(t *testing.T) {
	cases := []struct {
		name string
		args []string
	}{
		{"missing name", []string{"--command", "ls"}},
		{"neither command nor url", []string{"--name", "x"}},
		{"both command and url", []string{"--name", "x", "--command", "ls", "--url", "https://e.com"}},
		{"bad env", []string{"--name", "x", "--command", "ls", "--env", "NOEQUALS"}},
		{"bad transport", []string{"--name", "x", "--url", "https://e.com", "--transport", "ftp"}},
		{"unknown subcommand", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := t.TempDir() + "/config.toml"
			var stdout, stderr bytes.Buffer
			args := []string{"config", "add", "--config", path}
			if tc.args == nil {
				args = []string{"config", "bogus"}
			} else {
				args = append(args, tc.args...)
			}
			if code := RunWith(Deps{}, args, &stdout, &stderr); code != 2 {
				t.Fatalf("exit %d, want 2: %s", code, stdout.String())
			}
		})
	}
}

// TestConfigAddFlagTransportMatrix pins every flag/transport combination: a
// transport-only flag is rejected on the wrong kind of entry, accepted on the
// right one, and a rejected command writes no config at all. --env is
// stdio-only, --header and --transport are remote-only, so all three fail in
// the same shape instead of two of them silently writing a config that ignores
// what the caller asked for.
func TestConfigAddFlagTransportMatrix(t *testing.T) {
	cases := []struct {
		name    string
		flags   []string
		wantErr string // "" means the entry is accepted
	}{
		{"env on stdio", []string{"--command", "ls", "--env", "A=b"}, ""},
		{"headers on remote", []string{"--url", "https://e.com/mcp", "--header", "H=v"}, ""},
		{"transport on remote", []string{"--url", "https://e.com/mcp", "--transport", "sse"}, ""},
		{"transport on stdio", []string{"--command", "ls", "--transport", "sse"}, "transport requires url"},
		{"env on remote", []string{"--url", "https://e.com/mcp", "--env", "A=b"}, "env requires a command"},
		{"headers on stdio", []string{"--command", "ls", "--header", "H=v"}, "headers require a url"},
		{"env and headers on stdio", []string{"--command", "ls", "--env", "A=b", "--header", "H=v"}, "headers require a url"},
		{"env and headers on remote", []string{"--url", "https://e.com/mcp", "--env", "A=b", "--header", "H=v"}, "env requires a command"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := t.TempDir() + "/config.toml"
			code, out := runConfigAdd(t, path, append([]string{"--name", "x"}, tc.flags...)...)
			if tc.wantErr == "" {
				wantConfigAddAccepted(t, path, tc.flags, code, out)
				return
			}
			wantConfigAddRejected(t, path, tc.wantErr, code, out)
		})
	}
}

// wantConfigAddRejected asserts a usage failure and that no config was written:
// the entry the caller asked for must not exist in a misleading half-form.
func wantConfigAddRejected(t *testing.T, path, wantErr string, code int, out string) {
	t.Helper()
	if code != 2 {
		t.Fatalf("exit %d, want 2 (usage): %s", code, out)
	}
	if !strings.Contains(out, wantErr) {
		t.Errorf("output %q missing %q", out, wantErr)
	}
	if _, err := os.Stat(path); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("rejected command wrote a config (stat err = %v)", err)
	}
}

// wantConfigAddAccepted asserts the entry exists and carries every transport
// flag the case passed.
func wantConfigAddAccepted(t *testing.T, path string, flags []string, code int, out string) {
	t.Helper()
	if code != 0 {
		t.Fatalf("exit %d: %s", code, out)
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("load written config: %v", err)
	}
	srv := cfg.MCPServers["x"]
	if srv.Command == "" && srv.URL == "" {
		t.Errorf("entry = %+v, want command or url", srv)
	}
	if containsFlag(flags, "--env") && srv.Env["A"] != "b" {
		t.Errorf("env = %v, want A=b", srv.Env)
	}
	if containsFlag(flags, "--header") && srv.Headers["H"] != "v" {
		t.Errorf("headers = %v, want H=v", srv.Headers)
	}
}

// containsFlag reports whether args carries the named flag.
func containsFlag(args []string, flag string) bool {
	for _, a := range args {
		if a == flag {
			return true
		}
	}
	return false
}
