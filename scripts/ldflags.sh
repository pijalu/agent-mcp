#!/usr/bin/env bash
# Print the -ldflags value that stamps the build version into the binary.
# Makefile and CI both consume it so the stamped version has one source.
set -euo pipefail

cd "$(dirname "$0")/.."

# VERSION wins when set; otherwise derive a stamp from git and fall back to the
# development default baked into internal/version when git metadata is absent.
version="${VERSION:-$(git describe --tags --always --dirty 2>/dev/null || true)}"
if [ -z "$version" ]; then
  version="dev"
fi

echo "-X github.com/pijalu/agent-mcp/internal/version.Version=${version}"
