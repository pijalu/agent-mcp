package cli_test

import (
	"bytes"
	"context"
	"io"
	"net"
	"strings"
	"testing"

	"github.com/pijalu/agent-mcp/internal/cli"
	pb "github.com/pijalu/agent-mcp/proto/agentmcp/v1"
)

// TestServerStartAPIKeys exercises repeatable --api-key parsing; listen
// fails so no daemon is left running.
func TestServerStartAPIKeys(t *testing.T) {
	cfgPath := writeConfig(t, buildFixture(t))
	var stdout, stderr bytes.Buffer
	code := cli.RunWith(cli.Deps{}, []string{"server", "start",
		"--api-key", "k1", "--api-key", "k2",
		"--listen", "999.999.0.1:1", "--config", cfgPath}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("exit = %d, want 1 (out %s)", code, stdout.String())
	}
}

// TestDaemonCallWithAPIKey covers the api-key metadata branch.
func TestDaemonCallWithAPIKey(t *testing.T) {
	addr := startFakeDaemon(t)
	code, out, _ := runWith(t, addr, "list-tools", "--server", "fake", "--api-key", "k1")
	if code != 0 {
		t.Fatalf("exit = %d, out %s", code, out)
	}
	if !strings.Contains(out, "echo") {
		t.Errorf("out: %s", out)
	}
}

// TestDaemonCallTimeoutFlag covers the explicit call-timeout branch.
func TestDaemonDiscoveryFilters(t *testing.T) {
	addr := startFakeDaemon(t)
	code, out, _ := runWith(t, addr, "list-tools", "--server", "fake", "--search", "echo", "--limit", "1")
	if code != 0 {
		t.Fatalf("exit = %d, out %s", code, out)
	}
	if !strings.Contains(out, "echo") {
		t.Errorf("out: %s", out)
	}
	code, out, _ = runWith(t, addr, "list-tools", "--server", "fake", "--search", "missing")
	if code != 0 {
		t.Fatalf("exit = %d, out %s", code, out)
	}
	if strings.Contains(out, "echo") {
		t.Errorf("filtered out: %s", out)
	}
}

func TestDaemonCallTimeoutFlag(t *testing.T) {
	addr := startFakeDaemon(t)
	code, _, _ := runWith(t, addr, "list-tools", "--server", "fake", "--call-timeout", "2s")
	if code != 0 {
		t.Fatalf("exit = %d", code)
	}
}

// deadEndDial connects to a TCP listener that never speaks gRPC, so the
// ShutdownAll RPC fails while the port keeps accepting connections.
func deadEndDial(t *testing.T) (string, cli.DialFunc) {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lis.Close() })
	go func() {
		for {
			conn, err := lis.Accept()
			if err != nil {
				return
			}
			_ = conn.Close() // accept then drop: port stays open, no gRPC
		}
	}()
	dial := func(ctx context.Context, addr string) (pb.AgentMCPClient, io.Closer, error) {
		return dialReal(ctx, addr)
	}
	return lis.Addr().String(), dial
}

// TestShutdownRefusedByNonDaemon asserts a live non-gRPC port yields exit 1.
func TestShutdownRefusedByNonDaemon(t *testing.T) {
	addr, dial := deadEndDial(t)
	deps := cli.Deps{Dial: dial}
	var stdout, stderr bytes.Buffer
	code := cli.RunWith(deps, []string{"server", "shutdown", "--connect", addr,
		"--call-timeout", "200ms"}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("exit = %d, want 1 (out %s)", code, stdout.String())
	}
	if !strings.Contains(stdout.String(), "refused shutdown") {
		t.Errorf("out: %s", stdout.String())
	}
}

// TestShutdownTextFormat covers the text rendering branch of writeResult.
func TestShutdownTextFormat(t *testing.T) {
	addr := startFakeDaemon(t)
	deps := cli.Deps{Dial: dialReal}
	var stdout, stderr bytes.Buffer
	code := cli.RunWith(deps, []string{"server", "shutdown", "--connect", addr,
		"--timeout", "3s", "--format", "text"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit = %d, out %s", code, stdout.String())
	}
	if !strings.Contains(stdout.String(), "true") {
		t.Errorf("out: %s", stdout.String())
	}
}

// TestConnectTimeoutError exercises the waitReady timeout path after a
// successful spawn hook that never starts a daemon.
func TestConnectTimeoutError(t *testing.T) {
	deps := cli.Deps{
		Dial: dialReal,
		SpawnDaemon: func(cli.GlobalFlags) (cli.DaemonChild, error) {
			return &fakeChild{}, nil // spawned, still running, never listens
		},
	}
	var stdout, stderr bytes.Buffer
	code := cli.RunWith(deps, []string{"list-tools", "--server", "fake",
		"--connect", "127.0.0.1:1", "--connect-timeout", "100ms"}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("exit = %d, want 1 (out %s)", code, stdout.String())
	}
	if !strings.Contains(stdout.String(), "not ready") {
		t.Errorf("out: %s", stdout.String())
	}
}

// TestSpawnDaemonRefusesTestBinary locks in the fork-bomb guard: the
// production SpawnDaemon must refuse to re-exec a test binary.
func TestServerStatusTextFormat(t *testing.T) {
	addr := startFakeDaemon(t)
	var stdout, stderr bytes.Buffer
	code := cli.RunWith(cli.Deps{Dial: dialReal}, []string{"server", "status", "--connect", addr, "--format", "text"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit = %d, out %s", code, stdout.String())
	}
	if !strings.Contains(stdout.String(), "fake") {
		t.Errorf("out: %s", stdout.String())
	}
}

func TestDefaultGRPCDialError(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := cli.Run([]string{"server", "status", "--connect", "127.0.0.1:1", "--call-timeout", "50ms"}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("exit = %d, want 1 (out %s)", code, stdout.String())
	}
	if !strings.Contains(stdout.String(), "status") {
		t.Errorf("out: %s", stdout.String())
	}
}

func TestSpawnDaemonRefusesTestBinary(t *testing.T) {
	deps := cli.Deps{Dial: dialReal} // SpawnDaemon = production default
	var stdout, stderr bytes.Buffer
	code := cli.RunWith(deps, []string{"list-tools", "--server", "fake",
		"--connect", "127.0.0.1:1"}, &stdout, &stderr)
	if code != 3 {
		t.Fatalf("exit = %d, want 3 (out %s)", code, stdout.String())
	}
	if !strings.Contains(stdout.String(), "refusing to auto-start") {
		t.Errorf("out: %s", stdout.String())
	}
	if !strings.Contains(stderr.String(), "auto-starting") {
		t.Errorf("stderr: %s", stderr.String())
	}
}
