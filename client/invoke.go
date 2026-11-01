// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package client

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/pijalu/agent-mcp/internal/session"
	pb "github.com/pijalu/agent-mcp/proto/agentmcp/v1"
)

// Manager is the backend a Request runs against in NoServer mode. It is a
// type alias of the daemon's session-manager interface, so *session.Manager
// satisfies it directly and any other implementation with the same method set
// is interchangeable.
type Manager = ManagerCore

// ManagerCore is the structural interface shared by the daemon and the
// library: seven MCP operations plus status and teardown. Declared here so
// external modules can implement the backend without importing the daemon
// package.
type ManagerCore interface {
	CallTool(ctx context.Context, name string, params *mcp.CallToolParams) (*mcp.CallToolResult, error)
	ListTools(ctx context.Context, name string, params *mcp.ListToolsParams) (*mcp.ListToolsResult, error)
	ListResources(ctx context.Context, name string, params *mcp.ListResourcesParams) (*mcp.ListResourcesResult, error)
	ReadResource(ctx context.Context, name string, params *mcp.ReadResourceParams) (*mcp.ReadResourceResult, error)
	ListPrompts(ctx context.Context, name string, params *mcp.ListPromptsParams) (*mcp.ListPromptsResult, error)
	GetPrompt(ctx context.Context, name string, params *mcp.GetPromptParams) (*mcp.GetPromptResult, error)
	CloseAll() int
	ServerStatuses() []session.ServerInfo
}

// ClosableManager is a Manager whose teardown reports how many sessions it
// closed.
type ClosableManager = Manager

// InvokeManager runs r against an in-process manager (NoServer mode) and
// returns the raw result for shaping. Error mapping matches the daemon path:
// unknown servers and tools are caller mistakes (usage class), everything
// else is an MCP failure, and a tool-reported error result surfaces as an
// error, not a success payload.
func InvokeManager(ctx context.Context, mgr Manager, r Request) (any, error) {
	server := r.Server
	switch r.Op {
	case OpListServers:
		return mgr.ServerStatuses(), nil
	case OpListTools:
		return wrapMCPResult(mgr.ListTools(ctx, server, &mcp.ListToolsParams{Cursor: r.Cursor}))
	case OpCallTool:
		params, err := toolParams(r)
		if err != nil {
			return nil, err
		}
		result, err := mgr.CallTool(ctx, server, params)
		if err != nil {
			return nil, SessionFailure(err)
		}
		return result, CallToolError(result)
	case OpListResources:
		return wrapMCPResult(mgr.ListResources(ctx, server, &mcp.ListResourcesParams{Cursor: r.Cursor}))
	case OpReadResource:
		return wrapMCPResult(mgr.ReadResource(ctx, server, &mcp.ReadResourceParams{URI: r.URI}))
	case OpListPrompts:
		return wrapMCPResult(mgr.ListPrompts(ctx, server, &mcp.ListPromptsParams{Cursor: r.Cursor}))
	case OpGetPrompt:
		args, err := argsMap(r)
		if err != nil {
			return nil, err
		}
		return wrapMCPResult(mgr.GetPrompt(ctx, server, &mcp.GetPromptParams{Name: r.Prompt, Arguments: args}))
	}
	return nil, fmt.Errorf("%w: unknown command %q", ErrInternal, r.Op)
}

// InvokeDaemon runs r against a daemon RPC client and returns the raw result
// decoded as generic JSON for shaping.
func InvokeDaemon(ctx context.Context, cl pb.AgentMCPClient, r Request) (any, error) {
	switch r.Op {
	case OpListServers:
		resp, err := cl.ListServers(ctx, &pb.ListServersRequest{})
		return DecodeRaw(resp.GetJson(), err)
	case OpListTools:
		raw, err := r.cursorParams()
		if err != nil {
			return nil, err
		}
		resp, err := cl.ListTools(ctx, &pb.ListToolsRequest{Server: r.Server, Json: raw})
		return DecodeRaw(resp.GetJson(), err)
	case OpCallTool:
		params, err := toolParams(r)
		if err != nil {
			return nil, err
		}
		raw, err := json.Marshal(params)
		if err != nil {
			return nil, fmt.Errorf("%w: encode params: %v", ErrInternal, err)
		}
		resp, err := cl.CallTool(ctx, &pb.CallToolRequest{Server: r.Server, Json: raw})
		value, err := DecodeRaw(resp.GetJson(), err)
		if err != nil {
			return nil, err
		}
		return value, ToolErrorResult(value)
	case OpListResources:
		raw, err := r.cursorParams()
		if err != nil {
			return nil, err
		}
		resp, err := cl.ListResources(ctx, &pb.ListResourcesRequest{Server: r.Server, Json: raw})
		return DecodeRaw(resp.GetJson(), err)
	case OpReadResource:
		raw, err := json.Marshal(&mcp.ReadResourceParams{URI: r.URI})
		if err != nil {
			return nil, err
		}
		resp, err := cl.ReadResource(ctx, &pb.ReadResourceRequest{Server: r.Server, Json: raw})
		return DecodeRaw(resp.GetJson(), err)
	case OpListPrompts:
		raw, err := r.cursorParams()
		if err != nil {
			return nil, err
		}
		resp, err := cl.ListPrompts(ctx, &pb.ListPromptsRequest{Server: r.Server, Json: raw})
		return DecodeRaw(resp.GetJson(), err)
	case OpGetPrompt:
		args, err := argsMap(r)
		if err != nil {
			return nil, err
		}
		raw, err := json.Marshal(&mcp.GetPromptParams{Name: r.Prompt, Arguments: args})
		if err != nil {
			return nil, fmt.Errorf("%w: encode params: %v", ErrInternal, err)
		}
		resp, err := cl.GetPrompt(ctx, &pb.GetPromptRequest{Server: r.Server, Json: raw})
		return DecodeRaw(resp.GetJson(), err)
	}
	return nil, fmt.Errorf("%w: unknown command %q", ErrInternal, r.Op)
}

// DecodeRaw unwraps an RPC: err non-nil maps to a client failure, otherwise
// raw decodes into generic JSON for shaping.
func DecodeRaw(raw []byte, err error) (any, error) {
	if err != nil {
		return nil, RPCFailure(err)
	}
	var v any
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &v); err != nil {
			return nil, fmt.Errorf("%w: decode daemon response: %v", ErrInternal, err)
		}
	}
	return v, nil
}

// toolParams builds CallToolParams from a request.
func toolParams(r Request) (*mcp.CallToolParams, error) {
	args, err := r.toolArguments()
	if err != nil {
		return nil, err
	}
	return &mcp.CallToolParams{Name: r.Tool, Arguments: args}, nil
}

// argsMap decodes request args as a string map for get-prompt.
func argsMap(r Request) (map[string]string, error) {
	return r.promptArguments()
}

// wrapMCPResult maps session-layer errors onto the client sentinels.
func wrapMCPResult[T any](result *T, err error) (*T, error) {
	if err != nil {
		return nil, SessionFailure(err)
	}
	return result, nil
}

// The real session manager satisfies the library backend interface, so it can
// be handed to InvokeManager directly.
var _ Manager = (*session.Manager)(nil)
