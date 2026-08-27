package session

import (
	"fmt"
	"net/http"
	"os"
	"os/exec"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/pijalu/agent-mcp/internal/config"
)

// NewTransport builds the MCP client transport implied by cfg.
//
// The transport kind is derived from the config entry: a command means stdio,
// otherwise cfg.Transport selects between the HTTP variants.
func NewTransport(cfg config.ServerConfig) (mcp.Transport, error) {
	if cfg.Command != "" {
		return stdioTransport(cfg), nil
	}
	switch cfg.Transport {
	case "sse":
		return &mcp.SSEClientTransport{
			Endpoint:   cfg.URL,
			HTTPClient: httpClient(cfg.Headers),
		}, nil
	case "streamable":
		return &mcp.StreamableClientTransport{
			Endpoint:   cfg.URL,
			HTTPClient: httpClient(cfg.Headers),
		}, nil
	default:
		return nil, fmt.Errorf("session: unknown transport %q for url %q", cfg.Transport, cfg.URL)
	}
}

// stdioTransport prepares the server subprocess; env from cfg is appended to
// the inherited environment so operators can override single variables.
func stdioTransport(cfg config.ServerConfig) *mcp.CommandTransport {
	cmd := exec.Command(cfg.Command, cfg.Args...)
	// Keep teardown bounded even when a stdio server ignores EOF. The SDK then
	// escalates from SIGTERM to SIGKILL after this interval.
	cmd.SysProcAttr = serverProcAttr()
	if len(cfg.Env) > 0 {
		cmd.Env = os.Environ()
		for k, v := range cfg.Env {
			cmd.Env = append(cmd.Env, k+"="+v)
		}
	}
	return &mcp.CommandTransport{Command: cmd, TerminateDuration: serverTerminateDuration}
}

// httpClient returns nil when no per-server headers are configured, letting
// the SDK fall back to its default client.
func httpClient(headers map[string]string) *http.Client {
	if len(headers) == 0 {
		return nil
	}
	return &http.Client{Transport: headerRoundTripper{headers: headers}}
}

// headerRoundTripper injects static per-server headers into every request.
type headerRoundTripper struct {
	headers map[string]string
}

func (t headerRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	clone := req.Clone(req.Context())
	for k, v := range t.headers {
		clone.Header.Set(k, v)
	}
	return http.DefaultTransport.RoundTrip(clone)
}
