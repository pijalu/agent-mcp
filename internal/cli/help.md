# agent-mcp — command-line bridge for MCP servers

## Usage

```text
agent-mcp COMMAND [FLAGS]
agent-mcp --version | version        Print the build version
agent-mcp --quickstart | quickstart Print agent usage instructions
agent-mcp --help | help              Print this help
```

## Commands

```text
list-servers [--search TERM] [--limit N] List configured MCP servers
events [--server NAME]                       Follow server notifications
list-tools --server NAME             List tools (use --cursor, --search, --limit)
call-tool --server NAME --tool NAME  Call a tool; --args accepts JSON object
list-resources --server NAME         List resources (use --cursor, --search, --limit)
read-resource --server NAME --uri URI
list-prompts --server NAME           List prompts (use --cursor, --search, --limit)
get-prompt --server NAME --prompt NAME
config add --name NAME ...           Add or update an MCP server in the config
server start|status|shutdown         Manage the local daemon
version                              Print the build version
```

## list-servers

Lists configured MCP servers and their current state.

Reads the config directly: no daemon is contacted or started, so it holds no MCP child processes. The other discovery commands (`list-tools`, `list-resources`, `list-prompts`) auto-start a daemon when none is listening and print an stderr hint plus a `server shutdown` reminder, because that daemon outlives the command.

- `--search TERM` filters by server name or details.
- Unbounded by default: every configured server is listed, and the payload reports `total`.
- `--limit N` caps the list; when it drops servers the payload adds `truncated: true` alongside `total`.

## list-tools

Requires `--server NAME`.

- `--cursor CURSOR` passes an MCP pagination cursor unchanged.
- `--search TERM` filters the current page by tool name, title, or description.
- `--limit N` limits matches; default is 10.
- `--limit 0` disables the local limit.
- The payload reports `total` (matches before `--limit`) and adds `truncated: true` when `--limit` dropped matches.
- `nextCursor` remains available for subsequent pages.

## call-tool

Requires:

- `--server NAME`
- `--tool NAME`

Optional `--args JSON` must be a JSON object.

The binary-content flags below also apply to `read-resource`.

## Binary content (`--binaries-policy`, `--save-binaries`)

Tools like screenshot or file readers return binary payloads (images, audio,
PDFs) as base64 inline. `call-tool` and `read-resource` rewrite those payloads
according to `--binaries-policy POLICY`:

- `file` (default): decodes the payload, writes it to disk, and replaces the
  payload with a pointer marker: `[binary: image/png → /path/part-01.png (7 B)]`.
  Without `--save-binaries DIR`, artifacts go to a fresh temporary directory
  (`agent-mcp-binaries-*`); directories older than 24h are removed on the next
  run.
- `dataurl`: keeps the payload inline as a `data:<mime>;base64,…` URL. Text
  output summarizes it (`[binary: image/png dataurl (7 B)]`) — the URL itself is
  never printed.
- `strip`: replaces the payload with `[binary: <mime> stripped]`.
- `keep`: leaves the payload byte-for-byte unchanged (the library default;
  text output still shows only `[image: <mime>]`).

`--save-binaries DIR` chooses the directory the `file` policy writes to
(created on demand). Applies to `--raw` too: the policy is a caller-directed
content transform, not payload shaping. If a payload cannot be decoded or
stored, the part degrades to `[binary: <mime> undecodable: …]` /
`[binary: <mime> not saved: …]` and the rest of the result stays usable.

## list-resources

Requires `--server NAME`.

- `--cursor CURSOR` passes an MCP pagination cursor unchanged.
- `--search TERM` filters the current page by resource name, title, description, or URI.
- `--limit N` limits matches; default is 10.
- `--limit 0` disables the local limit.
- The payload reports `total` and adds `truncated: true` when matches were dropped.
- `nextCursor` remains available for subsequent pages.

## read-resource

Requires:

- `--server NAME`
- `--uri URI`.

Read-resource accepts the same binary-content flags as call-tool (`--binaries-policy`, `--save-binaries`); see "Binary content" above. Resource blobs follow the identical policy and markers.

## list-prompts

Requires `--server NAME`.

- `--cursor CURSOR` passes an MCP pagination cursor unchanged.
- `--search TERM` filters the current page by prompt name, title, or description.
- `--limit N` limits matches; default is 10.
- `--limit 0` disables the local limit.
- The payload reports `total` and adds `truncated: true` when matches were dropped.
- `nextCursor` remains available for subsequent pages.

## get-prompt

Requires:

- `--server NAME`
- `--prompt NAME`

Optional `--args JSON` must be a JSON object whose values are strings.

## events

Follows MCP notifications (progress, logging, list-changed) streamed by the daemon. One JSON event per line until the daemon closes the stream or the `--call-timeout` deadline ends the watch.

