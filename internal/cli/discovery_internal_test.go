package cli

import (
	"reflect"
	"testing"
)

// toolsResult builds a decoded list-tools payload with n tool entries.
func toolsResult(n int) map[string]any {
	tools := make([]any, 0, n)
	for i := 0; i < n; i++ {
		tools = append(tools, map[string]any{"name": string(rune('a' + i))})
	}
	return map[string]any{"tools": tools}
}

// TestFilterDiscoveryItemsLeavesInputIntact covers D-25: the kept slice used to
// alias items[:0], so filtering wrote kept entries back into the caller's
// backing array and silently rewrote (or duplicated) the caller's items.
func TestFilterDiscoveryItemsLeavesInputIntact(t *testing.T) {
	items := []any{
		map[string]any{"name": "alpha"},
		map[string]any{"name": "beta"},
		map[string]any{"name": "gamma"},
	}
	before := append([]any(nil), items...)

	matched, kept := filterDiscoveryItems(items, discoveryFilter{search: "beta"})
	if matched != 1 || len(kept) != 1 {
		t.Fatalf("matched = %d kept = %d, want 1 and 1", matched, len(kept))
	}
	if !reflect.DeepEqual(items, before) {
		t.Errorf("caller slice was rewritten: %v, want %v", items, before)
	}
	if kept[0].(map[string]any)["name"] != "beta" {
		t.Errorf("kept = %v, want the matching item", kept)
	}

	// The kept slice must not share storage with the input either: appending to
	// it must not clobber the caller's items.
	grown := append(kept, map[string]any{"name": "delta"})
	if len(grown) != 2 {
		t.Errorf("grown = %v, want 2 entries", grown)
	}
	if !reflect.DeepEqual(items, before) {
		t.Errorf("append to kept leaked into the input: %v", items)
	}
}

// TestFilterDiscoveryReportsTruncation covers the silent-truncation defect:
// items dropped by --limit must still be counted, so 12 configured servers can
// never look like 5.
func TestFilterDiscoveryReportsTruncation(t *testing.T) {
	got, err := filterDiscovery(toolsResult(12), discoveryFilter{limit: 5})
	if err != nil {
		t.Fatalf("filterDiscovery: %v", err)
	}
	object, ok := got.(map[string]any)
	if !ok {
		t.Fatalf("result is not an object: %T", got)
	}
	tools := object["tools"].([]any)
	if len(tools) != 5 {
		t.Errorf("kept %d tools, want 5", len(tools))
	}
	if object["total"] != 12 {
		t.Errorf("total = %v, want 12", object["total"])
	}
	if object["truncated"] != true {
		t.Errorf("truncated = %v, want true", object["truncated"])
	}
}

// TestFilterDiscoveryWithoutTruncation pins the concise default: a complete
// list reports its total and omits the truncated marker.
func TestFilterDiscoveryWithoutTruncation(t *testing.T) {
	got, err := filterDiscovery(toolsResult(3), discoveryFilter{limit: 10})
	if err != nil {
		t.Fatalf("filterDiscovery: %v", err)
	}
	object := got.(map[string]any)
	if len(object["tools"].([]any)) != 3 {
		t.Errorf("tools = %v, want 3 kept", object["tools"])
	}
	if object["total"] != 3 {
		t.Errorf("total = %v, want 3", object["total"])
	}
	if _, ok := object["truncated"]; ok {
		t.Errorf("truncated must be absent when nothing was dropped: %v", object)
	}
}

// TestFilterDiscoveryCountsSearchedItems covers the interaction of --search and
// --limit: total counts matches, not raw page size.
func TestFilterDiscoveryCountsSearchedItems(t *testing.T) {
	result := map[string]any{"tools": []any{
		map[string]any{"name": "alpha"}, map[string]any{"name": "beta"},
		map[string]any{"name": "gamma"}, map[string]any{"name": "delta"},
	}}
	got, err := filterDiscovery(result, discoveryFilter{search: "a", limit: 2})
	if err != nil {
		t.Fatalf("filterDiscovery: %v", err)
	}
	object := got.(map[string]any)
	if object["total"] != 4 {
		t.Errorf("total = %v, want 4 matches", object["total"])
	}
	if len(object["tools"].([]any)) != 2 {
		t.Errorf("tools = %v, want 2 kept", object["tools"])
	}
	if object["truncated"] != true {
		t.Errorf("truncated = %v, want true", object["truncated"])
	}
}

// TestFilterDiscoveryKeepsNextCursor is the pagination regression: filtering
// rewrites item arrays only, so a cursor must survive --search and --limit.
func TestFilterDiscoveryKeepsNextCursor(t *testing.T) {
	cases := []struct {
		name string
		f    discoveryFilter
	}{
		{"limit only", discoveryFilter{limit: 1}},
		{"search only", discoveryFilter{search: "a"}},
		{"search and limit", discoveryFilter{search: "a", limit: 1}},
		{"raw search", discoveryFilter{search: "a", limit: 1, raw: true}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result := toolsResult(4)
			result["nextCursor"] = "page-2"
			got, err := filterDiscovery(result, tc.f)
			if err != nil {
				t.Fatalf("filterDiscovery: %v", err)
			}
			object := got.(map[string]any)
			if object["nextCursor"] != "page-2" {
				t.Errorf("nextCursor = %v, want page-2", object["nextCursor"])
			}
			if len(object["tools"].([]any)) == 0 {
				t.Errorf("filtering dropped every entry: %v", object)
			}
		})
	}
}

// TestFilterDiscoveryWrapsBareServerArray covers list-servers, whose MCP result
// is a bare array: the array form cannot carry a count, so it is named.
func TestFilterDiscoveryWrapsBareServerArray(t *testing.T) {
	servers := make([]any, 0, 12)
	for i := 0; i < 12; i++ {
		servers = append(servers, map[string]any{"name": "fx"})
	}
	got, err := filterDiscovery(servers, discoveryFilter{})
	if err != nil {
		t.Fatalf("filterDiscovery: %v", err)
	}
	object := got.(map[string]any)
	if len(object["servers"].([]any)) != 12 {
		t.Errorf("servers = %v entries, want 12", len(object["servers"].([]any)))
	}
	if object["total"] != 12 {
		t.Errorf("total = %v, want 12", object["total"])
	}
}

// TestFilterDiscoveryRawIsVerbatim covers --raw: the payload keeps the server's
// shape (bare array stays bare, no added keys) while the filter still applies.
func TestFilterDiscoveryRawIsVerbatim(t *testing.T) {
	inert := []any{map[string]any{"name": "fx"}}
	got, err := filterDiscovery(inert, discoveryFilter{raw: true})
	if err != nil {
		t.Fatalf("filterDiscovery: %v", err)
	}
	if !reflect.DeepEqual(got, inert) {
		t.Errorf("raw inert pass changed the payload: %v", got)
	}

	got, err = filterDiscovery(toolsResult(3), discoveryFilter{limit: 2, raw: true})
	if err != nil {
		t.Fatalf("filterDiscovery: %v", err)
	}
	object := got.(map[string]any)
	if len(object["tools"].([]any)) != 2 {
		t.Errorf("tools = %v, want the limit applied", object["tools"])
	}
	if _, ok := object["total"]; ok {
		t.Errorf("raw must not annotate: %v", object)
	}
	if _, ok := object["truncated"]; ok {
		t.Errorf("raw must not annotate: %v", object)
	}
}
