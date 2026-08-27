package cli_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/pijalu/agent-mcp/internal/cli"
)

func TestOneShotListToolsUsesManager(t *testing.T) {
	cfgPath := writeConfig(t, buildFixture(t))
	mgr := &trackingManager{}
	deps := cli.Deps{
		NewManager: func(*configType) cli.ClosableManager { return mgr },
	}
	var stdout, stderr bytes.Buffer
	args := []string{"list-tools", "--server", "fx", "--no-server", "--config", cfgPath}
	code := cli.RunWith(deps, args, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit = %d err %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), `"echo"`) {
		t.Errorf("out: %s", stdout.String())
	}
	if mgr.closeCalls != 1 {
		t.Errorf("CloseAll = %d, want 1", mgr.closeCalls)
	}
}

func TestOneShotListServersUsesManager(t *testing.T) {
	cfgPath := writeConfig(t, buildFixture(t))
	mgr := &trackingManager{}
	deps := cli.Deps{NewManager: func(*configType) cli.ClosableManager { return mgr }}
	var stdout, stderr bytes.Buffer
	code := cli.RunWith(deps, []string{"list-servers", "--no-server", "--config", cfgPath}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit = %d err %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), `"fake"`) {
		t.Errorf("out: %s", stdout.String())
	}
}

func TestOneShotBadConfigPath(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := cli.RunWith(cli.Deps{}, []string{"list-servers", "--no-server", "--config", "/nope/missing.toml"}, &stdout, &stderr)
	if code != 2 {
		t.Fatalf("exit = %d, want 2 (out %s)", code, stdout.String())
	}
}

func TestOneShotBadArgsJSON(t *testing.T) {
	cfgPath := writeConfig(t, buildFixture(t))
	mgr := &trackingManager{}
	deps := cli.Deps{NewManager: func(*configType) cli.ClosableManager { return mgr }}
	var stdout, stderr bytes.Buffer
	args := []string{"call-tool", "--server", "fx", "--tool", "echo", "--args", "{bad",
		"--no-server", "--config", cfgPath}
	code := cli.RunWith(deps, args, &stdout, &stderr)
	if code != 2 {
		t.Fatalf("exit = %d, want 2 (out %s)", code, stdout.String())
	}
	if !strings.Contains(stdout.String(), "--args") {
		t.Errorf("out: %s", stdout.String())
	}
}

func TestOneShotBadPromptArgsJSON(t *testing.T) {
	cfgPath := writeConfig(t, buildFixture(t))
	mgr := &trackingManager{}
	deps := cli.Deps{NewManager: func(*configType) cli.ClosableManager { return mgr }}
	var stdout, stderr bytes.Buffer
	args := []string{"get-prompt", "--server", "fx", "--prompt", "greet", "--args", "{bad",
		"--no-server", "--config", cfgPath}
	code := cli.RunWith(deps, args, &stdout, &stderr)
	if code != 2 {
		t.Fatalf("exit = %d, want 2 (out %s)", code, stdout.String())
	}
}

func TestTextFormat(t *testing.T) {
	addr := startFakeDaemon(t)
	deps := cli.Deps{Dial: dialReal}
	var stdout, stderr bytes.Buffer
	code := cli.RunWith(deps, []string{"list-tools", "--server", "fake", "--connect", addr, "--format", "text"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit = %d err %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "echo") {
		t.Errorf("text out: %s", stdout.String())
	}
	if strings.Contains(stdout.String(), "name:") {
		t.Errorf("text out still flattens protocol keys: %s", stdout.String())
	}
}

func TestServerStartBadListen(t *testing.T) {
	cfgPath := writeConfig(t, buildFixture(t))
	var stdout, stderr bytes.Buffer
	code := cli.RunWith(cli.Deps{}, []string{"server", "start", "--listen", "999.999.0.1:1", "--config", cfgPath}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("exit = %d, want 1 (out %s)", code, stdout.String())
	}
}

func TestServerStartBadConfig(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := cli.RunWith(cli.Deps{}, []string{"server", "start", "--config", "/nope/missing.toml"}, &stdout, &stderr)
	if code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
}

func TestServerStartBadFlag(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := cli.RunWith(cli.Deps{}, []string{"server", "start", "--bogus"}, &stdout, &stderr)
	if code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
}

func TestStatusBadFlag(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := cli.RunWith(cli.Deps{Dial: dialReal}, []string{"server", "status", "--bogus"}, &stdout, &stderr)
	if code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
}

func TestShutdownBadFlag(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := cli.RunWith(cli.Deps{Dial: dialReal}, []string{"server", "shutdown", "--bogus"}, &stdout, &stderr)
	if code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
}

func TestCursorRequiresServer(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := cli.RunWith(cli.Deps{}, []string{"list-tools", "--cursor", "c9"}, &stdout, &stderr)
	if code != 2 || !strings.Contains(stdout.String(), "--server") {
		t.Fatalf("code=%d out=%s", code, stdout.String())
	}
}

func TestCursorPassthrough(t *testing.T) {
	addr := startFakeDaemon(t)
	deps := cli.Deps{Dial: dialReal}
	var stdout, stderr bytes.Buffer
	args := []string{"list-tools", "--server", "fake", "--cursor", "c9", "--connect", addr}
	code := cli.RunWith(deps, args, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit = %d out %s", code, stdout.String())
	}
}
