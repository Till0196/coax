// Copyright (c) 2015 Luis Alves; Rafael Micro; TurboSight; 2026 Till0196
// SPDX-License-Identifier: GPL-2.0-or-later

package r848

import "time"

// Calibration flavours, R848_Cal_Flag. Each sets up a different signal path.
type calFlag int

const (
	calIMR calFlag = iota
	calIMRLNA
	calTFLNA
	calLPFLNA
	calTF
	calLPF
)

// calibrationPrepare is R848_Cal_Prepare: it forces the gain stages, ring
// oscillator path and ADC into a known state, so a measurement is repeatable.
func (tuner *Tuner) calibrationPrepare(flag calFlag) error {
	// The defaults, before each flavour's overrides.
	rfBufOut, rfBufPower := byte(0x20), byte(0x04)
	tfCal, lnaGain, mixerAmpGain := byte(0x00), byte(0x9f), byte(0x08)
	const lnaPower, mixerBufferGain, filter6dB = 0x80, 0x10, 0x08

	switch flag {
	case calIMRLNA:
		rfBufOut, rfBufPower, tfCal, mixerAmpGain = 0x00, 0x00, 0x60, 0x00
	case calTFLNA:
		rfBufOut, rfBufPower, tfCal, lnaGain, mixerAmpGain = 0x00, 0x00, 0x60, 0x80, 0x00
	case calLPFLNA:
		rfBufOut, rfBufPower, tfCal, lnaGain, mixerAmpGain = 0x00, 0x00, 0x20, 0x80, 0x00
	case calTF:
		mixerAmpGain = 0x00
	}

	for _, step := range []struct{ register, keep, value byte }{
		{0x0c, 0xdf, rfBufOut},         // ring from RF buffer output
		{0x09, 0xfb, rfBufPower},       // RF buffer power
		{0x08, 0x7f, lnaPower},         // LNA power
		{0x0e, 0x9f, tfCal},            // tracking filter calibration path
		{0x0d, 0x60, lnaGain},          // LNA gain
		{0x0f, 0xe0, mixerAmpGain},     // mixer amplifier gain
		{0x22, 0xe0, mixerBufferGain},  // mixer buffer gain
		{0x26, 0xf7, filter6dB | 0x80}, // filter +6 dB, null detector off
		{0x28, 0xff, 0x08},             // null detector 710 off
		{0x0b, 0xfc, 0x00},             // IQ cap = 0
		{0x16, 0xff, 0x01},             // force far-end mode
		{0x12, 0xef, 0x00},             // ring PLL power on
		{0x12, 0xf0, 0x0f},             // LPF filter code = 15
		{0x13, 0x00, 0x60},             // HPF narrowest, LPF coarse 6M, no 1.7M
		{0x0f, 0x7f, 0x00},             // ADC power on
		{0x12, 0x7f, 0x00},             // VGA power on
		{0x0b, 0xf7, 0x08},             // large code mode gain
		{0x09, 0xfd, 0x02},             // VGA code mode
		{0x14, 0xf0, 0x0b},             // VGA gain = 26.5 dB
		{0x25, 0xf0, 0x02},             // detectors on, det2 cap = IF det
	} {
		if err := tuner.setBits(step.register, step.keep, step.value); err != nil {
			return err
		}
	}
	return nil
}

