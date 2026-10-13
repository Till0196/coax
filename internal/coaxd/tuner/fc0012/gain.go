// Copyright (c) 2012 Hans-Frieder Vogt; 2026 Till0196
// SPDX-License-Identifier: GPL-2.0-or-later

package fc0012

import "fmt"

// lnaGains is what the low-noise amplifier is doing, in tenths of a decibel,
// indexed by the low five bits of register 0x13. The three groups of eight are
// the chip's low, middle and high gain settings, register values 0x00, 0x08
// and 0x10.
var lnaGains = [24]int32{
	-63, -58, -99, -73, -63, -65, -54, -60, // low
	71, 70, 68, 67, 65, 63, 61, 58, // middle
	197, 191, 188, 186, 184, 182, 181, 179, // high
}

// lnaSteps orders those settings into a ladder from least gain to most, each
// entry a value for register 0x13. The chip's own numbering is not a ladder:
// within each group of eight the gains run backwards, and the low group is
// scrambled besides. Mapped through lnaGains this ordering never decreases,
// -9.9 dB at step 0 to +19.7 dB at step 23; a test checks that.
var lnaSteps = [24]byte{
	0x02, 0x03, 0x05, 0x04, 0x00, 0x07, 0x01, 0x06,
	0x0f, 0x0e, 0x0d, 0x0c, 0x0b, 0x0a, 0x09, 0x08,
	0x17, 0x16, 0x15, 0x14, 0x13, 0x12, 0x11, 0x10,
}

// LNASteps is how many settings the amplifier has.
const LNASteps = len(lnaSteps)

// SetLNAGain moves the low-noise amplifier, 0 for the least gain and 23 for the
// most. The board starts at the top of the ladder, so this only ever turns gain
// down, for an aerial strong enough to overload the front end.
func (tuner *Tuner) SetLNAGain(step int) error {
	if step < 0 || step >= LNASteps {
		return fmt.Errorf("the FC0012's amplifier has %d steps, 0 to %d, not %d",
			LNASteps, LNASteps-1, step)
	}
	return tuner.write(registerLNAGain, lnaSteps[step])
}

// GainStepsDB is the whole ladder in decibels, least gain first, generated from
// lnaGains and lnaSteps so there is no third copy to keep in step.
func GainStepsDB() []float64 {
	steps := make([]float64, LNASteps)
	for step := range steps {
		steps[step] = float64(lnaGains[lnaSteps[step]]) / 10
	}
	return steps
}

// LNAGainDB is what a step is worth, in decibels.
func LNAGainDB(step int) (float64, bool) {
	if step < 0 || step >= LNASteps {
		return 0, false
	}
	return float64(lnaGains[lnaSteps[step]]) / 10, true
}

// inputADCLevel is the level the analogue-to-digital converter is calibrated
// against.
const inputADCLevel = -8

// SignalStrength reads the level at the tuner's input, in dBm: both the
// automatic control's gain and the amplifier's are subtracted from a fixed
// reference, so the answer falls as either rises. The amplifier's tenths are
// truncated to whole decibels, as other programs using this chip report them.
func (tuner *Tuner) SignalStrength() (float64, error) {
	// Latching the reading is what the zero write is for.
	if err := tuner.write(registerAGCReading, 0x00); err != nil {
		return 0, err
	}
	reading, err := tuner.read(registerAGCReading)
	if err != nil {
		return 0, err
	}
	setting, err := tuner.read(registerLNAGain)
	if err != nil {
		return 0, err
	}
	step := int32(setting & 0x1f)
	if int(step) >= len(lnaGains) {
		return 0, fmt.Errorf("the FC0012's amplifier reads back as setting %d, which is not one of its own", step)
	}
	total := (abs32(int32(reading>>5)-7) - 2 + int32(reading&0x1f)) * 2
	return float64(inputADCLevel - total - lnaGains[step]/10), nil
}

func abs32(value int32) int32 {
	if value < 0 {
		return -value
	}
	return value
}
