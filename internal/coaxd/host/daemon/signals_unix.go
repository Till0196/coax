// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

//go:build !windows

package daemon

import (
	"os"
	"os/signal"
	"syscall"
)

// interruptSignals are the ways this platform asks a program to stop. SIGTERM
// matters most: a service manager sends it, and so does the front end to the
// command it started when a recording ends.
func interruptSignals() []os.Signal {
	return []os.Signal{os.Interrupt, syscall.SIGTERM}
}

// keepGoingOnBrokenPipe stops a broken standard error from ending the daemon.
// Go turns a failed write to descriptor 1 or 2 back into SIGPIPE, which ends
// the program by default; the client that starts a daemon hands it a pipe to
// report a failed start and then exits, so without this the next log line would
// kill a working daemon holding tuners.
//
// Registering the signal on a channel turns it back into an error return on the
// write.
func keepGoingOnBrokenPipe() {
	signal.Notify(make(chan os.Signal, 1), syscall.SIGPIPE)
}
