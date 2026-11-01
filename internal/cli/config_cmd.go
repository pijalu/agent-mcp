package cli

import (
	"errors"
	"flag"
	"io"
	"strings"

	"github.com/pijalu/agent-mcp/client"
	"github.com/pijalu/agent-mcp/internal/config"
	"github.com/pijalu/agent-mcp/internal/output"
)

// configParams holds parsed config add flags.
type configParams struct {
	name      string
	command   string
	args      multiFlag
	env       multiFlag
	url       string
	transport string
	headers   multiFlag
}

// runConfig dispatches config subcommands.
func runConfig(args []string, stdout io.Writer) (int, error) {
	if len(args) == 0 {
		return 2, usageError("config requires a subcommand: add")
	}
	switch args[0] {
	case "add":
		return configAdd(args[1:], stdout)
	default:
		return 2, usageError("unknown config subcommand %q (want add)", args[0])
	}
}

// configAdd upserts one MCP server entry in the config file.
func configAdd(args []string, stdout io.Writer) (int, error) {
	fs := newFlagSet("config add")
	var g GlobalFlags
	var p configParams
	bindGlobal(fs, &g)
	bindConfigAdd(fs, &p)
	if err := fs.Parse(args); err != nil {
		return 2, parseError(err)
	}
	format, err := output.ResolveFormat(g.format, stdout)
	if err != nil {
		return 2, err
	}
	srv, err := p.serverConfig()
	if err != nil {
		return 2, err
	}
	result, err := client.UpsertServer(g.config, p.name, srv)
	if err != nil {
		if errors.Is(err, client.ErrUsage) {
			return 2, err
		}
		return 3, err
	}
	return 0, writeResult(stdout, format, result)
}

// bindConfigAdd registers config add flags.
func bindConfigAdd(fs *flag.FlagSet, p *configParams) {
	fs.StringVar(&p.name, "name", "", "server name (required)")
	fs.StringVar(&p.command, "command", "", "stdio server executable")
	fs.Var(&p.args, "arg", "stdio server argument (repeatable)")
	fs.Var(&p.env, "env", "stdio environment KEY=VALUE (repeatable)")
	fs.StringVar(&p.url, "url", "", "remote server URL")
	fs.StringVar(&p.transport, "transport", "", "remote transport: sse|streamable (default streamable)")
	fs.Var(&p.headers, "header", "remote header KEY=VALUE (repeatable)")
}

// serverConfig builds and validates the entry from parsed flags.
func (p configParams) serverConfig() (config.ServerConfig, error) {
	var srv config.ServerConfig
	if p.name == "" {
		return srv, errUsageMissing("name", "config add")
	}
	env, err := parseKeyValues(p.env, "env")
	if err != nil {
		return srv, err
	}
	headers, err := parseKeyValues(p.headers, "header")
	if err != nil {
		return srv, err
	}
	transport := p.transport
	if p.url != "" && transport == "" {
		transport = "streamable"
	}
	srv = config.ServerConfig{
		Command:   p.command,
		Args:      []string(p.args),
		Env:       env,
		URL:       p.url,
		Transport: transport,
		Headers:   headers,
	}
	if err := srv.Validate(); err != nil {
		return srv, usageError("config add: %v", err)
	}
	return srv, nil
}

// parseKeyValues converts repeated KEY=VALUE flags into a map.
func parseKeyValues(items []string, flagName string) (map[string]string, error) {
	if len(items) == 0 {
		return nil, nil
	}
	m := make(map[string]string, len(items))
	for _, item := range items {
		key, value, ok := strings.Cut(item, "=")
		if !ok || key == "" {
			return nil, usageError("--%s expects KEY=VALUE, got %q", flagName, item)
		}
		m[key] = value
	}
	return m, nil
}
