// Copyright (c) Davin <smiledavin@gmail.com>; TurboSight; 2026 Till0196
// SPDX-License-Identifier: GPL-2.0-or-later
//
// Ported from TBS linux_media (rda5816.c) for the RDA Microelectronics RDA5816,
// the satellite tuner on TBS's 6508 feeding the second frontend of an Si2183.
//
// Dual-channel tuner in one package: the two halves answer at consecutive I2C
// addresses, and only the lower one enables the other half and throws the
// shared RF input switch, so Initialize on the lower address writes to the
// upper one. Its band plan has no low-side path: below 1075 MHz the chip is
// asked for twice the frequency and divides. Tune's signature is deliberately
// the AV201x's, the tuner the RDA5816 replaces on the sister cards. Not ported:
// the DVB frontend property-cache writes, and there is no documented sleep or
// standby entry point.
package rda5816

import (
	"fmt"
	"time"

	"github.com/Till0196/coax/pkg/i2c"
)

// Seven-bit addresses of the two halves on the TBS 6508.
const (
	// ChannelAAddress is the first half, and the half that owns Initialize.
	ChannelAAddress byte = 0x08
	ChannelBAddress byte = 0x09
)

// The two reference crystals the chip is trimmed for, in kilohertz. No other
// value has a register table.
const (
	Xtal27MHz uint32 = 27000
	Xtal24MHz uint32 = 24000
)

// Config describes how the tuner is wired on the board.
type Config struct {
	// Address is the tuner's seven-bit I2C address; zero means ChannelAAddress.
	Address byte
	// XtalKHz is Xtal27MHz or Xtal24MHz. Zero means Xtal27MHz, which is what
	// every board in this tree fits.
	XtalKHz uint32
}

// Tuner is an RDA5816 reached over an I2C bus.
type Tuner struct {
	bus    i2c.Bus
	config Config

	initialized bool
}

// New addresses an RDA5816 on the given bus.
func New(bus i2c.Bus, config Config) *Tuner {
	if config.Address == 0 {
		config.Address = ChannelAAddress
	}
	if config.XtalKHz == 0 {
		config.XtalKHz = Xtal27MHz
	}
	return &Tuner{bus: bus, config: config}
}

// writeTo takes an address because Initialize must reach the other half.
func (tuner *Tuner) writeTo(address, register, value byte) error {
	if err := tuner.bus.Write(address, []byte{register, value}); err != nil {
		return fmt.Errorf("RDA5816 write %#02x at %#02x: %w", register, address, err)
	}
	return nil
}

func (tuner *Tuner) write(register, value byte) error {
	return tuner.writeTo(tuner.config.Address, register, value)
}

func (tuner *Tuner) readFrom(address, register byte) (byte, error) {
	data, err := i2c.WriteRead(tuner.bus, address, []byte{register}, 1)
	if err != nil {
		return 0, fmt.Errorf("RDA5816 read %#02x at %#02x: %w", register, address, err)
	}
	return data[0], nil
}

func (tuner *Tuner) read(register byte) (byte, error) {
	return tuner.readFrom(tuner.config.Address, register)
}

// maskAt read-modify-writes: 0x65 holds the band bits and the RF switch bits
// side by side.
func (tuner *Tuner) maskAt(address, register, set, clear byte) error {
	current, err := tuner.readFrom(address, register)
	if err != nil {
		return err
	}
	return tuner.writeTo(address, register, current&^clear|set)
}

func (tuner *Tuner) writePairs(pairs []byte) error {
	for offset := 0; offset < len(pairs); offset += 2 {
		if err := tuner.write(pairs[offset], pairs[offset+1]); err != nil {
			return err
		}
	}
	return nil
}

// Identify reads the chip and revision identifiers. Any answer is accepted.
func (tuner *Tuner) Identify() (chip uint16, revision byte, err error) {
	high, err := tuner.read(registerChipID)
	if err != nil {
		return 0, 0, err
	}
	low, err := tuner.read(registerRevision)
	if err != nil {
		return 0, 0, err
	}
	return uint16(high)<<4 | uint16(low)>>4, low & 0x0f, nil
}

// Initialize resets the chip and loads its register tables. It is a no-op the
// second time: a frontend may ask for initialization on every tune, and a second
// reset would drop the other half out from under its frontend.
func (tuner *Tuner) Initialize() error {
	if tuner.initialized {
		return nil
	}
	clock, thresholds := xtal27Clock, xtal27Thresholds
	switch tuner.config.XtalKHz {
	case Xtal27MHz:
	case Xtal24MHz:
		clock, thresholds = xtal24Clock, xtal24Thresholds
	default:
		return fmt.Errorf("RDA5816 has register tables for a 27 or 24 MHz crystal, not %d kHz",
			tuner.config.XtalKHz)
	}

	time.Sleep(1 * time.Millisecond)
	if err := tuner.write(registerControl, controlResetAssert); err != nil {
		return err
	}
	if err := tuner.write(registerControl, controlResetRelease); err != nil {
		return err
	}
	time.Sleep(2 * time.Millisecond)

	for _, table := range [][]byte{pllSettings, clock, analogSettings, thresholds, agcSettings} {
		if err := tuner.writePairs(table); err != nil {
			return err
		}
	}
	// The answer is discarded: on an undocumented chip the access itself may be
	// the point, so dropping it would change a known-good sequence.
	if _, err := tuner.read(registerRevision); err != nil {
		return err
	}

	for _, table := range [][]byte{blockerStages, blockerStart, blockerEnd, blockerRise, blockerFall} {
		if err := tuner.writePairs(table); err != nil {
			return err
		}
	}
	time.Sleep(10 * time.Millisecond)

	if err := tuner.write(registerControl, controlEnableChannel); err != nil {
		return err
	}
	if tuner.config.Address == ChannelAAddress {
		// Set the other half's enable bit rather than rewriting its control
		// register: what else it holds is not ours to decide.
		if err := tuner.maskAt(ChannelBAddress, registerControl, controlEnable, 0); err != nil {
			return err
		}
	}
	time.Sleep(10 * time.Millisecond)

	if tuner.config.Address == ChannelAAddress {
		// One RF input switch for both halves: set its bits here, clear them on
		// the other.
		if err := tuner.maskAt(ChannelAAddress, registerBand, rfSwitchMask, 0); err != nil {
			return err
		}
		if err := tuner.maskAt(ChannelBAddress, registerBand, 0, rfSwitchMask); err != nil {
			return err
		}
	}
	tuner.initialized = true
	return nil
}
