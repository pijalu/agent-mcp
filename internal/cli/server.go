package cli

import (
	"fmt"
	"io"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/pijalu/agent-mcp/internal/config"
	"github.com/pijalu/agent-mcp/internal/daemon"
	"github.com/pijalu/agent-mcp/internal/output"
	"github.com/pijalu/agent-mcp/internal/session"
	pb "github.com/pijalu/agent-mcp/proto/agentmcp/v1"
)

// serverEnv carries parsed server-command state; hooks fakeable in tests.
type serverEnv struct {
	deps  Deps
	ready func(addr string, timeout time.Duration) error
}

func newServerEnv(deps Deps) *serverEnv {
	return &serverEnv{deps: deps, ready: waitForPort}
}

// runServer dispatches server subcommands.
func runServer(deps Deps, args []string, stdout, stderr io.Writer) (int, error) {
	if len(args) == 0 {
		return 2, usageError("server requires a subcommand: start|status|shutdown")
	}
	env := newServerEnv(deps)
	switch args[0] {
	case "start":
		return serverStart(args[1:], stdout, stderr)
	case "status":
		return env.serverStatus(args[1:], stdout)
	case "shutdown":
		return env.serverShutdown(args[1:], stdout)
	default:
		return 2, usageError("unknown server subcommand %q (want start|status|shutdown)", args[0])
	}
}

// serverStart runs the daemon in the foreground.
func serverStart(args []string, stdout, stderr io.Writer) (int, error) {
	fs := newFlagSet("server start")
	var listen string
	var apiKeys multiFlag
	var idleTime, grace time.Duration
	var configPath string
	fs.StringVar(&listen, "listen", defaultConnectAddr, "daemon listen address host:port")
	fs.Var(&apiKeys, "api-key", "accepted client API key (repeatable)")
	fs.DurationVar(&idleTime, "server-idle-time", 0, "idle timeout; 0 disables (manual start default)")
	fs.DurationVar(&grace, "shutdown-grace", daemon.DefaultShutdownGrace, "in-flight drain grace")
	fs.StringVar(&configPath, "config", "", "path to config.toml")
	if err := fs.Parse(args); err != nil {
		return 2, parseError(err)
	}
	cfg, err := config.Load(config.ResolvePath(configPath))
	if err != nil {
		return 2, fmt.Errorf("%w: %v", output.ErrUsage, err)
	}
	keys := append([]string(nil), apiKeys...)
	keys = append(keys, cfg.APIKeys...)
	mgr := session.NewManager(cfg.MCPServers, nil)
	d := daemon.New(mgr, daemon.Options{
		APIKeys:       keys,
		IdleTimeout:   idleTime,
		ShutdownGrace: grace,
		ConfigPath:    config.ResolvePath(configPath),
	})
	lis, err := net.Listen("tcp", listen)
	if err != nil {
		return 1, fmt.Errorf("%w: listen %s: %v", output.ErrMCP, listen, err)
	}
	fmt.Fprintf(stderr, "agent-mcp daemon listening on %s\n", lis.Addr())
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(signals)
	done := make(chan struct{})
	go func() {
		select {
		case <-signals:
			d.Shutdown()
		case <-done:
		}
	}()
	serveErr := d.Serve(lis)
	close(done)
	if serveErr != nil {
		return 3, fmt.Errorf("%w: %v", output.ErrInternal, serveErr)
	}
	return 0, nil
}

// serverStatus prints the daemon status; unreachable daemon exits 1.
func (e *serverEnv) serverStatus(args []string, stdout io.Writer) (int, error) {
	fs := newFlagSet("server status")
	var g GlobalFlags
	bindGlobal(fs, &g)
	if err := fs.Parse(args); err != nil {
		return 2, parseError(err)
	}
	if err := validateTimeouts(g); err != nil {
		return 2, err
	}
	format, err := output.ResolveFormat(g.format, stdout)
	if err != nil {
		return 2, err
	}
	addr := g.connect
	if addr == "" {
		addr = defaultConnectAddr
	}
	client, closer, err := e.deps.Dial(statusCtx(), addr)
	if err != nil {
		return 1, fmt.Errorf("%w: daemon at %s unreachable: %v", output.ErrMCP, addr, err)
	}
	defer func() { _ = closer.Close() }()
	ctx, cancel := timeoutCtx(g.callTimeout, g.apiKey)
	defer cancel()
	resp, err := client.Status(ctx, &pb.StatusRequest{})
	if err != nil {
		return 1, fmt.Errorf("%w: status: %v", output.ErrMCP, err)
	}
	return 0, renderStatus(stdout, format, resp)
}

