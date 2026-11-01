package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"github.com/pijalu/agent-mcp/client"
	"github.com/pijalu/agent-mcp/internal/config"
	"github.com/pijalu/agent-mcp/internal/daemon"
	"github.com/pijalu/agent-mcp/internal/output"
	"github.com/pijalu/agent-mcp/internal/session"
	pb "github.com/pijalu/agent-mcp/proto/agentmcp/v1"
)

// defaultConnectTimeout is the single source for the connect/readiness
// timeout: the --connect-timeout default and the runtime lookup both use it.
const defaultConnectTimeout = client.DefaultConnectTimeout

// defaultConnectAddr is where the daemon listens when --connect is unset.
const defaultConnectAddr = client.DefaultConnectAddr

// DialFunc probes or opens a daemon connection; faked in tests.
type DialFunc = client.ProbeDial

// ManagerFactory builds a one-shot session manager; faked in tests.
type ManagerFactory func(cfg *config.Config) ClosableManager

// ClosableManager is the one-shot backend surface.
type ClosableManager interface {
	daemon.SessionManager
	CloseAll() int
}

// grpcDial dials addr and returns the client plus the connection. The call
// options raise gRPC's 4 MiB message ceiling to daemon.MaxMessageBytes so
// large MCP results (screenshots, docs, renders) reach the caller intact.
func grpcDial(ctx context.Context, addr string) (pb.AgentMCPClient, io.Closer, error) {
	return client.GRPCDial(ctx, addr)
}

// newSessionManager builds a session.Manager from the loaded config.
func newSessionManager(cfg *config.Config) ClosableManager {
	return session.NewManager(cfg.MCPServers, nil)
}

// connectAddr resolves the daemon address.
func (env *clientEnv) connectAddr() string {
	if env.global.connect != "" {
		return env.global.connect
	}
	return client.DefaultConnectAddr
}

func (env *clientEnv) connectTimeoutValue() time.Duration {
	if env.global.connectTimeout <= 0 {
		return defaultConnectTimeout
	}
	return env.global.connectTimeout
}

// resolvedConfigPath returns the absolute path of the config file named by
// --config (or the default location). Daemon and client both resolve the flag
// this way, so a relative --config compares equal to the daemon's absolute
// path and an unresolvable path still yields a diagnosable value.
func resolvedConfigPath(g GlobalFlags) string {
	return client.ResolveConfigPath(g.config)
}

// checkConfigMatch rejects a daemon that serves a different config file than
// --config names: otherwise the flag is silently ignored and the client
// receives another config's servers. Daemons that predate the ConfigPath field
// report nothing, so they are trusted.
func (env *clientEnv) checkConfigMatch(st *pb.StatusResponse) error {
	return client.CheckConfigMatch(st, env.connectAddr(), resolvedConfigPath(env.global))
}

// callCtx derives the per-call context with timeout and API key metadata.
func (env *clientEnv) callCtx() (context.Context, context.CancelFunc) {
	return env.metadataCtx(env.global.callTimeout)
}

// probeCtx derives a short-lived context for reachability checks. It carries
// the same API-key metadata as a real call so an authenticated daemon is not
// mistaken for a stopped one.
func (env *clientEnv) probeCtx() (context.Context, context.CancelFunc) {
	return env.metadataCtx(env.connectTimeoutValue())
}

// metadataCtx builds a timeout context carrying API-key metadata when set.
func (env *clientEnv) metadataCtx(timeout time.Duration) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	if env.global.apiKey != "" {
		ctx = metadata.AppendToOutgoingContext(ctx, daemonMetadataHeader, env.global.apiKey)
	}
	return ctx, cancel
}

// daemonMetadataHeader is the daemon's API-key metadata key.
const daemonMetadataHeader = "x-agent-mcp-api-key"

// runDaemonBacked connects to the daemon (auto-starting on failure) and
// executes the command over gRPC.
func (env *clientEnv) runDaemonBacked(stdout io.Writer) (int, error) {
	clientConn, closer, err := env.connectDaemon()
	if err != nil {
		return 1, err
	}
	defer func() { _ = closer.Close() }()
	ctx, cancel := env.callCtx()
	defer cancel()
	if env.name == "events" {
		return 0, env.runEvents(ctx, clientConn, stdout)
	}
	result, err := env.invokeDaemon(ctx, clientConn)
	if err == nil {
		result, err = env.prepare(result)
	}
	if err != nil {
		return 0, err
	}
	return 0, env.writeData(stdout, result)
}

// errDaemonUnauthenticated marks a daemon that refused the client's API key.
var errDaemonUnauthenticated = errors.New("daemon rejected the API key")

