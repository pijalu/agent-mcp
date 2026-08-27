package cli_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/pijalu/agent-mcp/internal/cli"
	"github.com/pijalu/agent-mcp/internal/daemon"
	"github.com/pijalu/agent-mcp/internal/session"
	pb "github.com/pijalu/agent-mcp/proto/agentmcp/v1"
)

// streamingManager serves one canned notification per subscription: the event
// is delivered and the stream then ends cleanly, so a follow command returns
// without waiting for the call deadline.
type streamingManager struct {
	fakeManager
	event session.Event
}

func (m streamingManager) Subscribe(string) (<-chan session.Event, func()) {
	ch := make(chan session.Event, 1)
	go func() {
		ch <- m.event
		close(ch)
	}()
	return ch, func() {}
}

// silentManager accepts subscriptions and never produces an event.
type silentManager struct{ fakeManager }

func (silentManager) Subscribe(string) (<-chan session.Event, func()) {
	return make(chan session.Event), func() {}
}

// startDaemonWith runs a real gRPC daemon backed by mgr on loopback.
func startDaemonWith(t *testing.T, mgr daemon.SessionManager) string {
	t.Helper()
	d := daemon.New(mgr, daemon.Options{})
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	go func() { _ = d.Serve(lis) }()
	t.Cleanup(func() { d.Shutdown() })
	return lis.Addr().String()
}

// dialRefused stands in for a daemon that is not listening at all.
func dialRefused(context.Context, string) (pb.AgentMCPClient, io.Closer, error) {
	return nil, nil, errors.New("connection refused")
}

// TestEventsRequiresDaemon covers the trap where `events --no-server` fell
// through to the one-shot path and reported `internal error: unknown command
// "events"` with the internal exit code.
func TestEventsRequiresDaemon(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := cli.Run([]string{"events", "--no-server"}, &stdout, &stderr)
	if code != 2 {
		t.Fatalf("exit = %d, want 2 (out %s)", code, stdout.String())
	}
	out := stdout.String()
	if strings.Contains(out, "unknown command") {
		t.Errorf("out %q must not report an unknown command", out)
	}
	if !strings.Contains(out, "events requires the daemon") {
		t.Errorf("out %q must say events needs the daemon", out)
	}
	if !strings.Contains(out, `"code":"usage"`) {
		t.Errorf("out %q must be a usage failure", out)
	}
}

// TestEventsStreamsNotifications pins the happy path: one JSON line per
// notification, and a stream that ends cleanly exits 0.
func TestEventsStreamsNotifications(t *testing.T) {
	addr := startDaemonWith(t, streamingManager{event: session.Event{
		Server: "fake", Type: "progress", JSON: []byte(`{"progress":1}`),
	}})

	t.Run("json", func(t *testing.T) {
		code, out, errOut := runWith(t, addr, "events")
		if code != 0 {
			t.Fatalf("exit = %d, out %s, err %s", code, out, errOut)
		}
		for _, want := range []string{`"server":"fake"`, `"type":"progress"`, `"progress":1`} {
			if !strings.Contains(out, want) {
				t.Errorf("out %q missing %q", out, want)
			}
		}
	})

	t.Run("named server", func(t *testing.T) {
		code, out, _ := runWith(t, addr, "events", "--server", "fake")
		if code != 0 || !strings.Contains(out, `"type":"progress"`) {
			t.Errorf("exit = %d, out %s", code, out)
		}
	})

	t.Run("text", func(t *testing.T) {
		code, out, _ := runWith(t, addr, "events", "--format", "text")
		if code != 0 {
			t.Fatalf("exit = %d, out %s", code, out)
		}
		if !strings.Contains(out, `fake progress {"progress":1}`) {
			t.Errorf("out %q must be one server type json line", out)
		}
	})
}

