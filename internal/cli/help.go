package cli

import (
	_ "embed"
	"fmt"
	"io"
	"strings"

	"github.com/pijalu/agent-mcp/internal/output"
)

//go:embed help.md
var helpMarkdown string

//go:embed quickstart.md
var quickstartMarkdown string

func writeQuickstart(stdout io.Writer) int {
	if _, err := io.WriteString(stdout, quickstartMarkdown); err != nil {
		return 3
	}
	return 0
}

func writeHelp(stdout io.Writer) int {
	if _, err := io.WriteString(stdout, helpMarkdown); err != nil {
		return 3
	}
	return 0
}

func writeCommandHelp(stdout io.Writer, command string) error {
	lines := strings.Split(helpMarkdown, "\n")
	start := -1
	for i, line := range lines {
		if strings.HasPrefix(line, "## "+command) {
			start = i
			break
		}
	}
	if start < 0 {
		return fmt.Errorf("%w: unknown command %q", output.ErrUsage, command)
	}
	end := len(lines)
	for i := start + 1; i < len(lines); i++ {
		if strings.HasPrefix(lines[i], "## ") {
			end = i
			break
		}
	}
	_, err := io.WriteString(stdout, strings.Join(lines[start:end], "\n"))
	return err
}
