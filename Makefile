.PHONY: all build test race coverage lint ci install proto clean

BINARY ?= agent-mcp

# Release stamp injected at link time; override with `make VERSION=v1.2.3`.
# scripts/ldflags.sh derives it from git when VERSION is unset. VERSION is passed
# explicitly because a command-line make variable is not visible to $(shell ...)
# on older make versions.
VERSION ?=
export VERSION
LDFLAGS = $(shell VERSION='$(VERSION)' ./scripts/ldflags.sh)

all: build

build:
	go build -ldflags '$(LDFLAGS)' -o $(BINARY) ./cmd/agent-mcp

test:
	go test ./...

race:
	go test -race ./...

coverage:
	./scripts/check-coverage.sh

lint:
	.agents/skills/golang-check/golang-check.sh

ci:
	./scripts/ci.sh

install:
	go install -ldflags '$(LDFLAGS)' ./cmd/agent-mcp

proto:
	./scripts/gen-proto.sh

clean:
	rm -f $(BINARY) coverage.out
