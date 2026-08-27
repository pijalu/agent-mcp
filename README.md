# agent-mcp

> New here? Start with the [Quickstart](docs/quickstart.md).

`agent-mcp` is a Go command-line client and local daemon for MCP (Model Context Protocol) servers. It gives scripts, agent skills, and humans one stable command surface for discovering servers and invoking their tools, resources, and prompts.

The CLI writes machine-readable results to stdout, keeps diagnostics on stderr, and returns predictable exit codes. By default, a daemon keeps configured MCP server sessions warm between calls; `--no-server` provides a stateless one-shot mode.

## Features

- stdio MCP servers launched from a configured command
- remote MCP servers using SSE or streamable HTTP transports
- tool, resource, prompt, and server discovery
- JSON arguments and verbatim MCP pagination cursors
- reusable local daemon with idle shutdown and graceful teardown
- optional API-key authentication between CLI clients and daemon
- JSON output for automation and text output for interactive use

## Installation

### Build from source

```sh
git clone https://github.com/pijalu/agent-mcp.git
cd agent-mcp
make build
```

The build stamps the version into the binary (`make build VERSION=v1.0.0` overrides
the git-derived default); `agent-mcp --version` and `agent-mcp version` print it.
A plain `go build` leaves the development default `0.1.0` in place.

Building from source requires Go 1.25 or newer; all dependencies, including `github.com/modelcontextprotocol/go-sdk`, are resolved from published modules and no local `replace` directive is needed.

### Install a published version

```sh
go install github.com/pijalu/agent-mcp/cmd/agent-mcp@latest
```

For reproducible deployments, pin a release or commit rather than using `latest`.

## Configuration

Configuration is TOML. The default path is `~/.agent-mcp/config.toml`; every client command accepts `--config PATH` to select another file. The root table is `mcpServers`; optional `api_keys` authenticates daemon clients.

```toml
api_keys = ["replace-with-a-long-random-value"]

[mcpServers.filesystem]
command = "npx"
args = ["-y", "@modelcontextprotocol/server-filesystem", "/tmp/allowed"]
env = { API_KEY = "server-secret" }

[mcpServers.remote]
url = "https://example.example/mcp"
transport = "streamable" # sse or streamable
headers = { Authorization = "Bearer replace-me" }
```

Instead of editing the TOML by hand, add or update a server with the CLI (upserts by `--name`; respects `--config PATH`):

```sh
# stdio server (e.g. Chrome DevTools)
agent-mcp config add --name chrome-devtools --command npx --arg -y --arg chrome-devtools-mcp@latest

# remote server
agent-mcp config add --name remote --url https://example.example/mcp \
  --transport streamable --header "Authorization=Bearer replace-me"
```

Repeatable flags: `--arg`, `--env KEY=VALUE` (stdio), `--header KEY=VALUE` (remote). `--transport` defaults to `streamable` when `--url` is set.

Server entries must set exactly one of `command` or `url`:

- `command` selects stdio transport. `transport` is omitted or `stdio`.
- `url` selects `sse` or `streamable` transport through `transport`.
- `args` and `env` apply to command-launched servers.
- `headers` applies to URL transports.

Keep configuration permissions restrictive because it can contain credentials:

```sh
mkdir -p ~/.agent-mcp
chmod 700 ~/.agent-mcp
chmod 600 ~/.agent-mcp/config.toml
```

## Commands

```text
agent-mcp list-servers
agent-mcp events         [--server NAME]
agent-mcp list-tools     --server NAME [--cursor CURSOR]
agent-mcp call-tool      --server NAME --tool NAME [--args JSON]
agent-mcp list-resources --server NAME [--cursor CURSOR]
agent-mcp read-resource  --server NAME --uri URI
agent-mcp list-prompts   --server NAME [--cursor CURSOR]
agent-mcp get-prompt     --server NAME --prompt NAME [--args JSON]
agent-mcp config add     --name NAME (--command CMD | --url URL) [flags]
agent-mcp server start|status|shutdown
agent-mcp --version | version
```

`agent-mcp COMMAND --help` (or `-h`) prints that command's section and exits 0; the
flag is honored in any position, so `agent-mcp call-tool --server NAME --help` works
as well as `agent-mcp call-tool --help`. A mistyped command reports the closest match
(`did you mean "list-tools"?`) plus the full command list, and a global flag written
before the command is reported as misplaced rather than as an unknown command.

Global client flags:

