// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

// Package usbdev is how a USB board driver registers itself.
//
// What a driver implements lives in ../../device. What is here is the part
// specific to being on USB: a board is recognised by the vendor and product
// identifiers it enumerates with, and its tuner layout is worked out from them
// without opening it.
package usbdev

import (
	"github.com/Till0196/coax/internal/coaxd/device"
	"github.com/Till0196/coax/pkg/usbio"
)

// Backend describes one board driver to the registry. A new tuner ships as a
// package under ../board exporting one of these, plus a line in ../usb.go.
type Backend struct {
	// Name is the canonical -backend value, e.g. "t230".
	Name string
	// Aliases are additional accepted -backend values.
	Aliases []string
	// IDs are the USB vendor and product pairs the board enumerates as, so
	// that an attached device can be matched to its driver without opening it.
	IDs []usbio.ID
	// ColdIDs are what the board enumerates as before its firmware is in, for a
	// board that boots from the host. A board under one of these is not yet the
	// board: Detect leaves it out, and Boot turns it into one that is.
	ColdIDs []usbio.ID
	// Boot uploads the firmware to every attached board still under ColdIDs. The
	// boards drop off the bus and come back under IDs; Boot does not wait for
	// that. Nil when ColdIDs is empty.
	Boot func(options device.Options) error
	// Description names the hardware for help output.
	Description string
	// Deliveries lists every delivery system any tuner of the board can reach,
	// for help output. Per-tuner capability comes from Board.Tuners.
	Deliveries []string
	// Supports reports whether some tuner of the board can tune a delivery
	// system, without opening the hardware.
	Supports func(deliverySystem string) bool
	// Layout describes the tuners a board carries, worked out from the identifiers
	// it enumerated with rather than from the hardware. A listing uses it so that
	// nothing has to be opened; an open board answers with Board.Tuners, the same
	// list confirmed. Nil for a board whose layout cannot be told from its
	// identifiers.
	Layout func(id usbio.ID) []device.TunerInfo
	// Open claims the USB device.
	Open func(options device.Options) (device.Board, error)
}

// Matches reports whether name selects this backend, accepting the bare name,
// any alias, and a "usb-" prefixed form of either.
func (backend Backend) Matches(name string) bool {
	for _, candidate := range append([]string{backend.Name}, backend.Aliases...) {
		if name == candidate || name == "usb-"+candidate {
			return true
		}
	}
	return false
}
