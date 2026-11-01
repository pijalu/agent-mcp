// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package client

import (
	"context"
	"fmt"
	"io"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/pijalu/agent-mcp/internal/config"
	"github.com/pijalu/agent-mcp/internal/session"
)

// UpsertResult is the payload for a config add: the server name, whether it
// was added or updated, and the config file that now holds it.
type UpsertResult struct {
	Name   string `json:"name"`
	Action string `json:"action"` // added or updated
	Path   string `json:"path"`
}

// UpsertServer inserts or replaces one MCP server entry in the config file
// at path and saves it. The entry is validated before the file is touched;
// an invalid entry leaves the config unchanged.
func UpsertServer(path, name string, srv config.ServerConfig) (UpsertResult, error) {
	if err := srv.Validate(); err != nil {
		return UpsertResult{}, fmt.Errorf("%w: server %q: %v", ErrUsage, name, err)
	}
	resolved := config.ResolvePath(path)
	cfg, err := config.LoadOrEmpty(resolved)
	if err != nil {
		return UpsertResult{}, fmt.Errorf("%w: %v", ErrUsage, err)
	}
	action := "added"
	if _, ok := cfg.MCPServers[name]; ok {
		action = "updated"
	}
	cfg.MCPServers[name] = srv
	if err := config.Save(resolved, cfg); err != nil {
		return UpsertResult{}, fmt.Errorf("%w: %v", ErrInternal, err)
	}
	return UpsertResult{Name: name, Action: action, Path: resolved}, nil
}

// ServerEntry is the external-facing shape of one MCP server configuration.
// It exists because the daemon's config types are internal to this module;
// embedders (goa) build ServerEntry values and UpsertServerEntry converts.
type ServerEntry struct {
	Command   string            `json:"command,omitempty"`
	Args      []string          `json:"args,omitempty"`
	Env       map[string]string `json:"env,omitempty"`
	URL       string            `json:"url,omitempty"`
	Transport string            `json:"transport,omitempty"`
	Headers   map[string]string `json:"headers,omitempty"`
}

// UpsertServerEntry inserts or replaces one MCP server entry from the
// external-facing shape; see UpsertServer.
func UpsertServerEntry(path, name string, e ServerEntry) (UpsertResult, error) {
	return UpsertServer(path, name, config.ServerConfig{
		Command:   e.Command,
		Args:      e.Args,
		Env:       e.Env,
		URL:       e.URL,
		Transport: e.Transport,
		Headers:   e.Headers,
	})
}

// DialSession opens one client session for a configured server; the
// external-facing form of the session package's dial hook.
type DialSessionFunc func(ctx context.Context, name string, cfg ServerEntry) (*mcp.ClientSession, error)

// NewSessionManager builds an in-process manager over named servers with an
// optional custom dial hook — the NoServer backend for embedders that bring
// their own transport. A nil dial uses the config's stdio/HTTP transports.
func NewSessionManager(servers map[string]ServerEntry, dial DialSessionFunc) (ClosableManager, error) {
	internal := make(map[string]config.ServerConfig, len(servers))
	for name, e := range servers {
		internal[name] = config.ServerConfig{
			Command: e.Command, Args: e.Args, Env: e.Env,
			URL: e.URL, Transport: e.Transport, Headers: e.Headers,
		}
	}
	var d session.DialFunc
	if dial != nil {
		d = func(ctx context.Context, name string, cfg config.ServerConfig) (*mcp.ClientSession, error) {
			return dial(ctx, name, ServerEntry{
				Command: cfg.Command, Args: cfg.Args, Env: cfg.Env,
				URL: cfg.URL, Transport: cfg.Transport, Headers: cfg.Headers,
			})
		}
	}
	return session.NewManager(internal, d), nil
}

// NewWithManager returns a NoServer Client bound to a ready backend (e.g.
// NewSessionManager output), skipping config loading — the embedder path
// when the manager is built by hand. Close tears the manager's sessions
// down.
func NewWithManager(mgr ClosableManager) *Client {
	c := &Client{opts: Options{NoServer: true, Stderr: io.Discard}}
	c.opts.SpawnDaemon = SpawnDaemon
	c.opts.Dial = GRPCDial
	c.opts.NewManager = func(*config.Config) (ClosableManager, error) { return mgr, nil }
	c.mgr = mgr
	return c
}
