// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

// Package iokit drives USB devices on macOS through IOKit, reached at run time
// without cgo: the framework is opened by name and its entry points are called
// through the standard library's own bridge to C.
//
// Everything in it is built only on macOS; this file carries no build constraint
// so `go build ./...` walks the tree on any system.
package iokit
