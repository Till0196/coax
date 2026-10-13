// Copyright (c) 2017 Davin; 2026 Till0196
// SPDX-License-Identifier: GPL-2.0-only
//
// Ported from TBS linux_media (tbs5520.c) by Davin.

// Package tbs5520 drives the TBS 5520's USB bridge: vendor control transfers
// carry firmware download, I2C, an EEPROM read for the MAC address, and the
// board's GPIO bring-up, with the transport stream on bulk endpoint 0x82.
//
// The protocol is the 5520SE's, including its 0x93 "read from address" request.
// Each I2C message is forwarded independently rather than fusing a register write
// and its read, so a bare Read is meaningful here and the AVL6882's three-byte
// register pointer is a Write followed by a Read.
package tbs5520

import (
	"github.com/Till0196/coax/internal/coaxd/i2c"
	"github.com/Till0196/coax/internal/coaxd/usb/bridge/tbs"
	"github.com/Till0196/coax/internal/coaxd/usb/usbio"
)

// Bridge is an opened TBS 5520. It implements i2c.Bus.
type Bridge struct {
	*tbs.Bridge
}

// New wraps an opened USB device; Close releases the USB transport too.
func New(device *usbio.Transport) *Bridge {
	return &Bridge{tbs.New(device, "TBS5520")}
}

// Initialize runs the fixed GPIO bring-up the board needs once the
// demodulator and tuner have been attached.
func (bridge *Bridge) Initialize() error {
	if err := bridge.ControlWrite(tbs.RequestGPIOFixed, []byte{0x07, 0x01}); err != nil {
		return err
	}
	return bridge.ControlWrite(tbs.RequestGPIOFixed, []byte{0x06, 0x01})
}

var _ i2c.Bus = (*Bridge)(nil)
