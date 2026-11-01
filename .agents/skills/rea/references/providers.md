# REA provider setup (verified on macOS arm64)

REA splits its tool surface in two:

- **Target-free** parsers (`inspect_go_binary`, `inspect_pe_imports`,
  `inspect_elf_notes`, `inspect_sqlite_database`, …) need a file path and no
  provider. Registration alone is enough.
- **Deep native analysis** (`open_binary` → `binary_overview`,
  `procedure_pseudo_code`, `analyze_function`, …) needs a provider. Without one
  those tools stay unavailable; the target-free parsers still work.

Everything below was observed against `rea-agents` 6.8.0 with agent-mcp, not
copied from REA's documentation.

## Where settings go

Provider settings are environment variables read by the **MCP child process**.
Put them in the server's `env` block; a shell `export` never reaches it.

```sh
agent-mcp config add --name rea --command npx \
  --arg -y --arg rea-agents@latest --arg mcp \
  --env REA_MCP_INPUT_SCHEMA_PROFILE=compact \
  --env REA_MCP_OUTPUT_SCHEMA_PROFILE=off \
  --env GHIDRA_INSTALL_DIR=/opt/homebrew/Cellar/ghidra/12.2/libexec
```

`config add` rewrites the TOML wholesale (comments dropped) and re-running it
for an existing `--name` updates that entry. A running daemon keeps the old
config: call `agent-mcp server shutdown`, then re-issue the command. A client
using a different `--config` than the running daemon fails with `config
mismatch` naming both paths.

## Diagnose before repairing

```sh
agent-mcp call-tool --server rea --tool binary_session --args '{}'
```

Read `analysis_provider_candidates[]`. Each entry carries
`availability.status`, `code`, `reason`, and `diagnostics.checks[]` with one
row per gate. That is the authoritative repair list — it names the exact file
or variable REA could not use. Example from an unconfigured Ghidra:

```json
{"provider":{"id":"ghidra"},"availability":{"status":"unavailable","code":"not_configured",
 "reason":"Set GHIDRA_INSTALL_DIR to an extracted Ghidra 12.1.x release directory."}}
```

## Ghidra

REA drives Ghidra **headless** itself (`support/analyzeHeadless`). It has no way
to attach to an already-running Ghidra, a Ghidra GUI plugin, or a `ghidra-mcp`
bridge server: the only input it accepts is `GHIDRA_INSTALL_DIR`. An existing
Ghidra MCP registration is a separate server for a separate purpose.

### Required layout

`GHIDRA_INSTALL_DIR` must point at the release root containing:

| Path | Gate |
|---|---|
| `Ghidra/application.properties` | installation, version, Java bounds |
| `support/analyzeHeadless` | headless launch |
| `Ghidra/Features/Decompiler/os/<os>_<arch>/decompile` | native decompiler |

`Ghidra/Features/Decompiler/build/os/<os>_<arch>/decompile` is accepted as an
alternative location. The decompiler gate is a hard requirement: without a
host-native `decompile` binary the provider reports
`unavailable / executable_missing` and refuses sessions, even for operations
that would not decompile.

### Version gate

Published 6.8.0 requires the **12.1.x** line; the verified build is 12.1.4.
A newer 12.2 install is rejected:

```
unsupported_version: 12.2; accepted releases are 12.1.x (verified build 12.1.4)
```

The `12.1.x`-verified build stays a floor for newer releases on the same major
in current source: a 12.2 session is admitted as `compatible`, reports
`provider.version: "12.2"`, and records the limitation *"This session uses
Ghidra 12.2. The bridge is verified against 12.1.4; other 12.x (>=12.1) builds
are accepted and the observed version is reported."* In practice that means:
either install 12.1.x, or run REA from a source build that already accepts 12.2.

### Java

REA reads `application.java.min` from `Ghidra/application.properties` and probes
the JDK on `PATH` (or `JAVA_HOME`). Ghidra 12.2 declares `application.java.min=25`
and rejected nothing under Temurin 27. A JDK whose reported `os.arch`/`os.name`
disagrees with the host also fails the decompiler gate — the check compares the
JVM-selected decompiler directory with the host's.