// crystalCheck is R848_Xtal_Check: it sweeps the crystal drive from lowest to
// highest and reports the first level at which the PLL locks onto the VCO band
// the fixed initial divider should produce.
func (tuner *Tuner) crystalCheck() (byte, error) {
	for _, step := range []struct{ register, keep, value byte }{
		{0x16, 0xff, 0x01}, // TF force sharp mode, for a stable read
		{0x26, 0xff, 0x80}, // null detector off, likewise
		{0x28, 0xff, 0x08},
		{0x17, 0x9f, 0x60}, // crystal power lowest
		{0x1b, 0xfe, 0x00}, // crystal Gm small
		{0x17, 0xe7, 0x00}, // PLL autotune 128 kHz
		{0x1b, 0x80, 0x40}, // manual initial value, 30 pF cap
		{0x1b, 0xbf, 0x00}, // back to automatic
	} {
		if err := tuner.setBits(step.register, step.keep, step.value); err != nil {
			return 0, err
		}
	}
	time.Sleep(10 * time.Millisecond)
	if err := tuner.setBits(0x17, 0x9f, 0x60); err != nil {
		return 0, err
	}
	if err := tuner.setBits(0x1b, 0xfe, 0x00); err != nil {
		return 0, err
	}

	// The initial divider (N=59.6667, Div=16) puts the VCO at 2768 MHz, which
	// lands in a different band number depending on the crystal.
	lowLimit, highLimit := byte(42), byte(48)
	if tuner.config.XtalHz == 16_000_000 {
		lowLimit, highLimit = 5, 11
	}

	for level := byte(0); level < xtalCheckSize; level++ {
		if level <= xtalSmallHighest {
			if err := tuner.setBits(0x17, 0x9f, (xtalSmallHighest-level)<<5); err != nil {
				return 0, err
			}
			if err := tuner.setBits(0x1b, 0xfe, 0x00); err != nil { // small Gm
				return 0, err
			}
		} else {
			if err := tuner.setBits(0x17, 0x9f, 0x00); err != nil {
				return 0, err
			}
			if err := tuner.setBits(0x1b, 0xfe, 0x01); err != nil { // large Gm
				return 0, err
			}
		}
		time.Sleep(50 * time.Millisecond)

		data, err := tuner.read(3)
		if err != nil {
			return 0, err
		}
		if data[2]&0x40 != 0 {
			band := data[2] & 0x3f
			if band >= lowLimit && band <= highLimit {
				return level, nil
			}
		}
	}
	// Nothing locked in the expected band: settle for the highest drive level
	// rather than failing.
	return xtalCheckSize - 1, nil
}

// trackingFilterCheck drives the ring oscillator through the ultra-low-band
// tracking filter at two settings; the difference tells whether the board's
// ultra-low inductor is a ferrite bead or a 270 nH coil, which picks a
// different tracking-filter table.
func (tuner *Tuner) trackingFilterCheck() error {
	divNumRing := uint32(2)
	if xtalKHz == 16000 {
		divNumRing = 11
	}
	ringVCO := (16 + divNumRing) * 8 * xtalKHz
	ringFreq := ringVCO / 48

	if err := tuner.calibrationPrepare(calTFLNA); err != nil {
		return err
	}
	if err := tuner.setBits(0x27, 0x03, 0x80|byte(divNumRing)<<2); err != nil {
		return err
	}
	if err := tuner.setBits(0x12, 0xef, 0x00); err != nil { // ring PLL on
		return err
	}
	if err := tuner.setBits(0x25, 0x7f, 0x00); err != nil { // detector source: mixer buffer
		return err
	}
	// Out divide 8, RF polyfilter low band, minimum low-power mode.
	vcoBand := byte(0x00)
	if ringVCO < 3200000 {
		vcoBand = 0x20
	}
	if err := tuner.setBits(0x21, 0x00, 0x8b&^0x20|vcoBand); err != nil {
		return err
	}

	if err := tuner.setMux(ringFreq+5000, ringFreq, stdSize); err != nil {
		return err
	}
	if err := tuner.setPLL(ringFreq+5000, stdSize); err != nil {
		return err
	}

	for _, step := range []struct{ register, keep, value byte }{
		{0x08, 0x80, 0x1f}, // LNA tracking filter (1,15), for VGA training
		{0x0e, 0xdf, 0x00}, // Q control off
		{0x0e, 0xfb, 0x00}, // filter compensation off
		{0x0c, 0xbf, 0x00}, // bypass the low-pass filter
		{0x14, 0xf0, 0x00}, // VGA gain -12 dB
		{0x09, 0xff, 0x02}, // VGA code mode
		{0x0b, 0xf7, 0x00}, // VGA 6 dB normal
		{0x12, 0x7f, 0x00}, // VGA power on
		{0x0f, 0x7f, 0x00}, // ADC on
	} {
		if err := tuner.setBits(step.register, step.keep, step.value); err != nil {
			return err
		}
	}

	if _, err := tuner.rampVGA(0, 16, 40); err != nil {
		return err
	}

	// Now with the tracking filter at (0,0): a bead passes more of the ring
	// signal through than a 270 nH coil does.
	if err := tuner.setBits(0x08, 0x80, 0x00); err != nil {
		return err
	}
	time.Sleep(10 * time.Millisecond)
	reading, err := tuner.readIMR()
	if err != nil {
		return err
	}
	if reading > 36 {
		tuner.config.DetectTfType = ulUsingBead
	} else {
		tuner.config.DetectTfType = ulUsing270nH
	}
	return nil
}

