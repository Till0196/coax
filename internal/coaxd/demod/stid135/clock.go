// Copyright (c) STMicroelectronics (Mathias Hilaire, Thierry Delahaye); CrazyCat; TurboSight; 2026 Till0196
// SPDX-License-Identifier: GPL-2.0-only

package stid135

// Everything the demodulators do is scaled by the front end's VCO: the local
// oscillator the four RF inputs are mixed against is a quarter of it, and the
// master clock the symbol rate and carrier registers use is a twelfth of that
// again. Reading it back is all this package does, which is what makes resuming
// possible.

// roundU32DividerX10 divides by ten and rounds to nearest, except that a
// remainder of exactly five rounds down. The asymmetry matches the reference
// driver, and the value feeds every subsequent frequency calculation.
func roundU32DividerX10(value uint32) uint32 {
	truncated := (value / 10) * 10
	if value-truncated > 5 {
		return value/10 + 1
	}
	return value / 10
}

// fvcoMHz recovers the PLL's output frequency: the crystal doubled and
// multiplied by an integer part plus, when the sigma-delta modulator runs, a
// fifteen-bit fraction, then divided by the reference divider. The tenfold
// scaling puts the rounding above on the first decimal place.
func fvcoMHz(referenceMHz uint32, integer, fraction, sigmaDelta, referenceDivider uint32) uint32 {
	scaled := (20*referenceMHz*integer*(1<<15) + 20*referenceMHz*sigmaDelta*fraction) / (1 << 15)
	return roundU32DividerX10(scaled) / referenceDivider
}

// readClocks recovers the local oscillator and master clock from the PLL
// registers the running chip holds.
func (demod *Demod) readClocks() error {
	sigmaDelta, err := demod.fieldRead(demod.afeReg(regAFEPLLFRACH), fldPLLSDON)
	if err != nil {
		return err
	}
	integer, err := demod.fieldRead(demod.afeReg(regAFEPLLINT), fldPLLINT)
	if err != nil {
		return err
	}
	fractionHigh, err := demod.fieldRead(demod.afeReg(regAFEPLLFRACH), fldPLLFRACH)
	if err != nil {
		return err
	}
	fractionLow, err := demod.fieldRead(demod.afeReg(regAFEPLLFRACL), fldPLLFRACL)
	if err != nil {
		return err
	}
	dividerBit, err := demod.fieldRead(demod.afeReg(regAFEPLLREFDIV), fldPLLREFDIVN)
	if err != nil {
		return err
	}
	// The divider is one when the field's low bit is set and two otherwise,
	// rather than the field's own value.
	referenceDivider := uint32(2)
	if dividerBit&1 != 0 {
		referenceDivider = 1
	}
	fraction := (uint32(fractionHigh)<<8 | uint32(fractionLow)) & 0x7fff

	vco := fvcoMHz(demod.config.ReferenceClockMHz, uint32(integer), fraction, uint32(sigmaDelta), referenceDivider)
	// The local oscillator is the PLL output divided by four; the master clock
	// is that again divided by twelve (the ADC clock over twelve).
	loMHz := vco / 4
	demod.loFrequencyHz = loMHz * 1_000_000
	demod.masterClockHz = loMHz * 1_000_000 / 12
	return nil
}

// LocalOscillatorHz is the frequency the four RF inputs are mixed against.
// Tune takes an absolute frequency; the chip can only be told an offset from
// this.
func (demod *Demod) LocalOscillatorHz() uint32 { return demod.loFrequencyHz }

// MasterClockHz is the clock the symbol rate and carrier registers use.
func (demod *Demod) MasterClockHz() uint32 { return demod.masterClockHz }
