// Copyright (c) 2004 Gerd Knorr; 2026 Till0196
// SPDX-License-Identifier: GPL-2.0-or-later
//
// Ported from the Linux kernel (drivers/media/dvb-frontends/dvb-pll.c), the
// dvb_pll_tda665x_earth_pt1 entry and the arithmetic that programs it.

// Package tda6651 drives the Philips TDA6651 as an Earthsoft PT1 or PT2 wires
// it: the tuner half of the Sharp VA1J5JF8007T module. It is a phase-locked
// loop and nothing more -- no readback, no lock indicator, no signal level. The
// chip is told a divider and a band in four bytes; the band bytes below are
// this board's.
package tda6651

import (
	"fmt"

	"github.com/Till0196/coax/pkg/i2c"
)

// Tuner is one TDA6651 as this board wires it.
type Tuner struct {
	bus     i2c.Bus
	address byte
}

// New addresses one chip. address is the 8-bit (already shifted) I2C address.
func New(bus i2c.Bus, address byte) *Tuner { return &Tuner{bus: bus, address: address} }

// FrequencyMin and FrequencyMax bound what this board tunes with it, in Hz.
const (
	FrequencyMin = 90_000_000
	FrequencyMax = 770_000_000
)

// The intermediate frequency the demodulator behind it expects, and the step
// the loop counts in. The step is a seventh of a megahertz, the fraction ISDB-T
// carriers sit on, so every channel from VHF 1 to 62 lands within a seventh of
// a hertz of its centre.
//
// stepHz is that seventh rounded down. It is right for working out which step
// to ask for and wrong for where a step lands -- multiplying it back is up to
// eight hundred hertz adrift at the top of the band -- so nothing here does.
const (
	intermediateHz = 57_000_000
	stepHz         = 142_857
)

// The bands this board is wired for; a frequency belongs to the first row whose
// ceiling is at or above it. The two bytes are the chip's control and band
// registers, kept whole because what the bits mean is not published.
var bands = [...]struct {
	ceiling       int64 // Hz
	control, band byte
}{
	{140_000_000, 0xc1, 0x81},
	{170_000_000, 0xc1, 0xa1},
	{220_000_000, 0xc1, 0x62},
	{330_000_000, 0xc1, 0xa2},
	{402_000_000, 0xc1, 0xe2},
	{450_000_000, 0xc1, 0x64},
	{550_000_000, 0xc1, 0x84},
	{600_000_000, 0xc1, 0xa4},
	{700_000_000, 0xc1, 0xc4},
	{770_000_000, 0xc1, 0xe4},
}

// initialisation is what the chip is sent before its first tune.
var initialisation = []byte{0x0e, 0x7f, 0xc1, 0x80, 0x80}

// Initialize sends the chip its opening bytes.
func (tuner *Tuner) Initialize() error {
	return tuner.bus.Write(tuner.address, initialisation)
}

// Tune sets the chip to a frequency in Hz.
func (tuner *Tuner) Tune(frequency int64) error {
	if frequency < FrequencyMin || frequency > FrequencyMax {
		return fmt.Errorf("TDA6651 cannot tune %d Hz, which is outside %d to %d",
			frequency, FrequencyMin, FrequencyMax)
	}
	band := -1
	for index := range bands {
		if frequency <= bands[index].ceiling {
			band = index
			break
		}
	}
	if band < 0 {
		return fmt.Errorf("TDA6651 has no band for %d Hz", frequency)
	}
	divider := Divider(frequency)
	if divider>>16 != 0 {
		return fmt.Errorf("TDA6651 divider %d for %d Hz does not fit sixteen bits", divider, frequency)
	}
	return tuner.bus.Write(tuner.address, []byte{
		byte(divider >> 8), byte(divider), bands[band].control, bands[band].band,
	})
}

// Divider is how many steps above the intermediate frequency a frequency sits,
// which is all the chip is told about where to go.
func Divider(frequency int64) int64 {
	return (frequency + intermediateHz + stepHz/2) / stepHz
}
