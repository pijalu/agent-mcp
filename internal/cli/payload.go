package cli

import (
	"github.com/pijalu/agent-mcp/client"
)

// metadataKeys are MCP transport bookkeeping fields: protocol version markers,
// cache hints, and the server-info _meta block. They carry no agent value and
// cost tokens on every call, so the default payload drops them.
var metadataKeys = []string{"_meta", "resultType", "cacheScope", "ttlMs"}

// prepare reduces a raw MCP result to the default payload written to stdout:
// discovery lists carry their filtered items plus a match total, and MCP
// transport metadata is stripped everywhere. --raw skips both reductions and
// writes the result as the server returned it. The reductions live in the
// client library (client.Shape), keyed off the same request the invoke used.
func (env *clientEnv) prepare(result any) (any, error) {
	return client.Shape(result, env.request())
}

// stripMetadata removes MCP transport bookkeeping from the result object and
// from the entries of any discovery list. Nested payloads — tool content, tool
// arguments, resource bodies — are untouched: there those keys belong to the
// server's own data, not to the transport.
func stripMetadata(result any) any {
	return client.StripMetadata(result)
}
