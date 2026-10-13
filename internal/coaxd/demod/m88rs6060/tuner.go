// Copyright (c) Montage; TurboSight; 2026 Till0196
// SPDX-License-Identifier: GPL-2.0-or-later

package m88rs6060

import "time"

// selectMclk is rs6060_select_mclk: of the three candidate ADC clocks (96, 93,
// 99 MHz) it picks the one whose alias boundary sits furthest from the tuned
// frequency, unless the symbol rate (>= 46 MSym/s) forces 99 MHz.
func (demod *Demod) selectMclk(frequencyMHz, symbolRateKSs uint32) error {
	adcFreqMHz := [3]uint32{96, 93, 99}
	reg16List := [3]byte{96, 92, 100}

	reg16 := byte(96)
	demod.mclkKHz = 96000
	if symbolRateKSs >= 46000 {
		demod.mclkKHz = 99000
		reg16 = 100
	} else {
		var maxOffset uint32
		for i := range adcFreqMHz {
			offset := frequencyMHz % adcFreqMHz[i]
			if offset > adcFreqMHz[i]/2 {
				offset = adcFreqMHz[i] - offset
			}
			if offset > maxOffset {
				maxOffset = offset
				reg16 = reg16List[i]
				demod.mclkKHz = adcFreqMHz[i] * 1000
			}
		}
	}

	reg15, err := demod.tunerRead(0x15)
	if err != nil {
		return err
	}
	if err := demod.tunerWrite(0x15, reg15&^0x01); err != nil {
		return err
	}
	if err := demod.tunerWrite(0x16, reg16); err != nil {
		return err
	}
	if err := demod.tunerWrite(0x17, 0xc1); err != nil {
		return err
	}
	if err := demod.tunerWrite(0x17, 0x81); err != nil {
		return err
	}
	time.Sleep(5 * time.Millisecond)
	return nil
}

// setTSMclk is rs6060_set_ts_mclk: it programs the tuner's fractional TS
// output PLL divider for the target master clock, splitting it across 2, 3 or
// 4 taps by size and clamping each tap away from values the silicon rejects
// (16 means 0; below the floor -- 8 serial, 9 otherwise -- snaps up).
func (demod *Demod) setTSMclk(targetMclkKHz uint32) error {
	reg15, err := demod.tunerRead(0x15)
	if err != nil {
		return err
	}
	reg16, err := demod.tunerRead(0x16)
	if err != nil {
		return err
	}
	reg1D, err := demod.tunerRead(0x1d)
	if err != nil {
		return err
	}

	mclk := targetMclkKHz
	if demod.config.TSMode != TSModeSerial {
		var adjust uint32
		switch reg16 {
		case 92:
			adjust = 93
		case 100:
			adjust = 99
		default: // 96
			adjust = 96
		}
		mclk = mclk * adjust / 96
	}

	pllDivFb := uint32(reg15&0x01)<<8 + uint32(reg16) + 32
	div := 9000 * pllDivFb * 4 / mclk

	var n int
	var f [4]uint32
	serial := demod.config.TSMode == TSModeSerial
	floor := uint32(9)
	if serial {
		floor = 8
	}
	switch {
	case div <= 32:
		n = 2
		f[1] = div / 2
		f[2] = div - f[1]
	case !serial && div <= 48:
		n = 3
		f[0] = div / 3
		f[1] = (div - f[0]) / 2
		f[2] = div - f[0] - f[1]
	case div <= 64:
		n = 4
		f[0] = div / 4
		f[1] = (div - f[0]) / 3
		f[2] = (div - f[0] - f[1]) / 2
		f[3] = div - f[0] - f[1] - f[2]
	default:
		n = 4
		f = [4]uint32{16, 16, 16, 16}
	}
	for i := range f {
		switch {
		case f[i] == 16:
			f[i] = 0
		case f[i] < floor && f[i] != 0:
			f[i] = floor
		}
	}

	reg1D &^= 0x03
	reg1D |= byte(n - 1)
	reg1D |= 0x80
	reg1E := byte((f[3]<<4 + f[2]) & 0xff)
	reg1F := byte((f[1]<<4 + f[0]) & 0xff)

	if err := demod.tunerWrite(0x1d, reg1D); err != nil {
		return err
	}
	if err := demod.tunerWrite(0x1e, reg1E); err != nil {
		return err
	}
	if err := demod.tunerWrite(0x1f, reg1F); err != nil {
		return err
	}
	time.Sleep(time.Millisecond)
	return nil
}

