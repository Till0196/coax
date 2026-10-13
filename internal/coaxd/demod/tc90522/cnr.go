// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package tc90522

import "math/bits"

// The TC90522 reports its carrier-to-noise measurement as a raw counter, and
// the curve turning that into decibels is not published anywhere. The two
// routines below are fixed-point approximations kept integer-for-integer rather
// than in floating point: the curve is defined only by the arithmetic that
// produces it, so a tidier version would be a different curve.

// mulShift is a fixed-point helper: a 64-bit product shifted back down and
// truncated to 32 bits.
func mulShift(a, b int64, shift uint) int32 { return int32((a * b) >> shift) }

// cnrHundredthsDBS converts a satellite raw reading to hundredths of a dB.
// Readings outside the curve's domain saturate at its ends.
func cnrHundredthsDBS(raw uint32) uint32 {
	if raw > 37000 {
		return 100
	}
	ival := int32(raw) - 3000
	if ival < 0 {
		return 91
	}
	// A Newton iteration for the square root, in 14-bit fixed point.
	i2val := int32(0x2000) + (ival >> 1)
	i3val := ival << 14
	for j := 0; ; j++ {
		if i2val < 0 {
			i2val = -i2val
		} else if i2val == 0 {
			i2val = 1
		}
		ival = (i3val/i2val + i2val) >> 1
		if ival == i2val || j >= 16 {
			break
		}
		i2val = ival
	}
	// A polynomial in (P - 1.75), evaluated by Horner's method.
	ival -= 0x3800
	i2val = 979 + ((-41846 * ival) >> 13)
	i2val = 875 + ((i2val * ival) >> 14)
	i2val = 6584 + ((i2val * ival) >> 14)
	i2val = -14590 + ((i2val * ival) >> 15)
	i2val = 20684 + ((i2val * ival) >> 13) + (1 << 3)
	return uint32(i2val >> 4)
}

// cnrHundredthsDBT converts a terrestrial raw reading to hundredths of a dB.
// The counter falls as the signal improves, so the curve runs backwards.
func cnrHundredthsDBT(raw uint32) uint32 {
	if raw < 1404 {
		return 4000
	}
	if raw > 5000000 {
		return 100
	}
	// A base-2 logarithm: the exponent from the bit length, and the fraction
	// from a Mercator series on the remainder.
	exponent := uint(bits.Len32(raw))
	var uval uint32
	var ival int32
	if raw&(1<<(exponent-2)) != 0 {
		uval = uint32(exponent)
		ival = int32((uint32(1)<<exponent - raw) << (24 - exponent))
	} else {
		uval = uint32(exponent - 1)
		ival = -int32((raw - uint32(1)<<(exponent-1)) << (25 - exponent))
	}
	var i3val int32
	if ival != 0 {
		i2val := ival
		i3val = ival
		for term := int32(2); term < 40; term++ {
			i2val = int32((int64(i2val) * int64(ival)) >> 24)
			if i2val == 0 {
				break
			}
			i3val += i2val / term
		}
		i3val = mulShift(int64(i3val), 1165800373, 28)
	}
	// The mixed signed and unsigned arithmetic here relies on wrapping. It is
	// spelled in uint32 so the wrap is defined rather than left to chance.
	uval = uint32(mulShift(int64(uval), 1616142483, 29-24))
	i3val = int32(uint32(1130911734) - (uval - uint32(i3val)) - uint32(20<<24))
	ival = i3val >> 12
	i2val := int32(8389) + ((5033 * ival) >> 15)
	i2val = 2294 + ((i2val * ival) >> 16)
	i2val = 6482166 + ((i2val * ival) >> 10)
	i2val = 8617370 + mulShift(int64(i2val), int64(i3val), 28) + (1 << 11)
	return uint32(i2val >> 12)
}

// A reading the chip has not taken yet. The estimator does not start with the
// lock: measured on a PT3, a terrestrial core that has just locked answers 0 for
// about a second before a real counter appears. Zero is no signal measured, and
// through the curve below it gives 40 dB -- the best reading the chip can
// produce -- so a counter below the floor is called unavailable instead.
const (
	// satelliteFloor is the smallest satellite counter the curve is defined
	// for. Below it the curve saturates at 0.91 dB, which is not a
	// measurement either.
	satelliteFloor = 3000
)

// CNRS reports the satellite core's carrier-to-noise ratio in dB, and whether
// the chip has a measurement to report.
func (demod *Demod) CNRS() (float64, bool, error) {
	raw, err := demod.CNS()
	if err != nil {
		return 0, false, err
	}
	cnr, measured := CNRFromRawS(uint32(raw))
	return cnr, measured, nil
}

// CNRT reports the terrestrial core's carrier-to-noise ratio in dB, and
// whether the chip has a measurement to report.
func (demod *Demod) CNRT() (float64, bool, error) {
	raw, err := demod.CNDatT()
	if err != nil {
		return 0, false, err
	}
	cnr, measured := CNRFromRawT(raw)
	return cnr, measured, nil
}

// CNRFromRawT and CNRFromRawS convert a raw counter into decibels and report
// whether the counter was a measurement, for callers holding a reading they did
// not take through this driver. px4_drv's character devices answer PTX_GET_CNR
// with the raw counter and leave the curve to userspace; boards it drives whose
// demodulator is not a TC90522 convert to this chip's scale through a table
// first, so these two curves cover all of them.
func CNRFromRawT(raw uint32) (float64, bool) {
	if raw == 0 {
		return 0, false
	}
	return float64(cnrHundredthsDBT(raw)) / 100, true
}

func CNRFromRawS(raw uint32) (float64, bool) {
	if raw < satelliteFloor {
		return 0, false
	}
	return float64(cnrHundredthsDBS(raw)) / 100, true
}
