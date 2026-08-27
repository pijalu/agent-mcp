package session

import (
	"context"
	"errors"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/pijalu/agent-mcp/internal/config"
)

// wantInfo asserts one server's status entry.
func wantInfo(t *testing.T, infos []ServerInfo, idx int, state string, active int) {
	t.Helper()
	info := infos[idx]
	if info.State != state {
		t.Errorf("%s state = %q, want %q", info.Name, info.State, state)
	}
	if info.ActiveSessions != active {
		t.Errorf("%s active = %d, want %d", info.Name, info.ActiveSessions, active)
	}
}

// TestServerStatusesStates covers idle → connected → stopped transitions
// in deterministic name order.
func TestServerStatusesStates(t *testing.T) {
	m := NewManager(testConfigs(), fixtureDialer(t))

	infos := m.ServerStatuses()
	if len(infos) != 2 || infos[0].Name != "alpha" || infos[1].Name != "beta" {
		t.Fatalf("statuses = %+v, want [alpha beta]", infos)
	}
	wantInfo(t, infos, 0, "idle", 0)

	if _, err := m.ListTools(context.Background(), "alpha", nil); err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	wantInfo(t, m.ServerStatuses(), 0, "connected", 1)

	if err := m.Close("alpha"); err != nil {
		t.Fatalf("Close: %v", err)
	}
	infos = m.ServerStatuses()
	wantInfo(t, infos, 0, "stopped", 0)
	wantInfo(t, infos, 1, "idle", 0)
}

// TestServerStatusesLastError records dial failures for operators.
func TestServerStatusesLastError(t *testing.T) {
	boom := errors.New("dial boom")
	m := NewManager(testConfigs(), func(context.Context, string, config.ServerConfig) (*mcp.ClientSession, error) {
		return nil, boom
	})
	if _, err := m.Open(context.Background(), "alpha"); !errors.Is(err, boom) {
		t.Fatalf("Open err = %v, want %v", err, boom)
	}
	infos := m.ServerStatuses()
	if infos[0].LastError != boom.Error() {
		t.Errorf("last error = %q, want %q", infos[0].LastError, boom)
	}
	if infos[0].State != "idle" {
		t.Errorf("state = %q after failed dial, want idle", infos[0].State)
	}
}
