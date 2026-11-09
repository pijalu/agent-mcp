package server_test

import (
	"context"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pijalu/agent-mcp/client"
	pb "github.com/pijalu/agent-mcp/proto/agentmcp/v1"
	"github.com/pijalu/agent-mcp/server"
	"google.golang.org/grpc/metadata"
)

func TestRunRejectsOccupiedPort(t *testing.T) {
	configPath := writeConfig(t)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()

	err = server.Run(context.Background(), server.Options{
		ConfigPath: configPath,
		Listen:     listener.Addr().String(),
	}, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "listen") {
		t.Fatalf("Run() error = %v, want occupied-listen diagnostic", err)
	}
}

func TestRunServesLibraryDaemonUntilCancelled(t *testing.T) {
	configPath := writeConfig(t)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var log strings.Builder
	done := make(chan error, 1)
	go func() {
		done <- server.Run(ctx, server.Options{
			ConfigPath: configPath,
			Listen:     address,
			APIKeys:    []string{"secret"},
		}, &log)
	}()

	var rpc pb.AgentMCPClient
	var closer io.Closer
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		rpc, closer, err = client.GRPCDial(context.Background(), address)
		if err == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("dial library daemon: %v", err)
	}
	defer closer.Close()
	callCtx, callCancel := context.WithTimeout(context.Background(), time.Second)
	defer callCancel()
	callCtx = metadata.AppendToOutgoingContext(callCtx, "x-agent-mcp-api-key", "secret")
	status, err := rpc.Status(callCtx, &pb.StatusRequest{})
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if status.GetConfigPath() != configPath {
		t.Errorf("config path = %q, want %q", status.GetConfigPath(), configPath)
	}
	if !strings.Contains(log.String(), address) {
		t.Errorf("startup log = %q, want listen address", log.String())
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run after cancel: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Run did not stop after cancellation")
	}
}

func writeConfig(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("[mcpServers]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}
