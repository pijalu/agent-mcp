// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

// Package client is the importable agent-mcp client library. It carries the
// same operations, payload shaping, error taxonomy, and daemon lifecycle as
// the command-line interface — the CLI is a thin flag/rendering shell over
// this package, so library callers and scripts observe identical semantics.
//
// A Client runs either daemon-backed (default: connect, auto-start, warm
// sessions shared with other agent-mcp processes) or one-shot (NoServer:
// in-process session manager, torn down by Close). Use New to construct one,
// Do to execute a Request, and Close when done; ShutdownDaemon stops a daemon
// the Client may have started.
package client
