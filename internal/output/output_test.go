package output

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
)

// decodeEnvelope unmarshals buf into a generic map for shape assertions.
func decodeEnvelope(t *testing.T, buf *bytes.Buffer) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(buf.Bytes(), &m); err != nil {
		t.Fatalf("output is not valid JSON: %v\nraw: %q", err, buf.String())
	}
	return m
}

// --- 5.1 RED: JSON envelope shape ---

func TestWriteJSONSuccessEnvelope(t *testing.T) {
	var buf bytes.Buffer
	payload := map[string]any{"tools": []string{"a", "b"}}
	if err := WriteJSON(&buf, payload); err != nil {
		t.Fatalf("WriteJSON: %v", err)
	}
	m := decodeEnvelope(t, &buf)
	if m["version"] != float64(1) {
		t.Errorf("version = %v, want 1", m["version"])
	}
	if _, ok := m["data"]; !ok {
		t.Errorf("missing data field: %v", m)
	}
	if _, ok := m["error"]; ok {
		t.Errorf("unexpected error field on success: %v", m)
	}
	if len(m) != 2 {
		t.Errorf("envelope has extra fields: %v", m)
	}
}

func TestWriteJSONDataPreservesPayload(t *testing.T) {
	var buf bytes.Buffer
	payload := map[string]any{"nextCursor": "abc", "n": 42}
	if err := WriteJSON(&buf, payload); err != nil {
		t.Fatalf("WriteJSON: %v", err)
	}
	m := decodeEnvelope(t, &buf)
	data, ok := m["data"].(map[string]any)
	if !ok {
		t.Fatalf("data is not an object: %v", m["data"])
	}
	if data["nextCursor"] != "abc" || data["n"] != float64(42) {
		t.Errorf("data payload mangled: %v", data)
	}
}

func TestWriteErrorEnvelope(t *testing.T) {
	var buf bytes.Buffer
	err := fmt.Errorf("%w: tool exploded", ErrMCP)
	if werr := WriteError(&buf, err); werr != nil {
		t.Fatalf("WriteError: %v", werr)
	}
	m := decodeEnvelope(t, &buf)
	if m["version"] != float64(1) {
		t.Errorf("version = %v, want 1", m["version"])
	}
	if _, ok := m["data"]; ok {
		t.Errorf("unexpected data field on failure: %v", m)
	}
	e, ok := m["error"].(map[string]any)
	if !ok {
		t.Fatalf("error is not an object: %v", m["error"])
	}
	if e["code"] == "" || e["message"] == "" {
		t.Errorf("error needs code and message: %v", e)
	}
	if !strings.Contains(e["message"].(string), "tool exploded") {
		t.Errorf("message should carry cause: %v", e["message"])
	}
}

func TestWriteErrorFormatText(t *testing.T) {
	var buf bytes.Buffer
	err := fmt.Errorf("%w: unknown server \"nope\"", ErrUsage)
	if werr := WriteErrorFormat(&buf, err, FormatText); werr != nil {
		t.Fatalf("WriteErrorFormat: %v", werr)
	}
	out := buf.String()
	if strings.Contains(out, `"version"`) || strings.Contains(out, "{") {
		t.Errorf("text failures must not emit JSON: %q", out)
	}
	for _, want := range []string{"error.code: usage", "error.message:", "unknown server"} {
		if !strings.Contains(out, want) {
			t.Errorf("output %q missing %q", out, want)
		}
	}
}

func TestWriteErrorFormatJSON(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteErrorFormat(&buf, fmt.Errorf("%w: bad flag", ErrUsage), FormatJSON); err != nil {
		t.Fatalf("WriteErrorFormat: %v", err)
	}
	m := decodeEnvelope(t, &buf)
	if _, ok := m["error"]; !ok {
		t.Errorf("json rendering must keep the error envelope: %v", m)
	}
}

func TestWriteErrorCodeMatchesKind(t *testing.T) {
	cases := []struct {
		name string
		err  error
		code string
	}{
		{"usage", fmt.Errorf("%w: bad flag", ErrUsage), "usage"},
		{"mcp", fmt.Errorf("%w: unreachable", ErrMCP), "mcp"},
		{"internal", errors.New("boom"), "internal"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			if err := WriteError(&buf, tc.err); err != nil {
				t.Fatalf("WriteError: %v", err)
			}
			m := decodeEnvelope(t, &buf)
			e := m["error"].(map[string]any)
			if e["code"] != tc.code {
				t.Errorf("code = %v, want %q", e["code"], tc.code)
			}
		})
	}
}

func TestEnvelopeSingleObjectPerInvocation(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteJSON(&buf, map[string]any{"a": 1}); err != nil {
		t.Fatalf("WriteJSON: %v", err)
	}
	out := strings.TrimSpace(buf.String())
	if strings.Contains(out, "\n") {
		t.Errorf("expected single-line JSON object, got %q", out)
	}
	dec := json.NewDecoder(strings.NewReader(out))
	var v any
	if err := dec.Decode(&v); err != nil {
		t.Fatalf("decode: %v", err)
	}
	var extra any
	if err := dec.Decode(&extra); err == nil {
		t.Errorf("expected exactly one JSON object, got trailing data")
	}
}

// --- 5.3 RED: exit-code mapping ---

