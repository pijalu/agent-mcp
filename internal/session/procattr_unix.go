//go:build !windows

package session

import (
	"syscall"
	"time"
)

const serverTerminateDuration = 2 * time.Second

// serverProcAttr isolates each stdio server in its own process group. The SDK
// owns direct-process termination; the group also prevents accidental signal
// coupling with the agent-mcp process.
func serverProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setpgid: true}
}
