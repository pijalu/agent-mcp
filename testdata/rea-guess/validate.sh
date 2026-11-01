#!/usr/bin/env bash
# Validate the `rea` skill end to end against a compiled target.
#
# Recovers a hard-coded comparison constant from a binary with agent-mcp + REA
# plus a deep-analysis provider, using only what the binary contains: the
# prompt string, its xref, the containing procedure, and its pseudocode.
#
# Usage: ./validate.sh [path-to-binary]
#   Builds the fixture when no binary is given ($TMPDIR/rea-guess).
#
# Requires: agent-mcp, a `rea` server entry with an available provider,
# clang, jq. Honours AGENT_MCP_CONFIG for a non-default config path.
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
binary="${1:-}"
expected=0x2a # 42
call_timeout="${REA_CALL_TIMEOUT:-890s}"

if [ -z "$binary" ]; then
	binary="${TMPDIR:-/tmp}/rea-guess"
	clang -O2 -g0 -o "$binary" "$here/guess.c"
fi

config_flag=()
if [ -n "${AGENT_MCP_CONFIG:-}" ]; then
	config_flag=(--config "$AGENT_MCP_CONFIG")
fi

# Extract the canonical record REA returns inside the agent-mcp envelope.
rea() {
	local tool="$1" args="$2"
	agent-mcp call-tool "${config_flag[@]}" --server rea --tool "$tool" \
		--args "$args" --call-timeout "$call_timeout" |
		jq -r '.data.content[0].text' | jq -c '.'
}

fail() { printf 'FAIL: %s\n' "$1" >&2; exit 1; }

provider="$(rea binary_session '{}' |
	jq -r '.result.analysis_provider_candidates[] | select(.availability.status == "available") | .provider.id' |
	head -1)"
[ -n "$provider" ] || fail "no deep-analysis provider is available; see .agents/skills/rea/references/providers.md"
printf 'provider: %s\n' "$provider"

# Each check runs in one daemon session: retained Evidence does not survive a restart.
rea open_binary "$(jq -nc --arg p "$binary" '{path:$p}')" |
	jq -e '.result.sha256' >/dev/null || fail "open_binary did not return an artifact digest"

str_addr="$(rea search_strings '{"pattern":"Guess the number"}' |
	jq -r '.normalized_result[0].address // empty')"
[ -n "$str_addr" ] || fail "prompt string not found in the binary"
printf 'string: %s\n' "$str_addr"

xref_addr="$(rea xrefs "$(jq -nc --arg a "$str_addr" '{address:$a}')" |
	jq -r '.normalized_result[0] // empty')"
[ -n "$xref_addr" ] || fail "no reference to the prompt string"
printf 'xref: %s\n' "$xref_addr"

procedure="$(rea resolve_containing_procedure "$(jq -nc --arg a "$xref_addr" '{address:$a}')" |
	jq -r '.normalized_result.procedure.address // empty')"
[ -n "$procedure" ] || fail "no containing procedure for the string reference"
printf 'procedure: %s\n' "$procedure"

pseudo="$(rea procedure_pseudo_code "$(jq -nc --arg p "$procedure" '{procedure:$p}')" |
	jq -r '.normalized_result | if type == "string" then . else (.pseudocode // .code // tojson) end')"
printf '%s\n' "$pseudo"
printf '%s\n' "$pseudo" | grep -qi "$expected" ||
	fail "constant $expected not present in the pseudocode"

rea close_binary '{}' >/dev/null
printf 'PASS: %s compares against %s (42)\n' "$procedure" "$expected"
