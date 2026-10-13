// Copyright (c) Davin <smiledavin@gmail.com>; TurboSight; 2026 Till0196
// SPDX-License-Identifier: GPL-2.0-or-later

package rda5816

import (
	"fmt"
	"time"
)

// bandSplitMHz is where the chip changes band: above it the synthesizer runs at
// the wanted frequency, at or below it the low band is selected and it runs at
// twice the frequency, dividing down afterwards.
const bandSplitMHz = 1075

// The divider word is a 2^21 fraction of the reference. The two quotients are
// carried ready-computed rather than divided at runtime: 2^21/27 and 2^21/24,
// one per crystal, in megahertz.
const (
	stepsPer27MHz = 77672
	stepsPer24MHz = 87381
)

// pllWord returns the four-byte divider word for a frequency, and whether the
// low band was taken.
func pllWord(frequencyHz, xtalKHz uint32) (word uint32, lowBand bool) {
	// The arithmetic works in whole megahertz, so anything finer is truncated.
	megahertz := frequencyHz / 1_000_000
	if megahertz <= bandSplitMHz {
		lowBand = true
		megahertz *= 2
	}
	steps := uint32(stepsPer27MHz)
	if xtalKHz == Xtal24MHz {
		steps = stepsPer24MHz
	}
	return megahertz * steps, lowBand
}

// filterCode returns the value for registerFilter: the channel filter's
// bandwidth in megahertz, sized from the symbol rate. The clamp is applied
// before the figure is narrowed to a byte -- narrowing first would fold an
// absurd request (above some 370 Gsym/s) back into range instead of clamping.
func filterCode(symbolRateSPS uint32) byte {
	// The occupied bandwidth is the symbol rate widened by the worst roll-off the
	// chip is meant to pass, plus 4 MHz of margin.
	megahertz := (symbolRateSPS/1000*135/200 + 4000) / 1000
	switch {
	case megahertz < 4:
		megahertz = 4
	case megahertz > 40:
		megahertz = 40
	}
	return byte(megahertz)&filterMask | filterEnable
}

// Tune moves the tuner to a frequency in Hz, with a channel filter sized from
// the symbol rate. The signature is the AV201x's, the tuner this one replaces.
// A synthesizer that never settles is an error: a frequency the PLL will not
// lock onto carries nothing.
func (tuner *Tuner) Tune(frequencyHz, symbolRateSPS uint32) error {
	if err := tuner.write(registerControl, controlLoad); err != nil {
		return err
	}
	word, lowBand := pllWord(frequencyHz, tuner.config.XtalKHz)
	set, clear := byte(bandHigh), byte(bandLow)
	if lowBand {
		set, clear = bandLow, bandHigh
	}
	if err := tuner.maskAt(tuner.config.Address, registerBand, set, clear); err != nil {
		return err
	}
	// The divider goes in most significant byte first, one register at a time.
	for offset := 0; offset < 4; offset++ {
		value := byte(word >> (24 - 8*offset))
		if err := tuner.write(registerPLL+byte(offset), value); err != nil {
			return err
		}
	}
	if err := tuner.write(registerFilter, filterCode(symbolRateSPS)); err != nil {
		return err
	}
	if err := tuner.write(registerControl, controlTune); err != nil {
		return err
	}
	// Nothing can be read back before the synthesizer has had this long.
	time.Sleep(5 * time.Millisecond)

	locked, err := tuner.PLLLocked()
	if err != nil {
		return err
	}
	if !locked {
		return fmt.Errorf("RDA5816 PLL did not lock onto %d Hz", frequencyHz)
	}
	return nil
}
