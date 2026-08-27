package cli

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/pijalu/agent-mcp/internal/output"
)

// commandParams holds the parsed per-command flags.
type commandParams struct {
	server string
	cursor string
	tool   string
	uri    string
	prompt string
	args   string
	search string
	limit  int
}

// commandSpec describes one client-mode subcommand: bind registers the
// command-specific flags, validate checks required flags after parsing, and
// daemonOnly marks commands whose work belongs to a running daemon.
type commandSpec struct {
	bind     func(fs *flag.FlagSet, p *commandParams)
	validate func(name string, p commandParams) error
	// daemonOnly commands neither run one-shot nor auto-start a daemon: a
	// subscription only means something against a daemon the caller runs.
	daemonOnly bool
	// local commands always run one-shot: their answer is a property of the
	// config, so a daemon would only add a child process to hold. They never
	// dial and never auto-start one.
	local bool
}

// commandSpecs maps command names to their flag specs.
var commandSpecs = map[string]commandSpec{
	"list-servers":   {bind: bindNone, validate: validateNone, local: true},
	"list-tools":     {bind: bindList, validate: validateServer},
	"call-tool":      {bind: bindCallTool, validate: validateCallTool},
	"list-resources": {bind: bindList, validate: validateServer},
	"read-resource":  {bind: bindReadResource, validate: validateReadResource},
	"list-prompts":   {bind: bindList, validate: validateServer},
	"get-prompt":     {bind: bindGetPrompt, validate: validateGetPrompt},
	"events":         {bind: bindEvents, validate: validateNone, daemonOnly: true},
}

// newFlagSet builds a FlagSet that never writes to the real stderr and
// reports parse problems as usage errors.
func newFlagSet(name string) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	return fs
}

// validateTimeouts rejects non-positive timeouts. A zero or negative value
// expires every call immediately and reports a misleading deadline error.
func validateTimeouts(g GlobalFlags) error {
	if g.callTimeout <= 0 {
		return usageError("--call-timeout must be positive, got %s", g.callTimeout)
	}
	if g.connectTimeout <= 0 {
		return usageError("--connect-timeout must be positive, got %s", g.connectTimeout)
	}
	return nil
}

// parseFlags parses args for name with both global and command-specific
// flags bound on one FlagSet, so flag order does not matter.
func parseFlags(name string, args []string, spec commandSpec) (GlobalFlags, commandParams, error) {
	var g GlobalFlags
	var p commandParams
	fs := newFlagSet(name)
	bindGlobal(fs, &g)
	spec.bind(fs, &p)
	if err := fs.Parse(args); err != nil {
		return g, p, parseError(err)
	}
	if fs.NArg() != 0 {
		return g, p, usageError("%s: unexpected argument %q", name, fs.Arg(0))
	}
	if err := validateTimeouts(g); err != nil {
		return g, p, err
	}
	if spec.daemonOnly && g.noServer {
		return g, p, usageError("%s requires the daemon: --no-server cannot follow notifications", name)
	}
	if p.limit < 0 {
		return g, p, usageError("--limit must not be negative")
	}
	if err := spec.validate(name, p); err != nil {
		return g, p, err
	}
	return g, p, nil
}

// parseError maps a flag.Parse failure onto the CLI's usage-error vocabulary.
// A requested help screen (flag.ErrHelp) passes through untouched so the caller
// can print the command's help section instead of an error envelope.
func parseError(err error) error {
	if errors.Is(err, flag.ErrHelp) {
		return err
	}
	return fmt.Errorf("%w: %s", output.ErrUsage, flagErrorText(err))
}

// flagErrorText rewrites the stdlib flag package's phrasing into the CLI's
// uniform --flag style: "flag provided but not defined: -bogus" becomes
// "unknown flag --bogus".
func flagErrorText(err error) string {
	const undefined = "flag provided but not defined: -"
	msg := err.Error()
	if rest, ok := strings.CutPrefix(msg, undefined); ok {
		return "unknown flag --" + rest
	}
	return msg
}

