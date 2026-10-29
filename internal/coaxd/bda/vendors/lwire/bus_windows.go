// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

//go:build windows

// Package lwire is the private interface of the LWire BDA driver (cydtv.sys),
// which MyGica and Geniatech ship for their Cypress-bridged sticks.
//
// The driver puts one property on its capture filter, not on the tuner: a raw
// I2C transfer to the chips behind the bridge.
package lwire

import (
	"encoding/binary"
	"fmt"

	"github.com/Till0196/coax/pkg/i2c"
	"github.com/Till0196/coax/pkg/win32"
)

// Set is the driver's property set.
var Set = win32.GUID{
	Data1: 0x527dcf47, Data2: 0x1647, Data3: 0x46ff,
	Data4: [8]byte{0x84, 0x5c, 0xc9, 0x7d, 0x9a, 0xbf, 0x1c, 0xdc},
}

// PropI2C is the transfer: a write or a read, never both in one call.
const PropI2C = 0

// The request is the KSPROPERTY header followed by the transfer, and the driver
// answers into a structure of the same shape.
const (
	modeAt     = 24
	writeLenAt = 28
	readLenAt  = 32
	addressAt  = 36
	writeAt    = 40
	readAt     = 72
	// requestBytes is the whole structure.
	requestBytes = 104
	// maxTransfer is what each of the two buffers holds.
	maxTransfer = 32
)

// The transfer's direction.
const (
	modeRead  = 0
	modeWrite = 1
)

// Exchanger sends one request longer than the KSPROPERTY header. vendors.Control
// is one.
type Exchanger interface {
	Exchange(set *win32.GUID, id uint32, write bool, request, data []byte) (uint32, error)
}

// Bus is the I2C bus behind the bridge. Addresses are the seven-bit ones the
// chip drivers use; the driver wants them shifted up by one.
type Bus struct {
	Control Exchanger
	// Name is what to call this bus in a message.
	Name string
}

func (bus *Bus) request(mode uint32, address byte, write []byte, readLength int) []byte {
	request := make([]byte, requestBytes)
	binary.LittleEndian.PutUint32(request[modeAt:], mode)
	binary.LittleEndian.PutUint32(request[writeLenAt:], uint32(len(write)))
	binary.LittleEndian.PutUint32(request[readLenAt:], uint32(readLength))
	binary.LittleEndian.PutUint32(request[addressAt:], uint32(address)<<1)
	copy(request[writeAt:], write)
	return request
}

// Write sends bytes to a slave.
func (bus *Bus) Write(address byte, data []byte) error {
	if len(data) == 0 || len(data) > maxTransfer {
		return fmt.Errorf("%s: an I2C write of %d bytes is outside the 1..%d this bus carries", bus.Name, len(data), maxTransfer)
	}
	request := bus.request(modeWrite, address, data, 0)
	if _, err := bus.Control.Exchange(&Set, PropI2C, true, request, make([]byte, requestBytes)); err != nil {
		return fmt.Errorf("%s: I2C write to %#02x: %w", bus.Name, address, err)
	}
	return nil
}

// Read takes bytes from a slave, with no register address sent first.
func (bus *Bus) Read(address byte, length int) ([]byte, error) {
	if length < 1 || length > maxTransfer {
		return nil, fmt.Errorf("%s: an I2C read of %d bytes is outside the 1..%d this bus carries", bus.Name, length, maxTransfer)
	}
	reply := make([]byte, requestBytes)
	if _, err := bus.Control.Exchange(&Set, PropI2C, false, bus.request(modeRead, address, nil, length), reply); err != nil {
		return nil, fmt.Errorf("%s: I2C read from %#02x: %w", bus.Name, address, err)
	}
	return append([]byte(nil), reply[readAt:readAt+length]...), nil
}

var _ i2c.Bus = (*Bus)(nil)
