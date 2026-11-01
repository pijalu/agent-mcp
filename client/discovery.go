// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package client

import (
	"encoding/json"
	"strings"
)

// discoveryKeys are the MCP list-result arrays the local filter rewrites.
var discoveryKeys = []string{"tools", "resources", "prompts", "servers"}

// DiscoveryFilter is one --search/--limit pass over a discovery result.
type DiscoveryFilter struct {
	Search string
	Limit  int
	// Raw keeps the MCP result as the server returned it: no total/truncated
	// keys, and a bare item array stays bare when no filter is active.
	Raw bool
}

// FilterDiscovery applies f to result, returning the payload to write.
//
// A reduced pass always exposes the item array under its named key and reports
// how many items matched before the limit, so a shortened list is never
// mistaken for a short one. A raw pass reproduces the server's result verbatim
// (minus the requested search/limit) and is the escape hatch for callers that
// want the untouched MCP payload.
//
// applies is the list-result predicate: only list operations get the rewrite.
func FilterDiscovery(result any, f DiscoveryFilter, applies bool) (any, error) {
	if !applies || f.inert() {
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
func (f DiscoveryFilter) inert() bool {
	return f.Raw && f.Search == "" && f.Limit == 0
}

// filterDiscoveryArray wraps a bare item array (the list-servers shape) into
// the named object form, which has room for the total: the array form cannot
// carry a count.
func filterDiscoveryArray(raw []byte, result any, f DiscoveryFilter, objectErr error) (any, error) {
	var servers []any
	if err := json.Unmarshal(raw, &servers); err != nil {
		return result, objectErr
	}
	return filterDiscoveryObject(map[string]any{"servers": servers}, f), nil
}

// filterDiscoveryObject filters the discovery array in object and records its
// match counts alongside it. Discovery results carry one list, so the first
// match wins.
func filterDiscoveryObject(object map[string]any, f DiscoveryFilter) map[string]any {
	for _, key := range discoveryKeys {
		items, ok := object[key].([]any)
		if !ok {
			continue
		}
		matched, kept := filterDiscoveryItems(items, f)
		object[key] = kept
		if !f.Raw {
			object["total"] = matched
			if f.Limit > 0 && matched > len(kept) {
				object["truncated"] = true
			}
		}
		break
	}
	return object
}

// FilterDiscoveryItems reports how many items matched and which ones survive
// the filter's limit. Exported for embedders that filter their own discovery
// arrays with the same semantics as FilterDiscovery.
func FilterDiscoveryItems(items []any, f DiscoveryFilter) (int, []any) {
	return filterDiscoveryItems(items, f)
}

// filterDiscoveryItems reports how many items matched and which ones survive
// the limit. Items beyond the limit are dropped but still counted, so callers
// can tell truncation from a genuinely short list.
//
// The kept slice is allocated instead of aliasing items[:0]: callers hand in
// slices they still use (the raw result plus its named-key copy), and writing
// kept entries back into that backing array silently corrupts the caller's
// slice.
func filterDiscoveryItems(items []any, f DiscoveryFilter) (int, []any) {
	kept := make([]any, 0, len(items))
	matched := 0
	for _, item := range items {
		entry, ok := item.(map[string]any)
		if !ok || !matchesDiscovery(entry, f.Search) {
			continue
		}
		matched++
		if f.Limit > 0 && len(kept) >= f.Limit {
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
