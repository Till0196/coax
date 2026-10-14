// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

// Package winusb drives USB devices on Windows through WinUSB: SetupAPI to
// find what is attached, and the WinUSB entry points to talk to a device the
// driver has been bound to.
//
// Everything in it is built only on Windows; this file carries no build
// constraint so `go build ./...` walks the tree on any system.
package winusb
