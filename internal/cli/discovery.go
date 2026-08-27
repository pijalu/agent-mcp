package cli

import (
	"encoding/json"
	"strings"
)

// discoveryKeys are the MCP list-result arrays the local filter rewrites.
var discoveryKeys = []string{"tools", "resources", "prompts", "servers"}

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
func filterDiscovery(result any, f discoveryFilter) (any, error) {
	if f.inert() {
		return result, nil
	}
	raw, err := json.Marshal(result)
	if err != nil {
		return result, err
	}
	var object map[string]any
	if err := json.Unmarshal(raw, &object); err != nil {
		return filterDiscoveryArray(raw, result, f, err)
	}
	return filterDiscoveryObject(object, f), nil
}

// inert reports whether a pass leaves the payload byte-identical: no filter is
// active and the raw shape is requested. A reduced pass is never inert because
// it names array results and reports totals.
func (f discoveryFilter) inert() bool {
	return f.raw && f.search == "" && f.limit == 0
}

// filterDiscoveryArray wraps a bare item array (the list-servers shape) into
// the named object form, which has room for the total: the array form cannot
// carry a count.
func filterDiscoveryArray(raw []byte, result any, f discoveryFilter, objectErr error) (any, error) {
	var servers []any
	if err := json.Unmarshal(raw, &servers); err != nil {
		return result, objectErr
	}
	return filterDiscoveryObject(map[string]any{"servers": servers}, f), nil
}

// filterDiscoveryObject filters the discovery array in object and records its
// match counts alongside it. Discovery results carry one list, so the first
// match wins.
func filterDiscoveryObject(object map[string]any, f discoveryFilter) map[string]any {
	for _, key := range discoveryKeys {
		items, ok := object[key].([]any)
		if !ok {
			continue
		}
		matched, kept := filterDiscoveryItems(items, f)
		object[key] = kept
		if !f.raw {
			object["total"] = matched
			if f.limit > 0 && matched > len(kept) {
				object["truncated"] = true
			}
		}
		break
	}
	return object
}

// filterDiscoveryItems reports how many items matched and which ones survive
// --limit. Items beyond the limit are dropped but still counted, so callers can
// tell truncation from a genuinely short list.
//
// The kept slice is allocated instead of aliasing items[:0]: callers hand in
// slices they still use (the raw result plus its named-key copy), and writing
// kept entries back into that backing array silently corrupts the caller's
// slice.
func filterDiscoveryItems(items []any, f discoveryFilter) (int, []any) {
	kept := make([]any, 0, len(items))
	matched := 0
	for _, item := range items {
		entry, ok := item.(map[string]any)
		if !ok || !matchesDiscovery(entry, f.search) {
			continue
		}
		matched++
		if f.limit > 0 && len(kept) >= f.limit {
			continue
		}
		kept = append(kept, item)
	}
	return matched, kept
}

func matchesDiscovery(entry map[string]any, search string) bool {
	if search == "" {
		return true
	}
	needle := strings.ToLower(search)
	for _, key := range []string{"name", "description", "title", "uri"} {
		if value, ok := entry[key].(string); ok && strings.Contains(strings.ToLower(value), needle) {
			return true
		}
	}
	return false
}
