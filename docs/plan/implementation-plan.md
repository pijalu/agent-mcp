# Agent MCP — Implementation Plan

Micro-step plan for implementing `docs/spec/agent-mcp-spec.md`.

**Conventions**
- Each step is independently verifiable; run its **check** before moving on.
- `RED:` = write the failing test first. `IMPL:` = minimal code to pass.
- After every phase: `go test -race ./...`, `.agents/skills/golang-check/golang-check.sh`, coverage ≥ 80% on new packages, then commit.
- Dependencies confirmed up front (see §0.1 decisions).

---

## Phase 0 — Scaffolding & toolchain

Goal: empty but buildable Go module with all tools installed.

- [ ] **0.1 Decisions (record in commit message)**
  - Module path: `github.com/pijalu/agent-mcp` (placeholder — confirm with user).
  - RPC: gRPC (`google.golang.org/grpc`) over TCP — auth + streaming built in.
  - TOML: `github.com/pelletier/go-toml/v2`.
  - Protobuf: `google.golang.org/protobuf` (**not** the deprecated local `~/dev/protobuf` repo, which is reference-only).
- [ ] **0.2** `go mod init github.com/pijalu/agent-mcp` — check: `go mod verify` runs.
- [ ] **0.3** `go mod edit -replace github.com/modelcontextprotocol/go-sdk=/Users/pijalu/dev/go-sdk` (local dev; remove before publishing).
- [ ] **0.4** `go get github.com/modelcontextprotocol/go-sdk google.golang.org/grpc google.golang.org/protobuf github.com/pelletier/go-toml/v2` — check: `go build ./...` exits 0.
- [ ] **0.5** Install analysis tools: `go install github.com/uudashr/gocognit/cmd/gocognit@latest github.com/fzipp/gocyclo/cmd/gocyclo@latest honnef.co/go/tools/cmd/staticcheck@latest` — check: each `--help` runs.
- [ ] **0.6** Install protobuf toolchain: `protoc`, `protoc-gen-go`, `protoc-gen-go-grpc` — check: `protoc --version`.
- [ ] **0.7** Create package skeleton: `cmd/agent-mcp/`, `internal/{cli,config,daemon,session,output}/`, `proto/agentmcp/v1/`, `scripts/`.
- [ ] **0.8** `.gitignore` exists (done) — add `bin/`, `cover.out` if missing.
- [ ] **0.9** Check: `.agents/skills/golang-check/golang-check.sh` exits 0 ("No Go files found").
- [ ] **0.10** Commit: `chore: module scaffold and toolchain`.

## Phase 1 — `internal/config`

Goal: load + validate `config.toml`; ~1 file, ~150 LOC.

- [ ] **1.1 RED:** `config_test.go` — default path is `~/.agent-mcp/config.toml`.
- [ ] **1.2 IMPL:** `DefaultPath()` using `os.UserHomeDir`.
- [ ] **1.3 RED:** parse stdio server entry (command/args/env) from TOML fixture.
- [ ] **1.4 IMPL:** `ServerConfig` + `Config` structs, `Load(path)`.
- [ ] **1.5 RED:** table test — validation errors: both `command` and `url` set; neither set; `transport` invalid; `transport` set without `url`.
- [ ] **1.6 IMPL:** `ServerConfig.Validate()`.
- [ ] **1.7 RED:** `--config` flag path overrides default; missing file → clear error; malformed TOML → wrapped error with line info.
- [ ] **1.8 IMPL:** `ResolvePath(flagValue)` + error wrapping (`%w`).
- [x] **1.9** Check: `go test -race ./internal/config/`, coverage ≥ 80%, golang-check clean.
- [x] **1.10** Commit: `feat(config): TOML config loading and validation`.

## Phase 2 — `proto/agentmcp/v1` + codegen

Goal: generated protobuf/gRPC stubs committed.

- [x] **2.1** Write `agentmcp.proto`: service `AgentMCP` with `ListServers`, `ListTools`, `CallTool`, `ListResources`, `ReadResource`, `ListPrompts`, `GetPrompt`, `Status`, `ShutdownAll`.
- [x] **2.2** Messages: `{Name}Request{ bytes json }` / `{Name}Response{ bytes json }` payload pattern; `StatusResponse{ repeated ServerStatus }`; `ShutdownAllResponse{ int32 sessions_closed }`.
- [x] **2.3** `scripts/gen-proto.sh` (protoc invocation, `paths=source_relative`) + `//go:generate` directive in `internal/proto/doc.go`.
- [x] **2.4** Run generator — check: `go build ./internal/proto/...`.
- [x] **2.5** RED: smoke test marshals/unmarshals each message type round-trip.
- [x] **2.6** Check: golang-check (exclude generated code from gocognit if flagged — prefer `-ignore` on generated path, never edit generated files).
- [x] **2.7** Commit: `feat(proto): agentmcp.v1 schema and generated stubs`.

