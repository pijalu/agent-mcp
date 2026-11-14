// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package client

import (
	"encoding/base64"
	"fmt"
	"mime"
	"sort"
	"strings"
)

// BinaryPolicy names what Shape does with a non-text base64 content part
// (image, audio, embedded resource blob — anything pairing a base64 payload
// with a mimeType).
type BinaryPolicy string

const (
	// BinariesKeep ships the raw base64 to the caller. It is the zero-value
	// behavior so existing embeddings keep working unchanged.
	BinariesKeep BinaryPolicy = "keep"
	// BinariesDataURL replaces the payload with its data:<mime>;base64,…
	// form: one representation, decodable by a model provider without a
	// shared filesystem. The part keeps its type, so the payload stays
	// usable but still inline.
	BinariesDataURL BinaryPolicy = "dataurl"
	// BinariesFile decodes each part, hands the bytes to the request's
	// BinaryStore, and rewrites the part to a pointer marker naming the
	// stored location. Stdout/payloads stay tiny; the consumer reads the
	// artifact back by path.
	BinariesFile BinaryPolicy = "file"
	// BinariesStrip drops the payload and rewrites the part to a marker
	// that records only what was there.
	BinariesStrip BinaryPolicy = "strip"
)

// ParseBinaryPolicy maps a user-supplied string onto a policy. Empty means
// the default (keep). Unknown values are usage failures, so a typo never
// silently changes what happens to binary data.
func ParseBinaryPolicy(value string) (BinaryPolicy, error) {
	switch BinaryPolicy(strings.ToLower(value)) {
	case "":
		return BinariesKeep, nil
	case BinariesKeep, BinariesDataURL, BinariesFile, BinariesStrip:
		return BinaryPolicy(strings.ToLower(value)), nil
	}
	return "", fmt.Errorf("%w: unknown --binaries-policy %q (want keep|dataurl|file|strip)", ErrUsage, value)
}

// BinaryArtifact is one decoded binary content part handed to a store.
type BinaryArtifact struct {
	// MimeType is the part's declared content type.
	MimeType string
	// Data is the decoded payload — never the base64 text.
	Data []byte
}

// BinaryStore persists one artifact and returns its location (typically a
// file path or an http URL) for the pointer marker. The interface is defined
// at the point of use so the library shapes binary results without knowing
// anything about filesystems or servers; embeddings wire their own store
// (CLI: a temp dir; a daemon: an artifact endpoint).
type BinaryStore interface {
	Store(artifact BinaryArtifact) (string, error)
}

// binaryMarkers are the bracketed texts that replace a binary part's payload.
// One vocabulary for every policy and carrier keeps scripts' grep patterns
// stable across MIME types.
const (
	markerStripped    = "[binary: %s stripped]"
	markerUndecodable = "[binary: %s undecodable: %v]"
	markerNotSaved    = "[binary: %s not saved: %v]"
	markerStored      = "[binary: %s → %s (%s)]"
)

// ApplyBinariesPolicy rewrites every binary content part of result in place
// per policy and returns the artifacts extracted in file mode, in encounter
// order. The walk is generic: any decoded-JSON object pairing a base64
// payload (data or blob) with a mimeType is a binary part, whichever
// container holds it, so new MCP content shapes keep working without changes
// here (open/closed). keep returns the result untouched.
func ApplyBinariesPolicy(result any, policy BinaryPolicy, store BinaryStore) ([]BinaryArtifact, error) {
	if policy == "" || policy == BinariesKeep {
		return nil, nil
	}
	ap := applicator{policy: policy, store: store}
	ap.walk(result)
	return ap.artifacts, ap.err
}

// applicator carries one policy application pass over a result tree.
type applicator struct {
	policy BinaryPolicy
	// store persists file-policy artifacts; nil is a caller bug that the
	// part-rewriter reports inline instead of failing the whole result.
	store     BinaryStore
	artifacts []BinaryArtifact
	err       error
}

// walk recurses through decoded JSON containers; leaf objects may carry
// binary parts.
func (a *applicator) walk(value any) {
	switch typed := value.(type) {
	case map[string]any:
		if dataKey, mime, ok := binaryPartOf(typed); ok {
			a.apply(typed, dataKey, mime)
			return
		}
		for _, key := range sortedJSONKeys(typed) {
			a.walk(typed[key])
		}
	case []any:
		for _, item := range typed {
			a.walk(item)
		}
	}
}

