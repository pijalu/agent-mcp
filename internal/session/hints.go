package session

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// maxCandidates caps how many names one hint lists before it summarises the
// remainder: a large config or tool list must not bury the error itself.
const maxCandidates = 20

// unknownServerError reports name as unconfigured and names the configured
// servers, so a caller can correct the name without opening the config file.
func (m *Manager) unknownServerError(name string) error {
	return fmt.Errorf("%w %q%s", ErrUnknownServer, name, candidateHint("configured servers", m.serverNames()))
}

// serverNames returns the configured server names in name order.
func (m *Manager) serverNames() []string {
	names := make([]string, 0, len(m.configs))
	for name := range m.configs {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// unknownToolHint appends the server's tool names to an unknown-tool failure so
// the caller can correct --tool without probing the server itself. The hint is
// best-effort: a failed or empty probe leaves the original error untouched.
func (m *Manager) unknownToolHint(ctx context.Context, server string, err error) error {
	if err == nil || !strings.Contains(err.Error(), "unknown tool") {
		return err
	}
	list, lerr := m.ListTools(ctx, server, &mcp.ListToolsParams{})
	if lerr != nil || list == nil || len(list.Tools) == 0 {
		return err
	}
	names := make([]string, 0, len(list.Tools))
	for _, tool := range list.Tools {
		names = append(names, tool.Name)
	}
	sort.Strings(names)
	return withHint(err, candidateHint("available tools", names))
}

// hintedError appends a correction hint to another error's message while
// keeping the wrapped error's classification visible to errors.Is.
type hintedError struct {
	err  error
	hint string
}

func (e hintedError) Error() string { return e.err.Error() + e.hint }
func (e hintedError) Unwrap() error { return e.err }

// withHint returns err carrying hint, or err untouched when there is none.
func withHint(err error, hint string) error {
	if hint == "" {
		return err
	}
	return hintedError{err: err, hint: hint}
}

// CandidateHint renders a "; label: a, b, c" correction hint for names, or ""
// when there is nothing to suggest. It lets callers outside the session layer
// name candidates in the same shape without restating the format.
func CandidateHint(label string, names []string) string {
	return candidateHint(label, names)
}

// candidateHint renders "; label: a, b, c" for the first maxCandidates names,
// summarising the remainder, or "" when there is nothing to suggest.
func candidateHint(label string, names []string) string {
	if len(names) == 0 {
		return ""
	}
	shown, more := names, ""
	if len(names) > maxCandidates {
		shown = names[:maxCandidates]
		more = fmt.Sprintf(", +%d more", len(names)-maxCandidates)
	}
	return fmt.Sprintf("; %s: %s%s", label, strings.Join(shown, ", "), more)
}
