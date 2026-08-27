package cli_test

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/pijalu/agent-mcp/internal/cli"
)

// run invokes the CLI and captures stdout plus the exit code.
func run(t *testing.T, args ...string) (int, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := cli.Run(args, &stdout, &stderr)
	return code, stdout.String()
}

// envelopeOf decodes the JSON envelope from stdout.
func envelopeOf(t *testing.T, out string) map[string]any {
	t.Helper()
	var env map[string]any
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatalf("stdout is not one JSON object: %q: %v", out, err)
	}
	return env
}

func TestHelpCommands(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{"long help", []string{"--help"}, "agent-mcp"},
		{"short help", []string{"-h"}, "agent-mcp"},
		{"help command", []string{"help"}, "agent-mcp"},
		{"quickstart", []string{"--quickstart"}, "agent quickstart"},
		{"command help", []string{"list-tools", "--help"}, "list-tools"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			code, out := run(t, tc.args...)
			if code != 0 {
				t.Fatalf("exit = %d, want 0", code)
			}
			if !strings.Contains(out, tc.want) {
				t.Errorf("stdout %q missing %q", out, tc.want)
			}
		})
	}
}

func TestUnknownCommandHelp(t *testing.T) {
	// An unknown command is a usage failure whether or not --help follows, so
	// this must match the bare form (exit 2) rather than reporting internal.
	code, out := run(t, "frobnicate", "--help")
	if code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
	if !strings.Contains(out, "unknown command") {
		t.Errorf("stdout: %s", out)
	}
}

func TestRunNoArgs(t *testing.T) {
	code, out := run(t)
	if code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
	env := envelopeOf(t, out)
	errObj, ok := env["error"].(map[string]any)
	if !ok {
		t.Fatalf("missing error object: %v", env)
	}
	if errObj["code"] != "usage" {
		t.Errorf("error.code = %v, want usage", errObj["code"])
	}
}

func TestUnknownCommand(t *testing.T) {
	code, out := run(t, "frobnicate")
	if code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
	env := envelopeOf(t, out)
	if !strings.Contains(out, "unknown command") {
		t.Errorf("stdout missing unknown command message: %v", env)
	}
}

func TestUnknownServerSubcommand(t *testing.T) {
	code, out := run(t, "server", "dance")
	if code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
	if !strings.Contains(out, "unknown server subcommand") {
		t.Errorf("stdout: %s", out)
	}
}

func TestServerNoSubcommand(t *testing.T) {
	code, out := run(t, "server")
	if code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
	if !strings.Contains(out, "requires a subcommand") {
		t.Errorf("stdout: %s", out)
	}
}

// missingFlagCases covers required-flag validation per command.
var missingFlagCases = []struct {
	name string
	args []string
	want string
}{
	{"list-tools no server", []string{"list-tools"}, "--server"},
	{"call-tool no server", []string{"call-tool", "--tool", "x"}, "--server"},
	{"call-tool no tool", []string{"call-tool", "--server", "x"}, "--tool"},
	{"read-resource no uri", []string{"read-resource", "--server", "x"}, "--uri"},
	{"get-prompt no prompt", []string{"get-prompt", "--server", "x"}, "--prompt"},
	{"list-prompts no server", []string{"list-prompts"}, "--server"},
}

func TestMissingRequiredFlags(t *testing.T) {
	for _, tc := range missingFlagCases {
		t.Run(tc.name, func(t *testing.T) {
			code, out := run(t, tc.args...)
			if code != 2 {
				t.Fatalf("exit = %d, want 2 (out %s)", code, out)
			}
			if !strings.Contains(out, tc.want) {
				t.Errorf("stdout %q missing %q", out, tc.want)
			}
		})
	}
}

func TestBadFormatFlag(t *testing.T) {
	code, out := run(t, "list-servers", "--format", "yaml")
	if code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
	if !strings.Contains(out, "unknown --format") {
		t.Errorf("stdout: %s", out)
	}
}

func TestUnexpectedPositional(t *testing.T) {
	code, out := run(t, "list-servers", "extra")
	if code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
	if !strings.Contains(out, "unexpected argument") {
		t.Errorf("stdout: %s", out)
	}
}

func TestSingleLineEnvelope(t *testing.T) {
	_, out := run(t, "nope")
	if strings.Count(strings.TrimRight(out, "\n"), "\n") != 0 {
		t.Errorf("stdout must be a single line: %q", out)
	}
}
