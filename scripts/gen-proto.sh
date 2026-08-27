#!/usr/bin/env bash
# Generate Go protobuf/gRPC stubs for proto/agentmcp/v1.
# Requires: protoc, protoc-gen-go, protoc-gen-go-grpc on PATH.
set -euo pipefail

cd "$(dirname "$0")/.."

protoc \
  --proto_path=proto \
  --go_out=proto --go_opt=paths=source_relative \
  --go-grpc_out=proto --go-grpc_opt=paths=source_relative \
  proto/agentmcp/v1/agentmcp.proto
