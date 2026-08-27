package session

import (
	"context"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// call serializes fn against the session for name. Wrappers stay thin: all
// params — including pagination cursors — pass through to the SDK verbatim.
func (m *Manager) call(ctx context.Context, name string, fn func(*mcp.ClientSession) error) error {
	cfg, ok := m.configs[name]
	if !ok {
		return m.unknownServerError(name)
	}
	e := m.slot(name)
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed {
		return fmt.Errorf("session: server %q is closed", name)
	}
	if e.session == nil {
		session, err := m.dial(ctx, name, cfg)
		if err != nil {
			e.lastError = cleanErrorText(err.Error())
			return err
		}
		e.session = session
	}
	err := fn(e.session)
	if err != nil {
		e.lastError = cleanErrorText(err.Error())
	}
	return err
}

// cleanErrorText drops the go-sdk transport's method wrapper from a recorded
// error: `calling "tools/call": unknown tool "x"` becomes
// `unknown tool "x"`. The method name is client plumbing an operator cannot
// act on, and last_error is read out of context by list-servers and server
// status, where the server's own words are the whole message.
func cleanErrorText(msg string) string {
	for {
		rest, ok := strings.CutPrefix(msg, `calling "`)
		if !ok {
			return msg
		}
		_, tail, ok := strings.Cut(rest, `": `)
		if !ok {
			return msg
		}
		msg = tail
	}
}

// CallTool invokes a tool on the named server. A tool the server does not know
// comes back with the server's tool names attached, so the caller can correct
// --tool without a probe round trip of its own.
func (m *Manager) CallTool(ctx context.Context, name string, params *mcp.CallToolParams) (*mcp.CallToolResult, error) {
	var result *mcp.CallToolResult
	err := m.call(ctx, name, func(s *mcp.ClientSession) error {
		var err error
		result, err = s.CallTool(ctx, params)
		return err
	})
	return result, m.unknownToolHint(ctx, name, err)
}

// ListTools lists tools on the named server; params.Cursor is passed verbatim.
func (m *Manager) ListTools(ctx context.Context, name string, params *mcp.ListToolsParams) (*mcp.ListToolsResult, error) {
	var result *mcp.ListToolsResult
	err := m.call(ctx, name, func(s *mcp.ClientSession) error {
		var err error
		result, err = s.ListTools(ctx, params)
		return err
	})
	return result, err
}

// ListResources lists resources on the named server; params.Cursor is passed verbatim.
func (m *Manager) ListResources(ctx context.Context, name string, params *mcp.ListResourcesParams) (*mcp.ListResourcesResult, error) {
	var result *mcp.ListResourcesResult
	err := m.call(ctx, name, func(s *mcp.ClientSession) error {
		var err error
		result, err = s.ListResources(ctx, params)
		return err
	})
	return result, err
}

// ReadResource reads one resource from the named server.
func (m *Manager) ReadResource(ctx context.Context, name string, params *mcp.ReadResourceParams) (*mcp.ReadResourceResult, error) {
	var result *mcp.ReadResourceResult
	err := m.call(ctx, name, func(s *mcp.ClientSession) error {
		var err error
		result, err = s.ReadResource(ctx, params)
		return err
	})
	return result, err
}

// ListPrompts lists prompts on the named server; params.Cursor is passed verbatim.
func (m *Manager) ListPrompts(ctx context.Context, name string, params *mcp.ListPromptsParams) (*mcp.ListPromptsResult, error) {
	var result *mcp.ListPromptsResult
	err := m.call(ctx, name, func(s *mcp.ClientSession) error {
		var err error
		result, err = s.ListPrompts(ctx, params)
		return err
	})
	return result, err
}

// GetPrompt gets one prompt from the named server.
func (m *Manager) GetPrompt(ctx context.Context, name string, params *mcp.GetPromptParams) (*mcp.GetPromptResult, error) {
	var result *mcp.GetPromptResult
	err := m.call(ctx, name, func(s *mcp.ClientSession) error {
		var err error
		result, err = s.GetPrompt(ctx, params)
		return err
	})
	return result, err
}
