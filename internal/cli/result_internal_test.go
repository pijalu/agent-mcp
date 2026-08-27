package cli

import (
	"strings"
	"testing"

	"github.com/pijalu/agent-mcp/internal/output"
)

// TestToolErrorResult pins isError detection, the message text, and the
// exit-code mapping that keeps a failed tool call out of .data.
func TestToolErrorResult(t *testing.T) {
	tests := []struct {
		name  string
		value any
		want  string
	}{
		{
			name:  "successful tool result",
			value: map[string]any{"content": []any{map[string]any{"text": "ok", "type": "text"}}},
			want:  "",
		},
		{
			name:  "schema validation failure",
			value: map[string]any{"isError": true, "content": []any{map[string]any{"text": "bad arguments", "type": "text"}}},
			want:  "bad arguments",
		},
		{
			name:  "failure without text content",
			value: map[string]any{"isError": true},
			want:  "tool reported an error",
		},
		{
			name:  "non-object payload passes through",
			value: []any{"x"},
			want:  "",
		},
		{
			name:  "nil payload passes through",
			value: nil,
			want:  "",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assertToolError(t, tc.value, tc.want)
		})
	}
}

// assertToolError checks that value yields no error when want is empty, and
// otherwise the MCP-mapped error carrying want.
func assertToolError(t *testing.T, value any, want string) {
	t.Helper()
	err := toolErrorResult(value)
	if want == "" {
		if err != nil {
			t.Fatalf("err = %v, want nil", err)
		}
		return
	}
	if err == nil {
		t.Fatalf("err = nil, want message containing %q", want)
	}
	if !strings.Contains(err.Error(), want) {
		t.Errorf("err = %q, want substring %q", err, want)
	}
	if !strings.Contains(err.Error(), output.ErrMCP.Error()) {
		t.Errorf("err = %q, want the mcp sentinel so the envelope reports code mcp", err)
	}
	if code := output.ExitCode(err); code != 1 {
		t.Errorf("exit code = %d, want 1", code)
	}
}
