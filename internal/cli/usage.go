package cli

import (
	"flag"
	"sort"
	"strings"
)

// maxSuggestionDistance bounds how far a typo may be from a real command name
// before the CLI stops guessing and only lists the valid commands.
const maxSuggestionDistance = 2

// globalFlagNames is the set of flags bindGlobal registers, derived from the
// same definition so the two cannot drift.
var globalFlagNames = func() map[string]struct{} {
	fs := newFlagSet("globals")
	var g GlobalFlags
	bindGlobal(fs, &g)
	names := make(map[string]struct{})
	fs.VisitAll(func(f *flag.Flag) { names[f.Name] = struct{}{} })
	return names
}()

// checkLeadingFlag explains the position of a flag written before the command.
// Without this the dispatcher reads "--config" as a command name and reports the
// misleading `unknown command "--config"`.
func checkLeadingFlag(arg string) error {
	name, ok := flagName(arg)
	if !ok {
		return nil
	}
	if _, known := globalFlagNames[name]; known {
		return usageError("global flag --%s must follow the command: agent-mcp COMMAND --%s", name, name)
	}
	return usageError("unknown flag %s; flags follow the command: agent-mcp COMMAND %s", arg, arg)
}

// flagName extracts the flag name from an argument that looks like a flag
// (--name, -name, --name=value); ok is false for commands and arguments.
func flagName(arg string) (string, bool) {
	if len(arg) < 2 || arg[0] != '-' {
		return "", false
	}
	name := strings.TrimLeft(arg, "-")
	if i := strings.IndexByte(name, '='); i >= 0 {
		name = name[:i]
	}
	if name == "" {
		return "", false
	}
	return name, true
}

// unknownCommandError names every known command and, for a likely typo, the
// command the user probably meant.
func unknownCommandError(name string) error {
	known := strings.Join(knownCommands(), ", ")
	if guess := nearestCommand(name); guess != "" {
		return usageError("unknown command %q; did you mean %q? known commands: %s", name, guess, known)
	}
	return usageError("unknown command %q; known commands: %s", name, known)
}

// knownCommands returns every dispatchable command name in sorted order.
func knownCommands() []string {
	names := make([]string, 0, len(commandSpecs)+3)
	for name := range commandSpecs {
		names = append(names, name)
	}
	names = append(names, "server", "config", "version")
	sort.Strings(names)
	return names
}

// nearestCommand returns the known command closest to name within
// maxSuggestionDistance edits, or "" when nothing is close enough.
func nearestCommand(name string) string {
	best := ""
	bestDistance := maxSuggestionDistance + 1
	for _, cmd := range knownCommands() {
		if d := editDistance(name, cmd); d < bestDistance {
			best, bestDistance = cmd, d
		}
	}
	return best
}

// editDistance returns the Levenshtein distance between a and b.
func editDistance(a, b string) int {
	prev := make([]int, len(b)+1)
	curr := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		curr[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			curr[j] = min(prev[j]+1, curr[j-1]+1, prev[j-1]+cost)
		}
		prev, curr = curr, prev
	}
	return prev[len(b)]
}
