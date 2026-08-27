package cli_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeServersConfig writes a config with n fixture servers named fx01..fxNN, so
// a test can tell a truncated list from a complete one by counting entries.
func writeServersConfig(t *testing.T, bin string, n int) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.toml")
	var b strings.Builder
	for i := 1; i <= n; i++ {
		fmt.Fprintf(&b, "[mcpServers.fx%02d]\ncommand = %q\n\n", i, bin)
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// dataObject returns the envelope's data object.
func dataObject(t *testing.T, out string) map[string]any {
	t.Helper()
	data, ok := envelopeOf(t, out)["data"].(map[string]any)
	if !ok {
		t.Fatalf("data is not an object: %s", out)
	}
	return data
}

// TestListServersReturnsEveryConfiguredServer covers the silent-truncation
// defect: 12 configured servers must never be reported as 10 with no signal.
func TestListServersReturnsEveryConfiguredServer(t *testing.T) {
	cfgPath := writeServersConfig(t, buildFixture(t), 12)
	code, out := run(t, "list-servers", "--no-server", "--config", cfgPath)
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (out %s)", code, out)
	}
	data := dataObject(t, out)
	servers, ok := data["servers"].([]any)
	if !ok {
		t.Fatalf("servers missing: %s", out)
	}
	if len(servers) != 12 {
		t.Errorf("returned %d servers, want 12: %s", len(servers), out)
	}
	if data["total"] != float64(12) {
		t.Errorf("total = %v, want 12", data["total"])
	}
	if _, ok := data["truncated"]; ok {
		t.Errorf("nothing was dropped, so truncated must be absent: %s", out)
	}
}

// TestListServersReportsTruncation covers an explicit --limit: the dropped
// remainder is counted so the list cannot be read as complete.
func TestListServersReportsTruncation(t *testing.T) {
	cfgPath := writeServersConfig(t, buildFixture(t), 12)
	code, out := run(t, "list-servers", "--no-server", "--config", cfgPath, "--limit", "5")
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (out %s)", code, out)
	}
	data := dataObject(t, out)
	if len(data["servers"].([]any)) != 5 {
		t.Errorf("servers = %v, want 5", data["servers"])
	}
	if data["total"] != float64(12) {
		t.Errorf("total = %v, want 12 (the full configured count)", data["total"])
	}
	if data["truncated"] != true {
		t.Errorf("truncated = %v, want true: %s", data["truncated"], out)
	}
}

// TestListServersUnlimitedByDefault pins the default that makes the count
// trustworthy without an --limit hint.
func TestListServersUnlimitedByDefault(t *testing.T) {
	cfgPath := writeServersConfig(t, buildFixture(t), 12)
	_, out := run(t, "list-servers", "--no-server", "--config", cfgPath)
	if strings.Contains(out, `"truncated"`) {
		t.Errorf("an unrestricted list must not report truncation: %s", out)
	}
}

// TestDefaultPayloadDropsMCPTransportMetadata covers the token-cost defect:
// _meta, resultType, cacheScope, and ttlMs carry no agent value.
func TestDefaultPayloadDropsMCPTransportMetadata(t *testing.T) {
	cfgPath := writeConfig(t, buildFixture(t))
	commands := [][]string{
		{"list-tools", "--server", "fx"},
		{"list-resources", "--server", "fx"},
		{"list-prompts", "--server", "fx"},
		{"read-resource", "--server", "fx", "--uri", "file:///fixture.txt"},
		{"call-tool", "--server", "fx", "--tool", "echo", "--args", `{"text":"hi"}`},
		{"get-prompt", "--server", "fx", "--prompt", "greet", "--args", `{"name":"bob"}`},
	}
	for _, args := range commands {
		t.Run(args[0], func(t *testing.T) {
			cmdArgs := append(append([]string{}, args...), "--no-server", "--config", cfgPath)
			code, out := run(t, cmdArgs...)
			if code != 0 {
				t.Fatalf("exit = %d, want 0 (out %s)", code, out)
			}
			data := dataObject(t, out)
			for _, key := range []string{"_meta", "resultType", "cacheScope", "ttlMs"} {
				if _, ok := data[key]; ok {
					t.Errorf("payload leaks %q: %s", key, out)
				}
			}
			if _, ok := data["total"]; !ok && args[0] != "call-tool" && args[0] != "read-resource" && args[0] != "get-prompt" {
				t.Errorf("list payloads report a total: %s", out)
			}
		})
	}
}

