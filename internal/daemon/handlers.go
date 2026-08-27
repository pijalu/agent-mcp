package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/pijalu/agent-mcp/internal/session"
	pb "github.com/pijalu/agent-mcp/proto/agentmcp/v1"
)

// SessionManager is the slice of session.Manager the daemon depends on.
// Defined here (interface segregation) so tests substitute a fake.
type SessionManager interface {
	CallTool(ctx context.Context, name string, params *mcp.CallToolParams) (*mcp.CallToolResult, error)
	ListTools(ctx context.Context, name string, params *mcp.ListToolsParams) (*mcp.ListToolsResult, error)
	ListResources(ctx context.Context, name string, params *mcp.ListResourcesParams) (*mcp.ListResourcesResult, error)
	ReadResource(ctx context.Context, name string, params *mcp.ReadResourceParams) (*mcp.ReadResourceResult, error)
	ListPrompts(ctx context.Context, name string, params *mcp.ListPromptsParams) (*mcp.ListPromptsResult, error)
	GetPrompt(ctx context.Context, name string, params *mcp.GetPromptParams) (*mcp.GetPromptResult, error)
	CloseAll() int
	ServerStatuses() []session.ServerInfo
}

// unmarshalInto decodes raw JSON into out. Empty input decodes as the
// zero value so clients may omit optional params.
func unmarshalInto(raw []byte, out any) error {
	if len(raw) == 0 {
		return nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return status.Errorf(codes.InvalidArgument, "daemon: decode request json: %v", err)
	}
	return nil
}

// marshalResult encodes an MCP result back into raw JSON bytes.
func marshalResult(v any) ([]byte, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "daemon: encode result json: %v", err)
	}
	return raw, nil
}

// rpcError maps session-layer failures onto gRPC codes. Bad parameters are
// InvalidArgument so the client can report them as usage failures rather than
// transient MCP failures.
func rpcError(err error) error {
	if err == nil {
		return nil
	}
	if status.Code(err) != codes.Unknown {
		return err
	}
	if errors.Is(err, session.ErrUnknownServer) {
		return status.Error(codes.InvalidArgument, trimSessionPrefix(err.Error()))
	}
	return status.Error(codes.Internal, trimSessionPrefix(err.Error()))
}

// trimSessionPrefix drops the internal package prefix so agent-facing
// messages name the problem without leaking layering.
func trimSessionPrefix(msg string) string {
	return strings.TrimPrefix(msg, "session: ")
}

func (d *Daemon) ListTools(ctx context.Context, req *pb.ListToolsRequest) (*pb.ListToolsResponse, error) {
	var params mcp.ListToolsParams
	if err := unmarshalInto(req.GetJson(), &params); err != nil {
		return nil, err
	}
	result, err := d.mgr.ListTools(ctx, req.GetServer(), &params)
	if err != nil {
		return nil, rpcError(err)
	}
	raw, err := marshalResult(result)
	if err != nil {
		return nil, err
	}
	return &pb.ListToolsResponse{Json: raw}, nil
}

func (d *Daemon) CallTool(ctx context.Context, req *pb.CallToolRequest) (*pb.CallToolResponse, error) {
	var params mcp.CallToolParams
	if err := unmarshalInto(req.GetJson(), &params); err != nil {
		return nil, err
	}
	result, err := d.mgr.CallTool(ctx, req.GetServer(), &params)
	if err != nil {
		return nil, rpcError(err)
	}
	raw, err := marshalResult(result)
	if err != nil {
		return nil, err
	}
	return &pb.CallToolResponse{Json: raw}, nil
}

func (d *Daemon) ListResources(ctx context.Context, req *pb.ListResourcesRequest) (*pb.ListResourcesResponse, error) {
	var params mcp.ListResourcesParams
	if err := unmarshalInto(req.GetJson(), &params); err != nil {
		return nil, err
	}
	result, err := d.mgr.ListResources(ctx, req.GetServer(), &params)
	if err != nil {
		return nil, rpcError(err)
	}
	raw, err := marshalResult(result)
	if err != nil {
		return nil, err
	}
	return &pb.ListResourcesResponse{Json: raw}, nil
}

