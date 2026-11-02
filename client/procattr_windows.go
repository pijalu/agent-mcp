//go:build windows

// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package client

import "syscall"

// daemonProcAttr leaves Windows process creation flags at their defaults;
// Windows children do not receive Unix session or process-group semantics.
func daemonProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{}
}
