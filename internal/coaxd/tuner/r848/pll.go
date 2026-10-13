// Copyright (c) 2015 Luis Alves; Rafael Micro; TurboSight; 2026 Till0196
// SPDX-License-Identifier: GPL-2.0-or-later

package r848

import "time"

// setPLL is r848_set_pll: it picks a mixer divider that lands the VCO in range,
// splits the divide ratio into integer and sigma-delta fractional parts, then
// confirms the lock, raising the VCO current and finally the crystal drive if it
// did not lock.
func (tuner *Tuner) setPLL(loKHz uint32, system standard) error {
	// The tracking-filter and null-detector settings are forced to a stable
	// configuration for the lock, then put back.
	savedRFFlag := *tuner.shadow(0x16) & 0x01
	savedPulseFlag := *tuner.shadow(0x26) & 0x80
	savedShortPulse := *tuner.shadow(0x28) & 0x08

	if err := tuner.setBits(0x16, 0xff, 0x01); err != nil { // TF force sharp mode
		return err
	}
	if err := tuner.setBits(0x26, 0xff, 0x80); err != nil { // null detector off
		return err
	}
	if err := tuner.setBits(0x28, 0xff, 0x08); err != nil {
		return err
	}

	if err := tuner.setBits(0x19, 0x8f, 0x00); err != nil { // charge pump 0.7 mA
		return err
	}
	if err := tuner.setBits(0x14, 0x3f, 0x40); err != nil { // divider current 150 uA
		return err
	}
	cpDouble := byte(0x00)
	if system != stdDvbS && loKHz >= 865000 {
		cpDouble = 0x80
	}
	if err := tuner.setBits(0x1c, 0x7f, cpDouble); err != nil {
		return err
	}
	if err := tuner.setBits(0x1a, 0x1f, 0x40); err != nil { // VCO current = 2
		return err
	}
	if err := tuner.setBits(0x15, 0x7f, 0x00); err != nil { // charge pump offset 0
		return err
	}

	// Crystal drive: the sweep Initialize ran picks the level, except that a high
	// LO on a low-drive crystal is forced up one step.
	var xtalPow0, xtalGm byte
	switch {
	case tuner.config.XtalPower < xtalSmallHigh && loKHz > 64000+8500:
		xtalPow0, xtalGm = 0x20, 0x00
	case tuner.config.XtalPower <= xtalSmallHighest:
		xtalPow0, xtalGm = (xtalSmallHighest-tuner.config.XtalPower)<<5, 0x00
	default:
		xtalPow0, xtalGm = 0x00, 0x01
	}
	if err := tuner.setBits(0x1b, 0xfe, xtalGm); err != nil {
		return err
	}
	if err := tuner.setBits(0x17, 0x9f, xtalPow0); err != nil {
		return err
	}
	if err := tuner.setBits(0x17, 0x7f, 0x00); err != nil {
		return err
	}
	if err := tuner.setBits(0x27, 0xfd, 0x00); err != nil { // IQ generator on
		return err
	}
	if err := tuner.setBits(0x23, 0xcf, 0x00); err != nil { // minimum currents
		return err
	}
	if err := tuner.setBits(0x17, 0xe7, 0x00); err != nil { // autotune 128 kHz (fast)
		return err
	}

	// Mixer divider: the smallest power of two that puts the VCO in range.
	const vcoMin, vcoMax = 2410000, 2410000 * 2
	mixDiv, divNum := uint32(2), byte(0)
	for mixDiv <= 64 {
		if loKHz*mixDiv >= vcoMin && loKHz*mixDiv < vcoMax {
			for buf := mixDiv; buf > 2; buf >>= 1 {
				divNum++
			}
			break
		}
		mixDiv <<= 1
	}

	if err := tuner.setBits(0x1b, 0xe7, 0x00); err != nil { // sigma-delta short
		return err
	}

	// Crystal divider: halved unless the LO's 8 MHz step count is odd, or this is
	// a calibration tune, which always uses the undivided reference.
	pllRefKHz := uint32(xtalKHz)
	xtalDivBy2 := true
	if system == stdSize || (loKHz/1000/8)%2 == 1 {
		xtalDivBy2 = false
	}
	if xtalDivBy2 {
		*tuner.shadow(0x18) |= 0x04
		pllRefKHz = xtalKHz / 2
	} else {
		*tuner.shadow(0x18) &^= 0x04
	}
	if err := tuner.write(0x18, *tuner.shadow(0x18)); err != nil {
		return err
	}
	if err := tuner.setBits(0x18, 0x1f, divNum<<5); err != nil {
		return err
	}

	vcoFreq := loKHz * mixDiv
	nInt := uint16(vcoFreq / 2 / pllRefKHz)
	vcoFrac := vcoFreq - 2*pllRefKHz*uint32(nInt)

	// Keep the fractional part away from the boundary spurs.
	switch {
	case vcoFrac < pllRefKHz/64:
		vcoFrac = 0
	case vcoFrac > pllRefKHz*127/64:
		vcoFrac = 0
		nInt++
	case vcoFrac > pllRefKHz*127/128 && vcoFrac < pllRefKHz:
		vcoFrac = pllRefKHz * 127 / 128
	case vcoFrac > pllRefKHz && vcoFrac < pllRefKHz*129/128:
		vcoFrac = pllRefKHz * 129 / 128
	}

	// The integer part splits across two registers: a coarse count and a 0-3
	// remainder.
	ni := byte((nInt - 13) / 4)
	si := byte(nInt - 4*uint16(ni) - 13)
	if err := tuner.setBits(0x14, 0xcf, si<<4); err != nil {
		return err
	}
	if err := tuner.setBits(0x1c, 0x80, ni); err != nil {
		return err
	}

	// Power the sigma-delta modulator down when there is no fractional part.
	sdmPower := byte(0x00)
	if vcoFrac == 0 {
		sdmPower = 0x80
	}
	if err := tuner.setBits(0x1b, 0x7f, sdmPower); err != nil {
		return err
	}

	// Successive-approximation of the fractional part into a 16-bit word.
	var sdm uint16
	for nsdm := uint32(2); vcoFrac > 1; nsdm <<= 1 {
		if vcoFrac > 2*pllRefKHz/nsdm {
			sdm += uint16(32768 / (nsdm / 2))
			vcoFrac -= 2 * pllRefKHz / nsdm
			if nsdm >= 0x8000 {
				break
			}
		}
	}
	*tuner.shadow(0x1e) = byte(sdm >> 8)
	if err := tuner.write(0x1e, *tuner.shadow(0x1e)); err != nil {
		return err
	}
	*tuner.shadow(0x1d) = byte(sdm)
	if err := tuner.write(0x1d, *tuner.shadow(0x1d)); err != nil {
		return err
	}

	if xtalDivBy2 {
		time.Sleep(20 * time.Millisecond)
	} else {
		time.Sleep(10 * time.Millisecond)
	}

	// Three tries, raising the VCO current each time it has not locked.
	locked := false
	for trial := byte(0); trial < 3; trial++ {
		data, err := tuner.read(3)
		if err != nil {
			return err
		}
		if data[2]&0x40 != 0 {
			locked = true
			break
		}
		if err := tuner.setBits(0x1a, 0x1f, (2-trial)<<5); err != nil {
			return err
		}
	}
	// Still unlocked on a low-drive crystal: raise the crystal drive too.
	if !locked && tuner.config.XtalPower <= xtalSmallHighest {
		if err := tuner.setBits(0x1b, 0xfe, 0x01); err != nil {
			return err
		}
	}

	if err := tuner.setBits(0x17, 0xe7, 0x10); err != nil { // autotune 8 kHz (slow)
		return err
	}
	if err := tuner.setBits(0x16, 0xfe, savedRFFlag); err != nil {
		return err
	}
	if err := tuner.setBits(0x26, 0x7f, savedPulseFlag); err != nil {
		return err
	}
	return tuner.setBits(0x28, 0xf7, savedShortPulse)
}

