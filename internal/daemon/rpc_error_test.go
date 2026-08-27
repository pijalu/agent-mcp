package daemon

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/pijalu/agent-mcp/internal/session"
)

// TestRPCErrorClassifiesUnknownServer pins bad parameters to InvalidArgument so
// the client can report them as usage failures rather than MCP failures.
func TestRPCErrorClassifiesUnknownServer(t *testing.T) {
	err := fmt.Errorf("%w %q", session.ErrUnknownServer, "nosuch")
	got := rpcError(err)
	if code := status.Code(got); code != codes.InvalidArgument {
		t.Fatalf("code = %v, want InvalidArgument", code)
	}
	msg := status.Convert(got).Message()
	if strings.Contains(msg, "session:") {
		t.Errorf("internal package prefix leaked: %q", msg)
	}
	if !strings.Contains(msg, "unknown server") {
		t.Errorf("message = %q, want it to name the problem", msg)
	}
}

// TestRPCErrorDefaultsToInternal keeps unexpected failures internal.
func TestRPCErrorDefaultsToInternal(t *testing.T) {
	got := rpcError(errors.New("boom"))
	if code := status.Code(got); code != codes.Internal {
		t.Fatalf("code = %v, want Internal", code)
	}
	if msg := status.Convert(got).Message(); msg != "boom" {
		t.Errorf("message = %q, want %q", msg, "boom")
	}
}

// TestRPCErrorPassthrough keeps existing statuses and nil untouched.
func TestRPCErrorPassthrough(t *testing.T) {
	orig := status.Error(codes.NotFound, "x")
	if got := rpcError(orig); got != orig {
		t.Errorf("rpcError = %v, want passthrough", got)
	}
	if rpcError(nil) != nil {
		t.Error("rpcError(nil) must be nil")
	}
}
