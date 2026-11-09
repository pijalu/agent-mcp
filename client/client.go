// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package client

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"github.com/pijalu/agent-mcp/internal/config"
	"github.com/pijalu/agent-mcp/internal/daemon"
	"github.com/pijalu/agent-mcp/internal/session"
	pb "github.com/pijalu/agent-mcp/proto/agentmcp/v1"
)

// DefaultCallTimeout is the per-operation RPC timeout.
const DefaultCallTimeout = 30 * time.Second

// Options configures a Client. Zero values mean the CLI defaults; the hook
// fields replace infrastructure in tests and embedders.
type Options struct {
	// ConfigPath names the config file; empty means the default location.
	// It is resolved once and compared against a daemon's own path at
	// connect, so a mismatching daemon is rejected instead of silently
	// serving another config's servers.
	ConfigPath string
	// ConnectAddr is the daemon address; empty means DefaultConnectAddr.
	ConnectAddr string
	// APIKey is sent as RPC metadata when set.
	APIKey string
	// CallTimeout bounds each operation (default DefaultCallTimeout).
	CallTimeout time.Duration
	// ConnectTimeout bounds probing, auto-start readiness, and dialing
	// (default DefaultConnectTimeout).
	ConnectTimeout time.Duration
	// NoServer runs every operation in-process against a one-shot session
	// manager; Close tears its sessions down. No daemon is contacted.
	NoServer bool
	// DaemonOnly disables auto-start: an unreachable daemon is reported as
	// such instead of spawning one.
	DaemonOnly bool
	// Stderr receives the auto-start notice; nil means io.Discard.
	Stderr io.Writer
	// AutoStartNotice overrides the auto-start hint for embedding hosts.
	// Nil preserves the agent-mcp CLI's shutdown instructions.
	AutoStartNotice func(addr string) string

	// SpawnDaemon starts a detached daemon; default SpawnDaemon. Tests
	// inject a fake to avoid real processes.
	SpawnDaemon func(o Options) (DaemonChild, error)
	// Dial opens a daemon connection; default GRPCDial. The returned
	// io.Closer must close the underlying connection.
	Dial ProbeDial
	// NewManager builds the one-shot backend for NoServer mode; default is
	// a session.Manager over the config's servers. Inject one wired to an
	// in-memory MCP server for round-trip tests.
	NewManager func(cfg *config.Config) (ClosableManager, error)
}

// Client runs agent-mcp operations against either the daemon (connect,
// auto-start, warm shared sessions) or an in-process manager (NoServer). It
// is safe for concurrent use; the daemon connection and one-shot manager are
// built on first use and reused.
type Client struct {
	opts Options

	mu   sync.Mutex
	mgr  ClosableManager
	rpc  pb.AgentMCPClient
	conn io.Closer
}

// New builds a Client. In NoServer mode the config is loaded up front, so a
// bad config fails here instead of at first use.
func New(o Options) (*Client, error) {
	if o.Stderr == nil {
		o.Stderr = io.Discard
	}
	if o.SpawnDaemon == nil {
		o.SpawnDaemon = SpawnDaemon
	}
	if o.AutoStartNotice == nil {
		o.AutoStartNotice = AutoStartNotice
	}
	if o.Dial == nil {
		o.Dial = GRPCDial
	}
	if o.NewManager == nil {
		o.NewManager = func(cfg *config.Config) (ClosableManager, error) {
			return session.NewManager(cfg.MCPServers, nil), nil
		}
	}
	c := &Client{opts: o}
	if o.NoServer {
		if _, err := c.manager(); err != nil {
			return nil, err
		}
	}
	return c, nil
}

// Do validates and runs one request, returning the shaped payload: discovery
// totals and metadata stripping apply unless the request is Raw.
//
// ListServers always answers from the config in-process (CLI parity), even in
// daemon mode: contacting a daemon would spawn one — holding MCP child
// processes such as Chrome — only to read state the config already carries.
func (c *Client) Do(ctx context.Context, r Request) (any, error) {
	if err := r.Validate(); err != nil {
		return nil, err
	}
	if r.Op == OpListServers || c.opts.NoServer {
		mgr, err := c.manager()
		if err != nil {
			return nil, err
		}
		result, err := InvokeManager(ctx, mgr, r)
		if err != nil {
			return nil, err
		}
		return Shape(result, r)
	}
	rpc, err := c.connectDaemon()
	if err != nil {
		return nil, err
	}
	callCtx, cancel := c.callCtx(ctx)
	defer cancel()
	result, err := InvokeDaemon(callCtx, rpc, r)
	if err != nil {
		return nil, err
	}
	return Shape(result, r)
}