// setMux sets the frequency-dependent LNA band, polyfilter, low-pass filter and
// stored image-rejection correction. It has to run before setPLL.
func (tuner *Tuner) setMux(loKHz, rfKHz uint32, system standard) error {
	info := freqSelect(loKHz, rfKHz, system)

	if err := tuner.setBits(0x0d, 0x9f, info.lnaBand); err != nil {
		return err
	}
	if err := tuner.setBits(0x21, 0x3f, info.rfPoly); err != nil {
		return err
	}
	if err := tuner.setBits(0x09, 0x07, info.lpfCap<<3); err != nil {
		return err
	}
	if err := tuner.setBits(0x0a, 0xe0, info.lpfNotch); err != nil {
		return err
	}

	// Apply the image-rejection point measured for this part of the band.
	point := tuner.imr[info.imrMem]
	if err := tuner.setBits(0x10, 0xc0, point.gainX&0x3f); err != nil {
		return err
	}
	if err := tuner.setBits(0x11, 0xc0, point.phaseY&0x3f); err != nil {
		return err
	}
	return tuner.setBits(0x0b, 0xfc, point.iqcap&0x03)
}

// setTrackingFilter interpolates the tracking filter's register value between
// the two table steps bracketing the frequency. The result belongs in register
// 0x08, not the vendor's 0x0B: 0x0B would leave the tracking filter unset and
// clobber R11's filter-current and IQ-cap bits. trackingFilterCheck sets the
// same shadow register and writes it to 0x08.
func (tuner *Tuner) setTrackingFilter(freqKHz uint32, tfType byte) error {
	var value byte

	switch {
	case freqKHz < lnaLowLowest[tfBead]:
		value = interpolateTF(freqKHz, tfFreqLowest[tfType], tfResultLowest[tfType], 3, 0x10)
	case freqKHz < lnaMidLow[tfBead]:
		value = interpolateTF(freqKHz, tfFreqLow[tfType], tfResultLow[tfType], 1, 0x30)
	case freqKHz < lnaHighMid[tfBead]:
		value = interpolatePlain(freqKHz, tfFreqMid[tfType], tfResultMid[tfType])
	default:
		value = interpolatePlain(freqKHz, tfFreqHigh[tfType], tfResultHigh[tfType])
	}
	return tuner.setBits(0x08, 0x80, value)
}

