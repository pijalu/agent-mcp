package session

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/pijalu/agent-mcp/internal/config"
	"github.com/pijalu/agent-mcp/internal/version"
)

// DialFunc opens an initialized client session for one configured server.
// It is a field on Manager so tests can dial fixtures instead of processes.
type DialFunc func(ctx context.Context, name string, cfg config.ServerConfig) (*mcp.ClientSession, error)

// Event is a server-originated MCP notification.
type Event struct {
	Server string
	Type   string
	JSON   []byte
}

// entry is one server's session slot. mu serializes calls on the session:
// the MCP spec allows one in-flight request per JSON-RPC id space, and
// serializing keeps the wire ordering deterministic for callers.
type entry struct {
	mu        sync.Mutex
	session   *mcp.ClientSession
	closed    bool
	lastError string
}

// Manager owns one client session per configured server, opened lazily.
type Manager struct {
	mu      sync.Mutex
	entries map[string]*entry
	configs map[string]config.ServerConfig
	dial    DialFunc
	events  map[int]chan Event
	filters map[int]string
	nextID  int
}

// NewManager returns a Manager for the given server configs. dial may be nil
// to use the default go-sdk dialer.
func NewManager(configs map[string]config.ServerConfig, dial DialFunc) *Manager {
	m := &Manager{entries: make(map[string]*entry), configs: configs, events: make(map[int]chan Event), filters: make(map[int]string)}
	if dial == nil {
		m.dial = func(ctx context.Context, name string, cfg config.ServerConfig) (*mcp.ClientSession, error) {
			return m.defaultDial(ctx, name, cfg)
		}
	} else {
		m.dial = dial
	}
	return m
}

// defaultDial builds a transport from cfg and connects a new go-sdk client.
func (m *Manager) defaultDial(ctx context.Context, name string, cfg config.ServerConfig) (*mcp.ClientSession, error) {
	transport, err := NewTransport(cfg)
	if err != nil {
		return nil, err
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "agent-mcp", Version: version.Version}, &mcp.ClientOptions{
		ProgressNotificationHandler: func(_ context.Context, p *mcp.ProgressNotificationClientRequest) {
			m.publish(name, "progress", p.Params)
		},
		ToolListChangedHandler: func(_ context.Context, p *mcp.ToolListChangedRequest) { m.publish(name, "tools_changed", p.Params) },
		ResourceListChangedHandler: func(_ context.Context, p *mcp.ResourceListChangedRequest) {
			m.publish(name, "resources_changed", p.Params)
		},
		PromptListChangedHandler: func(_ context.Context, p *mcp.PromptListChangedRequest) { m.publish(name, "prompts_changed", p.Params) },
	})
	session, err := client.Connect(ctx, transport, nil)
	if err != nil {
		return nil, fmt.Errorf("session: connect %q: %w", name, err)
	}
	return session, nil
}

// Subscribe returns events matching server, and cancellation cleanup function.
func (m *Manager) Subscribe(server string) (<-chan Event, func()) {
	m.mu.Lock()
	m.nextID++
	id := m.nextID
	ch := make(chan Event, 32)
	m.events[id] = ch
	m.filters[id] = server
	m.mu.Unlock()
	return ch, func() {
		m.mu.Lock()
		if current, ok := m.events[id]; ok {
			delete(m.events, id)
			delete(m.filters, id)
			close(current)
		}
		m.mu.Unlock()
	}
}

func (m *Manager) publish(server, typ string, value any) {
	raw, err := json.Marshal(value)
	if err != nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, ch := range m.events {
		if m.filters[id] != "" && m.filters[id] != server {
			continue
		}
		select {
		case ch <- Event{Server: server, Type: typ, JSON: raw}:
		default:
		}
	}
}

// Open returns the live session for name, dialing on first use. Unknown
// server names and closed servers are errors.
func (m *Manager) Open(ctx context.Context, name string) (*mcp.ClientSession, error) {
	var session *mcp.ClientSession
	err := m.call(ctx, name, func(s *mcp.ClientSession) error { session = s; return nil })
	if err != nil {
		return nil, err
	}
	return session, nil
}

// slot returns the entry for name, creating it under the Manager lock.
func (m *Manager) slot(name string) *entry {
	m.mu.Lock()
	defer m.mu.Unlock()
	e, ok := m.entries[name]
	if !ok {
		e = &entry{}
		m.entries[name] = e
	}
	return e
}
