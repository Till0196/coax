// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package tray

// Options is what the icon needs to describe itself and what to do when asked
// to stop.
type Options struct {
	// Title is the name shown when the pointer rests on the icon.
	Title string
	// Version and Commit describe the build, so the icon can answer the first
	// question about a daemon nobody started on purpose: which one is this.
	// Either being empty leaves it out.
	Version string
	Commit  string
	// Stop is called once the person has chosen to stop the daemon and confirmed
	// it. The same orderly shutdown a signal starts, not a way out of it.
	Stop func()
	// Busy reports how many clients are attached, so the question can say what
	// stopping would interrupt. Nil where nobody is counting.
	Busy func() int
	// Report says anything worth saying, the way the daemon says it.
	Report func(string)
}
