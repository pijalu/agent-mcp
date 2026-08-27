package daemon_test

import (
	"context"
	"testing"
	"time"

	"github.com/pijalu/agent-mcp/internal/daemon"
	pb "github.com/pijalu/agent-mcp/proto/agentmcp/v1"
)

// TestIdleReaperStopsDaemon: an auto-started daemon with an idle timeout
// shuts itself down when the reaper fires; later RPCs fail.
func TestIdleReaperStopsDaemon(t *testing.T) {
	clock := newFakeClock()
	stopped := make(chan struct{})
	td := startDaemon(t, daemon.Options{
		IdleTimeout: 5 * time.Minute,
		Clock:       clock,
		OnShutdown:  func() { close(stopped) },
	})
	clock.waitArmed(t)
	clock.fireIdle()
	waitFor(t, "daemon idle shutdown", func() bool {
		select {
		case <-stopped:
			return true
		default:
			return false
		}
	})
	_, err := td.cli.Status(context.Background(), &pb.StatusRequest{})
	if err == nil {
		t.Fatal("Status after idle shutdown succeeded")
	}
	if td.mgr.closeAllCount == 0 {
		t.Error("idle shutdown did not close sessions")
	}
}

// TestActivityResetsIdleTimer: every RPC resets the reaper timer.
func TestActivityResetsIdleTimer(t *testing.T) {
	clock := newFakeClock()
	td := startDaemon(t, daemon.Options{
		IdleTimeout: 5 * time.Minute,
		Clock:       clock,
	})
	before := clock.idleResets()
	if _, err := td.cli.Status(context.Background(), &pb.StatusRequest{}); err != nil {
		t.Fatalf("Status: %v", err)
	}
	if _, err := td.cli.ListServers(context.Background(), &pb.ListServersRequest{}); err != nil {
		t.Fatalf("ListServers: %v", err)
	}
	if got := clock.idleResets(); got <= before {
		t.Fatalf("idle resets = %d, want > %d after RPC activity", got, before)
	}
}

// TestManualStartDisablesIdleTimeout: without IdleTimeout the reaper
// never arms and the daemon stays up.
func TestManualStartDisablesIdleTimeout(t *testing.T) {
	clock := newFakeClock()
	td := startDaemon(t, daemon.Options{Clock: clock})
	if _, err := td.cli.Status(context.Background(), &pb.StatusRequest{}); err != nil {
		t.Fatalf("Status: %v", err)
	}
	if got := clock.idleResets(); got != 0 {
		t.Fatalf("idle timer armed %d times with timeout disabled", got)
	}
	resp, err := td.cli.Status(context.Background(), &pb.StatusRequest{})
	if err != nil {
		t.Fatalf("Status after no-timeout window: %v", err)
	}
	if len(resp.GetServers()) != 2 {
		t.Errorf("servers = %d, want 2", len(resp.GetServers()))
	}
}