// TestEventsUnknownServerFailsFast covers the trap where a typo'd --server was
// never checked, so the client held a subscription that could not receive
// anything and reported a raw DeadlineExceeded after the whole 30s timeout.
func TestEventsUnknownServerFailsFast(t *testing.T) {
	addr := startDaemonWith(t, fakeManager{})
	start := time.Now()
	code, out, _ := runWith(t, addr, "events", "--server", "nosuch")
	elapsed := time.Since(start)
	if code != 2 {
		t.Fatalf("exit = %d, want 2 (out %s)", code, out)
	}
	if !strings.Contains(out, `unknown server \"nosuch\"`) && !strings.Contains(out, `unknown server "nosuch"`) {
		t.Errorf("out %q must name the unknown server", out)
	}
	if !strings.Contains(out, "configured servers: fake") {
		t.Errorf("out %q must name the candidates", out)
	}
	if strings.Contains(out, "DeadlineExceeded") || strings.Contains(out, "rpc error") {
		t.Errorf("out %q leaks the transport", out)
	}
	if elapsed > 2*time.Second {
		t.Errorf("took %s, want a fast rejection instead of the call timeout", elapsed)
	}
}

// TestEventsMapsSubscriptionFailure covers the trap where a failed subscription
// bypassed the error mapping and surfaced the raw gRPC status chain with the
// internal exit code.
func TestEventsMapsSubscriptionFailure(t *testing.T) {
	// fakeManager serves status but cannot subscribe, so the daemon answers
	// with Unimplemented.
	addr := startDaemonWith(t, fakeManager{})
	code, out, _ := runWith(t, addr, "events", "--server", "fake")
	if code != 1 {
		t.Fatalf("exit = %d, want 1 (out %s)", code, out)
	}
	if strings.Contains(out, "rpc error") || strings.Contains(out, "code = Unimplemented") {
		t.Errorf("out %q must not leak the gRPC status chain", out)
	}
	if !strings.Contains(out, "event subscriptions unavailable") {
		t.Errorf("out %q must carry the daemon's own message", out)
	}
}

// TestEventsDeadlineIsReported covers the follow that outlives every event: the
// call timeout must end it with an actionable message, not a raw RPC deadline.
func TestEventsDeadlineIsReported(t *testing.T) {
	addr := startDaemonWith(t, silentManager{})
	start := time.Now()
	code, out, _ := runWith(t, addr, "events", "--call-timeout", "300ms")
	elapsed := time.Since(start)
	if code != 1 {
		t.Fatalf("exit = %d, want 1 (out %s)", code, out)
	}
	if !strings.Contains(out, "no event within the --call-timeout 300ms deadline") {
		t.Errorf("out %q must name the deadline", out)
	}
	if strings.Contains(out, "rpc error") || strings.Contains(out, "DeadlineExceeded") {
		t.Errorf("out %q must not leak the transport", out)
	}
	if elapsed < 300*time.Millisecond || elapsed > 2*time.Second {
		t.Errorf("took %s, want ~300ms", elapsed)
	}
}

// TestEventsUnreachableDaemonIsClean covers the trap where a daemon that is not
// running was either auto-started behind the caller's back or reported as an
// internal failure. events needs a running daemon and says so.
func TestEventsUnreachableDaemonIsClean(t *testing.T) {
	spawned := false
	deps := cli.Deps{
		Dial: dialRefused,
		SpawnDaemon: func(cli.GlobalFlags) (cli.DaemonChild, error) {
			spawned = true
			return nil, errors.New("spawn must not be attempted")
		},
	}
	var stdout, stderr bytes.Buffer
	start := time.Now()
	code := cli.RunWith(deps, []string{"events", "--connect", "127.0.0.1:1"}, &stdout, &stderr)
	elapsed := time.Since(start)
	out := stdout.String()
	if code != 1 {
		t.Fatalf("exit = %d, want 1 (out %s)", code, out)
	}
	if spawned {
		t.Error("events must not auto-start a daemon")
	}
	if strings.Contains(stderr.String(), "auto-starting") {
		t.Errorf("stderr %q must not announce an auto-start", stderr.String())
	}
	if !strings.Contains(out, "unreachable") || !strings.Contains(out, "events requires a running daemon") {
		t.Errorf("out %q must say the daemon is unreachable and how to start it", out)
	}
	if strings.Contains(out, "rpc error") {
		t.Errorf("out %q must not leak the transport", out)
	}
	if elapsed > 2*time.Second {
		t.Errorf("took %s, want a fast failure", elapsed)
	}
}