### Homebrew install: point at `libexec`

Homebrew's cask/formula is a usable, package-managed Ghidra, but the directory
you point at is not the version directory:

```sh
brew install --cask ghidra      # or: brew install ghidra
ls /opt/homebrew/Cellar/ghidra/12.2          # bin libexec INSTALL_RECEIPT.json …
ls /opt/homebrew/Cellar/ghidra/12.2/libexec  # Ghidra support Extensions ghidraRun …
# GHIDRA_INSTALL_DIR=/opt/homebrew/Cellar/ghidra/12.2/libexec
```

`libexec` holds `Ghidra/application.properties`, `support/analyzeHeadless`, and
the macOS arm64 native decompiler — the three files REA checks. Pointing at the
version directory instead yields `executable_missing`, because it has no
`support/`.

Prefer the package manager: Homebrew's build ships the macOS native decompiler,
which the official 12.1.4 release zip does not (it contains only
`linux_x86_64` and `win_x86_64`). On Apple Silicon that mismatch is invisible
until the decompiler gate fails.

### Running REA from a source checkout

Needed when the installed release's version gate is older than the Ghidra you
have (for example published 6.8.0 vs. Homebrew's 12.2):

```sh
cd /path/to/rea
PATH="/opt/homebrew/opt/node@24/bin:$PATH" npm ci
PATH="/opt/homebrew/opt/node@24/bin:$PATH" npm run build:cached
```

Then register the checkout instead of the npm package:

```sh
agent-mcp config add --name rea \
  --command /opt/homebrew/opt/node@24/bin/node \
  --arg /path/to/rea/scripts/rea.mjs --arg mcp \
  --env REA_MCP_INPUT_SCHEMA_PROFILE=compact \
  --env REA_MCP_OUTPUT_SCHEMA_PROFILE=off \
  --env GHIDRA_INSTALL_DIR=/opt/homebrew/Cellar/ghidra/12.2/libexec
```

`scripts/rea.mjs` loads `dist/`, so the build step is mandatory. Use the Node
version pinned by the checkout (`.nvmrc`): Node 26 broke the `metadata:check`
task during `build:cached` with `ERR_INVALID_ARG_VALUE`, while the pinned 24.x
built cleanly. `brew install node@24` provides it keg-only; reference it by full
path rather than relinking.

## Hopper

`/Applications/Hopper Disassembler.app` must exist and its launcher be
executable (`…/Contents/MacOS/hopper`). Hopper also permits removing legacy
bookmarks outside mapped memory, which Ghidra does not.

## IDA

`REA_IDA_MCP_CONFIG` must point at an upstream IDA MCP JSON registration (see
REA's `docs/ida-provider.md`). Without it the provider reports
`not_configured`. Pass the original input binary to `open_binary`, never an
`.idb`/`.i64`.

## Kuna

`REA_KUNA_COMMAND` must be an absolute path to a caller-supplied `kuna`
executable (REA verifies against 1.833). REA never downloads or installs it.

## Timeouts

- agent-mcp's per-call timeout is 30 s by default; pass `--call-timeout 890s`
  for cold provider imports and decompilation.
- REA's own Ghidra startup deadline defaults to 330 s and is adjustable through
  `update_analysis_settings` (`ghidra.startup_timeout_ms`); changing the
  server's launch environment instead requires a new REA process.
- Cancelling a Ghidra request discards its ephemeral database — the target stays
  open and the next query re-imports the artifact.

## Cost knobs

```sh
--env REA_MCP_OUTPUT_SCHEMA_PROFILE=off      # 3.5 MB -> 302 KB for list-tools
--env REA_MCP_INPUT_SCHEMA_PROFILE=compact   # smaller, but often drops enum values
```

Set `REA_MCP_INPUT_SCHEMA_PROFILE=full` when you need exact enum members and
field guidance; it requires the daemon restart like any other env change.
