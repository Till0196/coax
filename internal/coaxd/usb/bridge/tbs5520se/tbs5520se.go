// Copyright (c) 2017 Davin; 2026 Till0196
// SPDX-License-Identifier: GPL-2.0-only
//
// Ported from TBS linux_media (tbs5520se.c) by Davin.

// Package tbs5520se drives the TBS 5520SE's USB bridge: vendor control
// transfers carry firmware download, I2C, an EEPROM read for the MAC address,
// and satellite LNB voltage, with the transport stream on bulk endpoint 0x82.
//
// The I2C protocol is address-routed: the demod and its two tuners answer
// through the shared read/write commands, while the LNB voltage and remote
// control are addressed as I2C devices at reserved addresses the real bus never
// uses. That routing lives here rather than in i2c.Bus.
package tbs5520se

import (
	"fmt"

	"github.com/Till0196/coax/internal/coaxd/i2c"
	"github.com/Till0196/coax/internal/coaxd/usb/bridge/tbs"
	"github.com/Till0196/coax/internal/coaxd/usb/usbio"
)

// Bridge is an opened TBS 5520SE. It implements i2c.Bus.
type Bridge struct {
	*tbs.Bridge
}

// New wraps an opened USB device; Close releases the USB transport too.
func New(device *usbio.Transport) *Bridge {
	return &Bridge{tbs.New(device, "TBS5520SE")}
}

// WriteRead keeps a demodulator register read in one transaction: a pointer
// write and the response read, rather than the plain Read above. Only the
// demodulator answers this way; the tuners are reached through Write/Read.
func (bridge *Bridge) WriteRead(address byte, write []byte, readLength int) ([]byte, error) {
	if len(write) != 1 {
		return nil, fmt.Errorf("TBS5520SE demod register read takes one register byte, got %d", len(write))
	}
	if err := bridge.ControlWrite(tbs.RequestI2CWriteRegister, []byte{byte(readLength), address << 1, write[0]}); err != nil {
		return nil, err
	}
	return bridge.ControlRead(tbs.RequestI2CRead, readLength)
}

// SetVoltage drives the LNB supply. false selects 13 V (vertical/right),
// true selects 18 V (horizontal/left).
func (bridge *Bridge) SetVoltage(high bool) error {
	value := byte(0x00)
	if high {
		value = 0x01
	}
	return bridge.ControlWrite(tbs.RequestGPIOFixed, []byte{0x03, value})
}

// Initialize runs the fixed GPIO bring-up once, after both the terrestrial
// (Si2157) and satellite (AV201x) tuners have been attached. The tuners are wired
// to separate antenna inputs, so unlike the combined CXD2878ER boards there is no
// RF path to switch: these three writes are bring-up only, not a tuner selector.
func (bridge *Bridge) Initialize() error {
	if err := bridge.ControlWrite(tbs.RequestGPIOFixed, []byte{0x01, 0x00}); err != nil {
		return err
	}
	if err := bridge.ControlWrite(tbs.RequestGPIO, []byte{0x00, 0x00}); err != nil {
		return err
	}
	return bridge.ControlWrite(tbs.RequestGPIOFixed, []byte{0x08, 0x01})
}

var _ i2c.Bus = (*Bridge)(nil)
