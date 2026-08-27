# Agent MCP Spec

## Introduction

Agent MCP is a CLI tool used to connect, control, and use any MCP server via the command line. It is the link between agent skills and the MCP ecosystem: a skill invokes `agent-mcp` as a subprocess instead of embedding an MCP client.

The CLI is optimized for agentic workflows (structured output, stable command surface, predictable exit codes) but can be used for any MCP purpose, including by humans.

## Language and Dependencies

Agent MCP is written in Go and uses:

- **MCP client**: `github.com/modelcontextprotocol/go-sdk/mcp` to connect to MCP servers.
- **Internal RPC**: Protocol Buffers over gRPC (or a framed protobuf-over-TCP transport) between the `agent-mcp` client and the `agent-mcp` daemon, using `google.golang.org/protobuf` (the `github.com/golang/protobuf` module is deprecated and must not be used).

## Agent-Facing Contract (Skills Interface)

Skills consume `agent-mcp` as an external tool. This contract is stable and versioned; breaking changes require a major version bump.

### Command Surface

```
agent-mcp list-servers
agent-mcp list-tools    --server <name> [--cursor <c>]
agent-mcp call-tool     --server <name> --tool <name> [--args '<json>']
agent-mcp list-resources --server <name> [--cursor <c>]
agent-mcp read-resource --server <name> --uri <uri>
agent-mcp list-prompts  --server <name> [--cursor <c>]
agent-mcp get-prompt    --server <name> --prompt <name> [--args '<json>']
agent-mcp server start|status|shutdown
agent-mcp version | --version
```

`agent-mcp COMMAND --help` (alias `-h`) prints that command's help section to stdout and
exits `0`. The flag is position-independent: it works before, between, or after the
command's other flags. `agent-mcp --version` and `agent-mcp version` print the build
version stamped at link time. A mistyped command reports the nearest known command and
lists the full command set; a global flag written before the command is reported as
misplaced (`global flag --config must follow the command`) rather than as an unknown
command; an unrecognized flag is reported as `unknown flag --name`.

`agent-mcp server shutdown` performs a complete teardown: it gracefully closes **all** MCP server sessions managed by the daemon (draining in-flight calls per `--shutdown-grace`), then stops the daemon itself. The command is idempotent — if no daemon is running it exits `0` with a `{"shutdown": false}` payload. This is the canonical way for a skill or human to guarantee no MCP server processes are left running.

### Output Contract

- **stdout**: structured JSON only, one JSON object per invocation. Schema is versioned via a top-level `"version"` field (currently `1`).
- **stderr**: human-readable diagnostics, progress, and errors. Never consumed by agents.
- **Exit codes**:
  - `0` success
  - `1` MCP-level error: a tool result with `isError: true`, an unreachable server, a rejected API key, or a daemon serving a different `--config`
  - `2` usage error (bad flags, bad config, unknown server or tool)
  - `3` internal error (bug, daemon crash)
- A tool failure is a failure of the invocation: `isError: true` produces the error envelope with `code: "mcp"`, the server's text as `message`, and no `data` field. Rejected API keys report `daemon at ADDR rejected the API key (check --api-key)` in the same class.
- A `--format json|text` flag selects output; `json` is the default when stdout is not a TTY. Failures honour it too: `text` renders `error.code` and `error.message` lines instead of the JSON envelope.
- `text` is a per-command human rendering, not a flattened dump: `list-servers`, `list-tools`, `list-resources`, and `list-prompts` print one line per item with its name and description (and a truncation/next-page footer when the list is capped); `call-tool`, `read-resource`, and `get-prompt` print their content text. Commands without a human rendering fall back to deterministic `key: value` lines.

**Default payloads are reduced.** MCP transport bookkeeping — `_meta`, `resultType`,
`cacheScope`, `ttlMs` — carries no agent value and is stripped from the result object
and from list entries. `--raw` restores the MCP result byte for byte, and skips the list
totals below. Nested server data (tool content, structured content, resource bodies) is
never rewritten.

**Lists never truncate silently.** List payloads report `total`, the number of items
matching `--search` before `--limit` was applied, and set `truncated: true` when
`--limit` dropped matches. `list-servers` is unbounded by default; tool, resource, and
prompt lists default to `--limit 10` and therefore report their totals, so a shortened
list is never mistaken for a complete one. `nextCursor` is preserved by the local
filter and stays available for the next page.

**Errors name candidates.** An unknown server reports the configured server names; an
unknown tool reports the tools the server exposes. Both are best-effort hints appended
to the message.

### Pagination

`list-*` commands expose MCP cursors verbatim via `--cursor` and return `nextCursor` in the JSON payload. Agents must treat an empty `nextCursor` as end-of-list. The CLI never auto-paginates silently: `--search`/`--limit` apply to the current page only, and the payload reports `total` plus `truncated` so dropped matches are visible.

## Agent MCP Configuration

