package client

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

// marshalForTest marshals a result tree the way Shape's callers see it.
func marshalForTest(t *testing.T, value any) string {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal result: %v", err)
	}
	return string(raw)
}

// memStore is a BinaryStore fake that records artifacts and answers with
// deterministic locations.
type memStore struct {
	artifacts []BinaryArtifact
	paths     []string
	err       error
}

func (m *memStore) Store(artifact BinaryArtifact) (string, error) {
	if m.err != nil {
		return "", m.err
	}
	m.artifacts = append(m.artifacts, artifact)
	path := filepath.Join("/tmp", "part-"+string(rune('a'+len(m.artifacts)-1))+ExtensionFor(artifact.MimeType))
	m.paths = append(m.paths, path)
	return path, nil
}

// pngPayload is base64("PNGDATA"); pdfPayload is base64("%PDF").
var (
	pngPayload = base64.StdEncoding.EncodeToString([]byte("PNGDATA"))
	pdfPayload = base64.StdEncoding.EncodeToString([]byte("%PDF"))
)

// imageResult builds a fresh call-tool result carrying one png image part.
func imageResult(data string) map[string]any {
	return map[string]any{"content": []any{
		map[string]any{"type": "image", "data": data, "mimeType": "image/png"},
	}}
}

// applyForTest runs a policy over a result tree and returns the marshalled
// output plus the recorded artifacts, failing the test on error.
func applyForTest(t *testing.T, result any, policy BinaryPolicy, store BinaryStore) (string, []BinaryArtifact) {
	t.Helper()
	artifacts, err := ApplyBinariesPolicy(result, policy, store)
	if err != nil {
		t.Fatalf("ApplyBinariesPolicy(%v): %v", policy, err)
	}
	return marshalForTest(t, result), artifacts
}

// shapeForTest runs the Shape pipeline, failing the test on error.
func shapeForTest(t *testing.T, result any, req Request) string {
	t.Helper()
	shaped, err := Shape(result, req)
	if err != nil {
		t.Fatalf("Shape: %v", err)
	}
	return marshalForTest(t, shaped)
}

// assertUsageError pins Validate rejecting a request as a usage failure.
func assertUsageError(t *testing.T, req Request) {
	t.Helper()
	if err := req.Validate(); !errors.Is(err, ErrUsage) {
		t.Fatalf("Validate = %v, want ErrUsage", err)
	}
}

// assertArtifactCount fails the test unless the store saw exactly want calls.
func assertArtifactCount(t *testing.T, got []BinaryArtifact, want int) {
	t.Helper()
	if len(got) != want {
		t.Fatalf("artifacts = %d, want %d", len(got), want)
	}
}

// assertContainsAll fails the test if any wanted substring is missing.
func assertContainsAll(t *testing.T, raw string, want []string) {
	t.Helper()
	for _, s := range want {
		if !strings.Contains(raw, s) {
			t.Errorf("output missing %q:\n%s", s, raw)
		}
	}
}

// assertContainsNone fails the test if any banned substring appears.
func assertContainsNone(t *testing.T, raw string, banned []string) {
	t.Helper()
	for _, s := range banned {
		if strings.Contains(raw, s) {
			t.Errorf("output still carries %q:\n%s", s, raw)
		}
	}
}

