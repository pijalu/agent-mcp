package cli

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/pijalu/agent-mcp/internal/output"
)

// stubChild is a DaemonChild with fixed state for internal unit tests.
type stubChild struct {
	code   int
	exited bool
	out    string
}

func (c *stubChild) Exited() (int, bool) { return c.code, c.exited }

func (c *stubChild) Output() string { return c.out }

func (c *stubChild) Cleanup() {}

// TestConnectTimeoutHasOneSource pins the single source of the connect
// timeout: the --connect-timeout flag default and the lookup waitReady,
// probe, probeCtx, and the dialer all use are the same value, and the
// "unset" case is explicit rather than an implicit per-call fallback.
func TestConnectTimeoutHasOneSource(t *testing.T) {
	fs := newFlagSet("t")
	var g GlobalFlags
	bindGlobal(fs, &g)
	if err := fs.Parse(nil); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if g.connectTimeout != defaultConnectTimeout {
		t.Errorf("flag default = %s, want %s", g.connectTimeout, defaultConnectTimeout)
	}

	tests := []struct {
		name string
		flag time.Duration
		want time.Duration
	}{
		{name: "unset falls back to the documented default", flag: 0, want: defaultConnectTimeout},
		{name: "negative is treated as unset", flag: -time.Second, want: defaultConnectTimeout},
		{name: "explicit value wins", flag: 250 * time.Millisecond, want: 250 * time.Millisecond},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			env := &clientEnv{global: GlobalFlags{connectTimeout: tc.flag}}
			if got := env.connectTimeoutValue(); got != tc.want {
				t.Errorf("connectTimeoutValue() = %s, want %s", got, tc.want)
			}
		})
	}
}

// TestWaitReadyUsesConnectTimeout proves the readiness poll is bounded by the
// same value and reports it, so a stale 5s constant cannot diverge.
func TestWaitReadyUsesConnectTimeout(t *testing.T) {
	const budget = 120 * time.Millisecond
	env := &clientEnv{global: GlobalFlags{connectTimeout: budget}}
	start := time.Now()
	err := env.waitReady("127.0.0.1:1", &stubChild{})
	elapsed := time.Since(start)
	if !errors.Is(err, output.ErrMCP) {
		t.Fatalf("err = %v, want an MCP error", err)
	}
	if !strings.Contains(err.Error(), "not ready within "+budget.String()) {
		t.Errorf("err = %v, want it to name the configured budget %s", err, budget)
	}
	if elapsed < budget || elapsed > 2*time.Second {
		t.Errorf("waitReady took %s, want ~%s", elapsed, budget)
	}
}

// TestWaitReadyReportsExitedChild proves a child that dies during startup fails
// fast with its own message and error class.
func TestWaitReadyReportsExitedChild(t *testing.T) {
	env := &clientEnv{global: GlobalFlags{connectTimeout: 5 * time.Second}}
	child := &stubChild{exited: true, code: 2, out: "usage error: config: load /nope.toml: no such file\n"}
	start := time.Now()
	err := env.waitReady("127.0.0.1:1", child)
	if !errors.Is(err, output.ErrUsage) {
		t.Fatalf("err = %v, want a usage error", err)
	}
	if !strings.Contains(err.Error(), "config: load /nope.toml") {
		t.Errorf("err = %v, want the child's own message", err)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Errorf("waitReady took %s, want a fast failure", elapsed)
	}
}

// TestDaemonStartupErrorMapping pins the child exit code -> error class mapping
// used for the client's exit code.
func TestDaemonStartupErrorMapping(t *testing.T) {
	tests := []struct {
		name string
		code int
		want error
	}{
		{"usage", 2, output.ErrUsage},
		{"internal", 3, output.ErrInternal},
		{"mcp", 1, output.ErrMCP},
		{"clean exit", 0, output.ErrMCP},
		{"signalled", -1, output.ErrMCP},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := daemonStartupError("127.0.0.1:1", tc.code, "boom")
			if !errors.Is(err, tc.want) {
				t.Errorf("err = %v, want %v", err, tc.want)
			}
			if !strings.Contains(err.Error(), "boom") {
				t.Errorf("err = %v, want the child output", err)
			}
		})
	}
}

// TestExitCodeMapping covers the Wait-error -> exit code conversion.
func TestExitCodeMapping(t *testing.T) {
	if got := exitCode(nil); got != 0 {
		t.Errorf("exitCode(nil) = %d, want 0", got)
	}
	if got := exitCode(errors.New("wait: no child processes")); got != -1 {
		t.Errorf("exitCode(non-wait error) = %d, want -1", got)
	}
}

// TestChildDetailUnwrapsEnvelope keeps the child's cause readable instead of
// nesting one JSON envelope inside another.
func TestChildDetailUnwrapsEnvelope(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want string
	}{
		{
			name: "json error envelope",
			raw:  `{"version":1,"error":{"code":"usage","message":"config: load /nope.toml: no such file"}}`,
			want: "config: load /nope.toml: no such file",
		},
		{
			name: "plain stderr and envelope",
			raw:  "listening on 127.0.0.1:1\n{bogus\n" + `{"error":{"message":"listen denied"}}` + "\n",
			want: "listening on 127.0.0.1:1; {bogus; listen denied",
		},
		{name: "silent child", raw: "\n \n", want: "(no output)"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := childDetail(tc.raw); got != tc.want {
				t.Errorf("childDetail() = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestChildDetailIsBounded keeps a chatty child from flooding the envelope.
func TestChildDetailIsBounded(t *testing.T) {
	got := childDetail(strings.Repeat("x", maxChildDetail*2))
	if len(got) > maxChildDetail+3 {
		t.Errorf("detail length = %d, want <= %d", len(got), maxChildDetail+3)
	}
	if !strings.HasSuffix(got, "...") {
		t.Errorf("truncated detail must be marked: %q", got[len(got)-10:])
	}
}
