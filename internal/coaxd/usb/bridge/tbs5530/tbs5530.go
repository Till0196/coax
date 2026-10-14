// Copyright (c) 2022 Davin; 2026 Till0196
// SPDX-License-Identifier: GPL-2.0-only
//
// Ported from TBS linux_media (tbs5530.c) by Davin.

// Package tbs5530 drives the TBS 5530's USB bridge: vendor control transfers
// carry firmware download, I2C, an EEPROM read for the MAC address, and the
// transport stream source selector, with the stream on bulk endpoint 0x82.
//
// The protocol is the 5520SE's minus its 0x93 "read from address" request: this
// board implements only the two-message and one-message I2C shapes, so a bare
// Read has no encoding here. Both chips on the board read registers the
// two-message way.
//
// Both frontends share the one bulk endpoint. Which one feeds it is a bridge
// GPIO, not a demodulator setting -- SelectStreamSource writes it.
package tbs5530

import (
	"fmt"
	"time"

	"github.com/Till0196/coax/internal/coaxd/usb/bridge/tbs"
	"github.com/Till0196/coax/pkg/i2c"
	"github.com/Till0196/coax/pkg/usbio"
)

// Bridge is an opened TBS 5530. It implements i2c.Bus.
type Bridge struct {
	*tbs.Bridge
}

// New wraps an opened USB device; Close releases the USB transport too.
func New(device *usbio.Transport) *Bridge {
	return &Bridge{tbs.New(device, "TBS5530")}
}

// Read has no encoding on this bridge, which implements only the
// pointer-write-then-read shape of WriteRead. It exists to satisfy i2c.Bus.
func (bridge *Bridge) Read(byte, int) ([]byte, error) {
	return nil, fmt.Errorf("TBS5530 cannot encode a bare I2C read; use WriteRead")
}

// WriteRead writes a register pointer and reads the response in one
// transaction. Only a single register byte is ever forwarded, so longer
// pointers are rejected rather than silently truncated.
func (bridge *Bridge) WriteRead(address byte, write []byte, readLength int) ([]byte, error) {
	if len(write) != 1 {
		return nil, fmt.Errorf("TBS5530 register read takes one register byte, got %d", len(write))
	}
	if err := bridge.ControlWrite(tbs.RequestI2CWriteRegister, []byte{byte(readLength), address << 1, write[0]}); err != nil {
		return nil, err
	}
	return bridge.ControlRead(tbs.RequestI2CRead, readLength)
}

// InitializeSatellite runs the fixed GPIO bring-up the board needs once the
// M88RS6060 has been attached, powering its half of the board.
func (bridge *Bridge) InitializeSatellite() error {
	if err := bridge.ControlWrite(tbs.RequestGPIO, []byte{0x00, 0x00}); err != nil {
		return err
	}
	if err := bridge.ControlWrite(tbs.RequestGPIOFixed, []byte{0x08, 0x01}); err != nil {
		return err
	}
	time.Sleep(10 * time.Millisecond)
	return nil
}

// SelectStreamSource points the shared bulk endpoint at one of the two frontends:
// true the M88RS6060, false the CXD2878ER. The endpoint is only switched, never
// gated, so the two frontends differ in this byte alone.
func (bridge *Bridge) SelectStreamSource(satellite bool) error {
	value := byte(0x00)
	if satellite {
		value = 0x01
	}
	return bridge.ControlWrite(tbs.RequestGPIOFixed, []byte{0x0a, value})
}

var _ i2c.Bus = (*Bridge)(nil)
