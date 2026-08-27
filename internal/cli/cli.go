// Package cli implements the agent-mcp command surface. main only calls Run.
package cli

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/pijalu/agent-mcp/internal/output"
)

// GlobalFlags are accepted by every subcommand.
type GlobalFlags struct {
	config         string
	format         string
	connect        string
	apiKey         string
	noServer       bool
	raw            bool
	callTimeout    time.Duration
	connectTimeout time.Duration
}

// Run executes args against stdout/stderr and returns the process exit code.
func Run(args []string, stdout, stderr io.Writer) int {
	return RunWith(Deps{}, args, stdout, stderr)
}

// RunWith executes args with overridable infrastructure hooks (tests).
// Nil fields fall back to the production defaults.
func RunWith(deps Deps, args []string, stdout, stderr io.Writer) int {
	deps = deps.withDefaults()
	format := errorFormat(args, stdout)
	if len(args) == 0 {
		return fail(stdout, fmt.Errorf("%w: missing command (see --help)", output.ErrUsage), format)
	}
	if isHelpFlag(args[0]) || args[0] == "help" {
		return writeHelp(stdout)
	}
	if args[0] == "--quickstart" || args[0] == "quickstart" {
		return writeQuickstart(stdout)
	}
	if args[0] == "--version" {
		return finish(stdout, 0, writeVersion(stdout), format)
	}
	code, err := dispatch(deps, args, stdout, stderr)
	return finish(stdout, code, err, format)
}

// errorFormat resolves the --format value for error rendering before dispatch.
// Command implementations resolve the same flag for their success payload, so a
// failure is reported in the format the caller asked for. A bad --format value
// falls back to JSON here; the command reports it as a usage failure itself.
func errorFormat(args []string, stdout io.Writer) output.Format {
	format, err := output.ResolveFormat(formatFlagValue(args), stdout)
	if err != nil {
		return output.FormatJSON
	}
	return format
}

// formatFlagValue scans args for the global --format value. Global flags follow
// the command, so the scan is positional instead of per-command.
func formatFlagValue(args []string) string {
	for i, arg := range args {
		if arg == "--format" && i+1 < len(args) {
			return args[i+1]
		}
		if value, ok := strings.CutPrefix(arg, "--format="); ok {
			return value
		}
	}
	return ""
}

// withDefaults fills nil hook fields with the production implementations.
func (d Deps) withDefaults() Deps {
	if d.Executable == nil {
		d.Executable = os.Executable
	}
	if d.SpawnDaemon == nil {
		d.SpawnDaemon = spawnDaemon
	}
	if d.Dial == nil {
		d.Dial = grpcDial
	}
	if d.NewManager == nil {
		d.NewManager = newSessionManager
	}
	return d
}

// Deps bundles the replaceable infrastructure the CLI relies on.
type Deps struct {
	Executable  func() (string, error)
	SpawnDaemon func(g GlobalFlags) (DaemonChild, error)
	Dial        DialFunc
	NewManager  ManagerFactory
}

// dispatch routes args to the command implementation.
func dispatch(deps Deps, args []string, stdout, stderr io.Writer) (int, error) {
	name := args[0]
	if len(args) > 1 && isHelpFlag(args[1]) {
		return 0, writeCommandHelp(stdout, name)
	}
	if name == "version" {
		return 0, writeVersion(stdout)
	}
	if err := checkLeadingFlag(name); err != nil {
		return 2, err
	}
	if name == "server" {
		code, err := runServer(deps, args[1:], stdout, stderr)
		return helpOnFlagErr(code, err, stdout, name)
	}
	if name == "config" {
		code, err := runConfig(args[1:], stdout)
		return helpOnFlagErr(code, err, stdout, name)
	}
	env, err := parseClient(name, args[1:])
	if err != nil {
		return helpOnFlagErr(2, err, stdout, name)
	}
	return runClientCommand(deps, env, stdout, stderr)
}

// helpOnFlagErr turns a requested-help parse failure into the command's help
// section with exit 0; every other result passes through untouched.
func helpOnFlagErr(code int, err error, stdout io.Writer, name string) (int, error) {
	if errors.Is(err, flag.ErrHelp) {
		return 0, writeCommandHelp(stdout, name)
	}
	return code, err
}

// isHelpFlag reports whether arg asks for help.
func isHelpFlag(arg string) bool { return arg == "--help" || arg == "-h" }

// finish writes the error envelope when err is set and maps the exit code.
func finish(stdout io.Writer, code int, err error, format output.Format) int {
	if err == nil {
		return 0
	}
	if werr := output.WriteErrorFormat(stdout, err, format); werr != nil {
		return 3
	}
	return output.ExitCode(err)
}

// fail reports err and returns its mapped exit code.
func fail(stdout io.Writer, err error, format output.Format) int {
	if werr := output.WriteErrorFormat(stdout, err, format); werr != nil {
		return 3
	}
	return output.ExitCode(err)
}

// clientEnv carries everything a client-mode command needs after parsing.
type clientEnv struct {
	name       string // command name
	params     commandParams
	global     GlobalFlags
	format     output.Format
	stderr     io.Writer
	deps       Deps
	daemonOnly bool // command requires a daemon the caller already runs
	local      bool // command reads the config directly and never uses a daemon
}

// parseClient validates the command name, flags, and required arguments.
func parseClient(name string, args []string) (*clientEnv, error) {
	spec, ok := commandSpecs[name]
	if !ok {
		return nil, unknownCommandError(name)
	}
	g, params, err := parseFlags(name, args, spec)
	if err != nil {
		return nil, err
	}
	return &clientEnv{name: name, params: params, global: g, daemonOnly: spec.daemonOnly, local: spec.local}, nil
}

// runClientCommand resolves the format and backend, then executes.
func runClientCommand(deps Deps, env *clientEnv, stdout, stderr io.Writer) (int, error) {
	format, err := output.ResolveFormat(env.global.format, stdout)
	if err != nil {
		return 2, err
	}
	env.format = format
	env.stderr = stderr
	env.deps = deps
	return execute(env, stdout)
}

// execute runs the command against the chosen backend and writes output. A
// local command always takes the one-shot path: `list-servers` answers from
// the config, so contacting a daemon would spawn one (holding MCP child
// processes such as Chrome) only to read state the config already carries.
func execute(env *clientEnv, stdout io.Writer) (int, error) {
	if env.global.noServer || env.local {
		return env.runOneShot(stdout)
	}
	return env.runDaemonBacked(stdout)
}

// writeData renders v in the negotiated format to stdout. Text mode uses the
// command's human renderer so a person sees names, descriptions, and content
// text instead of a flattened protocol dump.
func (env *clientEnv) writeData(stdout io.Writer, v any) error {
	if env.format == output.FormatText {
		return renderText(stdout, env.name, v)
	}
	return output.WriteJSON(stdout, v)
}

// usageError marks err as a usage failure with context.
func usageError(format string, args ...any) error {
	return fmt.Errorf("%w: %s", output.ErrUsage, fmt.Sprintf(format, args...))
}

// errUsageMissing reports a missing required flag.
func errUsageMissing(flag, command string) error {
	return usageError("%s requires --%s", command, flag)
}
