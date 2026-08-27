#!/usr/bin/env bash
# Run the full test suite with coverage and enforce the 80% per-package floor
# required by AGENTS.md. Exits non-zero when any package falls below the floor.
#
# Override the floor for local experiments with COVERAGE_FLOOR=<pct>.
set -euo pipefail

cd "$(dirname "$0")/.."

FLOOR="${COVERAGE_FLOOR:-80}"

# Packages exempt from the floor: the generated protobuf stubs and the
# main-entry package carry no hand-written logic to gate. Everything else
# must meet the floor.
exempt() {
  case "$1" in
    */cmd/agent-mcp|*/proto/agentmcp/v1) return 0 ;;
    *) return 1 ;;
  esac
}
PROFILE="$(mktemp -t agent-mcp-cover.XXXXXX)"
trap 'rm -f "$PROFILE"' EXIT

go test -coverprofile="$PROFILE" ./...

# Aggregate the coverprofile per package: sum covered statements / total
# statements. Lines look like:
#   github.com/pijalu/agent-mcp/internal/cli/cli.go:12.34,15.2 2 1
#   <file>:<range> <numStmts> <count>
report="$(
  awk '
    /^mode:/ { next }
    {
      line = $0
      sub(/:[0-9]+\.[0-9]+,[0-9]+\.[0-9]+ .*$/, "", line)   # strip :range suffix
      n = split(line, seg, "/")
      pkg = seg[1]; for (i = 2; i < n; i++) pkg = pkg "/" seg[i]  # drop filename
      stmts[pkg] += $2
      if ($3 > 0) covered[pkg] += $2
    }
    END {
      for (p in stmts) printf "%s %d %d\n", p, covered[p], stmts[p]
    }
  ' "$PROFILE" | sort
)"

fail=0
for pkg in $(go list ./...); do
  if exempt "$pkg"; then
    printf 'skip: %s (exempt)\n' "$pkg"
    continue
  fi
  row="$(printf '%s\n' "$report" | awk -v p="$pkg" '$1 == p { print; exit }')"
  if [ -z "$row" ]; then
    # No coverable statements (main package, doc-only): nothing to gate.
    printf 'skip: %s (no statements)\n' "$pkg"
    continue
  fi
  covered="$(printf '%s' "$row" | awk '{print $2}')"
  total="$(printf '%s' "$row" | awk '{print $3}')"
  pct="$(awk -v c="$covered" -v t="$total" 'BEGIN { printf "%.1f", (c / t) * 100 }')"
  if awk -v c="$pct" -v f="$FLOOR" 'BEGIN { exit !(c + 0 < f + 0) }'; then
    printf 'FAIL: %s coverage %s%% below %s%% floor\n' "$pkg" "$pct" "$FLOOR" >&2
    fail=1
  else
    printf 'ok:   %s coverage %s%%\n' "$pkg" "$pct"
  fi
done

if [ "$fail" -ne 0 ]; then
  echo "coverage gate failed (floor ${FLOOR}%)" >&2
  exit 1
fi
echo "coverage gate passed (floor ${FLOOR}%)"