- Requires a running daemon: `--no-server` is a usage error (exit 2), and events never auto-starts a daemon. Start one with `agent-mcp server start`.
- `--server NAME` restricts the stream to one server; empty follows all servers. An unknown name is a usage error (exit 2) that names the configured servers, so a typo fails immediately instead of waiting for events that cannot arrive.
- The watch is bounded by `--call-timeout` (default `30s`); raise it to follow for longer. Reaching the deadline exits 1 with a message that says so.

```text
agent-mcp events --server filesystem --call-timeout 5m
```

## config

Subcommand: `add` — add or update an MCP server entry without editing the TOML file. Writes to `--config PATH` or the default `~/.agent-mcp/config.toml`; the file is rewritten wholesale, so comments are dropped.

Requires:

- `--name NAME`
- one of `--command CMD` (stdio server) or `--url URL` (remote server)

Options:

- `--arg ARG` repeatable stdio arguments.
- `--env KEY=VALUE` repeatable stdio environment variables.
- `--transport sse|streamable` remote transport; defaults to `streamable` when `--url` is set.
- `--header KEY=VALUE` repeatable remote HTTP headers.

Examples:

```sh
agent-mcp config add --name chrome-devtools --command npx --arg -y --arg chrome-devtools-mcp@latest
agent-mcp config add --name remote --url https://example.com/mcp --header "Authorization=Bearer TOKEN"
```

Running `config add` again with an existing `--name` updates that entry. A running daemon picks up changes on next start; use `agent-mcp server shutdown` to restart it.

## server

Subcommands: `start`, `status`, and `shutdown`. See README for lifecycle details.

## version

Prints the build version stamped into the binary at link time (`make build`, or
`-ldflags "-X github.com/pijalu/agent-mcp/internal/version.Version=<version>"`).
Both `agent-mcp version` and `agent-mcp --version` print it and exit 0.

## Global flags

Place global flags after the command.

| Flag | Description | Default |
|---|---|---|
| `--config PATH` | TOML configuration file | `~/.agent-mcp/config.toml` |
| `--format json\|text` | JSON for agents; text for humans | automatic |
| `--connect HOST:PORT` | daemon address | `127.0.0.1:7474` |
| `--api-key KEY` | daemon authentication key | unset |
| `--no-server` | run one call in-process, then tear it down | false |
| `--raw` | write the full MCP result: no metadata stripping, no list totals | false |
| `--call-timeout DURATION` | MCP call timeout | `30s` |
| `--connect-timeout DURATION` | daemon startup timeout | `5s` |

## Agent protocol

Read one JSON object from stdout.

- Success: `{"version":1,"data":...}`
- Failure: `{"version":1,"error":{"code":...,"message":...}}`
- stderr contains diagnostics.
- Exit codes: `0` success, `1` MCP/connectivity, `2` usage/configuration, `3` internal.

A tool that reports a failure result (`isError: true`) is an invocation failure, not data: the CLI emits the error envelope with `code: "mcp"`, the server's text as `message`, no `data` field, and exit `1`. An unreachable server, a rejected API key (`daemon at ADDR rejected the API key (check --api-key)`), and a daemon serving a different `--config` (`config mismatch`) exit `1` as well.

Default payloads are reduced for token cost: MCP transport bookkeeping (`_meta`, `resultType`, `cacheScope`, `ttlMs`) is stripped, and list payloads report their match `total`. Pass `--raw` to get the MCP result exactly as the server returned it, metadata included.

Unknown server and unknown tool failures name the available candidates (`configured servers: ...`, `available tools: ...`), so a wrong name is correctable without a second call. Failures honour `--format`: `text` renders `error.code`/`error.message` lines instead of JSON.

With `--format text` (the TTY default) each command renders for a human: `list-servers`, `list-tools`, `list-resources`, and `list-prompts` print one line per item with its name and description (plus a truncation/next-page footer when the list is capped); `call-tool`, `read-resource`, and `get-prompt` print their content text. Other results fall back to `key: value` lines.

Discover tool schemas before calling a tool:

```sh
agent-mcp list-tools --server NAME
```

Always clean up daemon mode:

```sh
agent-mcp server shutdown
```

List commands support `--search TERM` (case-insensitive match against name, title, description, and URI) and `--limit N` (maximum returned matches). `--cursor` remains server-side pagination; filtering applies to the current page only.

Use `agent-mcp COMMAND --help` for command-specific syntax. `--help`/`-h` is accepted in any flag position, so `agent-mcp list-tools --server NAME --help` prints the same section as `agent-mcp list-tools --help`. Use `agent-mcp --quickstart` (or `agent-mcp quickstart`) to print an agent-oriented skill with configuration, invocation, cleanup, and cursor traversal examples. Global flags must follow the command: `agent-mcp list-tools --config PATH` — a global flag written before the command is reported as such instead of as an unknown command. A mistyped command reports the closest known command (`did you mean ...?`) and lists every known command.
