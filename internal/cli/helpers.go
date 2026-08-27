package cli

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

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

// readinessPollInterval is how often waitReady re-probes the daemon address.
const readinessPollInterval = 25 * time.Millisecond

// DaemonChild is the handle to an auto-started daemon process. It lets the
// client report why a child died instead of an opaque readiness timeout.
type DaemonChild interface {
	// Exited reports the child's exit code once the process has exited.
	Exited() (int, bool)
	// Output returns everything the child wrote to stdout/stderr so far.
	Output() string
	// Cleanup releases the captured output; safe to call more than once.
	Cleanup()
}

// childProcess captures a spawned daemon's output in a temp file so a daemon
// that dies at startup can be diagnosed. A file rather than a pipe keeps the
// daemon's stdio valid after the client exits (a broken pipe on fd 1/2 would
// otherwise kill the daemon with SIGPIPE).
type childProcess struct {
	cmd       *exec.Cmd
	logPath   string
	cleanOnce sync.Once

	mu     sync.Mutex
	code   int
	exited bool
}

func (c *childProcess) Exited() (int, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.code, c.exited
}

// Output reads the captured stdout/stderr written so far.
func (c *childProcess) Output() string {
	raw, err := os.ReadFile(c.logPath)
	if err != nil {
		return ""
	}
	return string(raw)
}

// Cleanup unlinks the capture file. A still-running daemon keeps its file
// descriptor, so later writes are discarded rather than fatal.
func (c *childProcess) Cleanup() {
	c.cleanOnce.Do(func() { _ = os.Remove(c.logPath) })
}

// watch reaps the child and records its exit code for Exited.
func (c *childProcess) watch() {
	err := c.cmd.Wait()
	c.mu.Lock()
	c.code, c.exited = exitCode(err), true
	c.mu.Unlock()
}

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
// handle once the child is spawned (readiness is polled by the caller).
func spawnDaemon(g GlobalFlags) (DaemonChild, error) {
	self, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("cli: locate executable: %w", err)
	}
	// Guard: re-executing a test binary re-runs the whole suite per spawn
	// (fork bomb). Tests must inject Deps.SpawnDaemon instead.
	if strings.HasSuffix(self, ".test") || strings.HasSuffix(self, ".test.exe") {
		return nil, fmt.Errorf("cli: refusing to auto-start daemon from test binary %q", self)
	}
	args := []string{"server", "start", "--server-idle-time", "5m"}
	if g.config != "" {
		args = append(args, "--config", g.config)
	}
	if g.connect != "" {
		args = append(args, "--listen", g.connect)
	}
	if g.apiKey != "" {
		args = append(args, "--api-key", g.apiKey)
	}
	log, err := os.CreateTemp("", "agent-mcp-daemon-*.log")
	if err != nil {
		return nil, fmt.Errorf("cli: capture daemon output: %w", err)
	}
	cmd := exec.Command(self, args...)
	// Detach: the daemon shares no terminal. Its stdout/stderr go to the temp
	// file so a daemon that dies during startup can report its real error.
	cmd.Stdout = log
	cmd.Stderr = log
	cmd.Stdin = nil
	cmd.SysProcAttr = daemonProcAttr()
	if err := cmd.Start(); err != nil {
		_ = log.Close()
		_ = os.Remove(log.Name())
		return nil, fmt.Errorf("cli: spawn daemon: %w", err)
	}
	_ = log.Close() // the child holds its own descriptor
	child := &childProcess{cmd: cmd, logPath: log.Name()}
	go child.watch()
	return child, nil
}
