package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/pijalu/agent-mcp/internal/daemon"
	"github.com/pijalu/agent-mcp/internal/output"
	"github.com/pijalu/agent-mcp/internal/session"
	pb "github.com/pijalu/agent-mcp/proto/agentmcp/v1"
)

// invokeDaemon runs the command against the daemon and returns the raw
// result decoded as generic JSON for the output layer.
func (env *clientEnv) invokeDaemon(ctx context.Context, client pb.AgentMCPClient) (any, error) {
	switch env.name {
	case "list-servers":
		resp, err := client.ListServers(ctx, &pb.ListServersRequest{})
		return decodeRaw(resp.GetJson(), err)
	case "list-tools":
		raw, err := env.marshalCursorParams()
		if err != nil {
			return nil, err
		}
		resp, err := client.ListTools(ctx, &pb.ListToolsRequest{Server: env.params.server, Json: raw})
		return decodeRaw(resp.GetJson(), err)
	case "call-tool":
		return env.callToolDaemon(ctx, client)
	case "list-resources":
		raw, err := env.marshalCursorParams()
		if err != nil {
			return nil, err
		}
		resp, err := client.ListResources(ctx, &pb.ListResourcesRequest{Server: env.params.server, Json: raw})
		return decodeRaw(resp.GetJson(), err)
	case "read-resource":
		raw, err := json.Marshal(&mcp.ReadResourceParams{URI: env.params.uri})
		if err != nil {
			return nil, err
		}
		resp, err := client.ReadResource(ctx, &pb.ReadResourceRequest{Server: env.params.server, Json: raw})
		return decodeRaw(resp.GetJson(), err)
	case "list-prompts":
		raw, err := env.marshalCursorParams()
		if err != nil {
			return nil, err
		}
		resp, err := client.ListPrompts(ctx, &pb.ListPromptsRequest{Server: env.params.server, Json: raw})
		return decodeRaw(resp.GetJson(), err)
	case "get-prompt":
		return env.getPromptDaemon(ctx, client)
	}
	return nil, fmt.Errorf("%w: unknown command %q", output.ErrInternal, env.name)
}

// callToolDaemon builds CallTool params and invokes the daemon.
func (env *clientEnv) callToolDaemon(ctx context.Context, client pb.AgentMCPClient) (any, error) {
	params, err := env.toolParams()
	if err != nil {
		return nil, err
	}
	raw, err := json.Marshal(params)
	if err != nil {
		return nil, fmt.Errorf("%w: encode params: %v", output.ErrInternal, err)
	}
	resp, err := client.CallTool(ctx, &pb.CallToolRequest{Server: env.params.server, Json: raw})
	value, err := decodeRaw(resp.GetJson(), err)
	if err != nil {
		return nil, err
	}
	return value, toolErrorResult(value)
}

// getPromptDaemon builds GetPrompt params and invokes the daemon.
func (env *clientEnv) getPromptDaemon(ctx context.Context, client pb.AgentMCPClient) (any, error) {
	args, err := env.argsMap()
	if err != nil {
		return nil, err
	}
	raw, err := json.Marshal(&mcp.GetPromptParams{Name: env.params.prompt, Arguments: args})
	if err != nil {
		return nil, fmt.Errorf("%w: encode params: %v", output.ErrInternal, err)
	}
	resp, err := client.GetPrompt(ctx, &pb.GetPromptRequest{Server: env.params.server, Json: raw})
	return decodeRaw(resp.GetJson(), err)
}

// marshalCursorParams encodes a cursor-only params body.
func (env *clientEnv) marshalCursorParams() ([]byte, error) {
	if env.params.cursor == "" {
		return nil, nil
	}
	raw, err := json.Marshal(map[string]string{"cursor": env.params.cursor})
	if err != nil {
		return nil, fmt.Errorf("%w: encode cursor: %v", output.ErrInternal, err)
	}
	return raw, nil
}

// toolParams builds CallToolParams from parsed flags.
func (env *clientEnv) toolParams() (*mcp.CallToolParams, error) {
	p := &mcp.CallToolParams{Name: env.params.tool}
	if env.params.args == "" {
		return p, nil
	}
	var args map[string]any
	if err := json.Unmarshal([]byte(env.params.args), &args); err != nil {
		return nil, fmt.Errorf("%w: --args is not valid JSON: %v", output.ErrUsage, err)
	}
	p.Arguments = args
	return p, nil
}

