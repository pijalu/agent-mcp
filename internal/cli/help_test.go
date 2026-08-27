package cli

import (
	"bytes"
	"strings"
	"testing"
)

// documentedCommands returns every command name the CLI dispatches and that
// needs its own section in the embedded help markdown.
func documentedCommands() []string {
	return knownCommands()
}

// TestEveryCommandHasHelpSection guards self-documentation: each command
// must have a `## <name>` section in the embedded help markdown.
func TestEveryCommandHasHelpSection(t *testing.T) {
	for _, name := range documentedCommands() {
		if !strings.Contains(helpMarkdown, "## "+name+"\n") {
			t.Errorf("help.md has no %q section", name)
		}
	}
}

// TestCommandHelpFlagPrintsSection guards `agent-mcp COMMAND --help`:
// it must exit 0 and print the matching help section.
func TestCommandHelpFlagPrintsSection(t *testing.T) {
	for _, name := range documentedCommands() {
		var stdout, stderr bytes.Buffer
		if code := RunWith(Deps{}, []string{name, "--help"}, &stdout, &stderr); code != 0 {
			t.Errorf("%s --help: exit %d", name, code)
			continue
		}
		if !strings.HasPrefix(stdout.String(), "## "+name) {
			t.Errorf("%s --help printed %q", name, stdout.String())
		}
	}
}
