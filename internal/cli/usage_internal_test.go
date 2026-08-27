package cli

import (
	"errors"
	"flag"
	"testing"

	"github.com/pijalu/agent-mcp/internal/output"
)

// TestEditDistance covers the suggestion metric with the classic examples.
func TestEditDistance(t *testing.T) {
	tests := []struct {
		a, b string
		want int
	}{
		{"", "", 0},
		{"abc", "abc", 0},
		{"list-tool", "list-tools", 1},
		{"kitten", "sitting", 3},
		{"", "abc", 3},
	}
	for _, tc := range tests {
		t.Run(tc.a+"/"+tc.b, func(t *testing.T) {
			if got := editDistance(tc.a, tc.b); got != tc.want {
				t.Errorf("editDistance(%q, %q) = %d, want %d", tc.a, tc.b, got, tc.want)
			}
			if got := editDistance(tc.b, tc.a); got != tc.want {
				t.Errorf("editDistance is not symmetric: %d, want %d", got, tc.want)
			}
		})
	}
}

// TestNearestCommand checks the suggestion cutoff on both sides.
func TestNearestCommand(t *testing.T) {
	tests := []struct{ name, want string }{
		{"list-tool", "list-tools"},
		{"calltool", "call-tool"},
		{"config", "config"},
		{"frobnicate", ""},
		{"", ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := nearestCommand(tc.name); got != tc.want {
				t.Errorf("nearestCommand(%q) = %q, want %q", tc.name, got, tc.want)
			}
		})
	}
}

// TestFlagName extracts the flag name from the shapes a leading argument can take.
func TestFlagName(t *testing.T) {
	tests := []struct {
		arg   string
		name  string
		isPtr bool
	}{
		{"--config", "config", true},
		{"-h", "h", true},
		{"--config=/tmp/x.toml", "config", true},
		{"-config", "config", true},
		{"list-tools", "", false},
		{"-", "", false},
		{"", "", false},
	}
	for _, tc := range tests {
		t.Run(tc.arg, func(t *testing.T) {
			name, ok := flagName(tc.arg)
			if ok != tc.isPtr || name != tc.name {
				t.Errorf("flagName(%q) = (%q, %v), want (%q, %v)", tc.arg, name, ok, tc.name, tc.isPtr)
			}
		})
	}
}

// TestGlobalFlagNamesMatchBindGlobal keeps the leading-flag detector in sync
// with the flags the CLI actually accepts.
func TestGlobalFlagNamesMatchBindGlobal(t *testing.T) {
	fs := newFlagSet("check")
	var g GlobalFlags
	bindGlobal(fs, &g)
	count := 0
	fs.VisitAll(func(f *flag.Flag) {
		count++
		if _, ok := globalFlagNames[f.Name]; !ok {
			t.Errorf("globalFlagNames missing %q", f.Name)
		}
	})
	if len(globalFlagNames) != count {
		t.Errorf("globalFlagNames has %d entries, bindGlobal registers %d", len(globalFlagNames), count)
	}
}

// TestParseError covers the help passthrough and the flag-phrasing rewrite.
func TestParseError(t *testing.T) {
	if err := parseError(flag.ErrHelp); !errors.Is(err, flag.ErrHelp) {
		t.Errorf("parseError(flag.ErrHelp) = %v, want ErrHelp preserved", err)
	}
	tests := []struct{ in, want string }{
		{"flag provided but not defined: -bogus", "unknown flag --bogus"},
		{"flag needs an argument: -config", "flag needs an argument: -config"},
	}
	for _, tc := range tests {
		t.Run(tc.in, func(t *testing.T) {
			err := parseError(errors.New(tc.in))
			if got := err.Error(); got != "usage error: "+tc.want {
				t.Errorf("parseError(%q) = %q, want %q", tc.in, got, "usage error: "+tc.want)
			}
			if !errors.Is(err, output.ErrUsage) {
				t.Errorf("parseError(%q) must classify as usage: %v", tc.in, err)
			}
		})
	}
}
