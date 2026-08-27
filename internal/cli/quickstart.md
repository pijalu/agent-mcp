# agent-mcp agent quickstart

Use `agent-mcp` as a subprocess. Read one JSON object from stdout; diagnostics are on stderr. Check the process exit code before using `.data`.

## Discover servers and tools

```sh
agent-mcp list-servers
agent-mcp list-tools --server NAME --limit 10
```

List commands default to 10 results. Use `--limit 0` for all results, or use `--search TERM` to match names, titles, descriptions, and URIs.

`list-servers` reads the config and starts nothing. The other discovery commands auto-start a daemon when none is listening (stderr names the address and reminds you to run `agent-mcp server shutdown`), so clean up when finished.

## Traverse pages with cursors

The MCP server controls pagination. The CLI sends `--cursor` unchanged and returns `nextCursor` in `.data` when another page exists. An empty or missing `nextCursor` means traversal is complete. Filtering applies to each server-returned page.

```sh
cursor=""
while :; do
  if [ -n "$cursor" ]; then
    page="$(agent-mcp list-tools --server NAME --cursor "$cursor" --limit 10)"
  else
    page="$(agent-mcp list-tools --server NAME --limit 10)"
  fi
  printf '%s\n' "$page" | jq -r '.data.tools[]?.name'
  cursor="$(printf '%s\n' "$page" | jq -r '.data.nextCursor // empty')"
  [ -n "$cursor" ] || break
done
```

Use `--search TERM` inside the loop to filter each page. Use `--limit 0` when local limiting is not wanted; this does not disable server pagination.

## Invoke a tool

First discover the tool schema, then pass arguments as a JSON object:

```sh
agent-mcp call-tool --server NAME --tool TOOL \
  --args '{"key":"value"}'
```

## One-shot and cleanup

Use `--no-server` for isolated calls; it closes the MCP session before exit:

```sh
agent-mcp call-tool --no-server --server NAME --tool TOOL --args '{}'
```

Daemon mode keeps sessions warm. Always clean it up when finished:

```sh
agent-mcp server shutdown
```

## Configuration

Default config: `~/.agent-mcp/config.toml`. Override with `--config PATH`.

Add or update a server without editing the file:

```sh
agent-mcp config add --name NAME --command your-mcp-server
agent-mcp config add --name NAME --url https://example.com/mcp
```

Equivalent TOML:

```toml
[mcpServers.NAME]
command = "your-mcp-server"
args = []
```

Use `agent-mcp COMMAND --help` for complete command details. Exit codes: `0` success, `1` MCP/connectivity, `2` usage/configuration, `3` internal.
