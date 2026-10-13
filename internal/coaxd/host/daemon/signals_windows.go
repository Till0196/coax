// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package daemon

import (
	"os"
	"syscall"
)

// interruptSignals are the ways this platform asks a program to stop.
//
// Windows has no SIGTERM, but Go's runtime raises one for the three console
// events meaning the program is about to be taken away -- the console window
// closing, the user logging off, the machine shutting down -- and blocks so a
// handler can run first.
//
// What is left behind is why it matters: a Digital Devices card whose graph was
// running when the process died keeps the tuner filter acquired, and nothing in
// user space can take it back -- Windows reports it as needing a reboot. See
// docs/boards/dd.md. TerminateProcess gives no notice at all and cannot be
// helped.
func interruptSignals() []os.Signal {
	return []os.Signal{os.Interrupt, syscall.SIGTERM}
}

// keepGoingOnBrokenPipe has nothing to do here: Windows has no SIGPIPE, and a
// write to a pipe whose other end has gone is an error the caller sees.
func keepGoingOnBrokenPipe() {}