// TestApplyBinariesPolicyTable drives every policy across the binary
// carriers: image/audio content parts, embedded resource parts, and resource
// contents entries.
func TestApplyBinariesPolicyTable(t *testing.T) {
	// imagePart is the call-tool content part chrome-devtools-mcp returns.
	imagePart := func() map[string]any {
		return map[string]any{"type": "image", "data": pngPayload, "mimeType": "image/png"}
	}
	cases := []struct {
		name   string
		policy BinaryPolicy
		// build returns a fresh result tree per subtest.
		build func() any
		// wantMarkers must appear in the marshalled result (empty for keep).
		wantMarkers []string
		// banned must NOT appear (the base64 payload, for non-keep policies).
		banned []string
		// wantArtifacts counts file-policy store calls.
		wantArtifacts int
	}{
		{
			name:   "keep leaves the payload",
			policy: BinariesKeep,
			build:  func() any { return map[string]any{"content": []any{imagePart()}} },
		},
		{
			name:        "dataurl rewrites the data field",
			policy:      BinariesDataURL,
			build:       func() any { return map[string]any{"content": []any{imagePart()}} },
			wantMarkers: []string{},
		},
		{
			name:          "file stores and points",
			policy:        BinariesFile,
			build:         func() any { return map[string]any{"content": []any{imagePart()}} },
			wantMarkers:   []string{"[binary: image/png → /tmp/part-a.png (7 B)]"},
			banned:        []string{pngPayload},
			wantArtifacts: 1,
		},
		{
			name:        "strip drops the payload",
			policy:      BinariesStrip,
			build:       func() any { return map[string]any{"content": []any{imagePart()}} },
			wantMarkers: []string{"[binary: image/png stripped]"},
			banned:      []string{pngPayload},
		},
		{
			name:   "file reaches resource contents and embedded parts",
			policy: BinariesFile,
			build: func() any {
				return map[string]any{
					"contents": []any{
						map[string]any{"uri": "file:///x.pdf", "blob": pdfPayload, "mimeType": "application/pdf"},
					},
					"content": []any{
						map[string]any{"type": "resource", "resource": map[string]any{
							"uri": "file:///y.bin", "blob": pngPayload, "mimeType": "application/octet-stream",
						}},
					},
				}
			},
			wantMarkers: []string{
				// Walk order is key-sorted: "content" precedes "contents".
				"[binary: application/octet-stream → /tmp/part-a.bin (7 B)]",
				"[binary: application/pdf → /tmp/part-b.pdf (4 B)]",
			},
			banned:        []string{pdfPayload, pngPayload},
			wantArtifacts: 2,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			raw, artifacts := applyForTest(t, tc.build(), tc.policy, &memStore{})
			assertArtifactCount(t, artifacts, tc.wantArtifacts)
			assertContainsAll(t, raw, tc.wantMarkers)
			assertContainsNone(t, raw, tc.banned)
			// The dataurl case asserts its exact form here: one data: URL with
			// correct mime, carrying the original payload only inside it.
			if tc.policy == BinariesDataURL {
				assertContainsAll(t, raw, []string{"data:image/png;base64," + pngPayload})
			}
		})
	}
}

// TestApplyBinariesPolicyKeepNilStore pins the back-compat default: keep (and
// empty) never touches the result, whatever the store.
func TestApplyBinariesPolicyKeepNilStore(t *testing.T) {
	result := map[string]any{"content": []any{
		map[string]any{"type": "image", "data": pngPayload, "mimeType": "image/png"},
	}}
	for _, policy := range []BinaryPolicy{"", BinariesKeep} {
		artifacts, err := ApplyBinariesPolicy(result, policy, nil)
		if err != nil {
			t.Fatalf("policy %q: %v", policy, err)
		}
		if artifacts != nil {
			t.Fatalf("policy %q returned artifacts %v", policy, artifacts)
		}
		if got := marshalForTest(t, result); !strings.Contains(got, pngPayload) {
			t.Fatalf("policy %q mutated the payload:\n%s", policy, got)
		}
	}
}

// TestApplyBinariesPolicyFileDegradesGracefully: an undecodable payload or a
// failing store degrades to an inline marker — the result stays usable and
// the raw base64 never ships.
func TestApplyBinariesPolicyFileDegradesGracefully(t *testing.T) {
	t.Run("undecodable payload", func(t *testing.T) {
		raw, artifacts := applyForTest(t, imageResult("!!!"), BinariesFile, &memStore{})
		assertArtifactCount(t, artifacts, 0)
		assertContainsAll(t, raw, []string{"[binary: image/png undecodable:"})
	})
	t.Run("store failure", func(t *testing.T) {
		store := &memStore{err: errors.New("disk full")}
		raw, artifacts := applyForTest(t, imageResult(pngPayload), BinariesFile, store)
		assertArtifactCount(t, artifacts, 0)
		assertContainsAll(t, raw, []string{"[binary: image/png not saved: disk full]"})
		assertContainsNone(t, raw, []string{pngPayload})
	})
	t.Run("nil store", func(t *testing.T) {
		raw, artifacts := applyForTest(t, imageResult(pngPayload), BinariesFile, nil)
		assertArtifactCount(t, artifacts, 0)
		assertContainsAll(t, raw, []string{"[binary: image/png not saved: "})
	})
}

