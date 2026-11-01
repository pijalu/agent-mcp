//go:build !windows

// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package client

import "syscall"

// daemonProcAttr detaches the spawned daemon into its own process group so
// it survives the client process exiting.
func daemonProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setsid: true}
}