// ShutdownDaemon asks the daemon at the connect address to tear down and
// waits until its port stops accepting connections. A daemon that is already
// gone reports Shutdown false (idempotent), matching `agent-mcp server
// shutdown`; one whose port is still open after the RPC is an error.
func (c *Client) ShutdownDaemon(ctx context.Context, wait time.Duration) (ShutdownResult, error) {
	addr := c.addr()
	cl, closer, err := c.opts.Dial(context.Background(), addr)
	if err != nil {
		return c.shutdownAbsent(addr)
	}
	defer func() { _ = closer.Close() }()
	callCtx, cancel := c.callCtx(ctx)
	defer cancel()
	resp, err := cl.ShutdownAll(callCtx, &pb.ShutdownAllRequest{})
	if err != nil {
		return c.shutdownAbsent(addr)
	}
	if err := waitDaemonGone(addr, wait); err != nil {
		return ShutdownResult{}, err
	}
	return ShutdownResult{Shutdown: true, SessionsClosed: resp.GetSessionsClosed()}, nil
}

// ShutdownResult is the shutdown payload.
type ShutdownResult struct {
	Shutdown       bool  `json:"shutdown"`
	SessionsClosed int32 `json:"sessions_closed"`
}

// shutdownAbsent renders the idempotent no-daemon result.
func (c *Client) shutdownAbsent(addr string) (ShutdownResult, error) {
	if Probe(addr, 500*time.Millisecond) == nil {
		return ShutdownResult{}, fmt.Errorf("%w: daemon at %s refused shutdown", ErrMCP, addr)
	}
	return ShutdownResult{Shutdown: false}, nil
}

// waitDaemonGone blocks until addr stops accepting connections or timeout.
func waitDaemonGone(addr string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if Probe(addr, 500*time.Millisecond) != nil {
			return nil
		}
		time.Sleep(ReadinessPollInterval)
	}
	return fmt.Errorf("%w: daemon at %s still running after %s", ErrMCP, addr, timeout)
}

// Close tears down what the Client built: the one-shot manager's sessions in
// NoServer mode, and the daemon connection otherwise. The daemon itself keeps
// running; use ShutdownDaemon to stop it.
func (c *Client) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.mgr != nil {
		c.mgr.CloseAll()
		c.mgr = nil
	}
	var err error
	if c.conn != nil {
		err = c.conn.Close()
		c.conn, c.rpc = nil, nil
	}
	return err
}

// manager returns the one-shot backend, building it on first use.
func (c *Client) manager() (ClosableManager, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.mgr != nil {
		return c.mgr, nil
	}
	cfg, err := config.Load(config.ResolvePath(c.opts.ConfigPath))
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUsage, err)
	}
	mgr, err := c.opts.NewManager(cfg)
	if err != nil {
		return nil, err
	}
	c.mgr = mgr
	return mgr, nil
}

// connectDaemon returns the daemon RPC client, connecting on first use.
//
// Reachability is decided by a short Status RPC rather than a raw TCP probe
// so callers with a custom Dial (tests) control the outcome. A running
// daemon that rejects the API key is never auto-started around: that would
// spawn a duplicate and hide the cause. Daemon-only mode reports an
// unreachable daemon as such instead of starting one.
func (c *Client) connectDaemon() (pb.AgentMCPClient, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.rpc != nil {
		return c.rpc, nil
	}
	addr := c.addr()
	cl, closer, st, err := c.probeDaemon(addr)
	if err == nil {
		if err := CheckConfigMatch(st, addr, ResolveConfigPath(c.opts.ConfigPath)); err != nil {
			_ = closer.Close()
			return nil, err
		}
		c.conn, c.rpc = closer, cl
		return cl, nil
	}
	if errors.Is(err, errDaemonUnauthenticated) {
		return nil, fmt.Errorf("%w: daemon at %s rejected the API key (check --api-key)", ErrMCP, addr)
	}
	if c.opts.DaemonOnly {
		return nil, fmt.Errorf("%w: daemon at %s unreachable (%v); this operation requires a running daemon, start one with `agent-mcp server start`",
			ErrMCP, addr, err)
	}
	fmt.Fprint(c.opts.Stderr, c.opts.AutoStartNotice(addr))
	child, err := c.opts.SpawnDaemon(c.opts)
	if err != nil {
		return nil, fmt.Errorf("%w: auto-start daemon: %v", ErrInternal, err)
	}
	defer child.Cleanup()
	if err := WaitReady(addr, child, c.connectTimeoutValue()); err != nil {
		return nil, err
	}
	dialCtx, cancel := context.WithTimeout(context.Background(), c.connectTimeoutValue())
	defer cancel()
	cl, closer, err = c.opts.Dial(dialCtx, addr)
	if err != nil {
		return nil, fmt.Errorf("%w: dial daemon at %s: %v", ErrMCP, addr, err)
	}
	if err := c.validateStartedDaemon(cl, child, addr); err != nil {
		_ = closer.Close()
		return nil, err
	}
	c.conn, c.rpc = closer, cl
	return cl, nil
}

