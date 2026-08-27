package cli_test

import (
	"bytes"
	"errors"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pijalu/agent-mcp/internal/cli"
	"github.com/pijalu/agent-mcp/internal/config"
	"github.com/pijalu/agent-mcp/internal/daemon"
)

// fakeChild stands in for a spawned daemon process whose output and exit code
// are captured by the production spawnDaemon.
type fakeChild struct {
	code    int
	exited  bool
	out     string
	cleaned int
}

func (c *fakeChild) Exited() (int, bool) { return c.code, c.exited }

func (c *fakeChild) Output() string { return c.out }

func (c *fakeChild) Cleanup() { c.cleaned++ }

// spawnFake returns a SpawnDaemon hook that hands out child and fails the test
// when the client spawns more than once.
func spawnFake(t *testing.T, child cli.DaemonChild) (func(cli.GlobalFlags) (cli.DaemonChild, error), *int) {
	t.Helper()
	calls := new(int)
	return func(cli.GlobalFlags) (cli.DaemonChild, error) {
		*calls++
		return child, nil
	}, calls
}

// TestDaemonStartupFailureReportsChildError covers the trap where the daemon's
// stdio was discarded, so any startup death surfaced as an opaque
// "daemon at ADDR not ready within 5s" after a full 5s stall.
func TestDaemonStartupFailureReportsChildError(t *testing.T) {
	tests := []struct {
		name     string
		child    *fakeChild
		wantCode int
		wantKind string
	}{
		{
			name:     "config load failure keeps the usage class",
			child:    &fakeChild{exited: true, code: 2, out: "usage error: config: load /nope/config.toml: no such file"},
			wantCode: 2,
			wantKind: "usage",
		},
		{
			name:     "internal child failure",
			child:    &fakeChild{exited: true, code: 3, out: "internal error: boom"},
			wantCode: 3,
			wantKind: "internal",
		},
		{
			name:     "mcp child failure",
			child:    &fakeChild{exited: true, code: 1, out: "mcp error: listen denied"},
			wantCode: 1,
			wantKind: "mcp",
		},
		{
			name:     "silent child failure",
			child:    &fakeChild{exited: true, code: 0},
			wantCode: 1,
			wantKind: "mcp",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			spawn, calls := spawnFake(t, tc.child)
			deps := cli.Deps{Dial: dialReal, SpawnDaemon: spawn}
			var stdout, stderr bytes.Buffer
			start := time.Now()
			code := cli.RunWith(deps, []string{"list-tools", "--server", "fake",
				"--connect", "127.0.0.1:1"}, &stdout, &stderr)
			assertStartupFailure(t, startupFailure{
				child:    tc.child,
				wantCode: tc.wantCode,
				wantKind: tc.wantKind,
				code:     code,
				out:      stdout.String(),
				elapsed:  time.Since(start),
				calls:    *calls,
			})
		})
	}
}

// startupFailure is one observed auto-start failure for assertStartupFailure.
type startupFailure struct {
	child    *fakeChild
	wantCode int
	wantKind string
	code     int
	out      string
	elapsed  time.Duration
	calls    int
}

// assertStartupFailure checks that a dead child was reported with its own error
// and error class, fast and without the opaque readiness timeout.
func assertStartupFailure(t *testing.T, got startupFailure) {
	t.Helper()
	if got.code != got.wantCode {
		t.Fatalf("exit = %d, want %d (out %s)", got.code, got.wantCode, got.out)
	}
	if got.calls != 1 {
		t.Errorf("SpawnDaemon calls = %d, want 1", got.calls)
	}
	// Fast failure: the child's death is decisive, so the client must not burn
	// the 5s readiness budget.
	if got.elapsed > 2*time.Second {
		t.Errorf("took %s, want a fast failure instead of the readiness timeout", got.elapsed)
	}
	if strings.Contains(got.out, "not ready within") {
		t.Errorf("opaque readiness timeout leaked: %s", got.out)
	}
	if !strings.Contains(got.out, got.wantKind) {
		t.Errorf("out %q missing kind %q", got.out, got.wantKind)
	}
	if got.child.out != "" && !strings.Contains(got.out, strings.TrimSpace(got.child.out)) {
		t.Errorf("out %q missing the child's own error", got.out)
	}
	if got.child.out == "" && !strings.Contains(got.out, "(no output)") {
		t.Errorf("out %q must say the child was silent", got.out)
	}
	if got.child.cleaned != 1 {
		t.Errorf("Cleanup calls = %d, want 1", got.child.cleaned)
	}
}

// cliBinary builds the real agent-mcp binary once per test run so the auto-start
// path can be exercised end to end (the fork-bomb guard refuses test binaries).
var (
	cliBinaryOnce sync.Once
	cliBinaryPath string
)

func buildCLI(t *testing.T) string {
	t.Helper()
	cliBinaryOnce.Do(func() {
		dir, err := os.MkdirTemp("", "agent-mcp-cli")
		if err != nil {
			t.Fatalf("mktemp: %v", err)
		}
		out := filepath.Join(dir, "agent-mcp")
		cmd := exec.Command("go", "build", "-o", out, "./cmd/agent-mcp")
		cmd.Dir = moduleRoot(t)
		if b, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("build cli: %v\n%s", err, b)
		}
		cliBinaryPath = out
	})
	return cliBinaryPath
}

