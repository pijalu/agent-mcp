package output

import (
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
)

// Format selects the stdout rendering of the envelope data payload.
type Format string

// Supported formats for the --format flag.
const (
	FormatJSON Format = "json"
	FormatText Format = "text"
)

// ParseFormat validates a --format flag value.
func ParseFormat(s string) (Format, error) {
	switch Format(s) {
	case FormatJSON:
		return FormatJSON, nil
	case FormatText:
		return FormatText, nil
	default:
		return "", fmt.Errorf("%w: unknown --format %q (want json|text)", ErrUsage, s)
	}
}

// DefaultFormat picks the format when --format is not passed:
// text on a TTY, json otherwise.
func DefaultFormat(w io.Writer) Format {
	if isTTY(w) {
		return FormatText
	}
	return FormatJSON
}

// ResolveFormat returns the explicit flag value when set,
// otherwise DefaultFormat(w).
func ResolveFormat(flagValue string, w io.Writer) (Format, error) {
	if flagValue != "" {
		return ParseFormat(flagValue)
	}
	return DefaultFormat(w), nil
}

// isTTY reports whether w is a character-device terminal.
func isTTY(w io.Writer) bool {
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	info, err := f.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}

// WriteText renders v as flattened key: value lines for human consumption.
func WriteText(w io.Writer, v any) error {
	var lines []string
	flatten("", v, &lines)
	for _, ln := range lines {
		if _, err := fmt.Fprintln(w, ln); err != nil {
			return fmt.Errorf("%w: write text: %v", ErrInternal, err)
		}
	}
	return nil
}

// flatten walks v depth-first, appending "path: scalar" lines.
// Map keys sort for deterministic output; slices index as name[i].
func flatten(prefix string, v any, lines *[]string) {
	switch t := v.(type) {
	case map[string]any:
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			flatten(join(prefix, k), t[k], lines)
		}
	case []any:
		for i, item := range t {
			flatten(fmt.Sprintf("%s[%d]", prefix, i), item, lines)
		}
	default:
		*lines = append(*lines, fmt.Sprintf("%s: %v", prefix, t))
	}
}

// join appends key to prefix with a dot separator.
func join(prefix, key string) string {
	if prefix == "" {
		return key
	}
	return strings.Join([]string{prefix, key}, ".")
}
