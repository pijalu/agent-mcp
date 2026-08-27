package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"

	"github.com/pijalu/agent-mcp/internal/output"
	"github.com/pijalu/agent-mcp/internal/session"
	pb "github.com/pijalu/agent-mcp/proto/agentmcp/v1"
)

// runEvents validates the requested server against the daemon's list, then
// follows notifications until the call deadline ends the subscription.
func (env *clientEnv) runEvents(ctx context.Context, client pb.AgentMCPClient, stdout io.Writer) error {
	if err := env.checkServerKnown(client); err != nil {
		return err
	}
	return env.followEvents(ctx, client, stdout)
}

// checkServerKnown rejects a --server the daemon does not serve before the
// subscription opens. Without it a typo looks like a daemon that never sends
// events, so the caller waits out the whole call timeout for nothing.
func (env *clientEnv) checkServerKnown(client pb.AgentMCPClient) error {
	if env.params.server == "" {
		return nil
	}
	ctx, cancel := env.probeCtx()
	defer cancel()
	st, err := client.Status(ctx, &pb.StatusRequest{})
	if err != nil {
		return rpcFailure(err)
	}
	names := serverNames(st)
	if slices.Contains(names, env.params.server) {
		return nil
	}
	return usageError("unknown server %q%s", env.params.server, session.CandidateHint("configured servers", names))
}

// serverNames returns the names a daemon reports, in name order, so hints are
// stable regardless of the map order the daemon iterated.
func serverNames(st *pb.StatusResponse) []string {
	names := make([]string, 0, len(st.GetServers()))
	for _, srv := range st.GetServers() {
		names = append(names, srv.GetName())
	}
	slices.Sort(names)
	return names
}

// followEvents streams notifications until the daemon closes the stream or the
// deadline ends it. Failures are classified like any other RPC, so the caller
// sees the cause instead of a gRPC status chain.
func (env *clientEnv) followEvents(ctx context.Context, client pb.AgentMCPClient, stdout io.Writer) error {
	stream, err := client.Subscribe(ctx, &pb.SubscribeRequest{Server: env.params.server})
	if err != nil {
		return rpcFailure(err)
	}
	for {
		event, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return env.streamFailure(ctx, err)
		}
		if err := env.writeEvent(stdout, event); err != nil {
			return err
		}
	}
}

// streamFailure classifies a failed subscription. The call deadline is the
// documented end of a bounded follow, so it is reported as such rather than as
// an opaque RPC deadline; everything else maps through rpcFailure.
func (env *clientEnv) streamFailure(ctx context.Context, err error) error {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return fmt.Errorf("%w: events: no event within the --call-timeout %s deadline; "+
			"raise --call-timeout to follow longer", output.ErrMCP, env.global.callTimeout)
	}
	return rpcFailure(err)
}

// writeEvent renders one notification as a single line in the negotiated
// format.
func (env *clientEnv) writeEvent(stdout io.Writer, event *pb.SessionEvent) error {
	payload := json.RawMessage(event.GetJson())
	if env.format == output.FormatText {
		if _, err := fmt.Fprintf(stdout, "%s %s %s\n", event.GetServer(), event.GetType(), payload); err != nil {
			return writeEventError(err)
		}
		return nil
	}
	line := map[string]any{"server": event.GetServer(), "type": event.GetType(), "json": payload}
	if err := json.NewEncoder(stdout).Encode(line); err != nil {
		return writeEventError(err)
	}
	return nil
}

// writeEventError marks a failed event write as internal: the subscription is
// healthy, only the CLI's own output sink failed.
func writeEventError(err error) error {
	return fmt.Errorf("%w: write event: %v", output.ErrInternal, err)
}
