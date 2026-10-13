// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

// Package usbio exposes the raw USB endpoints every tuner board is driven through. Bridge
// chips sit on top of it, IC drivers on top of those, and a board driver in ../board wires
// a particular combination together.
//
// Transfers reach the hardware through the Go standard library alone, with no cgo, using
// only what each operating system already ships:
//
//	backend  the contract the three below fulfil, and nothing else
//	usbfs    Linux: ioctls on the device nodes under /dev/bus/usb
//	winusb   Windows: SetupAPI to find a device, WinUSB to talk to it
//	iokit    macOS: IOKit, resolved at run time through the Darwin libc bridge
//
// Systems none of them covers fall back to the Linux DVB and Windows BDA backends, which
// do not come through here at all.
//
// # Unsafe
//
// This package uses none; the backends do, because the three systems' interfaces are C
// ones: kernel-fixed structs, memory outside the Go heap for buffers a kernel holds a
// pointer to, and the function-pointer tables COM and IOKit interfaces are. Values
// arriving in a buffer are decoded with encoding/binary instead.
package usbio
