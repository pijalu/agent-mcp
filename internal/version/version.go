// Package version holds the agent-mcp build version.
package version

// Version is the release identity reported by `agent-mcp --version` and sent
// with MCP client handshakes. Release builds stamp it at link time:
//
//	go build -ldflags "-X github.com/pijalu/agent-mcp/internal/version.Version=v1.2.3"
//
// The literal below is the development default for plain `go build`/`go test`.
var Version = "0.1.0"
