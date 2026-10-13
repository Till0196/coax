// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

//go:build windows

package progress

import (
	"os"
	"syscall"
	"unsafe"
)

var procGetConsoleScreenBufferInfo = syscall.NewLazyDLL("kernel32.dll").
	NewProc("GetConsoleScreenBufferInfo")

type coord struct{ x, y int16 }

type smallRect struct{ left, top, right, bottom int16 }

type consoleScreenBufferInfo struct {
	size              coord
	cursorPosition    coord
	attributes        uint16
	window            smallRect
	maximumWindowSize coord
}

// terminalColumns is how wide the console behind this file is, or zero when it
// will not say. The window rather than the buffer: the buffer is often wider
// than what is on screen, and it is the screen that wraps.
func terminalColumns(file *os.File) int {
	var info consoleScreenBufferInfo
	answered, _, _ := procGetConsoleScreenBufferInfo.Call(
		file.Fd(), uintptr(unsafe.Pointer(&info)))
	if answered == 0 {
		return 0
	}
	return int(info.window.right-info.window.left) + 1
}
