package session

import (
	"context"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// TestCleanErrorText drops the go-sdk transport's `calling "<method>":`
// wrapper and nothing else.
func TestCleanErrorText(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"method wrapper", `calling "tools/call": unknown tool "nosuchtool"`, `unknown tool "nosuchtool"`},
		{"nested wrappers", `calling "a": calling "b": boom`, "boom"},
		{"no wrapper", "dial boom", "dial boom"},
		{"unterminated wrapper", `calling "tools/call" unknown tool`, `calling "tools/call" unknown tool`},
		{"empty remainder", `calling "tools/call": `, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := cleanErrorText(tc.in); got != tc.want {
				t.Errorf("cleanErrorText(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestCallToolLastErrorIsClean pins the operator-facing copy: a failed tool
// call records the server's own words, not the transport method that carried
// them, so list-servers and server status stay free of layering noise.
func TestCallToolLastErrorIsClean(t *testing.T) {
	m := NewManager(testConfigs(), fixtureDialer(t))
	if _, err := m.CallTool(context.Background(), "alpha", &mcp.CallToolParams{Name: "nosuchtool"}); err == nil {
		t.Fatal("CallTool: want an error for an unknown tool")
	}
	info := m.ServerStatuses()[0]
	if !strings.Contains(info.LastError, "unknown tool") {
		t.Fatalf("last error = %q, want the server's unknown-tool message", info.LastError)
	}
	if strings.Contains(info.LastError, `calling "`) {
		t.Errorf("last error = %q, want no transport method wrapper", info.LastError)
	}
}