| Flag | Meaning | Default |
|---|---|---|
| `--config PATH` | configuration file | `~/.agent-mcp/config.toml` |
| `--format json\|text` | output rendering | text on TTY, JSON otherwise |
| `--connect HOST:PORT` | daemon address | `127.0.0.1:7474` |
| `--api-key KEY` | daemon authentication key | unset |
| `--no-server` | run in-process and tear down before exit | false |
| `--raw` | write the full MCP result: no metadata stripping, no list totals | false |
| `--call-timeout DURATION` | per-MCP-call timeout | `30s` |
| `--connect-timeout DURATION` | daemon connection/startup timeout | `5s` |

Unknown flags report the CLI spelling: `unknown flag --bogus` rather than the
standard library's `flag provided but not defined: -bogus`.

`call-tool --args` accepts a JSON object. `get-prompt --args` accepts a JSON object whose values are strings.

List commands paginate with `--cursor`, passed to MCP unchanged; the response `nextCursor` is empty when there are no more pages. They also support `--search TERM` (case-insensitive match against name, title, description, or URI) and `--limit N` (default 10 for tool/resource/prompt lists, unlimited for `list-servers`; `0` disables the cap). Filtering and limiting apply to the current server-returned page only — use `--cursor` to fetch more pages. List payloads report `total` (matches before `--limit`) and set `truncated: true` when `--limit` dropped matches, so a shortened list is never mistaken for a complete one.

## Output and exit codes

Successful invocations emit one result object on stdout. JSON output is wrapped in a versioned envelope:

```json
{"version":1,"data":{}}
```

Failures use the corresponding envelope:

```json
{"version":1,"error":{"code":"...","message":"..."}}
```

Default payloads are reduced: MCP transport bookkeeping (`_meta`, `resultType`, `cacheScope`, `ttlMs`) is stripped, and list payloads carry their `total`. `--raw` writes the MCP result as the server returned it, metadata included. Unknown server and unknown tool failures name the available candidates (`configured servers: ...`, `available tools: ...`), and failures honour `--format text` by emitting `error.code`/`error.message` lines.

A tool that reports a failure result (`isError: true` in the MCP result) is not a success: the CLI emits the failure envelope with `code: "mcp"` and the server's own text as the message, sets no `data` field, and exits `1`. Transport failures and rejected API keys (`daemon at ADDR rejected the API key (check --api-key)`) use the same `mcp` class and exit `1`.

stderr is reserved for human-readable diagnostics such as daemon startup messages. Exit codes are:

- `0` — success
- `1` — MCP failure (tool reported `isError`, server unreachable, rejected API key, config mismatch) or connectivity failure
- `2` — usage, flag, or configuration failure
- `3` — internal failure

Text mode is intended for humans and renders per command: `list-servers`, `list-tools`, `list-resources`, and `list-prompts` print one line per item with its name and description (with a truncation/next-page footer when `--limit` or `--cursor` apply), while `call-tool`, `read-resource`, and `get-prompt` print their content text. Commands without a human rendering (and failures) fall back to deterministic `key: value` lines. Use `--format json` for scripts.

## Daemon lifecycle

Client commands auto-start a daemon when none is listening on the configured address, printing an stderr hint that names the address plus a `server shutdown` reminder (the daemon outlives the command, so the reminder is the pointer to teardown). `list-servers` is the exception: the server inventory is a config property, so it reads the config directly, never dials and never starts a daemon. `events` is the other exception: it follows a live subscription, so it requires a daemon that is already running. It rejects `--no-server` (exit 2, `events requires the daemon`) and never auto-starts one: an unreachable address exits 1 with the address and a start hint. A `--server` name the daemon does not serve is rejected before subscribing (exit 2, naming the configured servers), and a watch ends at the `--call-timeout` deadline (exit 1) rather than running forever. The daemon listens on `127.0.0.1:7474` unless overridden and multiplexes clients over protobuf RPC. Each configured MCP server has one managed session, with requests serialized per session.

`--config` selects the configuration the daemon serves. When a daemon is already listening, the client compares its `--config` with the config the daemon reports in `Status` and fails with `config mismatch: daemon at ADDR serves X but this client uses Y` (exit 1) rather than silently answering from another configuration; `agent-mcp server status` prints the served `config_path`.

Auto-start failures are diagnosable: the spawned daemon's stdout/stderr is captured, so a daemon that dies during startup (for example because `--config` names a missing file) is reported with the child's own error message and exit code — a config-load failure exits 2 — instead of an opaque readiness timeout.

Start manually in the foreground:

```sh
agent-mcp server start \
    --listen 127.0.0.1:7474 \
    --config ~/.agent-mcp/config.toml \
    --api-key "$AGENT_MCP_API_KEY"
```

Manual starts have no idle timeout unless `--server-idle-time` is supplied. Auto-started daemons default to a five-minute idle timeout. Other server flags include `--shutdown-grace` (default `10s`) and repeatable `--api-key`.

Inspect or stop a daemon:

