// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

// Package trace is the daemon's diagnostic log: what a board did on the way to
// an error that a client only sees the last line of. It says nothing until the
// daemon is given somewhere to write it (coaxd --log, or COAXD_LOG for a daemon
// a client started), so the drivers can call it freely.
package trace

import (
	"fmt"
	"io"
	"sync"
	"time"
)

var (
	mutex  sync.Mutex
	output io.Writer
)

// SetOutput sends the log to w, or nowhere when w is nil.
func SetOutput(w io.Writer) {
	mutex.Lock()
	defer mutex.Unlock()
	output = w
}

// Enabled reports whether anything is being written, for a caller whose
// message is costly to build.
func Enabled() bool {
	mutex.Lock()
	defer mutex.Unlock()
	return output != nil
}

// Printf writes one line, stamped with the time to the millisecond.
func Printf(format string, arguments ...any) {
	mutex.Lock()
	defer mutex.Unlock()
	if output == nil {
		return
	}
	fmt.Fprintf(output, "%s %s\n", time.Now().Format("2006-01-02 15:04:05.000"), fmt.Sprintf(format, arguments...))
}

// Forward writes a line another process already stamped, as it came.
func Forward(line string) {
	mutex.Lock()
	defer mutex.Unlock()
	if output == nil {
		return
	}
	fmt.Fprintln(output, line)
}
