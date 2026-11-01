---
name: rea
description: Reverse-engineer shipped artifacts through the REA MCP server via agent-mcp — target-free parsers (Go/Rust/PE/ELF/APK/JS/ASM/metadata), provider-backed native analysis, and retained Evidence paging. Use when a claim depends on a binary, package, decompiler database, or recorded runtime evidence rather than available source.
---

# REA via agent-mcp

Drive REA (`rea-agents`) through the MCP bridge using the `agent-mcp` CLI. Never
embed an MCP client; shell out to `agent-mcp` instead.

Verified against `rea-agents` 6.8.0 (npm release and a source checkout) with
Ghidra 12.2 from Homebrew on macOS arm64. The native route — open a binary,
follow a string to its procedure, read the decompiled comparison — is exercised
by [`testdata/rea-guess`](../../../testdata/rea-guess/README.md).

## Prerequisites

1. Node.js and `npx`. The stdio server is `npx -y rea-agents@latest mcp`
   (verified against 6.8.0, which advertises 229 tools).
2. Register it with the schema profiles that keep context usable:

   ```sh
   agent-mcp config add --name rea --command npx \
     --arg -y --arg rea-agents@latest --arg mcp \
     --env REA_MCP_INPUT_SCHEMA_PROFILE=compact \
     --env REA_MCP_OUTPUT_SCHEMA_PROFILE=off
   ```

   Measured cost of one unbounded `list-tools`: **3.5 MB** with REA's defaults
   (complete output schemas), **302 KB** with `off`. There is no reason to pay
   the first number.
3. Deep native analysis needs a caller-supplied provider. Put provider
   variables in the **same `env` block** — the MCP child reads them; a shell
   `export` does not reach it (verified: `GHIDRA_INSTALL_DIR` changed Ghidra's
   status from `not_configured` to the next real check). Ghidra, Hopper, IDA,
   and Kuna each have their own variable, layout, and version gate.

   Read [references/providers.md](references/providers.md) before installing or
   repairing a provider. It has the verified details that matter in practice:
   the `GHIDRA_INSTALL_DIR` must point at a root holding
   `Ghidra/application.properties`, `support/analyzeHeadless`, **and** a
   host-native decompiler (Homebrew's `libexec` has all three; the official
   12.1.4 zip has no macOS decompiler), published 6.8.0 accepts only the 12.1.x
   line while newer source admits 12.2, and REA never attaches to a separate
   `ghidra-mcp`/GUI session.

Config changes need a daemon restart, and each daemon serves exactly one
`--config`: a client pointed at a different file fails with `config mismatch`
naming both paths.

```sh
agent-mcp server shutdown        # idempotent; also drops retained Evidence
```

## Read REA results correctly

- Read `.data.content[0].text` (the canonical JSON record) or
  `.data.structuredContent`. Diagnostics are on stderr; exit code `1` means
  *this invocation failed*, not "the analysis found nothing".
- `structuredContent.normalized_result` is the operation result;
  `structuredContent.evidence_id` identifies it.
- `--format text` prints the record text without the outer envelope — cheaper
  for scanning, but you lose the envelope.

## Context discipline

REA tool descriptions are long and the catalog is large. Treat discovery and
results as costs:

```sh
agent-mcp list-tools --server rea --search inspect_go --limit 3
agent-mcp list-tools --server rea --search evidence --limit 0 --format text
```

- Never `list-tools --limit 0` in JSON mode; always narrow with
  `--search`/`--limit`.
- The compact input profile often elides enum members (`{"type":"string"}`
  instead of the value list) and field guidance, while the server still
  validates the complete canonical input. When an exact enum value matters,
  re-advertise with `REA_MCP_INPUT_SCHEMA_PROFILE=full` after a daemon restart.
- Assume nothing about argument names: `binary_session` has no `detail`
  argument in 6.8.0 even though REA's own guidance mentions one. Check the
  advertised schema, or accept the returned `details.issues[]` as the schema.
