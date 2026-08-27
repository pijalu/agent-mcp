package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
)

// ErrInvalidConfig marks validation failures in server entries.
var ErrInvalidConfig = errors.New("invalid config")

// ServerConfig describes one MCP server entry from the config file.
type ServerConfig struct {
	Command   string            `toml:"command"`
	Args      []string          `toml:"args"`
	Env       map[string]string `toml:"env"`
	URL       string            `toml:"url"`
	Transport string            `toml:"transport"`
	Headers   map[string]string `toml:"headers"`
}

// Config is the root of config.toml.
type Config struct {
	MCPServers map[string]ServerConfig `toml:"mcpServers"`
	APIKeys    []string                `toml:"api_keys"`
}

// DefaultPath returns ~/.agent-mcp/config.toml.
func DefaultPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".agent-mcp", "config.toml")
	}
	return filepath.Join(home, ".agent-mcp", "config.toml")
}

// ResolvePath returns flagValue when set, otherwise DefaultPath().
func ResolvePath(flagValue string) string {
	if flagValue != "" {
		return flagValue
	}
	return DefaultPath()
}

// Load parses and validates the TOML config at path.
func Load(path string) (*Config, error) {
	var cfg Config
	if _, err := toml.DecodeFile(path, &cfg); err != nil {
		return nil, fmt.Errorf("config: load %s: %w", path, err)
	}
	for name, srv := range cfg.MCPServers {
		srv.applyDefaults()
		cfg.MCPServers[name] = srv
		if err := srv.Validate(); err != nil {
			return nil, fmt.Errorf("%w: server %q: %v", ErrInvalidConfig, name, err)
		}
	}
	return &cfg, nil
}

// Save writes cfg as TOML to path, creating parent directories.
// The file is rewritten wholesale; comments are not preserved.
func Save(path string, cfg *Config) error {
	for name, srv := range cfg.MCPServers {
		srv.applyDefaults()
		cfg.MCPServers[name] = srv
		if err := srv.Validate(); err != nil {
			return fmt.Errorf("%w: server %q: %v", ErrInvalidConfig, name, err)
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("config: create %s: %w", filepath.Dir(path), err)
	}
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("config: write %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()
	if err := toml.NewEncoder(f).Encode(cfg); err != nil {
		return fmt.Errorf("config: encode %s: %w", path, err)
	}
	return nil
}

// LoadOrEmpty returns the config at path, or an empty Config when the
// file does not exist.
func LoadOrEmpty(path string) (*Config, error) {
	cfg, err := Load(path)
	if errors.Is(err, os.ErrNotExist) {
		return &Config{MCPServers: map[string]ServerConfig{}}, nil
	}
	if cfg != nil && cfg.MCPServers == nil {
		cfg.MCPServers = map[string]ServerConfig{}
	}
	return cfg, err
}

// applyDefaults fills in the implied transport.
func (s *ServerConfig) applyDefaults() {
	if s.Transport == "" && s.Command != "" {
		s.Transport = "stdio"
	}
}

// Validate checks transport exclusivity rules per spec. Each transport-only
// setting is rejected on the other transport rather than silently ignored:
// headers are HTTP-only and env is stdio-only, so accepting either on the
// wrong kind of entry would write a config that looks meaningful and is not.
func (s *ServerConfig) Validate() error {
	if err := s.validateEndpoint(); err != nil {
		return err
	}
	if err := s.validateEndpointSettings(); err != nil {
		return err
	}
	return s.validateTransport()
}

// validateEndpoint requires exactly one of command and url.
func (s *ServerConfig) validateEndpoint() error {
	switch {
	case s.Command != "" && s.URL != "":
		return errors.New("both command and url set")
	case s.Command == "" && s.URL == "":
		return errors.New("neither command nor url set")
	}
	return nil
}

// validateEndpointSettings rejects settings that only apply to the other
// endpoint kind, so a silently ignored value cannot look configured.
func (s *ServerConfig) validateEndpointSettings() error {
	switch {
	case s.Command != "" && len(s.Headers) > 0:
		return errors.New("headers require a url (command implies stdio)")
	case s.URL != "" && len(s.Env) > 0:
		return errors.New("env requires a command (url implies remote)")
	}
	return nil
}

// validateTransport requires stdio for command entries and a known remote
// transport for url entries; an empty transport is the caller's default.
func (s *ServerConfig) validateTransport() error {
	switch {
	case s.Command != "" && s.Transport != "" && s.Transport != "stdio":
		return errors.New("transport requires url (command implies stdio)")
	case s.URL != "" && s.Transport != "" &&
		s.Transport != "sse" && s.Transport != "streamable":
		return fmt.Errorf("invalid transport %q (want sse or streamable)", s.Transport)
	}
	return nil
}