// TestRawRestoresFullMCPResult covers the escape hatch: --raw writes exactly the
// MCP result the server returned, byte for byte, including the metadata the
// default payload drops.
func TestRawRestoresFullMCPResult(t *testing.T) {
	cfgPath := writeConfig(t, buildFixture(t))
	code, out := run(t, "list-tools", "--server", "fx", "--raw", "--no-server", "--config", cfgPath)
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (out %s)", code, out)
	}
	const want = `{"version":1,"data":{"_meta":{"io.modelcontextprotocol/serverInfo":{"name":"fixture","version":"0.0.1"}},"cacheScope":"public","resultType":"complete","tools":[{"description":"echoes text","inputSchema":{"additionalProperties":false,"properties":{"text":{"type":"string"}},"required":["text"],"type":"object"},"name":"echo"}],"ttlMs":0}}` + "\n"
	if out != want {
		t.Errorf("--raw payload changed\n got: %s\nwant: %s", out, want)
	}
}

// TestDefaultListToolsPayloadIsPinned pins the reduced payload for the same
// call: byte-identical to the raw result minus the transport bookkeeping.
func TestDefaultListToolsPayloadIsPinned(t *testing.T) {
	cfgPath := writeConfig(t, buildFixture(t))
	code, out := run(t, "list-tools", "--server", "fx", "--no-server", "--config", cfgPath)
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (out %s)", code, out)
	}
	const want = `{"version":1,"data":{"tools":[{"description":"echoes text","inputSchema":{"additionalProperties":false,"properties":{"text":{"type":"string"}},"required":["text"],"type":"object"},"name":"echo"}],"total":1}}` + "\n"
	if out != want {
		t.Errorf("default payload changed\n got: %s\nwant: %s", out, want)
	}
}

// TestUnknownServerNamesCandidates covers the unhelpful-error defect: the
// failure lists the configured servers so the caller can correct the name.
func TestUnknownServerNamesCandidates(t *testing.T) {
	cfgPath := writeServersConfig(t, buildFixture(t), 3)
	code, out := run(t, "list-tools", "--server", "nope", "--no-server", "--config", cfgPath)
	if code != 2 {
		t.Fatalf("exit = %d, want 2 (out %s)", code, out)
	}
	if !strings.Contains(out, "configured servers: fx01, fx02, fx03") {
		t.Errorf("missing candidates: %s", out)
	}
}

// TestUnknownToolNamesCandidates covers the same defect for tools: the failure
// names what the server does expose.
func TestUnknownToolNamesCandidates(t *testing.T) {
	cfgPath := writeConfig(t, buildFixture(t))
	code, out := run(t, "call-tool", "--server", "fx", "--tool", "nope", "--no-server", "--config", cfgPath)
	if code != 2 {
		t.Fatalf("exit = %d, want 2 (out %s)", code, out)
	}
	if !strings.Contains(out, "unknown tool") {
		t.Fatalf("expected an unknown-tool failure: %s", out)
	}
	if !strings.Contains(out, "available tools: echo") {
		t.Errorf("missing candidates: %s", out)
	}
}

// TestFailureHonoursTextFormat covers the defect where a failure always emitted
// JSON even when the caller asked for text.
func TestFailureHonoursTextFormat(t *testing.T) {
	cfgPath := writeConfig(t, buildFixture(t))
	code, out := run(t, "list-tools", "--server", "nope", "--format", "text", "--no-server", "--config", cfgPath)
	if code != 2 {
		t.Fatalf("exit = %d, want 2 (out %s)", code, out)
	}
	if strings.Contains(out, "{") {
		t.Errorf("text failure emitted JSON: %s", out)
	}
	for _, want := range []string{"error.code: usage", "error.message: usage error: unknown server"} {
		if !strings.Contains(out, want) {
			t.Errorf("output %q missing %q", out, want)
		}
	}
}

// TestFailureHonoursEqualsTextFormat covers the --format=text spelling, which
// the flag package accepts and the error renderer must recognise too.
func TestFailureHonoursEqualsTextFormat(t *testing.T) {
	cfgPath := writeConfig(t, buildFixture(t))
	code, out := run(t, "list-tools", "--server", "nope", "--format=text", "--no-server", "--config", cfgPath)
	if code != 2 {
		t.Fatalf("exit = %d, want 2 (out %s)", code, out)
	}
	if !strings.Contains(out, "error.code: usage") {
		t.Errorf("output %q is not text-rendered", out)
	}
}

// TestFailureStillJSONWithoutFormat guards the default: agents keep the
// machine-readable envelope.
func TestFailureStillJSONWithoutFormat(t *testing.T) {
	cfgPath := writeConfig(t, buildFixture(t))
	code, out := run(t, "list-tools", "--server", "nope", "--no-server", "--config", cfgPath)
	if code != 2 {
		t.Fatalf("exit = %d, want 2 (out %s)", code, out)
	}
	env := envelopeOf(t, out)
	if env["version"] != float64(1) {
		t.Errorf("version = %v, want 1", env["version"])
	}
	if _, ok := env["error"]; !ok {
		t.Errorf("missing error envelope: %s", out)
	}
}
