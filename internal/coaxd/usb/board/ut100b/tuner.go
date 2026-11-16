// Copyright (c) 2008 FUKAUMI Naoki; 2012 tslroom; 2016 stz2012; 2026 KASHIWAYA Rikuto
// SPDX-License-Identifier: (Apache-2.0 OR GPL-2.0-or-later) AND BSD-2-Clause
//
// The register values and the order they go in are recfriio's Hdp.cpp for this
// board (BSD-2-Clause; see NOTICE). The rest is this module's own.

package ut100b

import (
	"fmt"

	"github.com/Till0196/coax/pkg/i2c"
)

// The RF tuner sits behind the demodulator's repeater at 0xc0. Its part number
// is not printed on the module and nothing published matches its registers: it
// is not an MxL136, MxL301RF or NM120, all of which answer there on other boards.
// What follows is the sequence the vendor driver sends, register by register,
// with names only where the role is clear from how the driver uses them.
//
// The tuner is only ever written. Reading it the usual way -- writing the
// register address and then reading -- is taken by this part as a one-byte
// command, and stops reception until the board is powered off; nothing here
// needs a read, and none is made.
const tunerAddress = 0xc0

// tunerInit is what the tuner is loaded with at power-up, after a one-byte reset.
var tunerInit = [][2]byte{
	{0x2c, 0x44}, {0x4d, 0x40}, {0x7f, 0x02}, {0x9a, 0x52}, {0x48, 0x5a}, {0x76, 0x1a},
	{0x6a, 0x48}, {0x64, 0x28}, {0x66, 0xe6}, {0x35, 0x11}, {0x7e, 0x01}, {0x0b, 0x99},
	{0x0c, 0x00}, {0x10, 0x40}, {0x12, 0xca}, {0x16, 0x90}, {0x32, 0x36}, {0xd8, 0x18},
	{0x05, 0x01},
}

// Registers the tune sequence writes.
const (
	// tunerControl is cleared before the frequency is written and set to 2 to
	// start the synthesiser on it.
	tunerControl  = 0x11
	tunerControl2 = 0x13
	tunerFreqLow  = 0x14
	tunerFreqHigh = 0x15
)

// tunerStepsPerMHz is the unit tunerFreqLow and tunerFreqHigh take.
const tunerStepsPerMHz = 64

// The frequencies the vendor driver tunes, in MHz: the terrestrial and cable
// channels Japan uses.
const (
	tunerMinMHz = 90
	tunerMaxMHz = 770
)

// tuner is the RF tuner, reached through the demodulator's tuner bus.
type tuner struct {
	bus i2c.Bus
}

func (tuner *tuner) write(register, value byte) error {
	return tuner.bus.Write(tunerAddress, []byte{register, value})
}

// load writes the power-up registers. The demodulator's own bring-up comes
// between the reset and this.
func (tuner *tuner) load() error {
	for _, pair := range tunerInit {
		if err := tuner.write(pair[0], pair[1]); err != nil {
			return fmt.Errorf("initialize the tuner: %w", err)
		}
	}
	return nil
}

// reset sends the one-byte reset alone.
func (tuner *tuner) reset() error {
	if err := tuner.bus.Write(tunerAddress, []byte{0xff}); err != nil {
		return fmt.Errorf("reset the tuner: %w", err)
	}
	return nil
}

// tune moves the synthesiser to a channel's nominal frequency in whole MHz. The
// vendor driver tunes whole megahertz, leaving the 1/7 MHz offset ISDB-T
// carriers sit on to the tuner's intermediate frequency, and that is what has
// been seen to lock, so the board rounds a channel file's frequency to it.
func (tuner *tuner) tune(mhz uint32) error {
	if mhz < tunerMinMHz || mhz > tunerMaxMHz {
		return fmt.Errorf("the tuner does not reach %d MHz (%d to %d)", mhz, tunerMinMHz, tunerMaxMHz)
	}
	code := uint16(mhz * tunerStepsPerMHz)
	for _, pair := range [][2]byte{
		{tunerControl, 0x00},
		{tunerControl2, 0x15},
		{tunerFreqLow, byte(code)},
		{tunerFreqHigh, byte(code >> 8)},
		{tunerControl, 0x02},
	} {
		if err := tuner.write(pair[0], pair[1]); err != nil {
			return fmt.Errorf("tune to %d MHz: %w", mhz, err)
		}
	}
	return nil
}