## Phase 3 — `internal/session` (MCP session manager)

Goal: one `mcp.ClientSession` per configured server; serialized calls; clean teardown.

- [x] **3.1 RED:** transport factory returns `*mcp.CommandTransport` for stdio config.
- [x] **3.2 IMPL:** `NewTransport(ServerConfig) (mcp.Transport, error)` — stdio case.
- [x] **3.3 RED:** factory cases for `sse` → `mcp.SSEClientTransport`, `streamable` → `mcp.StreamableClientTransport`; unknown → error.
- [x] **3.4 IMPL:** extend factory.
- [x] **3.5 RED:** `Manager.Open(ctx, name)` creates session once; second call returns same session (dedupe test with fake dialer).
- [x] **3.6 IMPL:** `Manager` struct: `map[string]*entry` + `sync.Mutex`; lazy open.
- [x] **3.7 RED:** `CallTool` passes params through and returns result — use in-memory go-sdk server fixture (test helper `newFixtureServer(t)` with one echo tool).
- [x] **3.8 IMPL:** `Manager.CallTool/ListTools/ListResources/ReadResource/ListPrompts/GetPrompt` — thin wrappers, cursor passed verbatim, per-entry `sync.Mutex` serializes calls.
- [x] **3.9 RED:** `Close(name)` closes one; `CloseAll()` closes all and returns count; closing unknown name → error; double close → no panic.
- [x] **3.10 IMPL:** `Close`/`CloseAll`.
- [x] **3.11 RED:** race test — concurrent `Open`/`CallTool`/`CloseAll` (`t.Parallel` + `-race`).
- [x] **3.12 IMPL:** fix any locking issues found.
- [x] **3.13** Check: `go test -race ./internal/session/`, coverage ≥ 80%, golang-check clean.
- [x] **3.14** Commit: `feat(session): per-server MCP session manager`.

## Phase 4 — `internal/daemon`

Goal: gRPC daemon with auth, lifecycle, and **ShutdownAll**.

- [ ] **4.1 RED:** gRPC server starts on `127.0.0.1:0` and registers `AgentMCP` (reflection-free connectivity test with real client).
- [ ] **4.2 IMPL:** `daemon.New(cfg, mgr, opts)` + `Serve(lis)`.
- [ ] **4.3 RED:** unary interceptor rejects missing/invalid API key with `Unauthenticated`; valid key passes.
- [ ] **4.4 IMPL:** API-key interceptor, `subtle.ConstantTimeCompare`, keys from `--api-key` + config.
- [ ] **4.5 RED:** each RPC delegates to `session.Manager` — one table test per RPC (7 RPCs), fake manager via small interface `type SessionManager interface { ... }` defined in daemon package (interface segregation).
- [ ] **4.6 IMPL:** handlers, JSON bytes passthrough, MCP error → gRPC status mapping.
- [ ] **4.7 RED:** idle reaper — daemon stops after `--server-idle-time` with no activity; activity resets timer (inject `clock` interface, fake in tests).
- [ ] **4.8 IMPL:** idle timer + last-activity tracking in interceptor.
- [ ] **4.9 RED:** manual start disables idle timeout unless flag passed.
- [ ] **4.10 IMPL:** flag wiring.
- [ ] **4.11 RED:** `ShutdownAll` → closes all sessions (returns count), drains in-flight calls up to `--shutdown-grace`, then stops gRPC server; subsequent RPCs fail fast.
- [ ] **4.12 IMPL:** graceful stop: `mgr.CloseAll()` → `grpcServer.GracefulStop()` with grace timer → process exit hook.
- [ ] **4.13 RED:** `Status` returns per-server session state (connected/idle, server name, uptime).
- [ ] **4.14 IMPL:** `Status` handler.
- [ ] **4.15** Integration: lifecycle test matrix — auto-start, manual start, idle timeout, concurrent clients, ShutdownAll teardown (real daemon + fixture MCP server on loopback).
- [ ] **4.16** Check: `go test -race ./internal/daemon/`, coverage ≥ 80%, golang-check clean.
- [ ] **4.17** Commit: `feat(daemon): gRPC daemon with auth, idle reaper, ShutdownAll`.

## Phase 5 — `internal/output`

Goal: agent-facing output contract.

