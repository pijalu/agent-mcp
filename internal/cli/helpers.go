package cli

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os/exec"
	"time"

	"github.com/pijalu/agent-mcp/client"
	"github.com/pijalu/agent-mcp/internal/output"
	"google.golang.org/grpc/metadata"
)

// statusCtx returns a context for daemon dials; gRPC dials lazily.
func statusCtx() context.Context {
	return context.Background()
}

// timeoutCtx builds an RPC context with timeout and optional API key.
func timeoutCtx(timeout time.Duration, apiKey string) (context.Context, context.CancelFunc) {
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	if apiKey != "" {
		ctx = metadata.AppendToOutgoingContext(ctx, "x-agent-mcp-api-key", apiKey)
	}
	return ctx, cancel
}

// quickProbe reports nil when addr accepts a TCP connection right now.
func quickProbe(addr string) error {
	conn, err := net.DialTimeout("tcp", addr, 500*time.Millisecond)
	if err != nil {
		return err
	}
	return conn.Close()
}

// waitForPort blocks until addr stops accepting connections or timeout.
func waitForPort(addr string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if quickProbe(addr) != nil {
			return nil
		}
		time.Sleep(25 * time.Millisecond)
	}
	return fmt.Errorf("%w: daemon at %s still running after %s", output.ErrMCP, addr, timeout)
}

// DaemonChild is the handle to an auto-started daemon process. It lets the
// client report why a child died instead of an opaque readiness timeout.
type DaemonChild = client.DaemonChild

// exitCode maps a Wait error onto a process exit code; -1 when the child never
// ran to completion (signalled or unstarted).
func exitCode(err error) int {
	if err == nil {
		return 0
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode()
	}
	return -1
}

// spawnDaemon re-executes this binary as a detached daemon and returns a
// handle once the child is spawned (readiness is polled by the caller). The
// spawn mechanics — test-binary guard, detached process group, captured
// output — live in the client library (client.SpawnDaemon).
func spawnDaemon(g GlobalFlags) (DaemonChild, error) {
	return client.SpawnDaemon(client.Options{
		ConfigPath:  g.config,
		ConnectAddr: g.connect,
		APIKey:      g.apiKey,
	})
}
