// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package asicen

import (
	"encoding/binary"
	"fmt"
	"io"
)

// propRead reads one byte from a device on the I2C bus: a write names the
// device and the register, and a read then gives the byte.
const propRead = 3

// The satellite demodulator lists the streams of the carrier it is locked to
// in its transmission control block: slot n's TSID at tmccStreams+2n, high
// byte first, 0xffff for an empty one.
const (
	tmccStreams = 0xce
	tmccSlots   = 8
	tmccEmpty   = 0xffff
)

// ReadRegister reads one register of a device on the enclosure's I2C bus.
// The caller chooses the filter that reaches this demodulator.
func (c *Control) ReadRegister(address, register byte) (byte, error) {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	return c.readRegisterLocked(address, register)
}

func (c *Control) readRegisterLocked(address, register byte) (byte, error) {
	if c.closed {
		return 0, io.ErrClosedPipe
	}
	if err := c.properties.Set(Set, propRead, []byte{address, register, 0, 0}); err != nil {
		return 0, fmt.Errorf("read register %#02x of %#02x: %w", register, address, err)
	}
	var value [4]byte
	if n, err := c.properties.Get(Set, propRead, value[:]); err != nil {
		return 0, fmt.Errorf("read register %#02x of %#02x: %w", register, address, err)
	} else if n != uint32(len(value)) {
		return 0, fmt.Errorf("read register %#02x of %#02x: got %d bytes, want %d", register, address, n, len(value))
	}
	return value[0], nil
}

// StreamAt reads the TSID in one slot of the transmission control block of
// the satellite demodulator at address, or reports the slot empty.
func (c *Control) StreamAt(address byte, slot int) (uint16, bool, error) {
	if slot < 0 || slot >= tmccSlots {
		return 0, false, fmt.Errorf("TMCC slot %d is outside 0..%d", slot, tmccSlots-1)
	}
	c.mutex.Lock()
	defer c.mutex.Unlock()
	high, err := c.readRegisterLocked(address, byte(tmccStreams+2*slot))
	if err != nil {
		return 0, false, err
	}
	low, err := c.readRegisterLocked(address, byte(tmccStreams+2*slot+1))
	if err != nil {
		return 0, false, err
	}
	tsid := uint16(high)<<8 | uint16(low)
	return tsid, tsid != tmccEmpty && tsid != 0, nil
}

// TransmissionStream asks the driver's own TMCC implementation. It knows its
// internal demodulator numbering and control route, including shared bridges.
// Older drivers that do not implement this item can use StreamAt as a fallback.
func (c *Control) TransmissionStream(slot int) (uint16, bool, error) {
	if slot < 0 || slot >= tmccSlots {
		return 0, false, fmt.Errorf("TMCC slot %d is outside 0..%d", slot, tmccSlots-1)
	}
	c.mutex.Lock()
	defer c.mutex.Unlock()
	if c.closed {
		return 0, false, io.ErrClosedPipe
	}
	var data [36]byte
	n, err := c.properties.Get(Set, 45, data[:])
	if err != nil {
		return 0, false, fmt.Errorf("read ASICEN TMCC: %w", err)
	}
	if n != uint32(len(data)) {
		return 0, false, fmt.Errorf("ASICEN TMCC: got %d bytes, want %d", n, len(data))
	}
	count := data[32]
	if count > tmccSlots {
		return 0, false, fmt.Errorf("ASICEN TMCC: invalid slot count %d", count)
	}
	if slot >= int(count) {
		return 0, false, nil
	}
	value := binary.LittleEndian.Uint32(data[slot*4:])
	if value > 0xffff {
		return 0, false, fmt.Errorf("ASICEN TMCC: invalid TSID %#x", value)
	}
	tsid := uint16(value)
	return tsid, tsid != 0 && tsid != tmccEmpty, nil
}
