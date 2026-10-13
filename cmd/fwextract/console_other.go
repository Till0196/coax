// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

//go:build !windows

package main

// ownsConsole is only ever true on Windows. A terminal here belongs to the
// shell and outlives this program, so nothing is waited for.
func ownsConsole() bool { return false }