// connectDaemon dials the daemon, auto-starting it when unreachable.
// Reachability is decided by a short Status RPC rather than a raw TCP
// probe so callers with a custom DialFunc (tests) control the outcome.
// Daemon-only commands never auto-start one: their subscription would race a
// daemon nobody asked for, and an unreachable daemon is reported as such.
func (env *clientEnv) connectDaemon() (pb.AgentMCPClient, io.Closer, error) {
	addr := env.connectAddr()
	clientConn, closer, st, err := env.probeDaemon(addr)
	if err == nil {
		return clientConn, closer, env.checkConfigMatch(st)
	}
	// A running daemon that rejects the key must not be treated as
	// absent: auto-starting would spawn a duplicate and hide the cause.
	if errors.Is(err, errDaemonUnauthenticated) {
		return nil, nil, fmt.Errorf("%w: daemon at %s rejected the API key (check --api-key)", output.ErrMCP, addr)
	}
	if env.daemonOnly {
		return nil, nil, fmt.Errorf("%w: daemon at %s unreachable (%v); %s requires a running daemon, start one with `agent-mcp server start`",
			output.ErrMCP, addr, err, env.name)
	}
	fmt.Fprint(env.stderr, autoStartNotice(addr))
	child, err := env.deps.SpawnDaemon(env.global)
	if err != nil {
		return nil, nil, fmt.Errorf("%w: auto-start daemon: %v", output.ErrInternal, err)
	}
	defer child.Cleanup()
	if err := env.waitReady(addr, child); err != nil {
		return nil, nil, err
	}
	dialCtx, dialCancel := context.WithTimeout(context.Background(), env.connectTimeoutValue())
	defer dialCancel()
	clientConn, closer, err = env.deps.Dial(dialCtx, addr)
	if err != nil {
		return nil, nil, fmt.Errorf("%w: dial daemon at %s: %v", output.ErrMCP, addr, err)
	}
	return clientConn, closer, nil
}

// autoStartNotice is the stderr hint printed when a discovery command has to
// start a daemon of its own. It names the address, says what is happening, and
// reminds the caller that the daemon outlives the command — the read-only
// discovery commands leave MCP child processes (Chrome and friends) alive, so
// the reminder is the only pointer to the teardown command.
func autoStartNotice(addr string) string {
	return client.AutoStartNotice(addr)
}

// probeDaemon dials addr and asks the daemon behind it for its status. A
// missing daemon and a rejected API key are both reported as errors; the dial
// failure keeps its own text so an unreachable address stays diagnosable.
func (env *clientEnv) probeDaemon(addr string) (pb.AgentMCPClient, io.Closer, *pb.StatusResponse, error) {
	dialCtx, cancel := context.WithTimeout(context.Background(), env.connectTimeoutValue())
	defer cancel()
	clientConn, closer, err := env.deps.Dial(dialCtx, addr)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("dial %s: %v", addr, err)
	}
	st, err := env.reachable(clientConn)
	if err == nil {
		return clientConn, closer, st, nil
	}
	if closer != nil {
		_ = closer.Close()
	}
	if status.Code(err) == codes.Unauthenticated {
		return nil, nil, nil, fmt.Errorf("%w: %v", errDaemonUnauthenticated, err)
	}
	return nil, nil, nil, errors.New(transportReason(err))
}

// transportReason flattens the wrapper gRPC puts around transport failures
// (`code: desc = "..."`) down to the cause, so an unreachable address stays
// diagnosable without a gRPC status dump in the envelope.
func transportReason(err error) string {
	return client.TransportReason(err)
}

// reachable returns the daemon Status when the RPC succeeds. The probe
// carries API-key metadata, so auth failures are distinguishable from a
// daemon that is not listening.
func (env *clientEnv) reachable(clientConn pb.AgentMCPClient) (*pb.StatusResponse, error) {
	ctx, cancel := env.probeCtx()
	defer cancel()
	return clientConn.Status(ctx, &pb.StatusRequest{})
}

// waitReady polls addr until the daemon accepts connections or the connect
// timeout expires. A child that exits first fails fast with its own error and
// exit code instead of stalling until the timeout.
func (env *clientEnv) waitReady(addr string, child DaemonChild) error {
	return client.WaitReady(addr, child, env.connectTimeoutValue())
}

// daemonStartupError reports an auto-started daemon that exited during startup.
// It carries the child's own error and maps its exit code onto the client's
// error kind, so a config-load failure surfaces as the config error with exit
// 2 rather than an opaque readiness timeout.
func daemonStartupError(addr string, code int, childOut string) error {
	return client.DaemonStartupError(addr, code, childOut)
}

// maxChildDetail caps how much of a child's output is echoed back.
const maxChildDetail = client.MaxChildDetail

// childDetail turns captured child output into a one-line diagnostic: the
// message of its JSON error envelope is unwrapped so the agent sees the cause
// without a nested envelope inside the envelope.
func childDetail(raw string) string {
	return client.ChildDetail(raw)
}

// runOneShot executes the command directly against a session.Manager and
// tears it down before returning.
func (env *clientEnv) runOneShot(stdout io.Writer) (int, error) {
	cfg, err := config.Load(config.ResolvePath(env.global.config))
	if err != nil {
		return 2, fmt.Errorf("%w: %v", output.ErrUsage, err)
	}
	mgr := env.deps.NewManager(cfg)
	defer mgr.CloseAll()
	ctx, cancel := env.callCtx()
	defer cancel()
	result, err := invokeManager(ctx, env, mgr)
	if err == nil {
		result, err = env.prepare(result)
	}
	if err != nil {
		return 0, err
	}
	return 0, env.writeData(stdout, result)
}