func (d *Daemon) ReadResource(ctx context.Context, req *pb.ReadResourceRequest) (*pb.ReadResourceResponse, error) {
	var params mcp.ReadResourceParams
	if err := unmarshalInto(req.GetJson(), &params); err != nil {
		return nil, err
	}
	result, err := d.mgr.ReadResource(ctx, req.GetServer(), &params)
	if err != nil {
		return nil, rpcError(err)
	}
	raw, err := marshalResult(result)
	if err != nil {
		return nil, err
	}
	return &pb.ReadResourceResponse{Json: raw}, nil
}

func (d *Daemon) ListPrompts(ctx context.Context, req *pb.ListPromptsRequest) (*pb.ListPromptsResponse, error) {
	var params mcp.ListPromptsParams
	if err := unmarshalInto(req.GetJson(), &params); err != nil {
		return nil, err
	}
	result, err := d.mgr.ListPrompts(ctx, req.GetServer(), &params)
	if err != nil {
		return nil, rpcError(err)
	}
	raw, err := marshalResult(result)
	if err != nil {
		return nil, err
	}
	return &pb.ListPromptsResponse{Json: raw}, nil
}

func (d *Daemon) GetPrompt(ctx context.Context, req *pb.GetPromptRequest) (*pb.GetPromptResponse, error) {
	var params mcp.GetPromptParams
	if err := unmarshalInto(req.GetJson(), &params); err != nil {
		return nil, err
	}
	result, err := d.mgr.GetPrompt(ctx, req.GetServer(), &params)
	if err != nil {
		return nil, rpcError(err)
	}
	raw, err := marshalResult(result)
	if err != nil {
		return nil, err
	}
	return &pb.GetPromptResponse{Json: raw}, nil
}

// ListServers reports the configured servers and their live state.
func (d *Daemon) ListServers(ctx context.Context, req *pb.ListServersRequest) (*pb.ListServersResponse, error) {
	statuses := d.mgr.ServerStatuses()
	raw, err := marshalResult(statuses)
	if err != nil {
		return nil, err
	}
	return &pb.ListServersResponse{Json: raw}, nil
}

// Status reports daemon uptime and per-server session state. It also reports
// the config file the daemon loaded so clients can reject a --config that
// names a different file.
func (d *Daemon) Status(ctx context.Context, req *pb.StatusRequest) (*pb.StatusResponse, error) {
	statuses := d.mgr.ServerStatuses()
	resp := &pb.StatusResponse{UptimeSeconds: d.uptime(), ConfigPath: d.configPath}
	for _, s := range statuses {
		resp.Servers = append(resp.Servers, &pb.ServerStatus{
			Name:           s.Name,
			State:          s.State,
			ActiveSessions: int32(s.ActiveSessions),
			LastError:      s.LastError,
		})
		if s.ActiveSessions > 0 {
			resp.ActiveSessions += int32(s.ActiveSessions)
		}
	}
	return resp, nil
}

// ShutdownAll closes every MCP session, then stops the daemon. It is the
// last RPC a daemon serves before exiting.

func (d *Daemon) ShutdownAll(ctx context.Context, req *pb.ShutdownAllRequest) (*pb.ShutdownAllResponse, error) {
	closed := d.Shutdown()
	return &pb.ShutdownAllResponse{SessionsClosed: int32(closed)}, nil
}

func (d *Daemon) Subscribe(req *pb.SubscribeRequest, stream grpc.ServerStreamingServer[pb.SessionEvent]) error {
	subscriber, ok := d.mgr.(interface {
		Subscribe(string) (<-chan session.Event, func())
	})
	if !ok {
		return status.Error(codes.Unimplemented, "daemon: event subscriptions unavailable")
	}
	ch, cancel := subscriber.Subscribe(req.GetServer())
	defer cancel()
	for {
		select {
		case <-stream.Context().Done():
			return stream.Context().Err()
		case event, ok := <-ch:
			if !ok {
				return nil
			}
			if err := stream.Send(&pb.SessionEvent{Server: event.Server, Type: event.Type, Json: event.JSON}); err != nil {
				return err
			}
		}
	}
}
