package cli

import (
	"github.com/pijalu/agent-mcp/client"
)

// discoveryFilter is one --search/--limit pass over a discovery result.
type discoveryFilter struct {
	search string
	limit  int
	// raw keeps the MCP result as the server returned it: no total/truncated
	// keys, and a bare item array stays bare when no filter is active.
	raw bool
}

// filterDiscovery applies f to result, returning the payload to write.
//
// A reduced pass always exposes the item array under its named key and reports
// how many items matched before --limit, so a shortened list is never mistaken
// for a short one. A raw pass reproduces the server's result verbatim (minus
// the requested search/limit) and is the escape hatch for callers that want the
// untouched MCP payload.
//
// The rewrite lives in the client library; the CLI passes are always list
// passes, so the predicate is always true here.
func filterDiscovery(result any, f discoveryFilter) (any, error) {
	return client.FilterDiscovery(result, client.DiscoveryFilter{
		Search: f.search,
		Limit:  f.limit,
		Raw:    f.raw,
	}, true)
}

// filterDiscoveryItems reports how many items matched and which ones survive
// --limit.
func filterDiscoveryItems(items []any, f discoveryFilter) (int, []any) {
	return client.FilterDiscoveryItems(items, client.DiscoveryFilter{
		Search: f.search,
		Limit:  f.limit,
		Raw:    f.raw,
	})
}