// bindGlobal registers the global flags on fs.
func bindGlobal(fs *flag.FlagSet, g *GlobalFlags) {
	fs.StringVar(&g.config, "config", "", "path to config.toml")
	fs.StringVar(&g.format, "format", "", "output format: json|text")
	fs.StringVar(&g.connect, "connect", "", "daemon address host:port")
	fs.StringVar(&g.apiKey, "api-key", "", "daemon API key")
	fs.BoolVar(&g.noServer, "no-server", false, "run one-shot without the daemon")
	fs.BoolVar(&g.raw, "raw", false, "write the full MCP result: no metadata stripping, no list totals")
	fs.DurationVar(&g.callTimeout, "call-timeout", 30*time.Second, "per-MCP-call timeout")
	fs.DurationVar(&g.connectTimeout, "connect-timeout", defaultConnectTimeout, "daemon connect timeout")
}

// bindNone registers the list-servers flags. The server list is unbounded by
// default: a configured-server count must never be silently wrong.
func bindNone(fs *flag.FlagSet, p *commandParams) {
	fs.StringVar(&p.search, "search", "", "case-insensitive search in name and description")
	fs.IntVar(&p.limit, "limit", 0, "maximum matching items to return (0 = unlimited; default 0)")
}

func bindEvents(fs *flag.FlagSet, p *commandParams) {
	fs.StringVar(&p.server, "server", "", "server name (empty for all)")
}

// bindList registers list command flags. Tool, resource, and prompt lists stay
// capped by default to keep default payloads small; they report totals so the
// cap is never silent.
func bindList(fs *flag.FlagSet, p *commandParams) {
	fs.StringVar(&p.server, "server", "", "server name")
	fs.StringVar(&p.cursor, "cursor", "", "pagination cursor")
	fs.StringVar(&p.search, "search", "", "case-insensitive search in name and description")
	fs.IntVar(&p.limit, "limit", 10, "maximum matching items to return (0 = unlimited; default 10)")
}

// bindCallTool registers call-tool flags.
func bindCallTool(fs *flag.FlagSet, p *commandParams) {
	fs.StringVar(&p.server, "server", "", "server name")
	fs.StringVar(&p.tool, "tool", "", "tool name")
	fs.StringVar(&p.args, "args", "", "tool arguments as JSON")
}

// bindReadResource registers read-resource flags.
func bindReadResource(fs *flag.FlagSet, p *commandParams) {
	fs.StringVar(&p.server, "server", "", "server name")
	fs.StringVar(&p.uri, "uri", "", "resource URI")
}

// bindGetPrompt registers get-prompt flags.
func bindGetPrompt(fs *flag.FlagSet, p *commandParams) {
	fs.StringVar(&p.server, "server", "", "server name")
	fs.StringVar(&p.prompt, "prompt", "", "prompt name")
	fs.StringVar(&p.args, "args", "", "prompt arguments as JSON")
}

// validateNone accepts anything (list-servers has no required flags).
func validateNone(_ string, _ commandParams) error { return nil }

// validateServer requires --server.
func validateServer(name string, p commandParams) error {
	if p.server == "" {
		return errUsageMissing("server", name)
	}
	return nil
}

// validateCallTool requires --server and --tool.
func validateCallTool(name string, p commandParams) error {
	if err := validateServer(name, p); err != nil {
		return err
	}
	if p.tool == "" {
		return errUsageMissing("tool", name)
	}
	return nil
}

// validateReadResource requires --server and --uri.
func validateReadResource(name string, p commandParams) error {
	if err := validateServer(name, p); err != nil {
		return err
	}
	if p.uri == "" {
		return errUsageMissing("uri", name)
	}
	return nil
}

// validateGetPrompt requires --server and --prompt.
func validateGetPrompt(name string, p commandParams) error {
	if err := validateServer(name, p); err != nil {
		return err
	}
	if p.prompt == "" {
		return errUsageMissing("prompt", name)
	}
	return nil
}
