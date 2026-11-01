// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"

	"github.com/pijalu/agent-mcp/internal/config"
	"github.com/pijalu/agent-mcp/internal/daemon"
	pb "github.com/pijalu/agent-mcp/proto/agentmcp/v1"
)

// DefaultConnectTimeout is the single source for the connect/readiness
// timeout: the CLI's --connect-timeout default and the runtime lookup both
// use it.
const DefaultConnectTimeout = 5 * time.Second

// DefaultConnectAddr is where the daemon listens when --connect is unset.
const DefaultConnectAddr = "127.0.0.1:7474"

// ReadinessPollInterval is how often WaitReady re-probes the daemon address.
const ReadinessPollInterval = 25 * time.Millisecond

// MaxChildDetail caps how much of a child's output is echoed back.
const MaxChildDetail = 2000

// errDaemonUnauthenticated marks a daemon that refused the client's API key.
var errDaemonUnauthenticated = errors.New("daemon rejected the API key")

// GRPCDial dials addr and returns the client plus the connection. The call
// options raise gRPC's 4 MiB message ceiling to daemon.MaxMessageBytes so
// large MCP results (screenshots, docs, renders) reach the caller intact.
func GRPCDial(_ context.Context, addr string) (pb.AgentMCPClient, io.Closer, error) {
	conn, err := grpc.NewClient(addr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithDefaultCallOptions(daemon.ClientCallOptions()...),
	)
	if err != nil {
		return nil, nil, fmt.Errorf("cli: dial %s: %v", addr, err)
	}
	return pb.NewAgentMCPClient(conn), conn, nil
}

// Probe reports nil when addr accepts a TCP connection within timeout.
func Probe(addr string, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	var d net.Dialer
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return err
	}
	return conn.Close()
}

// ProbeDial probes or opens a daemon connection; faked in tests.
type ProbeDial func(ctx context.Context, addr string) (pb.AgentMCPClient, io.Closer, error)

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

// WaitReady polls addr until the daemon accepts connections or the connect
// timeout expires. A child that exits first fails fast with its own error and
// exit code instead of stalling until the timeout.
func WaitReady(addr string, child DaemonChild, connectTimeout time.Duration) error {
	deadline := time.Now().Add(connectTimeout)
	for time.Now().Before(deadline) {
		if Probe(addr, connectTimeout) == nil {
			return nil
		}
		if code, exited := childStatus(child); exited {
			return DaemonStartupError(addr, code, child.Output())
		}
		time.Sleep(ReadinessPollInterval)
	}
	if code, exited := childStatus(child); exited {
		return DaemonStartupError(addr, code, child.Output())
	}
	return fmt.Errorf("%w: daemon at %s not ready within %s", ErrMCP, addr, connectTimeout)
}

// childStatus reports the child's exit code when the child has already exited.
func childStatus(child DaemonChild) (int, bool) {
	if child == nil {
		return 0, false
	}
	return child.Exited()
}

// DaemonStartupError reports an auto-started daemon that exited during
// startup. It carries the child's own error and maps its exit code onto the
// client's error kind, so a config-load failure surfaces as the config error
// with exit 2 rather than an opaque readiness timeout.
func DaemonStartupError(addr string, code int, childOut string) error {
	kind := ErrMCP
	switch code {
	case 2:
		kind = ErrUsage
	case 3:
		kind = ErrInternal
	}
	return fmt.Errorf("%w: auto-started daemon at %s exited with code %d during startup: %s",
		kind, addr, code, ChildDetail(childOut))
}

// ChildDetail turns captured child output into a one-line diagnostic: the
// message of its JSON error envelope is unwrapped so the agent sees the cause
// without a nested envelope inside the envelope.
func ChildDetail(raw string) string {
	parts := make([]string, 0, 2)
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if msg := envelopeMessage(line); msg != "" {
			parts = append(parts, msg)
			continue
		}
		parts = append(parts, line)
	}
	if len(parts) == 0 {
		return "(no output)"
	}
	detail := strings.Join(parts, "; ")
	if len(detail) > MaxChildDetail {
		detail = detail[:MaxChildDetail] + "..."
	}
	return detail
}

// envelopeMessage returns the error message of a JSON error envelope, or ""
// when line is not one.
func envelopeMessage(line string) string {
	var env struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(line), &env); err != nil {
		return ""
	}
	return env.Error.Message
}

// AutoStartNotice is the stderr hint printed when a discovery command has to
// start a daemon of its own. It names the address, says what is happening, and
// reminds the caller that the daemon outlives the command — the read-only
// discovery commands leave MCP child processes (Chrome and friends) alive, so
// the reminder is the only pointer to the teardown command.
func AutoStartNotice(addr string) string {
	return fmt.Sprintf("daemon not reachable at %s; auto-starting one (it stays up after this command; stop it with `agent-mcp server shutdown`)\n", addr)
}

// TransportReason flattens the wrapper gRPC puts around transport failures
// (`code: desc = "..."`) down to the cause, so an unreachable address stays
// diagnosable without a gRPC status dump in the envelope.
func TransportReason(err error) string {
	msg := status.Convert(err).Message()
	if _, rest, ok := strings.Cut(msg, `desc = "`); ok {
		msg = strings.TrimSuffix(rest, `"`)
	}
	return strings.TrimPrefix(msg, "transport: Error while dialing: ")
}

// ResolveConfigPath returns the absolute path of the config file named by
// path (or the default location). Daemon and client both resolve the flag
// this way, so a relative --config compares equal to the daemon's absolute
// path and an unresolvable path still yields a diagnosable value.
func ResolveConfigPath(path string) string {
	resolved := config.ResolvePath(path)
	abs, err := filepath.Abs(resolved)
	if err != nil {
		return resolved
	}
	return abs
}

// CheckConfigMatch rejects a daemon that serves a different config file than
// the client asked for: otherwise the flag is silently ignored and the client
// receives another config's servers. Daemons that predate the ConfigPath
// field report nothing, so they are trusted.
func CheckConfigMatch(st *pb.StatusResponse, addr, requestedPath string) error {
	served := st.GetConfigPath()
	if served == "" {
		return nil
	}
	if served == requestedPath {
		return nil
	}
	return fmt.Errorf("%w: config mismatch: daemon at %s serves %s but this client uses %s; "+
		"restart the daemon with that --config or point --connect at a matching daemon",
		ErrMCP, addr, served, requestedPath)
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

// SpawnDaemon re-executes this binary as a detached daemon and returns a
// handle once the child is spawned (readiness is polled by the caller). The
// child inherits the options' config, listen address, and API key.
func SpawnDaemon(o Options) (DaemonChild, error) {
	self, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("cli: locate executable: %w", err)
	}
	// Guard: re-executing a test binary re-runs the whole suite per spawn
	// (fork bomb). Tests must inject Options.SpawnDaemon instead.
	if strings.HasSuffix(self, ".test") || strings.HasSuffix(self, ".test.exe") {
		return nil, fmt.Errorf("cli: refusing to auto-start daemon from test binary %q", self)
	}
	args := []string{"server", "start", "--server-idle-time", "5m"}
	if o.ConfigPath != "" {
		args = append(args, "--config", o.ConfigPath)
	}
	if o.ConnectAddr != "" {
		args = append(args, "--listen", o.ConnectAddr)
	}
	if o.APIKey != "" {
		args = append(args, "--api-key", o.APIKey)
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