- [x] **5.1 RED:** JSON envelope `{"version":1,"data":...}` on success; `{"version":1,"error":{code,message}}` on failure; single object per invocation.
- [x] **5.2 IMPL:** `output.WriteJSON(w, v)`, `output.WriteError(w, err)`.
- [x] **5.3 RED:** exit-code mapping — usage err → 2, MCP err → 1, internal → 3, ok → 0.
- [x] **5.4 IMPL:** `output.ExitCode(err)` with sentinel error types.
- [x] **5.5 RED:** `--format json|text`; default `json` when stdout is not a TTY.
- [x] **5.6 IMPL:** format detection + text renderer (simple key/value).
- [x] **5.7** Check: tests, coverage ≥ 80%, golang-check; commit: `feat(output): JSON contract and exit codes`.

## Phase 6 — `internal/cli` + `cmd/agent-mcp`

Goal: full command surface wired end to end.

- [ ] **6.1** `main.go`: `os.Exit(cli.Run(os.Args[1:], os.Stdout, os.Stderr))` only.
- [ ] **6.2 RED:** subcommand dispatch — unknown command → usage error exit 2.
- [ ] **6.3 IMPL:** stdlib `flag.FlagSet` per subcommand; global flags `--config`, `--format`, `--connect`, `--api-key`, `--no-server`.
- [ ] **6.4 RED:** client probes `--connect` address; unreachable → triggers auto-start (fake dialer asserts spawn).
- [ ] **6.5 IMPL:** auto-start: re-exec self `agent-mcp server start --daemonize`, poll until ready or `--connect-timeout`.
- [ ] **6.6 RED:** `--no-server` path uses `session.Manager` directly, closes it before exit (assert Close called).
- [ ] **6.7 IMPL:** one-shot path.
- [ ] **6.8 RED+IMPL per command** (one micro-step pair each, table tests against fake daemon/fixture):
  - `list-servers`
  - `list-tools --server [--cursor]`
  - `call-tool --server --tool [--args]`
  - `list-resources --server [--cursor]`
  - `read-resource --server --uri`
  - `list-prompts --server [--cursor]`
  - `get-prompt --server --prompt [--args]`
- [ ] **6.9 RED:** `server start` — foreground daemon, flags `--listen`, `--api-key`, `--server-idle-time`.
- [ ] **6.10 IMPL:** `server start`.
- [ ] **6.11 RED:** `server status` — RPC `Status`, JSON table of sessions; unreachable daemon → exit 1.
- [ ] **6.12 IMPL:** `server status`.
- [ ] **6.13 RED:** **`server shutdown`** — RPC `ShutdownAll`; output `{"shutdown":true,"sessions_closed":N}`; daemon process exits; **idempotent**: no daemon → exit 0, `{"shutdown":false}`; `--timeout` flag for wait.
- [ ] **6.14 IMPL:** `server shutdown` (waits for port to close or pid to exit).
- [ ] **6.15** E2E: build fixture MCP server binary (`testdata/fixture-server/main.go` using go-sdk server, stdio), real CLI runs: list-tools → call-tool → server shutdown — assert JSON + exit codes.
- [ ] **6.16** Check: `go test -race ./...`, coverage ≥ 80% every package, golang-check clean.
- [ ] **6.17** Commit: `feat(cli): full command surface incl. server shutdown`.

## Phase 7 — Hardening & gates

- [x] **7.1** `scripts/check-coverage.sh`: fails if any package < 80% (`go test -coverprofile` + `go tool cover -func`).
- [x] **7.2** GitHub Actions (or local `scripts/ci.sh`): build → `go vet` → test -race → coverage gate → golang-check.
- [x] **7.3** README.md: quickstart, config example, skill-usage example (subprocess + JSON parse), `server shutdown` documented as teardown command.
- [x] **7.4** Spec conformance sweep: walk every line of `docs/spec/agent-mcp-spec.md`, tick off against implementation; file issues for gaps.
- [x] **7.5** Final: full suite + golang-check; commit: `chore: CI gates and docs`; tag `v0.1.0`.

---

## Dependency graph

```
0 scaffold
 └─ 1 config ──────────────┐
 └─ 2 proto ──┐            │
              ├─ 4 daemon ─┤
 └─ 3 session ┘            ├─ 6 cli ─ 7 hardening
              └─ 5 output ─┘
```

## Risks / watch-items

- ~~Local `go-sdk` replace directive is machine-specific — strip before any publish/CI.~~ Resolved at release prep: `go.mod` has no `replace` directive and all dependencies resolve from published modules.
- `~/dev/protobuf` is deprecated (`github.com/golang/protobuf`) — reference only; codegen uses `google.golang.org/protobuf`.
- Idle-reaper + ShutdownAll races: reaper must not fire during drain — covered by 4.11/4.15 tests.
- gocognit on generated protobuf: ignore path via tool flag, never hand-edit generated files.
