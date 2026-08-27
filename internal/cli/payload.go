package cli

import (
	"encoding/json"
	"fmt"

	"github.com/pijalu/agent-mcp/internal/output"
)

// metadataKeys are MCP transport bookkeeping fields: protocol version markers,
// cache hints, and the server-info _meta block. They carry no agent value and
// cost tokens on every call, so the default payload drops them.
var metadataKeys = []string{"_meta", "resultType", "cacheScope", "ttlMs"}

// prepare reduces a raw MCP result to the default payload written to stdout:
// discovery lists carry their filtered items plus a match total, and MCP
// transport metadata is stripped everywhere. --raw skips both reductions and
// writes the result as the server returned it.
func (env *clientEnv) prepare(result any) (any, error) {
	filtered, err := env.filterPayload(result)
	if err != nil {
		return nil, err
	}
	if env.global.raw {
		return filtered, nil
	}
	normalized, err := asJSONValue(filtered)
	if err != nil {
		return nil, err
	}
	return stripMetadata(normalized), nil
}

// asJSONValue normalizes a result into the generic JSON shape the reducer
// walks: daemon results arrive as decoded maps, in-process results as MCP
// structs. Maps pass through untouched so their payload is never re-encoded.
func asJSONValue(result any) (any, error) {
	switch result.(type) {
	case nil, map[string]any, []any:
		return result, nil
	}
	raw, err := json.Marshal(result)
	if err != nil {
		return nil, fmt.Errorf("%w: encode result: %v", output.ErrInternal, err)
	}
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil, fmt.Errorf("%w: decode result: %v", output.ErrInternal, err)
	}
	return value, nil
}

// filterPayload applies the local search/limit pass to the commands that return
// discovery lists; every other result passes through untouched.
func (env *clientEnv) filterPayload(result any) (any, error) {
	if !env.listResult() {
		return result, nil
	}
	return filterDiscovery(result, discoveryFilter{
		search: env.params.search,
		limit:  env.params.limit,
		raw:    env.global.raw,
	})
}

// listResult reports whether the command returns a discovery list that the
// local search/limit filter and its totals apply to.
func (env *clientEnv) listResult() bool {
	switch env.name {
	case "list-servers", "list-tools", "list-resources", "list-prompts":
		return true
	default:
		return false
	}
}

// stripMetadata removes MCP transport bookkeeping from the result object and
// from the entries of any discovery list. Nested payloads — tool content, tool
// arguments, resource bodies — are untouched: there those keys belong to the
// server's own data, not to the transport.
func stripMetadata(result any) any {
	object, ok := result.(map[string]any)
	if !ok {
		return result
	}
	deleteMetadata(object)
	for _, key := range discoveryKeys {
		for _, item := range asObjects(object[key]) {
			deleteMetadata(item)
		}
	}
	return object
}

// deleteMetadata drops the transport fields from one object.
func deleteMetadata(object map[string]any) {
	for _, key := range metadataKeys {
		delete(object, key)
	}
}

// asObjects returns the map entries of a decoded JSON array, ignoring anything
// that is not an object.
func asObjects(value any) []map[string]any {
	items, ok := value.([]any)
	if !ok {
		return nil
	}
	objects := make([]map[string]any, 0, len(items))
	for _, item := range items {
		if entry, ok := item.(map[string]any); ok {
			objects = append(objects, entry)
		}
	}
	return objects
}
