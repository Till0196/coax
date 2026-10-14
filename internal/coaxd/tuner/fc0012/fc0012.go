// Copyright (c) 2012 Hans-Frieder Vogt; 2026 Till0196
// SPDX-License-Identifier: GPL-2.0-or-later
//
// Ported from the kernel's fc0012 driver by Hans-Frieder Vogt.
//
// It exists alongside internal/coaxd/tuner/nm120 because only one board of this
// family carries that Newport Media part; the other four carry this Fitipower
// chip.
//
// Not ported: the automatic gain loop (software, not a chip feature --
// SetLNAGain and SignalStrength are the pieces it needs), and a VHF band-switch
// callback for a switch this family does not have.
package fc0012

import (
	"fmt"

	"github.com/Till0196/coax/pkg/i2c"
)

// The tuner sits behind the demodulator rather than on the bridge's bus, so
// every transfer is addressed to the demodulator, opens its repeater register
// and carries the tuner's own eight-bit address as the next byte.
const (
	repeater     = 0xfe // demodulator register that forwards a transfer
	writeAddress = 0xc6
	readAddress  = 0xc7 // the write address with its low bit set
)

// Crystal frequencies there are register settings for, in kilohertz. The boards
// here fit 36 MHz, which the initialization table is written for; the other two
// need bit 5 of register 0x07 set, which the table leaves clear.
const (
	Xtal36MHz   uint32 = 36000
	Xtal28800Hz uint32 = 28800
	Xtal27MHz   uint32 = 27000
)

// Config describes how the board wired the chip.
type Config struct {
	// XtalKHz is the reference crystal. Zero means Xtal36MHz.
	XtalKHz uint32
	// DualMaster sets the chip's dual-master bit, which no board here needs.
	DualMaster bool
	// LoopThrough leaves the aerial loop-through output driven, for a board that
	// daisy-chains a second tuner off this one.
	LoopThrough bool
}

// Tuner is an FC0012 reached through the demodulator in front of it.
type Tuner struct {
	bus     i2c.Bus
	address byte
	config  Config
}

// New addresses an FC0012 behind a demodulator. address is the demodulator's
// eight-bit I2C address, which is what a transfer is sent to; the tuner's own
// address rides inside it.
func New(bus i2c.Bus, address byte, config Config) *Tuner {
	if config.XtalKHz == 0 {
		config.XtalKHz = Xtal36MHz
	}
	return &Tuner{bus: bus, address: address, config: config}
}

func (tuner *Tuner) write(register, value byte) error {
	if err := tuner.bus.Write(tuner.address, []byte{repeater, writeAddress, register, value}); err != nil {
		return fmt.Errorf("FC0012 write %#02x: %w", register, err)
	}
	return nil
}

// read is two transfers rather than one repeated start: the demodulator's
// repeater carries the select and the turnaround separately.
func (tuner *Tuner) read(register byte) (byte, error) {
	if err := tuner.bus.Write(tuner.address, []byte{repeater, writeAddress, register}); err != nil {
		return 0, fmt.Errorf("FC0012 select %#02x: %w", register, err)
	}
	data, err := i2c.WriteRead(tuner.bus, tuner.address, []byte{repeater, readAddress}, 1)
	if err != nil {
		return 0, fmt.Errorf("FC0012 read %#02x: %w", register, err)
	}
	return data[0], nil
}

// Registers this driver reaches for by name; the rest are written once from the
// table below and never looked at again.
const (
	registerLoopThrough = 0x09
	registerAGCUpDown   = 0x0c
	registerVCOCal      = 0x0e
	registerAGCReading  = 0x12
	registerLNAGain     = 0x13
)

// defaults is the board's initialization table, registers 0x01 to 0x15. Three bytes are
// board-specific choices among the chip's alternatives:
//
//	0x0c  0xf8   the setting the AGC up-down mode in use needs
//	0x12  0x1b   the AGC reading's starting point
//	0x13  0x10   high LNA gain rather than the middle 0x08
var defaults = [0x16]byte{
	0x00, // register 0 does not exist; the table is written from 0x01
	0x05, 0x10, 0x00, 0x00, 0x0f, 0x00, 0x00, 0xff,
	0x6e, 0xb8, 0x82, 0xf8, 0x02, 0x00, 0x00, 0x00,
	0x00, 0x1b, 0x10, 0x00, 0x04,
}

// Initialize writes the board's register defaults.
func (tuner *Tuner) Initialize() error {
	registers := defaults
	switch tuner.config.XtalKHz {
	case Xtal36MHz:
	case Xtal27MHz, Xtal28800Hz:
		// The chip's one crystal-dependent bit.
		registers[0x07] |= 0x20
	default:
		return fmt.Errorf("FC0012 has register settings for a 36, 28.8 or 27 MHz crystal, not %d kHz",
			tuner.config.XtalKHz)
	}
	if tuner.config.DualMaster {
		registers[registerAGCUpDown] |= 0x02
	}
	if tuner.config.LoopThrough {
		registers[registerLoopThrough] |= 0x01
	}
	for register := 1; register < len(registers); register++ {
		if err := tuner.write(byte(register), registers[register]); err != nil {
			return err
		}
	}
	return nil
}
