// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package client

import (
	"encoding/json"
	"fmt"
)

// Op names one client operation. The strings are the CLI command names, so
// errors shaped by Validate read exactly like the CLI's usage failures.
type Op string

// The client operations. OpListServers reads the config directly and never
// dials a daemon; every other op contacts an MCP server.
const (
	OpListServers   Op = "list-servers"
	OpListTools     Op = "list-tools"
	OpCallTool      Op = "call-tool"
	OpListResources Op = "list-resources"
	OpReadResource  Op = "read-resource"
	OpListPrompts   Op = "list-prompts"
	OpGetPrompt     Op = "get-prompt"
)

// Request is one operation with its parameters. Field names mirror the CLI
// flags; absent optional fields are zero values.
type Request struct {
	Op Op
	// Server names the configured MCP server (required except list-servers).
	Server string
	// Tool names the tool call-tool invokes (required for call-tool).
	Tool string
	// URI names the resource read-resource reads (required for read-resource).
	URI string
	// Prompt names the prompt get-prompt fetches (required for get-prompt).
	Prompt string
	// Args carries call-tool's JSON object or get-prompt's JSON string map.
	// Empty means no arguments.
	Args json.RawMessage
	// Cursor is an MCP pagination cursor passed verbatim (list ops).
	Cursor string
	// Search filters a discovery list case-insensitively across name, title,
	// description, and URI.
	Search string
	// Limit caps a discovery list to the first Limit matches (0 = unlimited;
	// the CLI defaults to 10 for tool/resource/prompt lists, 0 for servers).
	Limit int
	// Raw keeps the MCP result as the server returned it: no metadata
	// stripping, no list totals.
	Raw bool
	// Binaries selects what Shape does with non-text base64 content parts
	// (images, audio, resource blobs). Empty/keep ships raw base64
	// (back-compat); dataurl, file, and strip are the model- and
	// context-friendly forms. See BinaryPolicy.
	Binaries BinaryPolicy
	// BinaryStore persists decoded parts for BinariesFile and returns their
	// location for the pointer marker. Required for the file policy; the
	// other policies ignore it.
	BinaryStore BinaryStore
}

// Validate checks the required fields of r.Op with the CLI's exact wording,
// so a library caller sees the same usage failures a script would.
func (r Request) Validate() error {
	if err := r.validateBinaries(); err != nil {
		return err
	}
	switch r.Op {
	case OpListServers:
		return nil
	case OpListTools, OpListResources, OpListPrompts:
		return requireField(r.Op, "server", r.Server)
	case OpCallTool:
		if err := requireField(r.Op, "server", r.Server); err != nil {
			return err
		}
		return requireField(r.Op, "tool", r.Tool)
	case OpReadResource:
		if err := requireField(r.Op, "server", r.Server); err != nil {
			return err
		}
		return requireField(r.Op, "uri", r.URI)
	case OpGetPrompt:
		if err := requireField(r.Op, "server", r.Server); err != nil {
			return err
		}
		return requireField(r.Op, "prompt", r.Prompt)
	case "":
		return fmt.Errorf("%w: missing operation", ErrUsage)
	default:
		return fmt.Errorf("%w: unknown operation %q", ErrUsage, string(r.Op))
	}
}

// validateBinaries checks the binary policy fields: the policy name must
// parse, and the file policy needs a store to write artifacts into.
func (r Request) validateBinaries() error {
	if r.Binaries == "" {
		return nil
	}
	if _, err := ParseBinaryPolicy(string(r.Binaries)); err != nil {
		return err
	}
	if r.Binaries == BinariesFile && r.BinaryStore == nil {
		return fmt.Errorf("%w: binaries=file requires BinaryStore", ErrUsage)
	}
	return nil
}

// isList reports whether the operation returns a discovery list that the
// local search/limit filter and its totals apply to.
func (r Request) isList() bool { return r.listResult() }

// listResult reports whether the operation returns a discovery list that the
// local search/limit filter and its totals apply to.
func (r Request) listResult() bool {
	switch r.Op {
	case OpListServers, OpListTools, OpListResources, OpListPrompts:
		return true
	default:
		return false
	}
}

// requireField reports a missing required field, or nil when value is set.
func requireField(op Op, field, value string) error {
	if value == "" {
		return fmt.Errorf("%w: %s requires --%s", ErrUsage, op, field)
	}
	return nil
}

// toolArguments decodes Args as call-tool's JSON object.
func (r Request) toolArguments() (map[string]any, error) {
	if len(r.Args) == 0 {
		return nil, nil
	}
	var args map[string]any
	if err := json.Unmarshal(r.Args, &args); err != nil {
		return nil, fmt.Errorf("%w: --args is not valid JSON: %v", ErrUsage, err)
	}
	return args, nil
}

// promptArguments decodes Args as get-prompt's JSON string map.
func (r Request) promptArguments() (map[string]string, error) {
	if len(r.Args) == 0 {
		return nil, nil
	}
	var args map[string]string
	if err := json.Unmarshal(r.Args, &args); err != nil {
		return nil, fmt.Errorf("%w: --args must be a JSON string map: %v", ErrUsage, err)
	}
	return args, nil
}

// cursorParams encodes a cursor-only params body.
func (r Request) cursorParams() ([]byte, error) {
	if r.Cursor == "" {
		return nil, nil
	}
	raw, err := json.Marshal(map[string]string{"cursor": r.Cursor})
	if err != nil {
		return nil, fmt.Errorf("%w: encode cursor: %v", ErrInternal, err)
	}
	return raw, nil
}
