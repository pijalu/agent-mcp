package cli

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/pijalu/agent-mcp/internal/output"
	pb "github.com/pijalu/agent-mcp/proto/agentmcp/v1"
)

// failingWriter always fails, standing in for a closed stdout.
type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("broken pipe") }

// TestTransportReasonStripsGRPCWrapper keeps the unreachable-daemon message
// readable: the gRPC status and the "Error while dialing" wrapper must not
// reach the envelope, but the underlying cause must.
func TestTransportReasonStripsGRPCWrapper(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want string
	}{
		{
			name: "transport failure unwrapped",
			err: status.Error(codes.Unavailable,
				`connection error: desc = "transport: Error while dialing: dial tcp 127.0.0.1:1: connect: connection refused"`),
			want: "dial tcp 127.0.0.1:1: connect: connection refused",
		},
		{
			name: "plain status message",
			err:  status.Error(codes.Unavailable, "daemon closed the connection"),
			want: "daemon closed the connection",
		},
		{
			name: "non status error",
			err:  errors.New("boom"),
			want: "boom",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := transportReason(tc.err)
			if got != tc.want {
				t.Errorf("transportReason() = %q, want %q", got, tc.want)
			}
			for _, leak := range []string{"desc =", "Error while dialing", "code ="} {
				if strings.Contains(got, leak) {
					t.Errorf("transportReason() = %q leaks %q", got, leak)
				}
			}
		})
	}
}

// TestServerNamesAreSorted keeps the candidate hint stable across daemon map
// iteration order.
func TestServerNamesAreSorted(t *testing.T) {
	st := &pb.StatusResponse{Servers: []*pb.ServerStatus{
		{Name: "zeta"}, {Name: "alpha"}, {Name: "mid"},
	}}
	got := serverNames(st)
	want := []string{"alpha", "mid", "zeta"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("serverNames() = %v, want %v", got, want)
	}
	if empty := serverNames(&pb.StatusResponse{}); empty == nil || len(empty) != 0 {
		t.Errorf("serverNames(empty) = %v, want an empty non-nil slice", empty)
	}
}

// TestWriteEventReportsSinkFailure keeps a broken stdout from reporting success
// or a raw I/O error.
func TestWriteEventReportsSinkFailure(t *testing.T) {
	event := &pb.SessionEvent{Server: "fake", Type: "progress", Json: []byte(`{"progress":1}`)}
	for _, format := range []output.Format{output.FormatJSON, output.FormatText} {
		t.Run(string(format), func(t *testing.T) {
			env := &clientEnv{format: format}
			err := env.writeEvent(failingWriter{}, event)
			if !errors.Is(err, output.ErrInternal) {
				t.Fatalf("err = %v, want an internal error", err)
			}
			if !strings.Contains(err.Error(), "write event") {
				t.Errorf("err = %v, want the write context", err)
			}
			if output.ExitCode(err) != 3 {
				t.Errorf("exit = %d, want 3", output.ExitCode(err))
			}
		})
	}
}

// TestStreamFailurePrefersTheDeadline pins the documented end of a bounded
// follow: the call deadline is reported as the deadline, not as an RPC failure.
func TestStreamFailurePrefersTheDeadline(t *testing.T) {
	env := &clientEnv{global: GlobalFlags{callTimeout: 30 * time.Second}}
	ctx, cancel := context.WithTimeout(context.Background(), -time.Second) // already expired
	defer cancel()
	err := env.streamFailure(ctx, status.Error(codes.DeadlineExceeded, "context deadline exceeded"))
	if !errors.Is(err, output.ErrMCP) {
		t.Fatalf("err = %v, want an MCP error", err)
	}
	if !strings.Contains(err.Error(), "--call-timeout 30s") {
		t.Errorf("err = %v, want it to name the timeout", err)
	}
	if strings.Contains(err.Error(), "rpc error") {
		t.Errorf("err = %v must not leak the transport", err)
	}
}