```sh
agent-mcp server status --connect 127.0.0.1:7474 --api-key "$AGENT_MCP_API_KEY"
agent-mcp server shutdown --connect 127.0.0.1:7474 --api-key "$AGENT_MCP_API_KEY"
```

Shutdown drains in-flight calls, closes all MCP sessions, and stops the daemon. It is idempotent: when no daemon is running, it exits successfully with `shutdown: false`.

For isolated automation, skip the daemon:

```sh
agent-mcp call-tool --no-server \
    --config ./config.toml --server filesystem --tool read_file \
    --args '{"path":"/tmp/allowed/notes.txt"}'
```

## Using from agent skills

Treat `agent-mcp` as a subprocess boundary: read exactly one JSON object from stdout, use stderr only for diagnostics, and branch on the exit code. Example:

```sh
set -euo pipefail
result="$(agent-mcp call-tool --no-server --server filesystem \
    --tool read_file --args '{"path":"/tmp/allowed/notes.txt"}')"
printf '%s\n' "$result" | jq -r '.data.content[0].text'
```

Call `agent-mcp server shutdown` during cleanup when a skill used daemon mode.

## Chrome DevTools automation

The repository includes a reusable skill at [`.agents/skills/chrome-devtools/SKILL.md`](.agents/skills/chrome-devtools/SKILL.md) for driving Chrome through the official [`chrome-devtools-mcp`](https://github.com/ChromeDevTools/chrome-devtools-mcp) server. Prerequisites: Node.js with `npx`, and Chrome or Chromium.

Register the server with the CLI:

```sh
agent-mcp config add --name chrome --command npx --arg -y --arg chrome-devtools-mcp@latest
```

or add the equivalent TOML entry by hand:

```toml
[mcpServers.chrome]
command = "npx"
args = ["-y", "chrome-devtools-mcp@latest"]
```

Discover tools before use because upstream tool schemas can change:

```sh
agent-mcp list-tools --server chrome
agent-mcp call-tool --server chrome --tool new_page \
    --args '{"url":"https://www.google.com"}'
```

`new_page` returns a page ID. Pass that ID to page-scoped tools such as `take_snapshot`:

```sh
agent-mcp call-tool --server chrome --tool take_snapshot \
    --args '{"pageId":2}'
```

Browser sessions hold Chrome open, so shut down the daemon after browser automation:

```sh
agent-mcp server shutdown
```

The live Chrome E2E test is opt-in. It opens Google, takes a DOM/accessibility snapshot, verifies the snapshot contains `google`, then shuts down the daemon and MCP session:

```sh
AGENT_MCP_CHROME_E2E=1 go test ./internal/cli -run TestChromeDevToolsGoogle -v
```

Normal test runs skip this test because it requires Chrome, Node.js, network access, and `npx`.

## Architecture

- `cmd/agent-mcp` — executable entry point
- `internal/cli` — command parsing, daemon/client selection, rendering
- `internal/config` — TOML loading, defaults, and validation
- `internal/session` — MCP transport/session management and serialized calls
- `internal/daemon` — protobuf RPC service, authentication, lifecycle, and reaping
- `internal/output` — JSON envelopes, text rendering, and exit-code mapping
- `proto/agentmcp/v1` — generated protobuf and gRPC bindings

The daemon/client protocol carries MCP parameters and results as raw JSON bytes so the internal RPC layer is decoupled from individual MCP schema revisions. The full contract is documented in [`docs/spec/agent-mcp-spec.md`](docs/spec/agent-mcp-spec.md).

## Security considerations

- Local daemon RPC uses an insecure TCP connection; API keys authenticate clients but do not encrypt traffic. Bind to loopback or place the endpoint behind a trusted encrypted network.
- Treat `--api-key`, `api_keys`, `env`, and `headers` as secrets. Avoid putting credentials in shell history or process listings where possible.
- MCP servers execute with the permissions of the launching user. Restrict filesystem paths and review remote endpoints before adding them.
- `--no-server` limits daemon persistence but still launches configured MCP processes for the duration of one command.

## Development

Requirements: Go 1.25, `protoc`, and the Go protobuf/gRPC plugins for protobuf regeneration.

```sh
go build ./...
go test ./...
go test -race ./...
./scripts/check-coverage.sh
./scripts/ci.sh
./scripts/gen-proto.sh
```

`./scripts/ci.sh` runs formatting, vet, build, race tests, the 80% per-package coverage gate, and `golang-check` (complexity and static analysis). Run it before submitting changes. Generated protobuf files should be regenerated with `scripts/gen-proto.sh`, not edited by hand.

## License

Copyright (C) 2026 Pierre Poissinger.

This project is licensed under the GNU General Public License, version 3 or later. See [`LICENSE`](LICENSE).
