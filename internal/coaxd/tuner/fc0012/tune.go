// Copyright (c) 2012 Hans-Frieder Vogt; 2026 Till0196
// SPDX-License-Identifier: GPL-2.0-or-later

package fc0012

import (
	"fmt"
	"time"
)

// The frequency divider ladder. Each rung is the highest frequency its
// multiplier can reach without the VCO passing 3.56 GHz, i.e.
// freq * multi < 3560000. A tuner on the wrong rung still locks, just badly,
// so the rungs and their register pairs are pinned by test.
var dividers = []struct {
	belowKHz uint32
	multi    uint32
	reg5     byte
	reg6     byte
}{
	{37084, 96, 0x82, 0x00},
	{55625, 64, 0x82, 0x02},
	{74167, 48, 0x42, 0x00},
	{111250, 32, 0x42, 0x02},
	{148334, 24, 0x22, 0x00},
	{222500, 16, 0x22, 0x02},
	{296667, 12, 0x12, 0x00},
	{445000, 8, 0x12, 0x02},
	{593334, 6, 0x0a, 0x00},
	{0, 4, 0x0a, 0x02}, // the last rung takes everything above
}

// vcoSelectKHz is where the oscillator's high band starts.
const vcoSelectKHz = 3_060_000

// Tune moves the tuner to a frequency, with the channel width the demodulator
// expects. The width reaches the chip as two bits of register 6 and only 6, 7
// and 8 MHz have an encoding; everything received here is a 6 MHz ISDB-T
// channel, and the other two are kept because the chip accepts them.
func (tuner *Tuner) Tune(frequencyHz, bandwidthHz uint32) error {
	frequencyKHz := frequencyHz / 1000
	halfXtalKHz := tuner.config.XtalKHz / 2

	var registers [7]byte
	var multi uint32
	for _, rung := range dividers {
		if rung.belowKHz == 0 || frequencyKHz < rung.belowKHz {
			multi, registers[5], registers[6] = rung.multi, rung.reg5, rung.reg6
			break
		}
	}
	oscillatorKHz := frequencyKHz * multi
	highBand := oscillatorKHz >= vcoSelectKHz
	if highBand {
		registers[6] |= 0x08
	}

	if frequencyKHz >= 45000 {
		// The integer part of the division, rounded rather than truncated.
		divider := oscillatorKHz / halfXtalKHz
		if oscillatorKHz-divider*halfXtalKHz >= halfXtalKHz/2 {
			divider++
		}
		coarse, fine := divider/8, divider%8
		if fine < 2 {
			registers[1], registers[2] = byte(fine+8), byte(coarse-1)
		} else {
			registers[1], registers[2] = byte(fine), byte(coarse)
		}
	} else {
		// A fixed divider for the bottom of the band, unreachable from any
		// television channel.
		registers[1], registers[2] = 0x06, 0x11
	}

	// Hold the output clock steady across the retune.
	registers[6] |= 0x20

	// The fractional part of the delta-sigma divider, in fifteen bits with a
	// carry into the sixteenth that the chip reads as a sign.
	fraction := oscillatorKHz - (oscillatorKHz/halfXtalKHz)*halfXtalKHz
	fraction = (fraction << 15) / halfXtalKHz
	if fraction >= 16384 {
		fraction += 32768
	}
	registers[3], registers[4] = byte(fraction>>8), byte(fraction)

	registers[6] &= 0x3f
	switch bandwidthHz {
	case 6_000_000:
		registers[6] |= 0x80
	case 7_000_000:
		registers[6] |= 0x40
	case 8_000_000:
	default:
		return fmt.Errorf("FC0012 receives 6, 7 or 8 MHz channels, not %d Hz", bandwidthHz)
	}

	// What these three bits do is not documented. They are set for every
	// demodulator this chip is paired with, so they stay.
	registers[5] |= 0x07

	for register := 1; register <= 6; register++ {
		if err := tuner.write(byte(register), registers[register]); err != nil {
			return err
		}
	}
	return tuner.calibrate(registers[6], highBand)
}

// calibrate runs the oscillator's calibration and, if it settled at the end of
// its range, moves it to the other band and runs it again.
func (tuner *Tuner) calibrate(register6 byte, highBand bool) error {
	if err := tuner.runCalibration(); err != nil {
		return err
	}
	// A second calibration pass before waiting: undocumented, costs a few
	// milliseconds, and dropping it changes a sequence known to work.
	if err := tuner.write(registerVCOCal, 0x00); err != nil {
		return err
	}
	time.Sleep(10 * time.Millisecond)
	settled, err := tuner.read(registerVCOCal)
	if err != nil {
		return err
	}
	settled &= 0x3f

	switch {
	case highBand && settled > 0x3c:
		register6 &^= 0x08
	case !highBand && settled < 0x02:
		register6 |= 0x08
	default:
		return nil
	}
	if err := tuner.write(0x06, register6); err != nil {
		return err
	}
	return tuner.runCalibration()
}

func (tuner *Tuner) runCalibration() error {
	if err := tuner.write(registerVCOCal, 0x80); err != nil {
		return err
	}
	return tuner.write(registerVCOCal, 0x00)
}
