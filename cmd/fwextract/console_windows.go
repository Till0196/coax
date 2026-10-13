// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

//go:build windows

package main

import (
	"syscall"
	"unsafe"
)

var (
	kernel32                  = syscall.NewLazyDLL("kernel32.dll")
	procGetConsoleProcessList = kernel32.NewProc("GetConsoleProcessList")
)

// ownsConsole reports whether the console being written to was made for this
// program, and will therefore be destroyed the moment it returns -- which is
// what tells an Explorer drop or double-click apart from a shell.
//
// The question is asked of the console because a drop is indistinguishable
// from a command line by its arguments; what differs is who owns the window.
// GetConsoleProcessList reports how many processes are attached: one means this
// program alone, and zero (no console at all) means there is nothing to wait for.
func ownsConsole() bool {
	var attached [1]uint32
	count, _, _ := procGetConsoleProcessList.Call(
		uintptr(unsafe.Pointer(&attached[0])), 1)
	return count == 1
}