// TestAutoStartedDaemonMissingConfigEndToEnd proves the real end-to-end path:
// the auto-started daemon cannot load its config, and the client reports that
// config error with exit 2 instead of an opaque readiness timeout.
func TestAutoStartedDaemonMissingConfigEndToEnd(t *testing.T) {
	bin := buildCLI(t)
	missing := filepath.Join(t.TempDir(), "absent-config.toml")
	cmd := exec.Command(bin, "list-tools", "--server", "fx",
		"--connect", "127.0.0.1:1", "--config", missing)
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	start := time.Now()
	err := cmd.Run()
	elapsed := time.Since(start)
	out := buf.String()
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("client error = %v (out %s)", err, out)
	}
	if code := exitErr.ExitCode(); code != 2 {
		t.Fatalf("exit = %d, want 2 (out %s)", code, out)
	}
	if elapsed > 4*time.Second {
		t.Errorf("took %s; the child's death must fail fast", elapsed)
	}
	if !strings.Contains(out, missing) {
		t.Errorf("out %q must name the missing config path", out)
	}
	if !strings.Contains(out, "config") {
		t.Errorf("out %q must carry the child's config-load error", out)
	}
	if strings.Contains(out, "not ready within") {
		t.Errorf("opaque readiness timeout leaked: %s", out)
	}
}

// startFakeDaemonWithConfig runs a real gRPC daemon that reports configPath in
// its Status response.
func startFakeDaemonWithConfig(t *testing.T, configPath string) string {
	t.Helper()
	d := daemon.New(fakeManager{}, daemon.Options{ConfigPath: configPath})
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	go func() { _ = d.Serve(lis) }()
	t.Cleanup(func() { d.Shutdown() })
	return lis.Addr().String()
}

// TestClientConfigMismatchRejected covers the trap where --config was silently
// ignored whenever a daemon already listened, so the client received another
// config's servers with no warning.
func TestClientConfigMismatchRejected(t *testing.T) {
	daemonCfg := filepath.Join(t.TempDir(), "daemon.toml")
	clientCfg := filepath.Join(t.TempDir(), "client.toml")
	addr := startFakeDaemonWithConfig(t, daemonCfg)
	spawn, calls := spawnFake(t, &fakeChild{})
	deps := cli.Deps{Dial: dialReal, SpawnDaemon: spawn}
	var stdout, stderr bytes.Buffer
	code := cli.RunWith(deps, []string{"list-tools", "--server", "fake", "--connect", addr, "--config", clientCfg}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("exit = %d, want 1 (out %s)", code, stdout.String())
	}
	if *calls != 0 {
		t.Errorf("SpawnDaemon calls = %d, want 0: a reachable daemon must not be duplicated", *calls)
	}
	out := stdout.String()
	if !strings.Contains(out, "config mismatch") {
		t.Errorf("out %q must name the problem", out)
	}
	if !strings.Contains(out, daemonCfg) || !strings.Contains(out, clientCfg) {
		t.Errorf("out %q must name both config paths (%s, %s)", out, daemonCfg, clientCfg)
	}
}

// TestClientConfigMatchAccepted is the negative control: a matching --config
// must keep working.
func TestClientConfigMatchAccepted(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "config.toml")
	addr := startFakeDaemonWithConfig(t, cfgPath)
	spawn, _ := spawnFake(t, &fakeChild{})
	deps := cli.Deps{Dial: dialReal, SpawnDaemon: spawn}
	var stdout, stderr bytes.Buffer
	code := cli.RunWith(deps, []string{"list-tools", "--server", "fake", "--connect", addr, "--config", cfgPath}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (out %s)", code, stdout.String())
	}
}

// TestClientDefaultConfigMatchesResolvedDefault proves both sides resolve the
// unflagged --config to the same default file.
func TestClientDefaultConfigMatchesResolvedDefault(t *testing.T) {
	def := config.ResolvePath("")
	if abs, err := filepath.Abs(def); err == nil {
		def = abs
	}
	addr := startFakeDaemonWithConfig(t, def)
	spawn, _ := spawnFake(t, &fakeChild{})
	deps := cli.Deps{Dial: dialReal, SpawnDaemon: spawn}
	var stdout, stderr bytes.Buffer
	code := cli.RunWith(deps, []string{"list-tools", "--server", "fake", "--connect", addr}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (out %s)", code, stdout.String())
	}
}

// TestClientAcceptsDaemonWithoutConfigPath keeps older daemons (no ConfigPath
// in Status) working.
func TestClientAcceptsDaemonWithoutConfigPath(t *testing.T) {
	addr := startFakeDaemon(t)
	spawn, _ := spawnFake(t, &fakeChild{})
	deps := cli.Deps{Dial: dialReal, SpawnDaemon: spawn}
	var stdout, stderr bytes.Buffer
	code := cli.RunWith(deps, []string{"list-tools", "--server", "fake", "--connect", addr,
		"--config", filepath.Join(t.TempDir(), "other.toml")}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (out %s)", code, stdout.String())
	}
}

// TestServerStatusShowsConfigPath gives operators a way to see which config a
// running daemon serves.
func TestServerStatusShowsConfigPath(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "served.toml")
	addr := startFakeDaemonWithConfig(t, cfgPath)
	code, out, _ := runWith2(t, addr, "server", "status", "--connect", addr)
	if code != 0 {
		t.Fatalf("exit = %d, out %s", code, out)
	}
	if !strings.Contains(out, cfgPath) {
		t.Errorf("out %q must report the served config path", out)
	}
}
