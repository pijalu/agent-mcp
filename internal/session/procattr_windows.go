//go:build windows

package session

import (
	"syscall"
	"time"
)

const serverTerminateDuration = 2 * time.Second

func serverProcAttr() *syscall.SysProcAttr { return &syscall.SysProcAttr{} }