// setPLLFreq is rs6060_set_pll_freq: LO divider and modulus values come from
// the target frequency's band, then the two fractional-N feedback dividers the
// dual synthesizer needs.
func (demod *Demod) setPLLFreq(tunerFreqMHz uint32) error {
	fcryKHz := demod.config.ClockHz / 1000

	var div1m, div1p5m uint32
	switch fcryKHz {
	case 24000:
		div1m, div1p5m = 16, 8
		if err := demod.tunerWrite(0x41, 0x8a); err != nil {
			return err
		}
	default: // 27000, the fallback
		div1m, div1p5m = 19, 10
		if err := demod.tunerWrite(0x41, 0x82); err != nil {
			return err
		}
	}

	var loDiv1, loMod1, refDiv1, loDiv2, loMod2, refDiv2 uint32
	lodivEnOptDiv2 := false
	switch {
	case tunerFreqMHz >= 1550:
		loDiv1, loMod1, refDiv1 = 2, 0, div1m
		loDiv2, loMod2, refDiv2 = 2, 0, div1m
	case tunerFreqMHz >= 1380:
		loDiv1, loMod1, refDiv1 = 3, 16, div1p5m
		loDiv2, loMod2, refDiv2 = 2, 0, div1m
	case tunerFreqMHz >= 1070:
		loDiv1, loMod1, refDiv1 = 3, 16, div1p5m
		loDiv2, loMod2, refDiv2 = 3, 16, div1p5m
	case tunerFreqMHz >= 775:
		// 775-1000 and 1000-1070 have identical dividers; merged here.
		loDiv1, loMod1, refDiv1 = 4, 64, div1m
		loDiv2, loMod2, refDiv2 = 4, 64, div1m
	case tunerFreqMHz >= 700:
		loDiv1, loMod1, refDiv1 = 6, 48, div1p5m
		loDiv2, loMod2, refDiv2 = 4, 64, div1m
	case tunerFreqMHz >= 520:
		loDiv1, loMod1, refDiv1 = 6, 48, div1p5m
		loDiv2, loMod2, refDiv2 = 6, 48, div1p5m
	case tunerFreqMHz >= 375:
		loDiv1, loMod1, refDiv1 = 8, 96, div1m
		loDiv2, loMod2, refDiv2 = 8, 96, div1m
	default:
		loDiv1, loMod1, refDiv1 = 12, 80, div1m
		loDiv2, loMod2, refDiv2 = 12, 80, div1m
		lodivEnOptDiv2 = true
	}

	nDiv1 := ((tunerFreqMHz*1000*loDiv1)*(refDiv1+8)/fcryKHz - 1024) / 2
	nDiv2 := ((tunerFreqMHz*1000*loDiv2)*(refDiv2+8)/fcryKHz - 1024) / 2

	reg27 := byte((((nDiv1 >> 8) & 0x0f) + loMod1) & 0x7f)
	if err := demod.tunerWrite(0x27, reg27); err != nil {
		return err
	}
	if err := demod.tunerWrite(0x28, byte(nDiv1&0xff)); err != nil {
		return err
	}
	reg29 := byte((((nDiv2 >> 8) & 0x0f) + loMod2) & 0x7f)
	if err := demod.tunerWrite(0x29, reg29); err != nil {
		return err
	}
	if err := demod.tunerWrite(0x2a, byte(nDiv2&0xff)); err != nil {
		return err
	}

	if err := demod.tunerWrite(0x36, byte(refDiv1&0x1f)); err != nil {
		return err
	}
	if err := demod.tunerWrite(0x39, byte(refDiv2)); err != nil {
		return err
	}

	tmp, err := demod.tunerRead(0x3d)
	if err != nil {
		return err
	}
	if lodivEnOptDiv2 {
		tmp |= 0x80
	} else {
		tmp &^= 0x80
	}
	if err := demod.tunerWrite(0x3d, tmp); err != nil {
		return err
	}

	if refDiv1 == 19 {
		return demod.tunerWrite(0x2c, 0x02)
	}
	return demod.tunerWrite(0x2c, 0x00)
}

// setBB is rs6060_set_bb: the baseband filter's 3 dB corner tracks the symbol
// rate, plus (at low symbol rates) the same LPF offset the tuner PLL was
// retuned by, clamped to the filter's 6-43 MHz usable range.
func (demod *Demod) setBB(symbolRateKSs uint32, lpfOffsetKHz int32) error {
	f3dB := int32(symbolRateKSs*9/14+2000) + lpfOffsetKHz
	switch {
	case f3dB < 6000:
		f3dB = 6000
	case f3dB > 43000:
		f3dB = 43000
	}
	return demod.tunerWrite(0x40, byte(f3dB/1000))
}