Agent MCP configuration supports a single configuration file, by default located at `~/.agent-mcp/config.toml`. It contains the MCP servers to connect to. The format is TOML:

```toml
[mcpServers.server-name]
command = "npx"
args = ["-y", "@modelcontextprotocol/server-filesystem", "/path/to/allowed/files"]
env = { API_KEY = "your-secret-key" }

[mcpServers.remote-sse]
url = "https://example.com/sse"
transport = "sse"          # stdio (default when command is set), sse, streamable
headers = { Authorization = "Bearer ..." }
```

- A server entry with `command` uses stdio transport (`mcp.CommandTransport`).
- A server entry with `url` uses `transport` to pick `sse` (`mcp.SSEClientTransport`) or `streamable` (`mcp.StreamableClientTransport`).
- The CLI allows specifying the configuration file path via the `--config` flag.
- `agent-mcp config add --name NAME (--command CMD | --url URL)` adds or updates a server entry without editing the file by hand. `--arg`, `--env KEY=VALUE`, `--transport sse|streamable` (defaults to `streamable` with `--url`), and `--header KEY=VALUE` are repeatable/optional flags. The command rewrites the whole file, preserving other entries and `api_keys`.

## Usage — Daemon Architecture

The CLI supports the complete MCP protocol via CLI parameters. To ensure performance (avoid respawning MCP servers per call), the CLI has two modes:

### Daemon (server) mode

- Runs and maintains MCP server connections locally. Auto-started on first client interaction.
- Keeps MCP servers running while idle; shuts down after `--server-idle-time` (default `5m`) with no active client connections.
- Communicates with `agent-mcp` clients via protobuf on a configurable host/port (`--listen host:port`).
- Supports an optional list of API keys to authenticate clients (`--api-key`, repeatable, or `api_keys` in config).
- Can be started manually via `agent-mcp server start`; when started manually the idle timeout is disabled unless explicitly passed.

### Client mode

- Connects via protobuf to a running daemon (`--connect host:port`, `--api-key`).
- If no daemon is reachable, auto-starts one (daemonize) unless `--no-server` is set, printing an stderr hint that names the address and a `server shutdown` reminder, because the daemon it starts outlives the command. `list-servers` never does this: the server inventory is a config property, so it reads the config directly (no dial, no daemon) and holds no MCP child processes. `events` never auto-starts one either: a subscription needs a daemon the caller runs, so an unreachable address is reported as an MCP failure with a start hint.
- With `--no-server`, the client spawns the required MCP server in-process, performs the call, and tears it down before exit. This mode is stateless and intended for one-shot skill invocations.

## Internal Protobuf Schema

The daemon/client RPC surface mirrors the MCP protocol one-to-one. The schema lives in `proto/agentmcp/v1/agentmcp.proto` and defines, at minimum:

- `ListServers`, `ListTools`, `CallTool`, `ListResources`, `ReadResource`, `ListPrompts`, `GetPrompt`, `Status`, `ShutdownAll` RPCs.
- `ShutdownAll` returns the number of MCP sessions closed and is the last RPC a daemon serves before exiting.
- Request/response messages carrying MCP params and results as JSON bytes (`google.protobuf.Any` or raw `bytes`) so the daemon does not need to version-lock against every MCP schema revision.
- A `SessionEvent` stream for progress notifications and server-initiated messages (logging, list-changed).

## Concurrency Model

- The daemon multiplexes multiple client connections over the same MCP server session.
- Each configured MCP server maps to exactly one `mcp.ClientSession`; the daemon serializes requests per session (MCP servers are not required to handle concurrent requests).
- Daemon shutdown drains in-flight calls with a configurable grace period (`--shutdown-grace`, default `10s`).

## Lifecycle and Timeouts

| Flag | Default | Meaning |
|------|---------|---------|
| `--server-idle-time` | `5m` | Idle duration after which the auto-started daemon stops |
| `--shutdown-grace` | `10s` | Time to drain in-flight MCP calls on daemon shutdown |
| `--call-timeout` | `30s` | Per-MCP-call timeout in client mode |
| `--connect-timeout` | `5s` | Time to wait for the daemon to accept a connection |

`--connect-timeout` is the single source for the daemon dial timeout, the
reachability probe, and the readiness poll that follows an auto-start. A value
of `0` or less is rejected as a usage error (exit `2`) rather than silently
expiring every call.

Client commands also verify that the running daemon serves the same
configuration file: `Status` reports the resolved `config_path` the daemon
loaded, and a client whose `--config` names a different file fails with
`config mismatch` naming both paths (exit `1`). This keeps `--config` from
being silently ignored when a daemon is already listening.

When the client auto-starts a daemon, the child's stdout/stderr is captured. If
the daemon exits during startup the client reports the child's own output and
maps its exit code onto the client's error class (`2` → usage, `3` → internal,
otherwise MCP), so a missing `--config` file surfaces as the config-load error
with exit `2` instead of an opaque `not ready` timeout.
