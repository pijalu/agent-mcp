package daemon_test

import (
	"context"
	"net"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"

	"github.com/pijalu/agent-mcp/internal/daemon"
	pb "github.com/pijalu/agent-mcp/proto/agentmcp/v1"
)

// TestShutdownAllGracefulTeardown: ShutdownAll closes all sessions,
// reports the count, stops the server, and fails subsequent RPCs.
func TestShutdownAllGracefulTeardown(t *testing.T) {
	stopped := make(chan struct{})
	td := startDaemon(t, daemon.Options{OnShutdown: func() { close(stopped) }})

	resp, err := td.cli.ShutdownAll(context.Background(), &pb.ShutdownAllRequest{})
	if err != nil {
		t.Fatalf("ShutdownAll: %v", err)
	}
	if resp.GetSessionsClosed() != 2 {
		t.Errorf("sessions closed = %d, want 2 (fake manager count)", resp.GetSessionsClosed())
	}

	waitFor(t, "server stop", func() bool {
		select {
		case <-stopped:
			return true
		default:
			return false
		}
	})

	_, err = td.cli.Status(context.Background(), &pb.StatusRequest{})
	if err == nil {
		t.Fatal("Status after ShutdownAll succeeded")
	}

	// ShutdownAll is idempotent from the daemon's side: a second direct
	// call reports nothing new instead of double-closing.
	if got := td.d.Shutdown(); got != 0 {
		t.Errorf("second Shutdown() = %d, want 0", got)
	}
}

// TestShutdownAllDrainsInFlight: an in-flight call completes within the
// grace period while ShutdownAll waits, and the server only stops after.
func TestShutdownAllDrainsInFlight(t *testing.T) {
	mgr := &slowMgr{started: make(chan struct{}), release: make(chan struct{})}
	d := daemon.New(mgr, daemon.Options{ShutdownGrace: 2 * time.Second})
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	serveErr := make(chan error, 1)
	go func() { serveErr <- d.Serve(lis) }()

	conn, err := grpc.NewClient(lis.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	cli := pb.NewAgentMCPClient(conn)

	callDone := make(chan error, 1)
	go func() {
		_, err := cli.CallTool(context.Background(), &pb.CallToolRequest{
			Server: "alpha",
			Json:   mustJSON(t, map[string]string{"name": "slow"}),
		})
		callDone <- err
	}()
	<-mgr.started

	// Start shutdown while the call is blocked; it must not return to the
	// caller until the in-flight call is released.
	shutdownDone := make(chan struct{})
	go func() {
		if _, err := cli.ShutdownAll(context.Background(), &pb.ShutdownAllRequest{}); err != nil {
			t.Errorf("ShutdownAll: %v", err)
		}
		close(shutdownDone)
	}()

	select {
	case err := <-callDone:
		t.Fatalf("in-flight call finished before release: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	close(mgr.release)

	if err := <-callDone; err != nil {
		t.Fatalf("in-flight CallTool: %v", err)
	}
	<-shutdownDone
	if err := <-serveErr; err != nil {
		t.Fatalf("serve: %v", err)
	}
	if mgr.inflight.Load() != 0 {
		t.Errorf("inflight = %d after drain, want 0", mgr.inflight.Load())
	}
}

// TestShutdownGraceForcesStop: when an in-flight call outlives the grace
// period the server stops hard instead of hanging forever.
func TestShutdownGraceForcesStop(t *testing.T) {
	mgr := &slowMgr{started: make(chan struct{}), release: make(chan struct{})}
	d := daemon.New(mgr, daemon.Options{ShutdownGrace: 100 * time.Millisecond})
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	serveErr := make(chan error, 1)
	go func() { serveErr <- d.Serve(lis) }()

	conn, err := grpc.NewClient(lis.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	cli := pb.NewAgentMCPClient(conn)

	go func() {
		_, _ = cli.CallTool(context.Background(), &pb.CallToolRequest{
			Server: "alpha",
			Json:   mustJSON(t, map[string]string{"name": "slow"}),
		})
	}()
	<-mgr.started

	if _, err := cli.ShutdownAll(context.Background(), &pb.ShutdownAllRequest{}); err != nil {
		t.Fatalf("ShutdownAll: %v", err)
	}
	// Server stops after ~100ms grace despite the never-released call.
	select {
	case err := <-serveErr:
		if err != nil {
			t.Fatalf("serve: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("server did not stop after grace expiry")
	}
	close(mgr.release)
}

// TestConcurrentClients: parallel clients multiplex over one daemon;
// every RPC succeeds and activity tracking stays race-free under -race.
func TestConcurrentClients(t *testing.T) {
	td := startDaemon(t, daemon.Options{})
	const workers = 8
	const callsPerWorker = 10

	var wg sync.WaitGroup
	errs := make(chan error, workers*callsPerWorker)
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < callsPerWorker; i++ {
				_, err := td.cli.ListTools(context.Background(), &pb.ListToolsRequest{Server: "alpha"})
				if err != nil {
					errs <- err
				}
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Errorf("concurrent ListTools: %v", err)
	}
}

// TestRPCsRejectedWhileDraining uses a blocked call to keep the daemon in
// draining state, then observes Unauthenticated-free Unavailable errors.
func TestRPCsRejectedWhileDraining(t *testing.T) {
	mgr := &slowMgr{started: make(chan struct{}), release: make(chan struct{})}
	d := daemon.New(mgr, daemon.Options{ShutdownGrace: 5 * time.Second})
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	serveErr := make(chan error, 1)
	go func() { serveErr <- d.Serve(lis) }()
	defer func() {
		close(mgr.release)
		<-serveErr
	}()

	conn, err := grpc.NewClient(lis.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	cli := pb.NewAgentMCPClient(conn)

	go func() {
		_, _ = cli.CallTool(context.Background(), &pb.CallToolRequest{
			Server: "alpha",
			Json:   mustJSON(t, map[string]string{"name": "slow"}),
		})
	}()
	<-mgr.started

	if _, err := cli.ShutdownAll(context.Background(), &pb.ShutdownAllRequest{}); err != nil {
		t.Fatalf("ShutdownAll: %v", err)
	}
	// While the slow call drains, new RPCs fail fast with Unavailable.
	_, err = cli.ListTools(context.Background(), &pb.ListToolsRequest{Server: "alpha"})
	if status.Code(err) != codes.Unavailable {
		t.Fatalf("code = %v, want Unavailable (err=%v)", status.Code(err), err)
	}
}
