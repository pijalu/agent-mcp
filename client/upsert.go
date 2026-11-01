// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package client

import (
	"fmt"

	"github.com/pijalu/agent-mcp/internal/config"
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
