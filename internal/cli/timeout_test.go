package cli_test

import (
	"strings"
	"testing"
)

// TestTimeoutFlagsMustBePositive covers the trap where --call-timeout 0 or a
// negative value expired every call immediately and reported a misleading
// "context deadline exceeded" MCP failure instead of a usage error.
func TestTimeoutFlagsMustBePositive(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{"call timeout zero", []string{"--call-timeout", "0"}, "--call-timeout must be positive"},
		{"call timeout negative", []string{"--call-timeout", "-1s"}, "--call-timeout must be positive"},
		{"connect timeout zero", []string{"--connect-timeout", "0"}, "--connect-timeout must be positive"},
		{"connect timeout negative", []string{"--connect-timeout", "-5s"}, "--connect-timeout must be positive"},
	}
	for _, tc := range tests {
		t.Run("client "+tc.name, func(t *testing.T) {
			args := append([]string{"list-tools", "--server", "fx"}, tc.args...)
			code, out := run(t, args...)
			assertTimeoutUsageError(t, code, out, tc.want)
		})
		t.Run("server status "+tc.name, func(t *testing.T) {
			args := append([]string{"server", "status"}, tc.args...)
			code, out := run(t, args...)
			assertTimeoutUsageError(t, code, out, tc.want)
		})
		t.Run("server shutdown "+tc.name, func(t *testing.T) {
			args := append([]string{"server", "shutdown"}, tc.args...)
			code, out := run(t, args...)
			assertTimeoutUsageError(t, code, out, tc.want)
		})
	}
}

// assertTimeoutUsageError checks a usage-class rejection with the given text.
func assertTimeoutUsageError(t *testing.T, code int, out, want string) {
	t.Helper()
	if code != 2 {
		t.Fatalf("exit = %d, want 2 (out %s)", code, out)
	}
	if !strings.Contains(out, want) {
		t.Errorf("out %q missing %q", out, want)
	}
}
