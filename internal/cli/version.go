package cli

import (
	"fmt"
	"io"

	"github.com/pijalu/agent-mcp/internal/version"
)

// writeVersion prints the build version stamped into the binary at link time.
func writeVersion(stdout io.Writer) error {
	_, err := fmt.Fprintf(stdout, "agent-mcp %s\n", version.Version)
	return err
}
