#!/usr/bin/env bash
# Local CI pipeline: format, vet, race tests, coverage gate, static checks.
# Mirrors what CI should run; every step must pass before a commit lands.
set -euo pipefail

cd "$(dirname "$0")/.."

step() { printf '\n==> %s\n' "$1"; }

step "gofmt"
unformatted="$(gofmt -l cmd internal proto 2>/dev/null || true)"
if [ -n "$unformatted" ]; then
  printf 'gofmt needed:\n%s\n' "$unformatted" >&2
  exit 1
fi

step "go vet"
go vet ./...

step "go build (version stamped)"
go build -ldflags "$(./scripts/ldflags.sh)" ./...

step "go test -race"
go test -race ./...

step "coverage gate (80% per-package floor)"
./scripts/check-coverage.sh

step "golang-check (gocognit, gocyclo, staticcheck)"
.agents/skills/golang-check/golang-check.sh

printf '\n==> ci: all steps passed\n'
