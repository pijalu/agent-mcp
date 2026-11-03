// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package client

import (
	"encoding/json"
	"fmt"
	"reflect"
)

// metadataKeys are MCP transport bookkeeping fields: protocol version markers,
// cache hints, and the server-info _meta block. They carry no agent value and
// cost tokens on every call, so the default payload drops them.
var metadataKeys = []string{"_meta", "resultType", "cacheScope", "ttlMs"}

// Shape reduces a raw MCP result to the default payload a caller writes out:
// discovery lists carry their filtered items plus a match total, MCP
// transport metadata is stripped everywhere, and call-tool results drop a
// content array that only mirrors structuredContent (one payload, one copy).
// Raw requests keep the server's result but receive the same single
// representation: a mirrored copy is protocol back-compat, not data.
func Shape(result any, r Request) (any, error) {
	filtered, err := FilterDiscovery(result, DiscoveryFilter{
		Search: r.Search,
		Limit:  r.Limit,
		Raw:    r.Raw,
	}, r.isList())
	if err != nil {
		return nil, err
	}
	normalized, err := asJSONValue(filtered)
	if err != nil {
		return nil, err
	}
	reduced := DropMirroredContent(normalized)
	if r.Raw {
		return reduced, nil
	}
	return StripMetadata(reduced), nil
}

// DropMirroredContent removes a call-tool result's content array when every
// part is the JSON serialization of structuredContent. MCP servers repeat the
// structured payload as text for back-compat with clients that cannot read
// structuredContent; for everyone else that repetition doubles the payload,
// so the mapper keeps the structured copy and drops the mirrored text.
// Results whose text differs from the structured payload (plain prose,
// images, partial mirrors) pass through untouched.
func DropMirroredContent(result any) any {
	object, ok := result.(map[string]any)
	if !ok {
		return result
	}
	structured, present := object["structuredContent"]
	if !present || structured == nil {
		return result
	}
	parts, ok := object["content"].([]any)
	if !ok || len(parts) == 0 {
		return result
	}
	for _, part := range parts {
		entry, ok := part.(map[string]any)
		if !ok {
			return result
		}
		text, ok := entry["text"].(string)
		if !ok || !mirrorsStructured(text, structured) {
			return result
		}
	}
	delete(object, "content")
	return object
}

// mirrorsStructured reports whether one text part decodes to the same value
// as the result's structured content. A part that is not JSON, or that is a
// different value, is independent data and must survive.
func mirrorsStructured(text string, structured any) bool {
	var parsed any
	if err := json.Unmarshal([]byte(text), &parsed); err != nil {
		return false
	}
	return reflect.DeepEqual(parsed, structured)
}

// asJSONValue normalizes a result into the generic JSON shape the reducer
// walks: daemon results arrive as decoded maps, in-process results as MCP
// structs. Maps pass through untouched so their payload is never re-encoded.
func asJSONValue(result any) (any, error) {
	switch result.(type) {
	case nil, map[string]any, []any:
		return result, nil
	}
	raw, err := json.Marshal(result)
	if err != nil {
		return nil, fmt.Errorf("%w: encode result: %v", ErrInternal, err)
	}
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil, fmt.Errorf("%w: decode result: %v", ErrInternal, err)
	}
	return value, nil
}

// StripMetadata removes MCP transport bookkeeping from the result object and
// from the entries of any discovery list. Nested payloads — tool content, tool
// arguments, resource bodies — are untouched: there those keys belong to the
// server's own data, not to the transport.
func StripMetadata(result any) any {
	object, ok := result.(map[string]any)
	if !ok {
		return result
	}
	deleteMetadata(object)
	for _, key := range discoveryKeys {
		for _, item := range asObjects(object[key]) {
			deleteMetadata(item)
		}
	}
	return object
}

// deleteMetadata drops the transport fields from one object.
func deleteMetadata(object map[string]any) {
	for _, key := range metadataKeys {
		delete(object, key)
	}
}

// asObjects returns the map entries of a decoded JSON array, ignoring anything
// that is not an object.
func asObjects(value any) []map[string]any {
	items, ok := value.([]any)
	if !ok {
		return nil
	}
	objects := make([]map[string]any, 0, len(items))
	for _, item := range items {
		if entry, ok := item.(map[string]any); ok {
			objects = append(objects, entry)
		}
	}
	return objects
}
