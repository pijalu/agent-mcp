package client

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// decodeResult builds the generic decoded shape Shape walks.
func decodeResult(t *testing.T, raw string) any {
	t.Helper()
	var v any
	if err := json.Unmarshal([]byte(raw), &v); err != nil {
		t.Fatalf("decode fixture: %v", err)
	}
	return v
}

// mirroredFixture is the doubled shape spec-compliant servers send: the
// structured payload serialized as text content plus the parsed copy.
func mirroredFixture(t *testing.T, payload string) string {
	t.Helper()
	return `{"content":[{"type":"text","text":` + quote(t, payload) + `}],"structuredContent":` + payload + `}`
}

// quote renders s as a JSON string literal.
func quote(t *testing.T, s string) string {
	t.Helper()
	raw, err := json.Marshal(s)
	if err != nil {
		t.Fatalf("quote: %v", err)
	}
	return string(raw)
}

// TestDropMirroredContentSingleCopy pins the mapper contract: a content array
// that only repeats structuredContent is dropped, so the payload appears once.
func TestDropMirroredContentSingleCopy(t *testing.T) {
	result := decodeResult(t, mirroredFixture(t, `{"pseudocode":"ldp x29","analysis_profile":{"provider":{"id":"p"}}}`))
	reduced := DropMirroredContent(result)
	object := reduced.(map[string]any)
	if _, present := object["content"]; present {
		t.Errorf("mirrored content survived: %v", object)
	}
	structured := object["structuredContent"].(map[string]any)
	if structured["pseudocode"] != "ldp x29" {
		t.Errorf("structured copy was altered: %v", structured)
	}
}

// TestDropMirroredContentByteCount is the byte-count assertion: shaping a
// doubled result must serialize to about one copy, not two. The payload is a
// realistic ~10 KB body so the check cannot pass on overhead alone.
func TestDropMirroredContentByteCount(t *testing.T) {
	body := map[string]any{
		"pseudocode": strings.Repeat("ldr x0, [x1]; ", 700),
		"limitations": []string{
			"reference resolution is provider-dependent",
			"indirect references may be incomplete",
		},
	}
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal body: %v", err)
	}
	result := decodeResult(t, mirroredFixture(t, string(raw)))
	shaped, err := Shape(result, Request{Op: OpCallTool})
	if err != nil {
		t.Fatalf("Shape: %v", err)
	}
	shapedJSON, err := json.Marshal(shaped)
	if err != nil {
		t.Fatalf("marshal shaped: %v", err)
	}
	// One copy is ~10.5 KB; a surviving mirror would be ~21 KB.
	if got, single := len(shapedJSON), len(raw); got > single+256 {
		t.Errorf("shaped output is %d bytes for a %d-byte payload; the mirror survived", got, single)
	}
	if !strings.Contains(shaped.(map[string]any)["structuredContent"].(map[string]any)["pseudocode"].(string), "ldr x0") {
		t.Errorf("payload body was altered")
	}
}

// TestDropMirroredContentKeepsDistinctText covers the boundary: text that is
// not the serialized structured payload is independent data and survives.
func TestDropMirroredContentKeepsDistinctText(t *testing.T) {
	raw := `{"content":[{"type":"text","text":"human summary"}],"structuredContent":{"count":2}}`
	result := decodeResult(t, raw)
	reduced := DropMirroredContent(result).(map[string]any)
	if _, present := reduced["content"]; !present {
		t.Errorf("distinct text was dropped: %v", reduced)
	}
}

// TestDropMirroredContentKeepsNonJSONText covers prose and image parts: a
// non-JSON text part can be a mirror of nothing, so content stays.
func TestDropMirroredContentKeepsNonJSONText(t *testing.T) {
	raw := `{"content":[{"type":"image","mimeType":"image/png"},{"type":"text","text":"plain prose"}],"structuredContent":{"a":1}}`
	result := decodeResult(t, raw)
	reduced := DropMirroredContent(result).(map[string]any)
	if _, present := reduced["content"]; !present {
		t.Errorf("content with image and prose was dropped: %v", reduced)
	}
}

// TestDropMirroredContentPassThrough covers results without the mirrored
// shape: no structured content, no content array, or a non-object payload.
func TestDropMirroredContentPassThrough(t *testing.T) {
	cases := []struct {
		name  string
		raw   string
		value any
	}{
		{name: "no structured content", raw: `{"content":[{"type":"text","text":"only"}]}`},
		{name: "null structured content", raw: `{"content":[],"structuredContent":null}`},
		{name: "empty content", raw: `{"content":[],"structuredContent":{"a":1}}`},
		{name: "array payload", value: []any{map[string]any{"structuredContent": map[string]any{}}}},
		{name: "scalar payload", value: "text"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var input any
			if tc.value != nil {
				input = tc.value
			} else {
				input = decodeResult(t, tc.raw)
			}
			if got := DropMirroredContent(input); !reflect.DeepEqual(got, input) {
				t.Errorf("DropMirroredContent changed the payload: %v -> %v", input, got)
			}
		})
	}
}

// TestShapeCallToolMirroredIsSingleRepresentation runs the full reducer: both
// the default and the raw pass must contain the payload once, and the raw
// pass must drop the mirrored text without adding totals or stripping the
// server's own fields.
func TestShapeCallToolMirroredIsSingleRepresentation(t *testing.T) {
	const payload = `{"normalized_result":[{"value":"DISTORT_BASS"}],"analysis_profile":{"provider":{"id":"p"}}}`
	result := decodeResult(t, mirroredFixture(t, payload))
	shaped, err := Shape(result, Request{Op: OpCallTool})
	if err != nil {
		t.Fatalf("Shape: %v", err)
	}
	want := map[string]any{"structuredContent": decodeResult(t, payload)}
	if !reflect.DeepEqual(shaped, want) {
		t.Errorf("shaped = %v, want the single structured copy", shaped)
	}
	shapedJSON, err := json.Marshal(shaped)
	if err != nil {
		t.Fatalf("marshal shaped: %v", err)
	}
	if got := len(shapedJSON); got > len(payload)+64 {
		t.Errorf("shaped output is %d bytes for a %d-byte payload; mirror survived", got, len(payload))
	}

	raw, err := Shape(decodeResult(t, mirroredFixture(t, payload)), Request{Op: OpCallTool, Raw: true})
	if err != nil {
		t.Fatalf("Shape raw: %v", err)
	}
	rawObject := raw.(map[string]any)
	if _, present := rawObject["content"]; present {
		t.Errorf("raw output still carries the mirrored text: %v", rawObject)
	}
	if _, present := rawObject["structuredContent"]; !present {
		t.Errorf("raw output lost the structured payload: %v", rawObject)
	}
}

// TestShapeDiscoveryRawPassthrough pins the list-op contract: a raw
// list-servers result stays a bare array with every item intact.
func TestShapeDiscoveryRawPassthrough(t *testing.T) {
	result := []any{map[string]any{"name": "alpha"}}
	out, err := Shape(result, Request{Op: OpListServers, Raw: true})
	if err != nil {
		t.Fatalf("Shape: %v", err)
	}
	if !reflect.DeepEqual(out, result) {
		t.Errorf("raw shape = %v, want passthrough", out)
	}
}
