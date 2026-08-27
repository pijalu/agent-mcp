package cli_test

import (
	"strings"
	"testing"
)

// TestTextOutputThroughCLI is the end-to-end D-20 check: with --format text,
// list commands print names and descriptions, and call/read/get print their
// content text, with no flattened protocol paths.
func TestTextOutputThroughCLI(t *testing.T) {
	addr := startFakeDaemon(t)
	// list-servers renders the config, not the daemon, so it needs a real file.
	cfgPath := writeConfig(t, buildFixture(t))
	cases := []struct {
		name string
		args []string
		want []string
	}{
		{
			name: "list-tools",
			args: []string{"list-tools", "--server", "fake"},
			want: []string{"echo"},
		},
		{
			name: "list-servers",
			args: []string{"list-servers", "--config", cfgPath},
			want: []string{"fx", "idle"},
		},
		{
			name: "list-resources",
			args: []string{"list-resources", "--server", "fake"},
			want: []string{"file:///a"},
		},
		{
			name: "list-prompts",
			args: []string{"list-prompts", "--server", "fake"},
			want: []string{"greet"},
		},
		{
			name: "call-tool",
			args: []string{"call-tool", "--server", "fake", "--tool", "echo"},
			want: []string{"ran echo"},
		},
		{
			name: "read-resource",
			args: []string{"read-resource", "--server", "fake", "--uri", "file:///a"},
			want: []string{"body"},
		},
		{
			name: "get-prompt",
			args: []string{"get-prompt", "--server", "fake", "--prompt", "greet", "--args", `{"name":"Ada"}`},
			want: []string{"user: hello Ada"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			args := append(append([]string{}, tc.args...), "--format", "text")
			code, out, errOut := runWith(t, addr, args...)
			if code != 0 {
				t.Fatalf("exit = %d, want 0 (out %s err %s)", code, out, errOut)
			}
			assertTextCase(t, out, tc.want)
		})
	}
}

// assertTextCase requires each want substring and no flattened protocol path.
func assertTextCase(t *testing.T, out string, want []string) {
	t.Helper()
	for _, w := range want {
		if !strings.Contains(out, w) {
			t.Errorf("output %q missing %q", out, w)
		}
	}
	for _, noise := range []string{"_meta.", "inputSchema.", "content[", "messages["} {
		if strings.Contains(out, noise) {
			t.Errorf("output %q leaks protocol path %q", out, noise)
		}
	}
}
