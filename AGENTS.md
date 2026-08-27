# AGENTS.md

## Project Overview

Agent MCP is a CLI tool that connects, controls, and uses any MCP server from the command line. It is the bridge between agent skills and the MCP ecosystem: a skill shells out to `agent-mcp` instead of embedding an MCP client.

- **Spec**: `docs/spec/agent-mcp-spec.md`
- **Language**: Go
- **Key dependencies**:
  - `github.com/modelcontextprotocol/go-sdk/mcp` — MCP client
  - `google.golang.org/protobuf` — daemon/client RPC (never use the deprecated `github.com/golang/protobuf`)

## Code Style — Idiomatic Go

- Follow Effective Go and the standard Go project layout.
- `gofmt` (and `go vet`) clean before every commit.
- Exported symbols have doc comments; unexported code is self-documenting. Comment the "why", not the "what".
- Errors are values: wrap with `fmt.Errorf("...: %w", err)` and add context at each layer; never swallow errors.
- Accept interfaces, return concrete types.
- Keep packages small and focused; `internal/` for implementation details not part of the public surface.

## Design — SOLID

- **Single responsibility**: one package = one concern (config loading, daemon, MCP session management, CLI surface, protobuf transport are separate packages).
- **Open/closed**: add new MCP transports or CLI subcommands by extension (new files/types), not by editing existing switch statements.
- **Liskov substitution**: transport and session abstractions must be substitutable; no type assertions on concrete types outside factories.
- **Interface segregation**: define small interfaces at the point of use (e.g. `type ToolCaller interface { CallTool(ctx, params) (result, error) }`), not one fat interface.
- **Dependency inversion**: depend on interfaces defined in the consuming package; wire concrete implementations in `main`/`cmd` only.

## Testing

- **Coverage floor: 80%** per package, enforced in CI (`go test -coverprofile` + `go tool cover`).
- Table-driven tests are the default. Use `t.Run` subtests.
- Test against the real MCP protocol using in-memory transports from `go-sdk` where possible; mock only at package boundaries.
- Daemon lifecycle tests must cover: auto-start, idle timeout, manual start, `--no-server` one-shot path, concurrent clients.
- Run `go test -race ./...` before merging.

## Complexity Budget — golang-check

Static analysis and complexity are **enforced, not advisory**. The skill is vendored at `.agents/skills/golang-check/`.

Run before committing:

```bash
.agents/skills/golang-check/golang-check.sh
```

Enforced thresholds:

| Check | Threshold |
|-------|-----------|
| gocognit | ≤ 15 cognitive complexity |
| gocyclo | ≤ 12 cyclomatic complexity |
| staticcheck | zero findings |
| file size | hard max 1000 lines, soft target 500 lines |

Rules:

1. Fix root causes. Never bypass with `//nolint`, threshold bumps, or restructuring just to silence a tool.
2. Over-threshold function → extract helpers, early returns, split responsibilities.
3. Wait for user confirmation before applying large refactors surfaced by the checks.

## Build and Test Commands

- Build: `go build ./...`
- Test: `go test ./...`
- Race: `go test -race ./...`
- Coverage: `go test -coverprofile=cover.out ./... && go tool cover -func=cover.out`
- Checks: `.agents/skills/golang-check/golang-check.sh`