// interpolateTF is the lowest/low bands' interpolation: it unpacks bit 6 of the
// table entries as a multiple of bit 4 before interpolating and repacks it
// afterwards. weight is how many units bit 6 stands for (3 in the lowest band,
// 1 in the low band); carry is added back when the result overflows into bit 6.
func interpolateTF(freqKHz uint32, freqs [tfBandSteps]uint32, results [tfBandSteps]byte, weight, carry byte) byte {
	index := stepIndex(freqKHz, freqs)
	if index == 0 {
		return results[0]
	}
	if index == tfBandSteps {
		return results[tfBandSteps-1]
	}
	unpack := func(entry byte) byte { return ((entry&0x40)>>2)*weight + entry&0x3f }
	low, high := unpack(results[index-1]), unpack(results[index])
	ratio := (freqs[index-1] - freqKHz) * 100 / (freqs[index-1] - freqs[index])
	value := low + byte(uint32(high-low)*ratio/100)
	if value >= 0x40 {
		value += carry
	}
	return value
}

// interpolatePlain is the mid/high bands' interpolation: table entries as-is.
func interpolatePlain(freqKHz uint32, freqs [tfBandSteps]uint32, results [tfBandSteps]byte) byte {
	index := stepIndex(freqKHz, freqs)
	if index == 0 {
		return results[0]
	}
	if index == tfBandSteps {
		return results[tfBandSteps-1]
	}
	ratio := (freqs[index-1] - freqKHz) * 100 / (freqs[index-1] - freqs[index])
	return results[index-1] + byte(uint32(results[index]-results[index-1])*ratio/100)
}

func stepIndex(freqKHz uint32, freqs [tfBandSteps]uint32) int {
	index := 0
	for index < tfBandSteps && freqKHz < freqs[index] {
		index++
	}
	return index
}
