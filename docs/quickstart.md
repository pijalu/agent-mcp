# Quickstart

This guide takes you from source checkout to your first MCP call.

## 1. Build agent-mcp

Requirements: Go 1.25 or newer. All dependencies, including
`github.com/modelcontextprotocol/go-sdk`, resolve from published modules; no
local `replace` directive or SDK checkout is required.

```sh
git clone https://github.com/pijalu/agent-mcp.git
cd agent-mcp
go build -o agent-mcp ./cmd/agent-mcp
```

## 2. Configure an MCP server

Create `~/.agent-mcp/config.toml`:

```toml
[mcpServers.filesystem]
command = "npx"
args = ["-y", "@modelcontextprotocol/server-filesystem", "/tmp/allowed"]
```

The server name (`filesystem`) is used with `--server`. Keep credentials in
`env` or `headers`, and protect the file:

```sh
mkdir -p ~/.agent-mcp
chmod 700 ~/.agent-mcp
chmod 600 ~/.agent-mcp/config.toml
```

Use another file with `--config ./config.toml`.

## 3. Discover servers and tools

```sh
./agent-mcp list-servers
./agent-mcp list-tools --server filesystem
```

JSON is selected automatically when stdout is not a terminal. Force a format
with `--format json` or `--format text`.

## 4. Call a tool

Pass tool arguments as one JSON object:

```sh
./agent-mcp call-tool \
  --server filesystem \
  --tool read_file \
  --args '{"path":"/tmp/allowed/notes.txt"}'
```

Successful JSON output has this shape:

```json
{"version":1,"data":{}}
```

Exit codes: `0` success, `1` MCP/connectivity failure, `2` usage/configuration
failure, `3` internal failure. Diagnostics are written to stderr.

## 5. Choose daemon or one-shot mode

Normal commands auto-start a local daemon at `127.0.0.1:7474`. The daemon
keeps MCP sessions warm and auto-started instances stop after five minutes of
inactivity.

For isolated calls, skip the daemon:

```sh
./agent-mcp call-tool --no-server \
  --server filesystem --tool read_file \
  --args '{"path":"/tmp/allowed/notes.txt"}'
```

One-shot mode closes the MCP session before the command exits.

Inspect or explicitly stop a daemon:

```sh
./agent-mcp server status
./agent-mcp server shutdown
```

Shutdown drains active calls, closes MCP sessions, and is safe to repeat.

## 6. Chrome DevTools MCP (optional)

Install Node.js, `npx`, and Chrome or Chromium. Add this entry:

```toml
[mcpServers.chrome]
command = "npx"
args = ["-y", "chrome-devtools-mcp@latest"]
```

Discover tools, open a page, then use the returned page ID for snapshots:

```sh
./agent-mcp list-tools --server chrome
./agent-mcp call-tool --server chrome --tool new_page \
  --args '{"url":"https://www.google.com"}'
./agent-mcp call-tool --server chrome --tool take_snapshot \
  --args '{"pageId":2}'
./agent-mcp server shutdown
```

Tool schemas can change upstream; use `list-tools` before automation. Keep
browser cleanup in a shell `trap` or equivalent finally block.

Run the repository's live browser check with:

```sh
AGENT_MCP_CHROME_E2E=1 go test ./internal/cli -run TestChromeDevToolsGoogle -v
```

The test opens Google, verifies the DOM/accessibility snapshot contains
`google`, and shuts down the daemon and MCP session. It is skipped by normal
unit-test runs.

## 7. Development checks

```sh
go test ./...
go test -race ./...
./scripts/check-coverage.sh
./scripts/ci.sh
```

See [`README.md`](../README.md) for the complete command reference and
[`docs/spec/agent-mcp-spec.md`](spec/agent-mcp-spec.md) for the protocol
contract.
