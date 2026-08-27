package config

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/BurntSushi/toml"
)

// 1.1 default path is ~/.agent-mcp/config.toml
func TestDefaultPath(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home dir")
	}
	want := filepath.Join(home, ".agent-mcp", "config.toml")
	if got := DefaultPath(); got != want {
		t.Fatalf("DefaultPath() = %q, want %q", got, want)
	}
}

const validTOML = `
[mcpServers.fs]
command = "npx"
args = ["-y", "@modelcontextprotocol/server-filesystem", "/tmp"]
env = { API_KEY = "secret" }

[mcpServers.remote]
url = "https://example.com/sse"
transport = "sse"
headers = { Authorization = "Bearer tok" }
`

func writeFixture(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// 1.3 parse stdio + remote entries from TOML fixture
func TestLoadParsesServers(t *testing.T) {
	cfg, err := Load(writeFixture(t, validTOML))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.MCPServers) != 2 {
		t.Fatalf("got %d servers, want 2", len(cfg.MCPServers))
	}
	fs := cfg.MCPServers["fs"]
	if fs.Command != "npx" {
		t.Errorf("fs.Command = %q", fs.Command)
	}
	if len(fs.Args) != 3 || fs.Args[0] != "-y" {
		t.Errorf("fs.Args = %v", fs.Args)
	}
	if fs.Env["API_KEY"] != "secret" {
		t.Errorf("fs.Env = %v", fs.Env)
	}
	if fs.Transport != "stdio" {
		t.Errorf("fs.Transport = %q, want default stdio", fs.Transport)
	}
	remote := cfg.MCPServers["remote"]
	if remote.URL != "https://example.com/sse" {
		t.Errorf("remote.URL = %q", remote.URL)
	}
	if remote.Transport != "sse" {
		t.Errorf("remote.Transport = %q", remote.Transport)
	}
	if remote.Headers["Authorization"] != "Bearer tok" {
		t.Errorf("remote.Headers = %v", remote.Headers)
	}
}

// 1.5 validation errors table test
func TestValidate(t *testing.T) {
	cases := []struct {
		name    string
		server  ServerConfig
		wantErr bool
	}{
		{"stdio ok", ServerConfig{Command: "npx"}, false},
		{"stdio env ok", ServerConfig{Command: "npx", Env: map[string]string{"API_KEY": "x"}}, false},
		{"sse ok", ServerConfig{URL: "https://x/sse", Transport: "sse"}, false},
		{"streamable ok", ServerConfig{URL: "https://x/mcp", Transport: "streamable"}, false},
		{"remote headers ok", ServerConfig{URL: "https://x/mcp", Headers: map[string]string{"H": "v"}}, false},
		{"both command and url", ServerConfig{Command: "npx", URL: "https://x"}, true},
		{"neither command nor url", ServerConfig{}, true},
		{"invalid transport", ServerConfig{URL: "https://x", Transport: "grpc"}, true},
		{"transport without url", ServerConfig{Command: "npx", Transport: "sse"}, true},
		{"env without command", ServerConfig{URL: "https://x/mcp", Env: map[string]string{"API_KEY": "x"}}, true},
		{"headers without url", ServerConfig{Command: "npx", Headers: map[string]string{"H": "v"}}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.server.Validate()
			if (err != nil) != tc.wantErr {
				t.Fatalf("Validate() err = %v, wantErr %v", err, tc.wantErr)
			}
		})
	}
}

// 1.5b Load rejects invalid server entries, naming the server
func TestLoadValidates(t *testing.T) {
	p := writeFixture(t, "[mcpServers.bad]\n")
	_, err := Load(p)
	if err == nil {
		t.Fatal("Load: expected validation error")
	}
	if !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("err = %v, want ErrInvalidConfig", err)
	}
}

// 1.7 ResolvePath: flag overrides default; empty flag uses default
func TestResolvePath(t *testing.T) {
	if got := ResolvePath("/custom/cfg.toml"); got != "/custom/cfg.toml" {
		t.Errorf("ResolvePath(flag) = %q", got)
	}
	if got := ResolvePath(""); got != DefaultPath() {
		t.Errorf("ResolvePath(empty) = %q, want default", got)
	}
}

// 1.7 missing file → clear error; malformed TOML → wrapped error
func TestLoadMissingFile(t *testing.T) {
	_, err := Load(filepath.Join(t.TempDir(), "nope.toml"))
	if err == nil {
		t.Fatal("expected error for missing file")
	}
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("err = %v, want os.ErrNotExist", err)
	}
}

func TestLoadMalformedTOML(t *testing.T) {
	p := writeFixture(t, "[mcpServers.broken\nkey = = value\n")
	_, err := Load(p)
	if err == nil {
		t.Fatal("expected error for malformed TOML")
	}
	var perr toml.ParseError
	if !errors.As(err, &perr) {
		t.Fatalf("err = %v (%T), want toml.ParseError (line info)", err, err)
	}
}

func TestSaveRoundTrip(t *testing.T) {
	p := filepath.Join(t.TempDir(), "nested", "config.toml")
	cfg := &Config{
		APIKeys: []string{"key-1"},
		MCPServers: map[string]ServerConfig{
			"stdio":  {Command: "npx", Args: []string{"-y", "pkg"}, Env: map[string]string{"A": "b"}},
			"remote": {URL: "https://example.com/mcp", Transport: "sse", Headers: map[string]string{"H": "v"}},
		},
	}
	if err := Save(p, cfg); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := Load(p)
	if err != nil {
		t.Fatalf("Load after Save: %v", err)
	}
	if got.APIKeys[0] != "key-1" {
		t.Errorf("api keys = %v", got.APIKeys)
	}
	if got.MCPServers["stdio"].Transport != "stdio" {
		t.Errorf("stdio transport = %q, want stdio default", got.MCPServers["stdio"].Transport)
	}
	if got.MCPServers["remote"].Headers["H"] != "v" {
		t.Errorf("remote = %+v", got.MCPServers["remote"])
	}
}

func TestSaveRejectsInvalidEntry(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.toml")
	cfg := &Config{MCPServers: map[string]ServerConfig{"bad": {}}}
	if err := Save(p, cfg); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("err = %v, want ErrInvalidConfig", err)
	}
	if _, statErr := os.Stat(p); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("file must not be written on validation failure")
	}
}

func TestLoadOrEmpty(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "nope.toml")
	cfg, err := LoadOrEmpty(missing)
	if err != nil {
		t.Fatalf("LoadOrEmpty missing: %v", err)
	}
	if cfg.MCPServers == nil {
		t.Fatal("MCPServers must be non-nil for missing file")
	}

	p := writeFixture(t, "[mcpServers.s]\ncommand = \"ls\"\n")
	cfg, err = LoadOrEmpty(p)
	if err != nil {
		t.Fatalf("LoadOrEmpty existing: %v", err)
	}
	if cfg.MCPServers["s"].Command != "ls" {
		t.Errorf("got %+v", cfg.MCPServers)
	}

	bad := writeFixture(t, "[mcpServers.broken\n")
	if _, err = LoadOrEmpty(bad); err == nil {
		t.Fatal("expected error for malformed TOML")
	}
}
