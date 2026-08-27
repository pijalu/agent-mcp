---
name: ghidra
description: Reverse-engineer binaries through a live Ghidra MCP server via agent-mcp (search strings/bytes, decompile functions, xrefs, memory reads). Use for static analysis of programs in a Ghidra project exposed by ghidra-mcp.
---

# Ghidra via agent-mcp

Drive Ghidra through the MCP bridge using the `agent-mcp` CLI. Never embed an
MCP client; shell out to `agent-mcp` instead.

## Prerequisites

1. GhidraMCP HTTP server running (GUI plugin: *Tools > GhidraMCP > Start MCP
   Server*, or headless `GhidraMCPHeadlessServer`, default `127.0.0.1:8089`).
2. Python bridge from the ghidra-mcp checkout, wired into
   `~/.agent-mcp/config.toml`:

   ```toml
   [mcpServers.ghidra]
   command = "uv"
   args = ["run", "--directory", "/path/to/ghidra-mcp", "bridge-mcp-ghidra"]
   ```

3. A project with the target binaries imported and analyzed
   (`analyzeHeadless <projdir> <projname> -import <files>` or the
   `import_file`/`run_analysis` tools).

## Command Patterns

All output is JSON on stdout; diagnostics go to stderr. Default per-call
timeout is 30s — pass `--call-timeout 120s` for decompilation of large
functions.

```bash
# Tool discovery (233+ tools; names are lowercase_snake)
agent-mcp list-tools --server ghidra

# Multi-program servers: pick the target explicitly on every call.
agent-mcp call-tool --server ghidra --tool list_open_programs --args '{}'

# String work. `search_strings` scans *defined* strings only; resource-section
# strings usually need a raw byte-pattern search (UTF-16LE for Windows
# resources: "AB" -> hex "41004200").
agent-mcp call-tool --server ghidra --tool search_strings \
  --args '{"search_term":"activation","program":"app.exe"}'
agent-mcp call-tool --server ghidra --tool search_byte_patterns \
  --args '{"pattern":"66 00 6f 00 72 00 6d 00 61 00 74 00","program":"app.dll"}'

# From address to meaning.
agent-mcp call-tool --server ghidra --tool read_memory \
  --args '{"address":"101eb5d0","length":136,"program":"app.dll"}'
agent-mcp call-tool --server ghidra --tool get_xrefs_to \
  --args '{"address":"101eb5d0","program":"app.dll"}'
agent-mcp call-tool --server ghidra --tool get_function_by_address \
  --args '{"address":"0041cefa","program":"app.exe"}'
agent-mcp call-tool --server ghidra --tool decompile_function \
  --args '{"address":"0041cc60","program":"app.exe"}' --call-timeout 120s

# Instruction-level search: find who materializes a constant (error codes,
# magic values). Delphi/C++Builder binaries keep mangled symbol names.
agent-mcp call-tool --server ghidra --tool search_instructions \
  --args '{"operand_pattern":"0x22a","program":"SSCProt.dll","limit":50}'
agent-mcp call-tool --server ghidra --tool get_function_callers \
  --args '{"address":"0041cc60","program":"app.exe"}'
```

## Analysis Workflow: Trace a Message to Its Source

1. **Find the bytes.** `search_strings` first; if empty (resource-section
   data), encode the phrase as UTF-16LE hex and use `search_byte_patterns`.
2. **Read context.** `read_memory` around the hit reveals length prefixes and
   neighboring strings (Windows string-table entries: 2-byte length + UTF-16).
3. **Recover the resource ID.** Parse the PE `.rsrc` string table offline; the
   ID is `(block-1)*16 + index`.
4. **Find the consumer.** Search the main executable for the ID as an
   immediate: little-endian dword `68 <id> 00 00` is `push <id>`
   (LoadString/AfxLoadString argument).
5. **Climb the call chain.** `get_function_by_address` → `decompile_function`
   → `get_function_callers`, repeating until the error/condition that selects
   the message is visible.
6. **Chase the code origin.** If the message is selected from a numeric code
   produced elsewhere (DLL, COM object), `search_instructions` for the code
   constant in every loaded module (`MOV [x], 0x<code>`, `CMP`, `SUB`).

## Pitfalls

- **Argument names are validated** (pydantic). A `Field required` error
  returns the expected name — re-issue with that exact key.
- **Scope**: tools default to the *current* program; pass `"program"` or call
  `switch_program` to avoid silently searching the wrong module.
- **Freshly loaded programs are not analyzed**: `analysis_status` then
  `run_analysis` (long; exceeds the default call timeout — poll
  `analysis_status` after a deadline error instead of retrying blindly).
- **Image base**: DLLs default to `0x10000000`, exe to `0x400000`; byte-search
  hits are virtual addresses, not file offsets.