// rampVGA raises the VGA gain from start until the image-rejection ADC reads
// above threshold, so later measurements see a large enough signal. It returns
// the last reading.
func (tuner *Tuner) rampVGA(start, limit, threshold byte) (byte, error) {
	var reading byte
	for count := start; count < limit; count++ {
		if err := tuner.write(0x14, *tuner.shadow(0x14)&0xf0+count); err != nil {
			return 0, err
		}
		time.Sleep(10 * time.Millisecond)
		value, err := tuner.readIMR()
		if err != nil {
			return 0, err
		}
		reading = value
		if reading > threshold {
			break
		}
	}
	return reading, nil
}

// filterCalibrate is R848_Filt_Cal_ADC: it sweeps the channel filter's coarse
// bandwidth then its fine code, and reports the first fine code whose response
// has dropped by gap counts — the code whose corner sits at the wanted IF.
func (tuner *Tuner) filterCalibrate(ifKHz uint32, bandwidth, gap byte) (byte, error) {
	const ringFreqKHz = 72000

	if err := tuner.initRegisters(stdAtsc); err != nil {
		return 0, err
	}
	if err := tuner.calibrationPrepare(calLPF); err != nil {
		return 0, err
	}
	for _, step := range []struct{ register, keep, value byte }{
		{0x27, 0xc3, 0x2c}, // ring divider number
		{0x12, 0xef, 0x00}, // ring PLL power on
		{0x25, 0x7f, 0x00}, // detector source
		{0x27, 0x3f, 0x80}, // ring pre-divider
		{0x21, 0x00, 0x8b}, // out divide 8, RF polyfilter low, min low power
	} {
		if err := tuner.setBits(step.register, step.keep, step.value); err != nil {
			return 0, err
		}
	}

	if err := tuner.setMux(ringFreqKHz+ifKHz, ringFreqKHz, stdSize); err != nil {
		return 0, err
	}
	if err := tuner.setPLL(ringFreqKHz+ifKHz, stdSize); err != nil {
		return 0, err
	}

	for _, step := range []struct{ register, keep, value byte }{
		{0x08, 0x80, 0x00}, // LNA tracking filter unused at 72 MHz
		{0x0f, 0x7f, 0x00}, // ADC on
		{0x12, 0x7f, 0x00}, // VGA power on
		{0x0b, 0xf7, 0x00}, // VGA 6 dB normal
		{0x14, 0xf0, 0x00}, // VGA gain -12 dB
		{0x26, 0x9f, 0x00}, // filter compensation off
		{0x13, 0x00, 0x00}, // 8 MHz, HPF narrowest, no 1.7 M
		{0x12, 0xf0, 0x00}, // filter code 0
	} {
		if err := tuner.setBits(step.register, step.keep, step.value); err != nil {
			return 0, err
		}
	}
	if _, err := tuner.rampVGA(0, 16, 40); err != nil {
		return 0, err
	}

	// Coarse bandwidth: start at 7 MHz for a 6 MHz channel, 8 MHz otherwise;
	// take the first width whose response actually rolls off.
	start := 0
	if bandwidth == 0x60 {
		start = 1
	}
	for i := start; i < 3; i++ {
		coarse := []byte{0x00, 0x40, 0x60}[i] // 8M, 7M, 6M
		if err := tuner.setBits(0x13, 0x00, coarse); err != nil {
			return 0, err
		}
		if err := tuner.setBits(0x12, 0xf0, 0); err != nil {
			return 0, err
		}
		time.Sleep(10 * time.Millisecond)
		atCode0, err := tuner.readIMR()
		if err != nil {
			return 0, err
		}
		if err := tuner.setBits(0x12, 0xf0, 13); err != nil {
			return 0, err
		}
		time.Sleep(10 * time.Millisecond)
		atCode13, err := tuner.readIMR()
		if err != nil {
			return 0, err
		}
		if atCode0 > atCode13+8 {
			break
		}
	}

	// Fine code: the first one whose reading has fallen gap counts below the
	// code-0 reference.
	var reference byte
	for code := byte(0); code < 16; code++ {
		if err := tuner.setBits(0x12, 0xf0, code); err != nil {
			return 0, err
		}
		time.Sleep(10 * time.Millisecond)
		reading, err := tuner.readIMR()
		if err != nil {
			return 0, err
		}
		if code == 0 {
			reference = reading
		}
		if reading+gap < reference {
			return code, nil
		}
	}
	// No code rolled off far enough: take the widest.
	return 15, nil
}
