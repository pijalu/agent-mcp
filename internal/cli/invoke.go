package cli

import (
	"context"
	"encoding/json"

	"github.com/pijalu/agent-mcp/client"
	"github.com/pijalu/agent-mcp/internal/daemon"
	pb "github.com/pijalu/agent-mcp/proto/agentmcp/v1"
)

// invokeDaemon runs the command against the daemon and returns the raw
// result decoded as generic JSON for the output layer. The mapping to the
// library request lives in client.InvokeDaemon.
func (env *clientEnv) invokeDaemon(ctx context.Context, cl pb.AgentMCPClient) (any, error) {
	return client.InvokeDaemon(ctx, cl, env.request())
}

// invokeManager runs the command against an in-process session manager.
// The mapping to the library request lives in client.InvokeManager.
func invokeManager(ctx context.Context, env *clientEnv, mgr daemon.SessionManager) (any, error) {
	return client.InvokeManager(ctx, mgr, env.request())
}

// rpcFailure converts a daemon RPC failure into a clean CLI error. The gRPC
// status chain is dropped so agents see the problem, not the transport.
func rpcFailure(err error) error {
	return client.RPCFailure(err)
}

// request translates parsed flags into a library request. The operation name
// is the command name, so validation and error wording match the CLI's.
func (env *clientEnv) request() client.Request {
	req := client.Request{
		Op:     client.Op(env.name),
		Server: env.params.server,
		Tool:   env.params.tool,
		URI:    env.params.uri,
		Prompt: env.params.prompt,
		Cursor: env.params.cursor,
		Search: env.params.search,
		Limit:  env.params.limit,
		Raw:    env.global.raw,
	}
	// The binary policy is the library's decision path (Shape applies it);
	// the CLI only carries the user's choice and the file store.
	req.Binaries = env.binaries.policy
	req.BinaryStore = env.binaries.store
	if env.params.args != "" {
		req.Args = json.RawMessage(env.params.args)
	}
	return req
}
