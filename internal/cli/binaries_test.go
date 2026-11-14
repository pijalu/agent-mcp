package cli_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// runShot invokes call-tool --tool shot against the fixture server with the
// given extra args, returning exit code and stdout.
func runShot(t *testing.T, extra ...string) (int, string) {
	t.Helper()
	cfgPath := writeConfig(t, buildFixture(t))
	args := append([]string{"call-tool", "--server", "fx", "--tool", "shot",
		"--no-server", "--config", cfgPath}, extra...)
	return run(t, args...)
}

// fixturePDF is the raw bytes the fixture's pdf resource carries.
const fixturePNG = "PNGDATA"

// TestCallToolFilePolicyDefaultIsPointerMarker: the default call resolves
// the binary into a file and prints a one-line pointer — never base64.
func TestCallToolFilePolicyDefaultIsPointerMarker(t *testing.T) {
	dir := t.TempDir()
	code, out := runShot(t, "--format", "text", "--save-binaries", dir)
	if code != 0 {
		t.Fatalf("exit = %d (out %s)", code, out)
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 {
		t.Fatalf("artifact dir: %v (%d entries)", err, len(entries))
	}
	path := filepath.Join(dir, entries[0].Name())
	if ext := filepath.Ext(path); ext != ".png" {
		t.Errorf("artifact extension = %q, want .png", ext)
	}
	raw, err := os.ReadFile(path)
	if err != nil || string(raw) != fixturePNG {
		t.Fatalf("artifact bytes = %q err %v, want %q", raw, err, fixturePNG)
	}
	if !strings.Contains(out, "[binary: image/png → "+path+" (7 B)]") {
		t.Errorf("missing pointer marker:\n%s", out)
	}
	if strings.Contains(out, "UE5HREFUQQ==") {
		t.Errorf("stdout carries base64:\n%s", out)
	}
}

// TestCallToolBinariesPolicyTable drives dataurl/strip/keep through the CLI.
func TestCallToolBinariesPolicyTable(t *testing.T) {
	cases := []struct {
		policy    string
		wantText  string
		notWanted string
	}{
		{"dataurl", "[binary: image/png dataurl (7 B)]", ""},
		{"strip", "[binary: image/png stripped]", ""},
		{"keep", "[image: image/png]", ""},
	}
	for _, tc := range cases {
		t.Run(tc.policy, func(t *testing.T) {
			code, out := runShot(t, "--format", "text", "--binaries-policy", tc.policy)
			if code != 0 {
				t.Fatalf("exit = %d (out %s)", code, out)
			}
			if !strings.Contains(out, tc.wantText) {
				t.Errorf("missing %q:\n%s", tc.wantText, out)
			}
			if tc.notWanted != "" && strings.Contains(out, tc.notWanted) {
				t.Errorf("unexpected %q:\n%s", tc.notWanted, out)
			}
		})
	}
}

// TestCallToolDataurlJSONCarriesDataURL: the JSON payload must carry the
// data: URL (single representation) so a remote harness can hand it to a
// provider without filesystem access.
func TestCallToolDataurlJSONCarriesDataURL(t *testing.T) {
	code, out := runShot(t, "--binaries-policy", "dataurl")
	if code != 0 {
		t.Fatalf("exit = %d (out %s)", code, out)
	}
	if !strings.Contains(out, "data:image/png;base64,UE5HREFUQQ==") {
		t.Errorf("json payload missing data URL:\n%s", out)
	}
}

// TestReadResourceFilePolicyWritesArtifact: a binary resource body follows
// the same route as image parts — file + pointer marker.
func TestReadResourceFilePolicyWritesArtifact(t *testing.T) {
	dir := t.TempDir()
	cfgPath := writeConfig(t, buildFixture(t))
	args := []string{"read-resource", "--server", "fx", "--uri", "file:///fixture.pdf",
		"--no-server", "--config", cfgPath, "--format", "text", "--save-binaries", dir}
	code, out := run(t, args...)
	if code != 0 {
		t.Fatalf("exit = %d (out %s)", code, out)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 || filepath.Ext(entries[0].Name()) != ".pdf" {
		t.Fatalf("artifact entries = %v, want one .pdf", entries)
	}
	raw, err := os.ReadFile(filepath.Join(dir, entries[0].Name()))
	if err != nil || string(raw) != "%PDF-body" {
		t.Fatalf("artifact bytes = %q err %v", raw, err)
	}
	if !strings.Contains(out, "[binary: application/pdf → "+filepath.Join(dir, entries[0].Name())) {
		t.Errorf("missing pointer marker:\n%s", out)
	}
}

// TestReadResourceDataurlTextIsSummarized: a data: URL body must never be
// printed — stdout stays a one-line summary.
func TestReadResourceDataurlTextIsSummarized(t *testing.T) {
	cfgPath := writeConfig(t, buildFixture(t))
	args := []string{"read-resource", "--server", "fx", "--uri", "file:///fixture.pdf",
		"--no-server", "--config", cfgPath, "--format", "text", "--binaries-policy", "dataurl"}
	code, out := run(t, args...)
	if code != 0 {
		t.Fatalf("exit = %d (out %s)", code, out)
	}
	if !strings.Contains(out, "[binary: application/pdf dataurl (9 B)]") {
		t.Errorf("missing dataurl summary:\n%s", out)
	}
	if strings.Contains(out, "data:application/pdf") {
		t.Errorf("data URL leaked to stdout:\n%s", out)
	}
}

// TestCallToolBadPolicyIsUsageError: a typo in --binaries-policy is a usage
// failure (exit 2), never a silently different binary handling.
func TestCallToolBadPolicyIsUsageError(t *testing.T) {
	code, out := runShot(t, "--binaries-policy", "explode")
	if code != 2 {
		t.Fatalf("exit = %d, want 2 (out %s)", code, out)
	}
	if !strings.Contains(out, "unknown --binaries-policy") {
		t.Errorf("error envelope lacks policy wording:\n%s", out)
	}
}

// TestFilePolicyWithoutSaveDirUsesTempSweepableDir: default runs create a
// sweepable temp dir, not a per-invocation leak in the cwd.
func TestFilePolicyWithoutSaveDirUsesTempSweepableDir(t *testing.T) {
	code, out := runShot(t, "--format", "text")
	if code != 0 {
		t.Fatalf("exit = %d (out %s)", code, out)
	}
	if !strings.Contains(out, filepath.Join(os.TempDir(), "agent-mcp-binaries-")) {
		t.Errorf("marker does not point at a sweepable temp dir:\n%s", out)
	}
}
