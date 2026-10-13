// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

//go:build !linux && !windows

package native

import (
	"errors"

	"github.com/Till0196/coax/internal/coaxd/device"
	"github.com/Till0196/coax/internal/coaxd/host/pool"
)

// Backend is the name these devices would be listed under, on a system that
// had any.
const Backend = "native"

// Owns reports whether a backend name is served from here.
func Owns(backend string) bool { return backend == Backend }

// Recover has nothing to put back: no backend here takes hardware out from
// under another driver.
func Recover() int { return 0 }

// Recovering names the backends Recover touches, which is none of them.
func Recovering() []string { return nil }

// SetRoot does nothing here: this system has no DVB device tree.
func SetRoot(string) {}

// SetChardevRoot does nothing here: px4_drv is a Linux driver.
func SetChardevRoot(string) {}

// Claimed reports the USB devices a system driver holds: none here, since
// nothing on this system can be listed twice.
func Claimed() map[string]bool { return nil }

// Detect finds nothing: macOS has no tuner API of its own, and the hardware this
// program drives there is reached over USB and listed by the USB registry.
func Detect() ([]pool.Detected, error) { return nil, nil }

// Open is never reached, because Detect never names anything to open.
func Open(one pool.Detected, _ device.Options) (device.Board, error) {
	return nil, errors.New("this operating system has no tuners of its own")
}