// validateStartedDaemon confirms a gRPC daemon, not merely an open TCP port.
// If the child lost the listen race, its own diagnostic is more useful than
// the transport error from the unrelated process occupying the port.
func (c *Client) validateStartedDaemon(cl pb.AgentMCPClient, child DaemonChild, addr string) error {
	st, err := c.reachable(cl)
	if err != nil {
		if code, exited := childStatus(child); exited {
			return DaemonStartupError(addr, code, child.Output())
		}
		return fmt.Errorf("%w: daemon at %s did not become ready: %v", ErrMCP, addr, err)
	}
	return CheckConfigMatch(st, addr, ResolveConfigPath(c.opts.ConfigPath))
}

// probeDaemon dials addr and asks the daemon behind it for its status. A
// missing daemon and a rejected API key are both reported as errors; the dial
// failure keeps its own text so an unreachable address stays diagnosable.
func (c *Client) probeDaemon(addr string) (pb.AgentMCPClient, io.Closer, *pb.StatusResponse, error) {
	dialCtx, cancel := context.WithTimeout(context.Background(), c.connectTimeoutValue())
	defer cancel()
	cl, closer, err := c.opts.Dial(dialCtx, addr)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("dial %s: %v", addr, err)
	}
	st, err := c.reachable(cl)
	if err == nil {
		return cl, closer, st, nil
	}
	if closer != nil {
		_ = closer.Close()
	}
	if status.Code(err) == codes.Unauthenticated {
		return nil, nil, nil, fmt.Errorf("%w: %v", errDaemonUnauthenticated, err)
	}
	return nil, nil, nil, errors.New(TransportReason(err))
}

// reachable returns the daemon Status when the RPC succeeds. The probe
// carries API-key metadata, so auth failures are distinguishable from a
// daemon that is not listening.
func (c *Client) reachable(cl pb.AgentMCPClient) (*pb.StatusResponse, error) {
	ctx, cancel := c.metadataCtx(c.connectTimeoutValue())
	defer cancel()
	return cl.Status(ctx, &pb.StatusRequest{})
}

// callCtx derives the per-call context with timeout and API key metadata,
// retaining the caller's cancellation.
func (c *Client) callCtx(ctx context.Context) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithTimeout(ctx, c.callTimeoutValue())
	if c.opts.APIKey != "" {
		ctx = metadata.AppendToOutgoingContext(ctx, daemonMetadataHeader, c.opts.APIKey)
	}
	return ctx, cancel
}

// metadataCtx builds a timeout context carrying API-key metadata when set.
func (c *Client) metadataCtx(timeout time.Duration) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	if c.opts.APIKey != "" {
		ctx = metadata.AppendToOutgoingContext(ctx, daemonMetadataHeader, c.opts.APIKey)
	}
	return ctx, cancel
}

// daemonMetadataHeader is the daemon's API-key metadata key.
const daemonMetadataHeader = daemon.MetadataHeader

func (c *Client) addr() string {
	if c.opts.ConnectAddr != "" {
		return c.opts.ConnectAddr
	}
	return DefaultConnectAddr
}

func (c *Client) callTimeoutValue() time.Duration {
	if c.opts.CallTimeout <= 0 {
		return DefaultCallTimeout
	}
	return c.opts.CallTimeout
}

func (c *Client) connectTimeoutValue() time.Duration {
	if c.opts.ConnectTimeout <= 0 {
		return DefaultConnectTimeout
	}
	return c.opts.ConnectTimeout
}
