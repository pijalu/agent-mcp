package cli

import (
	"bytes"
	"strings"
	"testing"
)

// rendered runs one command's text renderer and returns its output.
func rendered(t *testing.T, command string, payload any) string {
	t.Helper()
	var buf bytes.Buffer
	if err := renderText(&buf, command, payload); err != nil {
		t.Fatalf("renderText(%s): %v", command, err)
	}
	return buf.String()
}

// listPayload wraps items under a discovery key with the reduced-list totals.
func listPayload(key string, items ...any) map[string]any {
	return map[string]any{key: items, "total": len(items)}
}

// assertContainsAll requires every want substring in out.
func assertContainsAll(t *testing.T, out string, want []string) {
	t.Helper()
	for _, w := range want {
		if !strings.Contains(out, w) {
			t.Errorf("output %q missing %q", out, w)
		}
	}
}

// assertNoProtocolNoise rejects the dotted protocol paths and flattened array
// keys that the generic dump produced.
func assertNoProtocolNoise(t *testing.T, out string) {
	t.Helper()
	for _, noise := range []string{"_meta", "inputSchema", "serverInfo"} {
		if strings.Contains(out, noise) {
			t.Errorf("output %q leaks protocol noise %q", out, noise)
		}
	}
	if strings.Contains(out, "name:") || strings.Contains(out, "tools[") {
		t.Errorf("output %q is a flattened dump", out)
	}
}

// TestRenderTextDiscovery is the D-20 regression: each list command prints one
// readable line per item with its name and description, and none of the
// protocol noise (dotted _meta or inputSchema paths) that the generic flatten
// produced.
func TestRenderTextDiscovery(t *testing.T) {
	noisy := map[string]any{
		"name":        "echo",
		"description": "echoes text",
		"inputSchema": map[string]any{
			"properties": map[string]any{"text": map[string]any{"type": "string"}},
		},
		"_meta": map[string]any{"io.modelcontextprotocol/serverInfo": map[string]any{"name": "fixture"}},
	}
	cases := []struct {
		name    string
		command string
		payload any
		want    []string
	}{
		{
			name:    "list-tools",
			command: "list-tools",
			payload: listPayload("tools", noisy),
			want:    []string{"echo", "echoes text"},
		},
		{
			name:    "list-prompts",
			command: "list-prompts",
			payload: listPayload("prompts", map[string]any{"name": "greet", "description": "say hello"}),
			want:    []string{"greet", "say hello"},
		},
		{
			name:    "list-resources",
			command: "list-resources",
			payload: listPayload("resources", map[string]any{"name": "a", "uri": "file:///a", "description": "the a file"}),
			want:    []string{"a", "the a file", "file:///a"},
		},
		{
			name:    "list-servers",
			command: "list-servers",
			payload: listPayload("servers", map[string]any{"name": "fixture", "state": "connected", "active_sessions": 1}),
			want:    []string{"fixture", "connected, 1 session"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out := rendered(t, tc.command, tc.payload)
			assertContainsAll(t, out, tc.want)
			assertNoProtocolNoise(t, out)
		})
	}
}

// TestRenderTextDiscoveryEmpty reports an empty list instead of printing
// nothing, so an empty result is never mistaken for a silent failure.
func TestRenderTextDiscoveryEmpty(t *testing.T) {
	out := rendered(t, "list-tools", listPayload("tools"))
	if !strings.Contains(out, "(no tools)") {
		t.Errorf("empty list output = %q, want an empty-list notice", out)
	}
}

// TestRenderTextDiscoveryTruncated reports dropped matches and the next page.
func TestRenderTextDiscoveryTruncated(t *testing.T) {
	payload := map[string]any{
		"tools":      []any{map[string]any{"name": "echo", "description": "echoes text"}},
		"total":      25,
		"truncated":  true,
		"nextCursor": "c1",
	}
	out := rendered(t, "list-tools", payload)
	if !strings.Contains(out, "1 of 25 tools") {
		t.Errorf("output %q missing truncation footer", out)
	}
	if !strings.Contains(out, "next page: --cursor c1") {
		t.Errorf("output %q missing next-page hint", out)
	}
}

