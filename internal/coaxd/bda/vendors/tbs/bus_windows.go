// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

//go:build windows

package tbs

import (
	"fmt"

	"github.com/Till0196/coax/internal/coaxd/bda/vendors"
	"github.com/Till0196/coax/pkg/i2c"
)

// Access is a property set that also takes a request in its instance data,
// which the read half of this bus needs and the write half does not.
type Access interface {
	vendors.Properties
	GetWithInstance(set vendors.GUID, id uint32, instance, data []byte) (uint32, error)
}

// Bus is a tuner's own I2C bus, reached through the vendor property this driver
// puts on every tuner's input pin. The property carries one transfer per call:
// a slave address, bytes to write, and room for bytes read back. The chips on
// it are driven by the same code the Linux side uses, through the same
// seven-bit addresses; the property wants them shifted up by one.
type Bus struct {
	Properties Access
	// Name is what to call this bus in a message.
	Name string

	// repeats records that a tuner sits behind the demodulator's repeater.
	repeats      bool
	repeaterAt   byte
	tunerAddress byte
}

// BehindRepeater says the chip at tunerAddress is reached through the
// demodulator's I2C repeater, which has to be opened for it. Sony demodulators
// put their embedded tuner behind a repeater rather than on the bus, and it is
// closed unless asked. It is a property of how a board is wired, so the chip
// driver does not do it.
func (bus *Bus) BehindRepeater(demodAddress, tunerAddress byte) {
	bus.repeaterAt = demodAddress + 2 // SLVX
	bus.tunerAddress = tunerAddress
	bus.repeats = true
}

// openRepeater runs before every transfer aimed at the tuner, each time rather
// than once: nothing here owns the demodulator, and the vendor driver using it
// may have closed the repeater since.
func (bus *Bus) openRepeater(address byte) error {
	if !bus.repeats || address != bus.tunerAddress {
		return nil
	}
	if err := bus.writeRaw(bus.repeaterAt, []byte{0x00, 0x00}); err != nil {
		return fmt.Errorf("select the demodulator's bank 0: %w", err)
	}
	if err := bus.writeRaw(bus.repeaterAt, []byte{RepeaterRegister, 0x01}); err != nil {
		return fmt.Errorf("open the demodulator's I2C repeater: %w", err)
	}
	return nil
}

// Write sends bytes to a slave.
func (bus *Bus) Write(address byte, data []byte) error {
	if err := bus.openRepeater(address); err != nil {
		return err
	}
	return bus.writeRaw(address, data)
}

func (bus *Bus) writeRaw(address byte, data []byte) error {
	if len(data) == 0 {
		return fmt.Errorf("%s: an I2C write needs at least one byte", bus.Name)
	}
	if len(data) > I2CMaxWrite {
		return fmt.Errorf("%s: an I2C write of %d bytes is longer than the %d this bus carries",
			bus.Name, len(data), I2CMaxWrite)
	}
	request := make([]byte, I2CStructBytes)
	request[0] = address << 1
	copy(request[I2CWriteAt:], data)
	request[I2CWriteLenAt] = byte(len(data))
	err := bus.Properties.Set(Set, PropI2C, request)
	if err != nil {
		return fmt.Errorf("%s: I2C write to %#02x: %w", bus.Name, address, err)
	}
	return nil
}

// Read is not offered: the driver's read engine always sends a register byte
// first and there is no way to ask it for a bare read. Chip drivers reach this
// through i2c.WriteRead, which uses WriteRead below.
func (bus *Bus) Read(address byte, length int) ([]byte, error) {
	return nil, fmt.Errorf("%s: this bus cannot read without addressing a register first", bus.Name)
}

// WriteRead sends a register address and reads the answer in one transaction.
func (bus *Bus) WriteRead(address byte, write []byte, readLength int) ([]byte, error) {
	if err := bus.openRepeater(address); err != nil {
		return nil, err
	}
	if len(write) < 1 || len(write) > I2CMaxRegisterBytes {
		return nil, fmt.Errorf("%s: this bus addresses a register with one or two bytes, not %d",
			bus.Name, len(write))
	}
	if readLength < 1 || readLength > I2CMaxRead {
		return nil, fmt.Errorf("%s: an I2C read of %d bytes is outside the 1..%d this bus carries",
			bus.Name, readLength, I2CMaxRead)
	}
	request := make([]byte, I2CStructBytes)
	request[0] = address << 1
	copy(request[I2CWriteAt:], write)
	request[I2CWriteLenAt] = byte(len(write))
	request[I2CReadLenAt] = byte(readLength)

	// The driver fills the read area of the structure it was handed and copies
	// the whole thing back, so the reply is read out of the same offsets.
	reply := make([]byte, I2CStructBytes)
	_, err := bus.Properties.GetWithInstance(Set, PropI2C, request, reply)
	if err != nil {
		return nil, fmt.Errorf("%s: I2C read from %#02x: %w", bus.Name, address, err)
	}
	return append([]byte(nil), reply[I2CReadAt:I2CReadAt+readLength]...), nil
}

// Interface checks: the chip drivers take these and nothing else.
var (
	_ i2c.Bus         = (*Bus)(nil)
	_ i2c.WriteReader = (*Bus)(nil)
)
