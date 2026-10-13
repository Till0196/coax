// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

//go:build unix

package progress

import (
	"os"
	"syscall"
	"unsafe"
)

// terminalColumns is how wide the terminal behind this file is, or zero when
// it will not say.
func terminalColumns(file *os.File) int {
	var size struct{ rows, columns, widthPx, heightPx uint16 }
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, file.Fd(),
		uintptr(syscall.TIOCGWINSZ), uintptr(unsafe.Pointer(&size)))
	if errno != 0 {
		return 0
	}
	return int(size.columns)
}
