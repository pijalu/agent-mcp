// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package client

import (
	"errors"
	"fmt"
	"strings"

	"github.com/pijalu/agent-mcp/internal/output"
	"github.com/pijalu/agent-mcp/internal/session"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Sentinel error kinds, identical to the CLI's: the same values wrap library
// failures, so errors.Is matches across both surfaces and exit codes map the
// same way (usage 2, mcp 1, internal 3).
var (
	// ErrUsage marks bad parameters, bad config, or malformed input.
	ErrUsage = output.ErrUsage
	// ErrMCP marks MCP-level failures: tool errors, unreachable servers.
	ErrMCP = output.ErrMCP
	// ErrInternal marks bugs and unexpected failures.
	ErrInternal = output.ErrInternal
)

// ErrUnknownServer marks Close/lookup references to servers not in the config.
var ErrUnknownServer = session.ErrUnknownServer

// errToolFailed marks a tool result that the MCP server flagged as an error.
// The call reached the server, but reporting it as success would let agents
// act on a failure, so it maps to the MCP error class.
func errToolFailed(text string) error {
	if text == "" {
		return fmt.Errorf("%w: tool reported an error", ErrMCP)
	}
	return fmt.Errorf("%w: %s", ErrMCP, text)
}

// RPCFailure converts a daemon RPC failure into a clean error. The gRPC
// status chain is dropped so agents see the problem, not the transport.
func RPCFailure(err error) error {
	st, ok := status.FromError(err)
	if !ok {
		return fmt.Errorf("%w: %v", ErrMCP, err)
	}
	msg := TrimSessionPrefix(st.Message())
	if st.Code() == codes.InvalidArgument || IsUnknownTarget(msg) {
		return fmt.Errorf("%w: %s", ErrUsage, msg)
	}
	return fmt.Errorf("%w: %s", ErrMCP, msg)
}

// SessionFailure is the in-process equivalent of RPCFailure, for NoServer
// mode. Unknown servers and tools are caller mistakes, so they map to the
// usage error class exactly as the daemon-mapped form does.
func SessionFailure(err error) error {
	msg := TrimSessionPrefix(err.Error())
	if errors.Is(err, session.ErrUnknownServer) || IsUnknownTool(msg) {
		return fmt.Errorf("%w: %s", ErrUsage, msg)
	}
	return fmt.Errorf("%w: %v", ErrMCP, err)
}

// TrimSessionPrefix drops the internal package prefix from a message.
func TrimSessionPrefix(msg string) string {
	return strings.TrimPrefix(msg, "session: ")
}

// IsUnknownTarget reports whether a message names a missing server or tool.
// servers word this themselves, so the match is best-effort.
func IsUnknownTarget(msg string) bool {
	return strings.Contains(msg, "unknown server") || IsUnknownTool(msg)
}

// IsUnknownTool reports whether a message came from a tool name the server
// does not expose.
func IsUnknownTool(msg string) bool { return strings.Contains(msg, "unknown tool") }
