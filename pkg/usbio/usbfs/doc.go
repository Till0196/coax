// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

// Package usbfs drives USB devices on Linux through usbfs: the device nodes
// under /dev/bus/usb, and the ioctls on them that submit transfers.
//
// Everything in it is built only on Linux; this file carries no build constraint
// so `go build ./...` walks the tree on any system.
package usbfs