// TestShapeAppliesBinariesPolicy verifies the pipeline integration: the
// policy rides the same Request the invoke used, applies to raw payloads,
// and file-without-store is a usage failure.
func TestShapeAppliesBinariesPolicy(t *testing.T) {
	newResult := func() any { return imageResult(pngPayload) }
	t.Run("file via Shape", func(t *testing.T) {
		store := &memStore{}
		raw := shapeForTest(t, newResult(), Request{Op: OpCallTool, Binaries: BinariesFile, BinaryStore: store})
		assertContainsNone(t, raw, []string{pngPayload})
		assertContainsAll(t, raw, []string{"[binary: image/png → /tmp/part-a.png"})
		assertArtifactCount(t, store.artifacts, 1)
	})
	t.Run("raw still applies the policy", func(t *testing.T) {
		raw := shapeForTest(t, newResult(), Request{Op: OpCallTool, Raw: true, Binaries: BinariesStrip})
		assertContainsNone(t, raw, []string{pngPayload})
	})
	t.Run("file without store is a usage error", func(t *testing.T) {
		assertUsageError(t, Request{Op: OpCallTool, Binaries: BinariesFile})
	})
	t.Run("unknown policy is a usage error", func(t *testing.T) {
		assertUsageError(t, Request{Op: OpCallTool, Binaries: "explode"})
	})
	t.Run("keep is the default", func(t *testing.T) {
		raw := shapeForTest(t, newResult(), Request{Op: OpCallTool})
		assertContainsAll(t, raw, []string{pngPayload})
	})
}

// TestParseBinaryPolicyTable covers the CLI-facing parser.
func TestParseBinaryPolicyTable(t *testing.T) {
	cases := []struct {
		in   string
		want BinaryPolicy
		ok   bool
	}{
		{"", BinariesKeep, true},
		{"keep", BinariesKeep, true},
		{"dataurl", BinariesDataURL, true},
		{"file", BinariesFile, true},
		{"strip", BinariesStrip, true},
		{"FILE", BinariesFile, true},
		{"explode", "", false},
		{"data url", "", false},
	}
	for _, tc := range cases {
		got, err := ParseBinaryPolicy(tc.in)
		if tc.ok && err != nil {
			t.Errorf("ParseBinaryPolicy(%q) error: %v", tc.in, err)
			continue
		}
		if !tc.ok {
			if !errors.Is(err, ErrUsage) {
				t.Errorf("ParseBinaryPolicy(%q) = %v, want ErrUsage", tc.in, err)
			}
			continue
		}
		if got != tc.want {
			t.Errorf("ParseBinaryPolicy(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestExtensionForTable pins the well-known extensions and the fallback.
func TestExtensionForTable(t *testing.T) {
	cases := []struct {
		mime string
		want string
	}{
		{"image/png", ".png"},
		{"image/jpeg", ".jpg"},
		{"image/webp", ".webp"},
		{"audio/wav", ".wav"},
		{"audio/mpeg", ".mp3"},
		{"video/mp4", ".mp4"},
		{"application/pdf", ".pdf"},
		{"application/octet-stream", ".bin"},
		{"image/png; charset=bogus", ".png"}, // lenient parse: params tolerated
	}
	for _, tc := range cases {
		if got := ExtensionFor(tc.mime); got != tc.want {
			t.Errorf("ExtensionFor(%q) = %q, want %q", tc.mime, got, tc.want)
		}
	}
}

// TestHumanSizeTable pins the marker size rendering.
func TestHumanSizeTable(t *testing.T) {
	cases := []struct {
		in   int
		want string
	}{
		{512, "512 B"},
		{1024, "1.0 KiB"},
		{217088, "212.0 KiB"},
		{1572864, "1.5 MiB"},
	}
	for _, tc := range cases {
		if got := HumanSize(tc.in); got != tc.want {
			t.Errorf("HumanSize(%d) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
