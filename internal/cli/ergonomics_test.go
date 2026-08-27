package cli_test

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pijalu/agent-mcp/internal/version"
)

// helpPositions lists, per client command, the flag groups that may precede
// `--help`. Every command must print its section from each position.
var helpPositions = map[string][][]string{
	"list-servers":   {{"--search", "x"}, {"--limit", "3"}, {"--config", "x.toml"}},
	"list-tools":     {{"--server", "s"}, {"--cursor", "c"}, {"--config", "x.toml"}},
	"call-tool":      {{"--server", "s"}, {"--tool", "t"}, {"--config", "x.toml"}},
	"list-resources": {{"--server", "s"}, {"--cursor", "c"}, {"--config", "x.toml"}},
	"read-resource":  {{"--server", "s"}, {"--uri", "u"}, {"--config", "x.toml"}},
	"list-prompts":   {{"--server", "s"}, {"--cursor", "c"}, {"--config", "x.toml"}},
	"get-prompt":     {{"--server", "s"}, {"--prompt", "p"}, {"--config", "x.toml"}},
	"events":         {{"--server", "s"}, {"--config", "x.toml"}},
}

// TestCommandHelpAnyFlagPosition pins the documented `agent-mcp COMMAND --help`
// contract: whatever flag order precedes it, the command's help section is
// printed byte-identically to the `--help`-first form with exit 0.
func TestCommandHelpAnyFlagPosition(t *testing.T) {
	if len(helpPositions) != 8 {
		t.Fatalf("helpPositions covers %d commands, want 8", len(helpPositions))
	}
	for name, prefixes := range helpPositions {
		t.Run(name, func(t *testing.T) { checkHelpPositions(t, name, prefixes) })
	}
}

// checkHelpPositions asserts that every prefix flag group yields the same
// section as `COMMAND --help`.
func checkHelpPositions(t *testing.T, name string, prefixes [][]string) {
	t.Helper()
	code, want := run(t, name, "--help")
	if code != 0 {
		t.Fatalf("first-flag %s --help: exit %d (%s)", name, code, want)
	}
	if !strings.HasPrefix(want, "## "+name) {
		t.Fatalf("first-flag %s --help printed %q", name, want)
	}
	for _, prefix := range prefixes {
		for _, flag := range []string{"--help", "-h"} {
			assertCommandHelp(t, append(append([]string{name}, prefix...), flag), name, want)
		}
	}
}

// assertCommandHelp runs args and requires the command's help section.
func assertCommandHelp(t *testing.T, args []string, name, want string) {
	t.Helper()
	code, got := run(t, args...)
	if code != 0 {
		t.Errorf("%v: exit %d (%s)", args, code, got)
		return
	}
	if got != want {
		t.Errorf("%v output differs from %s --help:\ngot  %q\nwant %q", args, name, got, want)
	}
}

// TestVersionFlagAndCommand covers both spellings of the version surface.
func TestVersionFlagAndCommand(t *testing.T) {
	for _, args := range [][]string{{"--version"}, {"version"}} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			code, out := run(t, args...)
			if code != 0 {
				t.Fatalf("exit = %d, want 0 (%s)", code, out)
			}
			if !strings.Contains(out, "agent-mcp "+version.Version) {
				t.Errorf("stdout %q missing version %q", out, version.Version)
			}
		})
	}
	if code, out := run(t, "version", "--help"); code != 0 || !strings.HasPrefix(out, "## version") {
		t.Errorf("version --help: exit %d, output %q", code, out)
	}
}

// TestVersionInjectedAtBuildTime proves the version is a link-time stamp, not a
// literal: a binary built with -ldflags reports the stamped value.
func TestVersionInjectedAtBuildTime(t *testing.T) {
	if testing.Short() {
		t.Skip("builds a binary")
	}
	root := moduleRoot(t)
	bin := filepath.Join(t.TempDir(), "agent-mcp-stamped")
	stamp := "v9.9.9-ldflags-test"
	build := exec.Command("go", "build",
		"-ldflags", "-X github.com/pijalu/agent-mcp/internal/version.Version="+stamp,
		"-o", bin, "./cmd/agent-mcp")
	build.Dir = root
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	out, err := exec.Command(bin, "--version").Output()
	if err != nil {
		t.Fatalf("run stamped binary: %v", err)
	}
	if got := strings.TrimSpace(string(out)); got != "agent-mcp "+stamp {
		t.Errorf("stamped binary printed %q, want %q", got, "agent-mcp "+stamp)
	}
}