- Default per-call timeout is 30 s. Cold provider imports exceed it — pass
  `--call-timeout 600s` (REA's own Ghidra startup deadline defaults to 330 s).

## Session and provider state

```sh
agent-mcp call-tool --server rea --tool binary_session --args '{}'
```

Returns `open`, `capabilities` (active route), `tool_availability` groups
(`available`, `reason`, `remediation`, `client_requirements`), and
`analysis_provider_candidates[]` whose `availability.code` explains exactly
what is missing. Target-bound tools appear in `tool_availability` groups with
`available: false` and `reason: "target_required"` until `open_binary`
succeeds. Read that payload before concluding a capability is absent.

## Target-free analysis — no provider, no session

```sh
agent-mcp call-tool --server rea --tool inspect_go_binary \
  --args '{"path":"/Users/me/go/bin/agent-mcp","detail":"summary"}'
```

`detail: "summary"` returned a 3.4 KB `inspect_analysis_view` record whose
`normalized_result.parent_evidence_id` names the retained complete record — the
default posture for large outputs. The same pattern (explicit absolute `path`,
optional `detail: "summary"`) holds for `inspect_pe_imports`,
`inspect_elf_notes`, `inspect_il2cpp_metadata`, `inspect_sqlite_database`,
`inspect_apk_signature`, and `inspect_rust_binary`.

Check the advertised schema per tool rather than assuming a shape:

- `analyze_javascript_application` takes `input_path`, not `path`.
- `inspect_artifact`, `inspect_macho`, and `inspect_plist` operate on an
already-open session and reject `path`, despite REA's prose describing them
with explicit files.

## Retained Evidence — one daemon lifetime

```sh
agent-mcp call-tool --server rea --tool inspect_analysis_view --args '{
  "source":{"kind":"retained-evidence","evidence_id":"ev_<sha256>"},
  "view":{"kind":"summary"}}'
```

- `inspect_analysis_view` requires `source` **and** `view` objects. A bare
  `evidence_id` is rejected with `unknown_argument`. REA's own docs use that
  shorthand; the wire schema does not.
- Evidence IDs are content-addressed and stable across restarts, but
  *availability* is not: a record produced before a daemon restart fails with
  `evidence_integrity_mismatch` / `reason: "missing"` (verified). Produce and
  page within the same daemon session, or persist first:

  ```sh
  agent-mcp call-tool --server rea --tool export_evidence_bundle \
    --args '{"path":"/abs/out/bundle.json"}'      # -> {path, bytes, records}
  agent-mcp call-tool --server rea --tool import_evidence_bundle \
    --args '{"path":"/abs/out/bundle.json"}'      # -> {imported, unknown_added, total}
  ```

  The exported bundle re-resolves the original IDs after a restart (verified
  round trip: a 2-record, 13 991-byte bundle imported into a fresh daemon).
- Prefer a narrow `view` (`kind`, section, `offset`/`limit`, selected fields)
  over `detail: "complete"`. Page the parent instead of re-running the
  producer.

## Native deep analysis — provider required

```sh
agent-mcp call-tool --server rea --tool open_binary \
  --args '{"path":"/abs/path/target"}' --call-timeout 890s
agent-mcp call-tool --server rea --tool binary_overview --args '{}'
agent-mcp call-tool --server rea --tool analyze_function \
  --args '{"procedure":"0x100003f20"}'
agent-mcp call-tool --server rea --tool close_binary --args '{}'
```

`analyze_function` is summary-first: the default reply is a projection whose
`parent_evidence_id` holds the complete dossier for facet paging. Pass the
retained dossier reference to `compare_functions` /
`match_native_functions`; pass the summary to nothing.

`open_binary` returns as soon as the target is bound; the first analysis
operation pays the import cost. Cancelling a Ghidra request discards its
ephemeral database (the target stays open, the next query re-imports).

### Follow the data, not the symbol name

A verified route that does not depend on what the provider named a function:
search a distinctive string, take its xref, resolve the containing procedure,
then read the pseudocode.

```sh
agent-mcp call-tool --server rea --tool search_strings \
  --args '{"pattern":"Guess the number"}'          # field is `pattern`
agent-mcp call-tool --server rea --tool xrefs --args '{"address":"0x100000514"}'
agent-mcp call-tool --server rea --tool resolve_containing_procedure \
  --args '{"address":"0x100000478"}'
agent-mcp call-tool --server rea --tool procedure_pseudo_code \
  --args '{"procedure":"0x100000460"}' --call-timeout 890s
```

`search_procedures` is not a reliable entry point: `pattern: "main"` returned
`[]` on both builds of a C program whose function Ghidra had named `entry`.
Start from data that exists in the binary, then walk to code. This route is
validated end to end on a hard-coded comparison target —
[`testdata/rea-guess`](../../../testdata/rea-guess/README.md), runnable as
`testdata/rea-guess/validate.sh` — and it reads the answer, `0x2a`, straight
out of the decompiler output on both `-O0` and `-O2` builds.

Target-bound operations succeed only after the provider binds the artifact;
`open_binary` itself does not trigger the import, so the first analysis call
carries the cost. Keep `--call-timeout` generous for that call.

## Errors: fix the named field, never resend unchanged

A rejected call exits `1` with REA's JSON error in `error.message`:

- `invalid_request` → `details.issues[]` names each offending `path` and reason
  (`missing_argument`, `unknown_argument`, `invalid_value`).
- `resource_constraint` with `details.resource: "transport"` → the answer
  exists but is too large for one frame: take a `summary`/section/page view, or
  export the bundle.
- `evidence_integrity_mismatch` → the reference is not available from that
  source (see retained-Evidence lifetime).

Do not repeat an identical rejected or failed call; change the input or resolve
the reported blocker first.

## Cleanup

```sh
agent-mcp call-tool --server rea --tool close_binary --args '{}'  # when a session is open
agent-mcp server shutdown                                        # ends sessions + daemon
```

`reset_investigation` appears in REA's own skill guidance but is not advertised
by 6.8.0 — do not call it. `server shutdown` is the reliable teardown and is
idempotent.

## Reporting findings

Keep observations, inferences, and unknowns separate; cite `evidence_id`,
carry REA's `limitations`/`unknowns` forward, and never describe static
analysis as observed execution. For a question that no advertised tool settles,
say what is unknown rather than extrapolating from strings or layout.
