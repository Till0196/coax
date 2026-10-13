// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package scanner

import (
	"fmt"
	"io"
)

// discoveryDisplay writes what discovery is doing, in the same shape as a
// scan's progress but without a total to count towards.
type discoveryDisplay struct {
	out io.Writer
}

func newDiscoveryDisplay(out io.Writer) *discoveryDisplay {
	if out == nil {
		out = io.Discard
	}
	return &discoveryDisplay{out: out}
}

func (display *discoveryDisplay) line(format string, arguments ...any) {
	fmt.Fprintf(display.out, format, arguments...)
}

func (display *discoveryDisplay) finish(message string) {
	fmt.Fprintln(display.out, message)
}