// argsMap decodes --args as a string map for get-prompt.
func (env *clientEnv) argsMap() (map[string]string, error) {
	if env.params.args == "" {
		return nil, nil
	}
	var args map[string]string
	if err := json.Unmarshal([]byte(env.params.args), &args); err != nil {
		return nil, fmt.Errorf("%w: --args must be a JSON string map: %v", output.ErrUsage, err)
	}
	return args, nil
}

// decodeRaw unwraps an RPC: err non-nil maps to a CLI failure, otherwise
// raw decodes into generic JSON for the output layer.
func decodeRaw(raw []byte, err error) (any, error) {
	if err != nil {
		return nil, rpcFailure(err)
	}
	var v any
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &v); err != nil {
			return nil, fmt.Errorf("%w: decode daemon response: %v", output.ErrInternal, err)
		}
	}
	return v, nil
}

// rpcFailure converts a daemon RPC failure into a clean CLI error. The gRPC
// status chain is dropped so agents see the problem, not the transport.
func rpcFailure(err error) error {
	st, ok := status.FromError(err)
	if !ok {
		return fmt.Errorf("%w: %v", output.ErrMCP, err)
	}
	msg := trimSessionPrefix(st.Message())
	if st.Code() == codes.InvalidArgument || isUnknownTarget(msg) {
		return fmt.Errorf("%w: %s", output.ErrUsage, msg)
	}
	return fmt.Errorf("%w: %s", output.ErrMCP, msg)
}

// sessionFailure is the in-process equivalent of rpcFailure, for --no-server.
// Unknown servers and tools are caller mistakes, so they map to the usage exit
// code exactly as the daemon-mapped form does.
func sessionFailure(err error) error {
	msg := trimSessionPrefix(err.Error())
	if errors.Is(err, session.ErrUnknownServer) || isUnknownTool(msg) {
		return fmt.Errorf("%w: %s", output.ErrUsage, msg)
	}
	return fmt.Errorf("%w: %v", output.ErrMCP, err)
}

// trimSessionPrefix drops the internal package prefix from a message.
func trimSessionPrefix(msg string) string {
	return strings.TrimPrefix(msg, "session: ")
}

// isUnknownTarget reports whether a message names a missing server or tool.
// servers word this themselves, so the match is best-effort.
func isUnknownTarget(msg string) bool {
	return strings.Contains(msg, "unknown server") || isUnknownTool(msg)
}

// isUnknownTool reports whether a message came from a tool name the server does
// not expose.
func isUnknownTool(msg string) bool { return strings.Contains(msg, "unknown tool") }

// invokeManager runs the command against an in-process session manager.
func invokeManager(ctx context.Context, env *clientEnv, mgr daemon.SessionManager) (any, error) {
	server := env.params.server
	switch env.name {
	case "list-servers":
		return mgr.ServerStatuses(), nil
	case "list-tools":
		return wrapMCPResult(mgr.ListTools(ctx, server, &mcp.ListToolsParams{Cursor: env.params.cursor}))
	case "call-tool":
		params, err := env.toolParams()
		if err != nil {
			return nil, err
		}
		result, err := mgr.CallTool(ctx, server, params)
		if err != nil {
			return nil, sessionFailure(err)
		}
		return result, callToolError(result)
	case "list-resources":
		return wrapMCPResult(mgr.ListResources(ctx, server, &mcp.ListResourcesParams{Cursor: env.params.cursor}))
	case "read-resource":
		return wrapMCPResult(mgr.ReadResource(ctx, server, &mcp.ReadResourceParams{URI: env.params.uri}))
	case "list-prompts":
		return wrapMCPResult(mgr.ListPrompts(ctx, server, &mcp.ListPromptsParams{Cursor: env.params.cursor}))
	case "get-prompt":
		args, err := env.argsMap()
		if err != nil {
			return nil, err
		}
		return wrapMCPResult(mgr.GetPrompt(ctx, server, &mcp.GetPromptParams{Name: env.params.prompt, Arguments: args}))
	}
	return nil, fmt.Errorf("%w: unknown command %q", output.ErrInternal, env.name)
}

// wrapMCPResult maps session-layer errors onto the CLI sentinels.
func wrapMCPResult[T any](result *T, err error) (*T, error) {
	if err != nil {
		return nil, sessionFailure(err)
	}
	return result, nil
}