// statusView is the JSON payload for server status.
type statusView struct {
	UptimeSeconds  int64               `json:"uptime_seconds"`
	ActiveSessions int32               `json:"active_sessions"`
	ConfigPath     string              `json:"config_path,omitempty"`
	Servers        []serverStatusEntry `json:"servers"`
}

type serverStatusEntry struct {
	Name           string `json:"name"`
	State          string `json:"state"`
	ActiveSessions int32  `json:"active_sessions"`
	LastError      string `json:"last_error,omitempty"`
}

// renderStatus maps the proto response onto the output payload.
func renderStatus(stdout io.Writer, format output.Format, resp *pb.StatusResponse) error {
	view := statusView{
		UptimeSeconds:  resp.GetUptimeSeconds(),
		ActiveSessions: resp.GetActiveSessions(),
		ConfigPath:     resp.GetConfigPath(),
	}
	for _, s := range resp.GetServers() {
		view.Servers = append(view.Servers, serverStatusEntry{
			Name:           s.GetName(),
			State:          s.GetState(),
			ActiveSessions: s.GetActiveSessions(),
			LastError:      s.GetLastError(),
		})
	}
	if format == output.FormatText {
		return output.WriteText(stdout, view)
	}
	return output.WriteJSON(stdout, view)
}

// shutdownResult is the JSON payload for server shutdown.
type shutdownResult struct {
	Shutdown       bool  `json:"shutdown"`
	SessionsClosed int32 `json:"sessions_closed"`
}

// serverShutdown asks the daemon to tear down; idempotent when absent.
func (e *serverEnv) serverShutdown(args []string, stdout io.Writer) (int, error) {
	fs := newFlagSet("server shutdown")
	var g GlobalFlags
	var timeout time.Duration
	bindGlobal(fs, &g)
	fs.DurationVar(&timeout, "timeout", 10*time.Second, "wait for the daemon to exit")
	if err := fs.Parse(args); err != nil {
		return 2, parseError(err)
	}
	if err := validateTimeouts(g); err != nil {
		return 2, err
	}
	format, err := output.ResolveFormat(g.format, stdout)
	if err != nil {
		return 2, err
	}
	addr := g.connect
	if addr == "" {
		addr = defaultConnectAddr
	}
	return e.shutdownAt(addr, g, timeout, format, stdout)
}

// shutdownAt performs the shutdown RPC against addr.
func (e *serverEnv) shutdownAt(addr string, g GlobalFlags, timeout time.Duration, format output.Format, stdout io.Writer) (int, error) {
	client, closer, err := e.deps.Dial(statusCtx(), addr)
	if err != nil {
		return e.shutdownAbsent(stdout, format, addr)
	}
	defer func() { _ = closer.Close() }()
	ctx, cancel := timeoutCtx(g.callTimeout, g.apiKey)
	defer cancel()
	resp, err := client.ShutdownAll(ctx, &pb.ShutdownAllRequest{})
	if err != nil {
		return e.shutdownAbsent(stdout, format, addr)
	}
	if err := e.ready(addr, timeout); err != nil {
		return 1, err
	}
	result := shutdownResult{Shutdown: true, SessionsClosed: resp.GetSessionsClosed()}
	return 0, writeResult(stdout, format, result)
}

// shutdownAbsent renders the idempotent no-daemon result.
func (e *serverEnv) shutdownAbsent(stdout io.Writer, format output.Format, addr string) (int, error) {
	if probeErr := quickProbe(addr); probeErr == nil {
		return 1, fmt.Errorf("%w: daemon at %s refused shutdown", output.ErrMCP, addr)
	}
	return 0, writeResult(stdout, format, shutdownResult{Shutdown: false})
}

// writeResult renders v honoring format.
func writeResult(stdout io.Writer, format output.Format, v any) error {
	if format == output.FormatText {
		return output.WriteText(stdout, v)
	}
	return output.WriteJSON(stdout, v)
}

// multiFlag collects repeatable string flags.
type multiFlag []string

func (m *multiFlag) String() string { return fmt.Sprint([]string(*m)) }

func (m *multiFlag) Set(v string) error {
	*m = append(*m, v)
	return nil
}