// errorMessageOf decodes the failure envelope and returns its message, so
// assertions read the real text rather than its JSON escaping.
func errorMessageOf(t *testing.T, out string) string {
	t.Helper()
	env := envelopeOf(t, out)
	errObj, ok := env["error"].(map[string]any)
	if !ok {
		t.Fatalf("stdout has no error object: %s", out)
	}
	msg, _ := errObj["message"].(string)
	return msg
}

// TestUnknownCommandSuggestsNearestMatch covers the did-you-mean path and the
// plain unknown-command path, both of which must list the known commands.
func TestUnknownCommandSuggestsNearestMatch(t *testing.T) {
	suggestions := []struct{ typo, want string }{
		{"list-tool", "list-tools"},
		{"calltool", "call-tool"},
		{"get-promt", "get-prompt"},
		{"list-serverss", "list-servers"},
		{"versionn", "version"},
	}
	for _, tc := range suggestions {
		t.Run(tc.typo, func(t *testing.T) { assertSuggestion(t, tc.typo, tc.want) })
	}
	code, out := run(t, "frobnicate")
	if code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
	msg := errorMessageOf(t, out)
	if strings.Contains(msg, "did you mean") {
		t.Errorf("frobnicate should not suggest a command: %q", msg)
	}
	if !strings.Contains(msg, "known commands:") {
		t.Errorf("message %q does not list known commands", msg)
	}
}

// assertSuggestion requires the did-you-mean hit and the command list for typo.
func assertSuggestion(t *testing.T, typo, want string) {
	t.Helper()
	code, out := run(t, typo)
	if code != 2 {
		t.Fatalf("exit = %d, want 2 (%s)", code, out)
	}
	msg := errorMessageOf(t, out)
	if !strings.Contains(msg, "unknown command") {
		t.Errorf("message %q missing unknown command", msg)
	}
	if !strings.Contains(msg, `did you mean "`+want+`"`) {
		t.Errorf("message %q missing suggestion %q", msg, want)
	}
	if !strings.Contains(msg, "known commands: call-tool") || !strings.Contains(msg, "list-tools") {
		t.Errorf("message %q does not list known commands", msg)
	}
}

// TestLeadingFlagGuidance covers a global flag written before the command (the
// old bug reported `unknown command "--config"`) and an unknown leading flag.
func TestLeadingFlagGuidance(t *testing.T) {
	global := [][]string{
		{"--config", "x.toml"},
		{"--api-key", "k"},
		{"--format", "json"},
		{"--no-server"},
		{"--connect-timeout", "3s"},
	}
	for _, leading := range global {
		t.Run(strings.Join(leading, " "), func(t *testing.T) {
			code, out := run(t, append(append([]string{}, leading...), "list-tools")...)
			if code != 2 {
				t.Fatalf("exit = %d, want 2 (%s)", code, out)
			}
			want := "global flag " + leading[0] + " must follow the command"
			if !strings.Contains(out, want) {
				t.Errorf("stdout %q missing %q", out, want)
			}
			if strings.Contains(out, "unknown command") {
				t.Errorf("stdout %q still reports an unknown command", out)
			}
		})
	}
	code, out := run(t, "--bogus", "list-tools")
	if code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
	if !strings.Contains(out, "unknown flag --bogus") {
		t.Errorf("stdout %q missing unknown flag phrasing", out)
	}
}

// TestUnknownFlagPhrasing replaces the stdlib `flag provided but not defined:
// -bogus` with the CLI spelling.
func TestUnknownFlagPhrasing(t *testing.T) {
	for _, args := range [][]string{
		{"list-servers", "--bogus"},
		{"call-tool", "--server", "s", "--tool", "t", "--bogus"},
		{"server", "start", "--bogus"},
		{"config", "add", "--bogus"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			code, out := run(t, args...)
			if code != 2 {
				t.Fatalf("exit = %d, want 2 (%s)", code, out)
			}
			if !strings.Contains(out, "unknown flag --bogus") {
				t.Errorf("stdout %q missing `unknown flag --bogus`", out)
			}
			if strings.Contains(out, "flag provided but not defined") {
				t.Errorf("stdout %q keeps the stdlib phrasing", out)
			}
		})
	}
}

// TestBareQuickstart: the undashed word is as good as the flag.
func TestBareQuickstart(t *testing.T) {
	flagCode, flagOut := run(t, "--quickstart")
	wordCode, wordOut := run(t, "quickstart")
	if wordCode != 0 || flagCode != 0 {
		t.Fatalf("exits = %d/%d, want 0/0", flagCode, wordCode)
	}
	if wordOut != flagOut {
		t.Errorf("quickstart output differs from --quickstart:\n%s\n---\n%s", wordOut, flagOut)
	}
	if !strings.Contains(wordOut, "agent quickstart") {
		t.Errorf("quickstart output %q missing agent quickstart", wordOut)
	}
}
