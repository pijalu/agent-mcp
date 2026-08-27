# Changelog

All notable changes to `agent-mcp` are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and the project uses
[semantic versioning](https://semver.org/).

## [Unreleased]

### Initial release — CLI contract v1

The first published CLI contract. The JSON envelope is versioned
`"version":1`; agents must accept `{"version":1,...}`.

- **Success**: `{"version":1,"data":{...}}`. **Failure**:
  `{"version":1,"error":{"code":...,"message":...}}`. Exactly one JSON object
  per invocation, on stdout; diagnostics stay on stderr.
- **Concise payloads are the default.** MCP transport bookkeeping (`_meta`,
  `resultType`, `cacheScope`, `ttlMs`) is stripped from results and from list
  entries; nested server data (tool content, structured content, resource
  bodies) is never rewritten. `--raw` restores the MCP result byte for byte and
  skips list totals; raw `_meta` passthrough is not the default.
- **Lists never truncate silently.** List payloads report `total` (matches
  before `--limit`) and set `truncated: true` when `--limit` dropped matches.
  `nextCursor` stays available for the next page; `list-servers` is unbounded by
  default, tool/resource/prompt lists default to `--limit 10`.
- **Exit codes are stable and documented**: `0` success; `1` MCP failure (tool
  result with `isError: true`, unreachable server, rejected API key, daemon
  `config mismatch`); `2` usage/configuration failure; `3` internal failure. A
  tool that reports `isError: true` is an invocation failure: the error envelope
  carries `code: "mcp"` and the server's text, with no `data` field.
- **Help and usage text**: `--help`/`-h` is accepted in any flag position and
  exits `0`; a mistyped command reports `did you mean "<command>"?` plus the full
  command list; a global flag written before the command is reported as
  misplaced rather than as an unknown flag.
- **Discovery**: `list-servers` reads the config directly (no dial, no daemon,
  no MCP child processes). `events` requires an already-running daemon and
  rejects `--no-server` as a usage error. Auto-start prints an stderr hint naming
  the address plus a `server shutdown` reminder.
- **`--no-server`** runs one call in-process and tears the MCP session down
  before exit.
- **Configuration**: `config add --name NAME (--command CMD | --url URL)` updates
  one entry without editing the file by hand; repeatable `--arg`, `--env
  KEY=VALUE`, `--header KEY=VALUE`, and `--transport sse|streamable`. Incompatible
  combinations (for example `--env` with `--url`) are usage errors.
- **Timeouts**: `--call-timeout` (default `30s`) and `--connect-timeout`
  (default `5s`) must be positive; `0` or less is a usage error.
- **Version**: `agent-mcp --version` and `agent-mcp version` print the build
  version stamped at link time (`make build VERSION=v1.0.0`); a plain `go build`
  reports the development default.

### Notes

- Building from source requires Go 1.25 or newer. All dependencies, including
  `github.com/modelcontextprotocol/go-sdk`, resolve from published modules; no
  local `replace` directive or SDK checkout is required.
- The daemon/client RPC carries MCP parameters and results as raw JSON bytes, so
  the internal transport is decoupled from individual MCP schema revisions.
