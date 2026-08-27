package session

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/pijalu/agent-mcp/internal/config"
)

func TestNewTransportStdio(t *testing.T) {
	tr, err := NewTransport(config.ServerConfig{Command: "mcp-echo", Args: []string{"--fast"}})
	if err != nil {
		t.Fatalf("NewTransport: %v", err)
	}
	cmd, ok := tr.(*mcp.CommandTransport)
	if !ok {
		t.Fatalf("transport = %T, want *mcp.CommandTransport", tr)
	}
	if cmd.Command.Path != "mcp-echo" {
		t.Errorf("command path = %q, want mcp-echo", cmd.Command.Path)
	}
}

func TestNewTransportStdioEnv(t *testing.T) {
	tr, err := NewTransport(config.ServerConfig{
		Command: "mcp-echo",
		Env:     map[string]string{"API_KEY": "x"},
	})
	if err != nil {
		t.Fatalf("NewTransport: %v", err)
	}
	cmd := tr.(*mcp.CommandTransport)
	for _, kv := range cmd.Command.Env {
		if kv == "API_KEY=x" {
			return
		}
	}
	t.Errorf("env %v lacks API_KEY=x", cmd.Command.Env)
}

func TestNewTransportHTTP(t *testing.T) {
	tests := []struct {
		name string
		cfg  config.ServerConfig
		want func(mcp.Transport) bool
	}{
		{
			name: "sse",
			cfg:  config.ServerConfig{URL: "http://localhost:9000/sse", Transport: "sse"},
			want: func(tr mcp.Transport) bool {
				sse, ok := tr.(*mcp.SSEClientTransport)
				return ok && sse.Endpoint == "http://localhost:9000/sse"
			},
		},
		{
			name: "streamable",
			cfg:  config.ServerConfig{URL: "http://localhost:9000/mcp", Transport: "streamable"},
			want: func(tr mcp.Transport) bool {
				st, ok := tr.(*mcp.StreamableClientTransport)
				return ok && st.Endpoint == "http://localhost:9000/mcp"
			},
		},
		{
			name: "headers produce custom http client",
			cfg: config.ServerConfig{
				URL:       "http://localhost:9000/mcp",
				Transport: "streamable",
				Headers:   map[string]string{"Authorization": "Bearer t"},
			},
			want: func(tr mcp.Transport) bool {
				return tr.(*mcp.StreamableClientTransport).HTTPClient != nil
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tr, err := NewTransport(tt.cfg)
			if err != nil {
				t.Fatalf("NewTransport: %v", err)
			}
			if !tt.want(tr) {
				t.Errorf("NewTransport(%+v) = %T, predicate failed", tt.cfg, tr)
			}
		})
	}
}

func TestNewTransportUnknown(t *testing.T) {
	_, err := NewTransport(config.ServerConfig{URL: "http://localhost:9000", Transport: "grpc"})
	if err == nil {
		t.Fatal("NewTransport(grpc): want error, got nil")
	}
}

// TestHeaderRoundTripper verifies configured headers reach outbound requests.
func TestHeaderRoundTripper(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	client := httpClient(map[string]string{"Authorization": "Bearer secret"})
	resp, err := client.Get(srv.URL)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	if gotAuth != "Bearer secret" {
		t.Errorf("Authorization header = %q, want %q", gotAuth, "Bearer secret")
	}
}

// TestHeaderRoundTripperPreservesExisting confirms Set (not Add) semantics.
func TestHeaderRoundTripperPreservesExisting(t *testing.T) {
	rt := headerRoundTripper{headers: map[string]string{"X-Test": "override"}}
	req, err := http.NewRequest("GET", "http://example.invalid", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("X-Test", "original")
	// The transport is unreachable; we only exercise the clone path up to the
	// network call and accept the dial error.
	_, _ = rt.RoundTrip(req)
	if strings.Contains(req.Header.Get("X-Test"), "override") {
		t.Error("RoundTrip must not mutate the original request")
	}
}
