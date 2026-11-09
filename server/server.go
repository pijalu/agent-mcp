// Package server exposes agent-mcp's shared daemon to embedding binaries.
package server

import (
	"context"
	"fmt"
	"io"
	"net"
	"path/filepath"
	"time"

	"github.com/pijalu/agent-mcp/internal/config"
	"github.com/pijalu/agent-mcp/internal/daemon"
	"github.com/pijalu/agent-mcp/internal/session"
)

const (
	// DefaultListenAddress is used when Options.Listen is empty.
	DefaultListenAddress = "127.0.0.1:7474"
	// DefaultShutdownGrace bounds in-flight RPC draining by default.
	DefaultShutdownGrace = daemon.DefaultShutdownGrace
)

// Options configures the embedded agent-mcp daemon.
type Options struct {
	// Listen is the daemon's TCP host:port; empty uses DefaultListenAddress.
	Listen string
	// ConfigPath names agent-mcp's TOML config; empty uses its default path.
	ConfigPath string
	// APIKeys adds accepted client keys to those in the config file.
	APIKeys []string
	// IdleTimeout stops the daemon after inactivity; zero disables it.
	IdleTimeout time.Duration
	// ShutdownGrace bounds in-flight request draining; zero uses the default.
	ShutdownGrace time.Duration
}

// Run loads MCP servers and serves their shared daemon until ctx cancellation
// or a ShutdownAll RPC. It blocks while serving and closes all MCP sessions on
// exit. The host owns signal handling and process lifetime.
func Run(ctx context.Context, opts Options, log io.Writer) error {
	configPath, err := resolveConfigPath(opts.ConfigPath)
	if err != nil {
		return err
	}
	cfg, err := config.Load(configPath)
	if err != nil {
		return fmt.Errorf("server: load config: %w", err)
	}
	listen := opts.Listen
	if listen == "" {
		listen = DefaultListenAddress
	}
	keys := append([]string(nil), opts.APIKeys...)
	keys = append(keys, cfg.APIKeys...)
	mgr := session.NewManager(cfg.MCPServers, nil)
	d := daemon.New(mgr, daemon.Options{
		APIKeys:       keys,
		IdleTimeout:   opts.IdleTimeout,
		ShutdownGrace: opts.ShutdownGrace,
		ConfigPath:    configPath,
	})
	listener, err := net.Listen("tcp", listen)
	if err != nil {
		return fmt.Errorf("server: listen %s: %w", listen, err)
	}
	if log != nil {
		fmt.Fprintf(log, "agent-mcp daemon listening on %s\n", listener.Addr())
	}

	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			d.Shutdown()
		case <-done:
		}
	}()
	serveErr := d.Serve(listener)
	close(done)
	if serveErr != nil {
		return fmt.Errorf("server: serve: %w", serveErr)
	}
	return nil
}

// resolveConfigPath matches the absolute paths reported by client.Status.
func resolveConfigPath(path string) (string, error) {
	resolved := config.ResolvePath(path)
	absolute, err := filepath.Abs(resolved)
	if err != nil {
		return "", fmt.Errorf("server: resolve config path %s: %w", resolved, err)
	}
	return absolute, nil
}