func TestExitCodeMapping(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want int
	}{
		{"nil means success", nil, 0},
		{"usage error", fmt.Errorf("%w: unknown flag", ErrUsage), 2},
		{"mcp error", fmt.Errorf("%w: server unreachable", ErrMCP), 1},
		{"internal error", fmt.Errorf("%w: nil deref", ErrInternal), 3},
		{"unclassified error is internal", errors.New("wat"), 3},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ExitCode(tc.err); got != tc.want {
				t.Errorf("ExitCode(%v) = %d, want %d", tc.err, got, tc.want)
			}
		})
	}
}

func TestExitCodeUnwrapsJoins(t *testing.T) {
	joined := errors.Join(errors.New("x"), fmt.Errorf("%w: bad", ErrUsage))
	if got := ExitCode(joined); got != 2 {
		t.Errorf("ExitCode(joined) = %d, want 2", got)
	}
}

// --- 5.5 RED: format detection ---

func TestFormatFlagParsing(t *testing.T) {
	cases := []struct {
		in      string
		want    Format
		wantErr bool
	}{
		{"json", FormatJSON, false},
		{"text", FormatText, false},
		{"", "", true},
		{"yaml", "", true},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			got, err := ParseFormat(tc.in)
			if (err != nil) != tc.wantErr {
				t.Fatalf("ParseFormat(%q) err = %v, wantErr %v", tc.in, err, tc.wantErr)
			}
			if got != tc.want {
				t.Errorf("ParseFormat(%q) = %v, want %v", tc.in, got, tc.want)
			}
			if tc.wantErr && !errors.Is(err, ErrUsage) {
				t.Errorf("ParseFormat error should wrap ErrUsage: %v", err)
			}
		})
	}
}

func TestDefaultFormatNonTTY(t *testing.T) {
	// A bytes.Buffer is never a TTY → json default per spec.
	var buf bytes.Buffer
	if got := DefaultFormat(&buf); got != FormatJSON {
		t.Errorf("DefaultFormat(non-tty) = %v, want %v", got, FormatJSON)
	}
}

func TestResolveFormat(t *testing.T) {
	var buf bytes.Buffer
	f, err := ResolveFormat("text", &buf)
	if err != nil {
		t.Fatalf("ResolveFormat: %v", err)
	}
	if f != FormatText {
		t.Errorf("explicit flag should win: %v", f)
	}
	f, err = ResolveFormat("", &buf)
	if err != nil {
		t.Fatalf("ResolveFormat: %v", err)
	}
	if f != FormatJSON {
		t.Errorf("empty flag on non-tty = %v, want json", f)
	}
	if _, err := ResolveFormat("yaml", &buf); !errors.Is(err, ErrUsage) {
		t.Errorf("bad flag should be usage error, got %v", err)
	}
}

// --- 5.6 RED-ish: text renderer ---

func TestWriteTextRendersKeyValues(t *testing.T) {
	var buf bytes.Buffer
	payload := map[string]any{"server": "fs", "tools": 3}
	if err := WriteText(&buf, payload); err != nil {
		t.Fatalf("WriteText: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "server: fs") || !strings.Contains(out, "tools: 3") {
		t.Errorf("text output missing key/value lines:\n%s", out)
	}
}

func TestWriteTextNested(t *testing.T) {
	var buf bytes.Buffer
	payload := map[string]any{
		"session": map[string]any{"name": "fs", "up": true},
	}
	if err := WriteText(&buf, payload); err != nil {
		t.Fatalf("WriteText: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "session.name: fs") || !strings.Contains(out, "session.up: true") {
		t.Errorf("nested keys should flatten with dots:\n%s", out)
	}
}

func TestWriteTextSlices(t *testing.T) {
	var buf bytes.Buffer
	payload := map[string]any{"tools": []any{"read", "write"}}
	if err := WriteText(&buf, payload); err != nil {
		t.Fatalf("WriteText: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "tools[0]: read") || !strings.Contains(out, "tools[1]: write") {
		t.Errorf("slice items should be indexed:\n%s", out)
	}
}

// --- edge coverage: TTY branch, writer failures ---

func TestDefaultFormatTTY(t *testing.T) {
	f, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		t.Skip("no controlling terminal")
	}
	defer f.Close()
	if got := DefaultFormat(f); got != FormatText {
		t.Errorf("DefaultFormat(tty) = %v, want %v", got, FormatText)
	}
}

// failWriter always errors on Write.
type failWriter struct{}

func (failWriter) Write([]byte) (int, error) { return 0, errors.New("disk full") }

func TestWriteJSONWriterFailure(t *testing.T) {
	err := WriteJSON(failWriter{}, map[string]any{"a": 1})
	if !errors.Is(err, ErrInternal) {
		t.Errorf("writer failure should be internal error, got %v", err)
	}
}

func TestWriteTextWriterFailure(t *testing.T) {
	err := WriteText(failWriter{}, map[string]any{"a": 1})
	if !errors.Is(err, ErrInternal) {
		t.Errorf("writer failure should be internal error, got %v", err)
	}
}

func TestWriteJSONUnmarshalable(t *testing.T) {
	err := WriteJSON(&bytes.Buffer{}, func() {})
	if !errors.Is(err, ErrInternal) {
		t.Errorf("marshal failure should be internal error, got %v", err)
	}
}
