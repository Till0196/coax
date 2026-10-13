// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package host

import (
	"os/exec"
	"syscall"
)

// Detach starts the daemon in its own process group and without a window, so
// that it is not stopped along with the program that started it and does not
// put a console on the screen.
func Detach(command *exec.Cmd) {
	command.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP | 0x00000008, // DETACHED_PROCESS
	}
}
