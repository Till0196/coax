// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

//go:build !windows

package host

import (
	"os/exec"
	"syscall"
)

// Detach puts the daemon in a session of its own, so that it is not stopped
// along with the program that happened to start it. A recording started from a
// terminal outlives the terminal.
func Detach(command *exec.Cmd) {
	command.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}
