//go:build !windows

package cli

import "syscall"

// daemonProcAttr detaches the spawned daemon into its own process group so
// it survives the client process exiting.
func daemonProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setsid: true}
}
