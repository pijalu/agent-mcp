package daemon

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pb "github.com/pijalu/agent-mcp/proto/agentmcp/v1"
)

// Clock abstracts time so the idle reaper can be driven by a fake in tests.
type Clock interface {
	Now() time.Time
	AfterFunc(d time.Duration, fn func()) Timer
}

// Timer is the subset of *time.Timer the reaper relies on.
type Timer interface {
	Stop() bool
	Reset(d time.Duration) bool
}

type realClock struct{}

func (realClock) Now() time.Time { return time.Now() }

func (realClock) AfterFunc(d time.Duration, fn func()) Timer {
	return time.AfterFunc(d, fn)
}

// ErrShuttingDown fails RPCs that arrive after ShutdownAll has begun.
var ErrShuttingDown = errors.New("daemon: shutting down")

// Options carries operator flags into the daemon. Zero values are safe.
type Options struct {
	// APIKeys are accepted client keys. Empty disables authentication.
	APIKeys []string
	// IdleTimeout stops the daemon after this long without RPC activity.
	// Zero disables the idle reaper (manual start default).
	IdleTimeout time.Duration
	// ShutdownGrace caps how long ShutdownAll drains in-flight calls.
	// Zero uses DefaultShutdownGrace.
	ShutdownGrace time.Duration
	// ConfigPath is the config file this daemon loaded. Status reports it so
	// clients can detect that their own --config names a different file.
	ConfigPath string
	// Clock overrides time; nil uses the real clock.
	Clock Clock
	// OnShutdown runs after the gRPC server stops (process exit hook).
	OnShutdown func()
}

// DefaultShutdownGrace applies when Options.ShutdownGrace is zero.
const DefaultShutdownGrace = 10 * time.Second

// MetadataHeader is the gRPC metadata key clients send their API key in.
const MetadataHeader = "x-agent-mcp-api-key"

// Daemon is the gRPC front end to the session manager.
type Daemon struct {
	pb.UnimplementedAgentMCPServer

	mgr    SessionManager
	keys   [][]byte
	grace  time.Duration
	clock  Clock
	onStop func()

	// configPath is reported by Status; empty for daemons that do not know it.
	configPath string

	server *grpc.Server

	mu         sync.Mutex
	idleTimer  Timer
	idleAfter  time.Duration
	draining   bool
	shutdownCh chan struct{}
	inflight   sync.WaitGroup

	startedAt time.Time
}

// New builds a Daemon serving AgentMCP over mgr.
func New(mgr SessionManager, opts Options) *Daemon {
	clock := opts.Clock
	if clock == nil {
		clock = realClock{}
	}
	grace := opts.ShutdownGrace
	if grace <= 0 {
		grace = DefaultShutdownGrace
	}
	d := &Daemon{
		mgr:        mgr,
		grace:      grace,
		clock:      clock,
		onStop:     opts.OnShutdown,
		configPath: opts.ConfigPath,
		idleAfter:  opts.IdleTimeout,
		shutdownCh: make(chan struct{}),
	}
	for _, k := range opts.APIKeys {
		d.keys = append(d.keys, []byte(k))
	}
	d.server = grpc.NewServer(grpc.UnaryInterceptor(d.interceptor))
	pb.RegisterAgentMCPServer(d.server, d)
	return d
}

// Serve binds lis and blocks until the server stops. When it returns
// without error the shutdown hook has run and the daemon is fully stopped.
func (d *Daemon) Serve(lis net.Listener) error {
	d.mu.Lock()
	d.startedAt = d.clock.Now()
	d.mu.Unlock()
	d.touch()
	err := d.server.Serve(lis)
	// A listener failure is an abnormal exit path; close managed MCP sessions
	// instead of waiting indefinitely for a shutdown RPC or idle timer.
	if err != nil {
		d.Shutdown()
	}
	<-d.shutdownCh
	if d.onStop != nil {
		d.onStop()
	}
	if err != nil {
		return fmt.Errorf("daemon: serve: %w", err)
	}
	return nil
}

// Shutdown initiates graceful teardown: close all MCP sessions, drain
// in-flight calls up to the grace period, then stop the gRPC server.
// It returns the number of sessions closed. Repeated calls are no-ops
// that report zero.
func (d *Daemon) Shutdown() int {
	if !d.beginShutdown() {
		return 0
	}
	closed := d.mgr.CloseAll()
	go d.stopWhenDrained()
	return closed
}

// beginShutdown flips draining exactly once and disarms the idle reaper.
func (d *Daemon) beginShutdown() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.draining {
		return false
	}
	d.draining = true
	if d.idleTimer != nil {
		d.idleTimer.Stop()
	}
	return true
}

// stopWhenDrained waits for in-flight RPCs up to the grace period, then
// stops the gRPC server and signals completion on shutdownCh.
func (d *Daemon) stopWhenDrained() {
	drained := make(chan struct{})
	go func() {
		d.inflight.Wait()
		close(drained)
	}()
	expired := make(chan struct{})
	timer := d.clock.AfterFunc(d.grace, func() { close(expired) })
	defer timer.Stop()

	select {
	case <-drained:
		d.server.GracefulStop()
	case <-expired:
		d.server.Stop()
	}
	close(d.shutdownCh)
}

// touch records RPC activity for the idle reaper.
func (d *Daemon) touch() {
	if d.idleAfter <= 0 {
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.draining {
		return
	}
	if d.idleTimer == nil {
		d.idleTimer = d.clock.AfterFunc(d.idleAfter, d.onIdle)
		return
	}
	d.idleTimer.Reset(d.idleAfter)
}

// onIdle fires when no RPC arrived within the idle timeout.
func (d *Daemon) onIdle() {
	d.Shutdown()
}

// interceptor authenticates, rejects calls once draining, and tracks
// in-flight calls plus activity around every unary RPC.
func (d *Daemon) interceptor(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
	if err := d.authenticate(ctx); err != nil {
		return nil, err
	}
	d.mu.Lock()
	if d.draining {
		d.mu.Unlock()
		return nil, status.Error(codes.Unavailable, ErrShuttingDown.Error())
	}
	// Admission and Add must be atomic relative to Shutdown's Wait. Otherwise
	// a request can pass the check after shutdown starts but increment the
	// WaitGroup after Wait has begun.
	d.inflight.Add(1)
	d.mu.Unlock()
	defer d.inflight.Done()
	d.touch()
	return handler(ctx, req)
}

// uptime returns seconds since Serve started.
func (d *Daemon) uptime() int64 {
	d.mu.Lock()
	started := d.startedAt
	d.mu.Unlock()
	if started.IsZero() {
		return 0
	}
	return int64(d.clock.Now().Sub(started).Seconds())
}
