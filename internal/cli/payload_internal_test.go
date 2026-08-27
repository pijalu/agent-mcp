package cli

import (
	"encoding/json"
	"reflect"
	"testing"
)

// decodeJSON decodes literal JSON into the generic shape the reducer walks.
func decodeJSON(t *testing.T, raw string) any {
	t.Helper()
	var v any
	if err := json.Unmarshal([]byte(raw), &v); err != nil {
		t.Fatalf("decode fixture: %v", err)
	}
	return v
}

// TestStripMetadataRemovesTransportFields pins the reduction contract: the four
// MCP transport fields carry no agent value and must not reach the payload.
func TestStripMetadataRemovesTransportFields(t *testing.T) {
	payload := decodeJSON(t, `{
		"_meta": {"io.modelcontextprotocol/serverInfo": {"name": "fixture"}},
		"resultType": "complete",
		"cacheScope": "public",
		"ttlMs": 0,
		"tools": [{"name": "echo", "_meta": {"nested": true}, "cacheScope": "private"}],
		"nextCursor": "page-2"
	}`)
	got := stripMetadata(payload).(map[string]any)
	for _, key := range metadataKeys {
		if _, ok := got[key]; ok {
			t.Errorf("top-level metadata %q survived: %v", key, got)
		}
	}
	tool := got["tools"].([]any)[0].(map[string]any)
	if _, ok := tool["_meta"]; ok {
		t.Errorf("item metadata survived: %v", tool)
	}
	if _, ok := tool["cacheScope"]; ok {
		t.Errorf("item metadata survived: %v", tool)
	}
	if got["nextCursor"] != "page-2" || tool["name"] != "echo" {
		t.Errorf("payload data was removed: %v", got)
	}
}

// TestStripMetadataKeepsToolContent covers the boundary: inside a tool result
// the same keys belong to the server's own data, not to the transport.
func TestStripMetadataKeepsToolContent(t *testing.T) {
	payload := decodeJSON(t, `{
		"_meta": {"transport": true},
		"content": [{"type": "text", "text": "{\"ttlMs\": 30}"}],
		"structuredContent": {"ttlMs": 30}
	}`)
	got := stripMetadata(payload).(map[string]any)
	if _, ok := got["_meta"]; ok {
		t.Errorf("top-level metadata survived: %v", got)
	}
	if _, ok := got["structuredContent"]; !ok {
		t.Errorf("nested server data was removed: %v", got)
	}
	content := got["content"].([]any)[0].(map[string]any)
	if content["text"] != `{"ttlMs": 30}` {
		t.Errorf("content payload changed: %v", content)
	}
}

// TestStripMetadataPassesThroughNonObjects covers scalar and array results.
func TestStripMetadataPassesThroughNonObjects(t *testing.T) {
	for _, value := range []any{nil, []any{"a"}, "text"} {
		if got := stripMetadata(value); !reflect.DeepEqual(got, value) {
			t.Errorf("stripMetadata(%v) = %v, want unchanged", value, got)
		}
	}
}
