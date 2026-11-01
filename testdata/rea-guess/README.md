# REA guess-number test case

A deliberately small reverse-engineering target that validates the [`rea`
skill](../../.agents/skills/rea/SKILL.md) end to end: a compiled binary whose
only secret is an integer compared in `main`.

The point is not the constant. The point is that the documented agent-mcp
workflow reaches the constant **from the binary**, with provider evidence, and
without reading `guess.c` or its build flags.

## Target

`guess.c` prompts for a number, reads it, and prints `YES` when the input equals
the hard-coded constant, otherwise `NO`.

```sh
clang -O0 -g0 -o /tmp/rea-guess-O0 guess.c
clang -O2 -g0 -o /tmp/rea-guess-O2 guess.c
```

Both builds are valid targets. They differ enough to be worth running both: the
optimized build folds both branches into a single conditional move over two
rodata pointers, while the unoptimized build keeps two separate calls. A tool
that only works on one of them is not trustworthy.

Binaries are build output, so they are never committed. Compile them locally.

## Expected finding

The compared constant is `42` (`0x2a`). Verifiable without any analysis:

```sh
echo 42 | /tmp/rea-guess-O2   # Guess the number: YES
echo 41 | /tmp/rea-guess-O2   # Guess the number: NO
```

## Run it

```sh
./validate.sh                 # builds and checks the -O2 fixture
./validate.sh /tmp/rea-guess-O0  # or point it at any build
```

The script discovers every address at run time (string → xref → procedure), so
it does not depend on a fixed load address, and it exits non-zero unless the
pseudocode contains the constant. Both the `-O2` and `-O0` builds pass.

Set `AGENT_MCP_CONFIG` when the `rea` server lives in a non-default config file:

```sh
AGENT_MCP_CONFIG=~/.agent-mcp/config.toml ./validate.sh
```

This is an opt-in check, not part of `go test`: it needs `agent-mcp`, a `rea`
server entry with an available provider, `clang`, and `jq`.

## Validated workflow (agent-mcp + REA)

Prerequisites: a REA entry in `~/.agent-mcp/config.toml` with a working
Ghidra provider and bounded schema profiles — see the skill's
[provider setup](../../.agents/skills/rea/references/providers.md).

Route a target first, then follow the data instead of guessing symbol names:

```sh
# 1. Bind the binary as the active target (ephemeral Ghidra database, no writes).
agent-mcp call-tool --server rea --tool open_binary \
  --args '{"path":"/tmp/rea-guess-O2"}' --call-timeout 890s

# 2. Find the decision string. The field is `pattern`; `search_term` is rejected.
agent-mcp call-tool --server rea --tool search_strings \
  --args '{"pattern":"Guess the number"}'
# -> [{"address":"0x100000514","value":"Guess the number: "}]

# 3. Who references it.
agent-mcp call-tool --server rea --tool xrefs --args '{"address":"0x100000514"}'
# -> ["0x100000478"]

# 4. Resolve the enclosing function.
agent-mcp call-tool --server rea --tool resolve_containing_procedure \
  --args '{"address":"0x100000478"}'
# -> procedure 0x100000460 "entry", body 0x100000460-0x1000004e3

# 5. Read the pseudocode.
agent-mcp call-tool --server rea --tool procedure_pseudo_code \
  --args '{"procedure":"0x100000460"}' --call-timeout 890s
```

Observed result (`-O2`, Ghidra 12.2 through REA):

```c
  local_24 = 0;
  _printf("Guess the number: ");
  _fflush(*(FILE **)PTR____stdoutp_100004000);
  iVar3 = _scanf("%d",&local_24);
  pcVar1 = "YES";
  if (local_24 != 0x2a) {
    pcVar1 = "NO";
  }
```

`0x2a` is the answer. The `-O0` build resolves the same constant through the
same route (`"YES\n"` at `0x10000055a` → xref `0x1000004e8` → `entry` at
`0x100000460`), reaching it as `local_18 - 0x2aU == 0`.

Evidence IDs recorded for the `-O2` run: `ev_28993123…` (string search),
`ev_b3bcf6da…` (xrefs), `ev_ab1bc677…` (containing procedure), `ev_501332dc…`
(pseudocode). IDs are content-addressed, so they reproduce while the inputs and
REA version stay fixed — but they are only *available* to a tool call inside the
daemon session that produced them.

## What this exercises

| Skill guidance | Exercised by |
|---|---|
| `open_binary` route with an explicit provider target | steps 1–2 |
| Argument names taken from the advertised schema, not prose | `search_term` → `pattern` correction |
| `--call-timeout` for provider work | cold import and pseudocode calls |
| Where retained Evidence lives (and dies) | evidence IDs above |
| Cleanup | `close_binary`, then `agent-mcp server shutdown` |

## Notes and limits

- Function naming came from Ghidra's own analysis; `search_procedures` with
  `pattern: "main"` returned `[]` on both builds, so the string → xref →
  containing-procedure route is the reliable one here. Do not build a workflow
  on the symbol name.
- `procedure_pseudo_code` is decompiler output, not source. It is a faithful
  reading of what the binary does, which is exactly what the test asks for.
- Pseudocode required a host-native Ghidra decompiler; without it the provider
  refuses the session. That is the check most likely to fail on a fresh install,
  and it is documented in the skill's provider reference.