// apply rewrites one binary part of the given carrier key (data for content
// parts, blob for resource contents). A part that cannot be decoded or
// stored degrades to an inline error marker rather than failing the result:
// the payload is already unusable, and the raw base64 has no consumer.
func (a *applicator) apply(entry map[string]any, dataKey, mimeType string) {
	payload, _ := entry[dataKey].(string)
	switch a.policy {
	case BinariesDataURL:
		entry[dataKey] = dataURL(mimeType, payload)
	case BinariesStrip:
		rewritePart(entry, dataKey, fmt.Sprintf(markerStripped, mimeType))
	case BinariesFile:
		raw, err := decodeBase64(payload)
		if err != nil {
			rewritePart(entry, dataKey, fmt.Sprintf(markerUndecodable, mimeType, err))
			return
		}
		if a.store == nil {
			rewritePart(entry, dataKey, fmt.Sprintf(markerNotSaved, mimeType, errNoBinaryStore))
			return
		}
		location, err := a.store.Store(BinaryArtifact{MimeType: mimeType, Data: raw})
		if err != nil {
			rewritePart(entry, dataKey, fmt.Sprintf(markerNotSaved, mimeType, err))
			return
		}
		a.artifacts = append(a.artifacts, BinaryArtifact{MimeType: mimeType, Data: raw})
		rewritePart(entry, dataKey, fmt.Sprintf(markerStored, mimeType, location, HumanSize(len(raw))))
	}
}

// errNoBinaryStore marks the defensive case of a file policy without a
// store; Request.Validate rejects that combination before an invoke, so this
// marker should never reach a real payload.
var errNoBinaryStore = fmt.Errorf("%w: binaries=file requires BinaryStore", ErrUsage)

// binaryPartOf recognizes a decoded JSON object as a base64 binary part: a
// data (content part) or blob (resource contents) string next to a mimeType.
func binaryPartOf(entry map[string]any) (string, string, bool) {
	for _, dataKey := range []string{"data", "blob"} {
		payload, ok := entry[dataKey].(string)
		if !ok || payload == "" {
			continue
		}
		mimeType, ok := entry["mimeType"].(string)
		if !ok || mimeType == "" {
			continue
		}
		return dataKey, mimeType, true
	}
	return "", "", false
}

// rewritePart replaces one binary part's payload with marker. Content parts
// become text parts (they keep their position in the content array);
// resource contents gain a text field (the standard text representation),
// keeping uri and mimeType for context.
func rewritePart(entry map[string]any, dataKey, marker string) {
	delete(entry, dataKey)
	if _, hasType := entry["type"]; hasType {
		entry["type"] = "text"
	}
	entry["text"] = marker
}

// dataURL renders a base64 payload as the canonical data: URL.
func dataURL(mimeType, payload string) string {
	return "data:" + mimeType + ";base64," + payload
}

// decodeBase64 accepts both standard and URL-safe encodings, with or without
// padding: MCP servers disagree on which variant they ship.
func decodeBase64(payload string) ([]byte, error) {
	if raw, err := base64.StdEncoding.DecodeString(payload); err == nil {
		return raw, nil
	}
	return base64.RawURLEncoding.DecodeString(payload)
}

// ExtensionFor maps a MIME type to a file extension, falling back to the OS
// mime database and then to ".bin". Well-known types are pinned because the
// platform mime databases disagree (macOS hands out ".jfif" for image/jpeg)
// and artifact names must stay predictable across machines.
func ExtensionFor(mimeType string) string {
	if ext, ok := canonicalExt[strings.ToLower(mimeType)]; ok {
		return ext
	}
	exts, err := mime.ExtensionsByType(mimeType)
	if err != nil || len(exts) == 0 {
		return ".bin"
	}
	return exts[0]
}

// canonicalExt pins the extensions of the types binary results actually
// carry today.
var canonicalExt = map[string]string{
	"image/png": ".png", "image/jpeg": ".jpg", "image/gif": ".gif",
	"image/webp": ".webp", "audio/wav": ".wav", "audio/mpeg": ".mp3",
	"audio/ogg": ".ogg", "video/mp4": ".mp4", "video/webm": ".webm",
	"application/pdf": ".pdf",
}

// HumanSize renders a byte count the way ls -h does, for pointer markers.
func HumanSize(n int) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := unit, 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}

// sortedJSONKeys returns a decoded object's keys in sorted order: Go
// randomizes map iteration, and a deterministic walk keeps extracted
// artifact numbering stable across runs.
func sortedJSONKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