// TestRenderTextDiscoveryBareArray covers the --raw list-servers shape: the
// payload is a bare array rather than a keyed object.
func TestRenderTextDiscoveryBareArray(t *testing.T) {
	payload := []any{map[string]any{"name": "fx", "state": "idle"}}
	out := rendered(t, "list-servers", payload)
	if !strings.Contains(out, "fx") || !strings.Contains(out, "idle") {
		t.Errorf("bare-array output = %q", out)
	}
}

// TestRenderTextCallTool prints the result's content text rather than the
// protocol envelope.
func TestRenderTextCallTool(t *testing.T) {
	payload := map[string]any{
		"content": []any{map[string]any{"type": "text", "text": "ran echo"}},
		"isError": false,
	}
	out := rendered(t, "call-tool", payload)
	if !strings.Contains(out, "ran echo") {
		t.Errorf("call-tool output = %q, want the content text", out)
	}
	if strings.Contains(out, "content[0]") || strings.Contains(out, "_meta") {
		t.Errorf("call-tool output leaks envelope keys: %q", out)
	}
}

// TestRenderTextCallToolImage marks non-text content instead of dumping it.
func TestRenderTextCallToolImage(t *testing.T) {
	payload := map[string]any{
		"content": []any{map[string]any{"type": "image", "mimeType": "image/png", "data": "AAAA"}},
	}
	out := rendered(t, "call-tool", payload)
	if !strings.Contains(out, "[image: image/png]") {
		t.Errorf("call-tool image output = %q", out)
	}
}

// TestRenderTextCallToolStructured covers a structured result with no content.
func TestRenderTextCallToolStructured(t *testing.T) {
	payload := map[string]any{"structuredContent": map[string]any{"count": 2}}
	out := rendered(t, "call-tool", payload)
	if !strings.Contains(out, `"count": 2`) {
		t.Errorf("structured output = %q", out)
	}
}

// TestRenderTextReadResource prints the resource body, and marks binary parts.
func TestRenderTextReadResource(t *testing.T) {
	out := rendered(t, "read-resource", map[string]any{
		"contents": []any{map[string]any{"uri": "file:///a", "text": "body"}},
	})
	if !strings.Contains(out, "body") {
		t.Errorf("read-resource output = %q, want the body", out)
	}
}

func TestRenderTextReadResourceBinary(t *testing.T) {
	out := rendered(t, "read-resource", map[string]any{
		"contents": []any{map[string]any{"uri": "file:///a", "mimeType": "application/octet-stream", "blob": "AAAA"}},
	})
	if !strings.Contains(out, "[binary application/octet-stream: file:///a]") {
		t.Errorf("read-resource binary output = %q", out)
	}
}

// TestRenderTextGetPrompt prints each message as its role and content text.
func TestRenderTextGetPrompt(t *testing.T) {
	payload := map[string]any{
		"messages": []any{
			map[string]any{"role": "user", "content": map[string]any{"type": "text", "text": "hello Ada"}},
		},
	}
	out := rendered(t, "get-prompt", payload)
	if !strings.Contains(out, "user: hello Ada") {
		t.Errorf("get-prompt output = %q", out)
	}
}

// TestRenderTextFieldFallback keeps the flattened dump for commands without a
// human renderer.
func TestRenderTextFieldFallback(t *testing.T) {
	out := rendered(t, "server-status", map[string]any{"uptime": 5})
	if !strings.Contains(out, "uptime: 5") {
		t.Errorf("fallback output = %q, want the flattened dump", out)
	}
}

// TestRenderTextNonObjectPayload falls back for a shape no renderer describes.
func TestRenderTextNonObjectPayload(t *testing.T) {
	out := rendered(t, "call-tool", "surprising")
	if !strings.Contains(out, "surprising") {
		t.Errorf("non-object output = %q", out)
	}
}
