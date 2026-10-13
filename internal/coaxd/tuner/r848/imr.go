// Copyright (c) 2015 Luis Alves; Rafael Micro; TurboSight; 2026 Till0196
// SPDX-License-Identifier: GPL-2.0-or-later

package r848

// Image rejection calibration: the internal ring oscillator is driven into the
// mixer and the (gain, phase) trim plane searched for the pair that minimises
// the image the ADC reports.
//
// The gain and phase registers are not plain magnitudes: bit 5 selects the I or
// Q path and bits 4:0 are a magnitude on that path, so "one step left of zero"
// crosses to the other path rather than underflowing -- hence the |0x20 and
// &0xDF fiddling throughout.

// compareCorrelation is R848_CompreCor: it bubbles the smallest of three
// measurements into slot 0.
func compareCorrelation(points *[3]imrPoint) {
	// The i=1 pass compares points[0] with itself, a no-op kept to match the
	// reference C loop.
	for i := 3; i > 0; i-- {
		if points[0].value > points[i-1].value {
			points[0], points[i-1] = points[i-1], points[0]
		}
	}
}

// iqTree is R848_IQ_Tree: it measures the given point and its two neighbours
// along one axis, stepping across the I/Q path boundary rather than through it.
func (tuner *Tuner) iqTree(fixed, fluctuating, fixedRegister byte, points *[3]imrPoint) error {
	otherRegister := byte(0x10)
	if fixedRegister == 0x10 {
		otherRegister = 0x11
	}

	for i := range 3 {
		if err := tuner.write(fixedRegister, fixed); err != nil {
			return err
		}
		if err := tuner.write(otherRegister, fluctuating); err != nil {
			return err
		}
		value, err := tuner.readIMR()
		if err != nil {
			return err
		}
		points[i].value = value
		if fixedRegister == 0x10 {
			points[i].gainX, points[i].phaseY = fixed, fluctuating
		} else {
			points[i].phaseY, points[i].gainX = fixed, fluctuating
		}

		switch i {
		case 0: // try the right-side point
			fluctuating++
		case 1: // try the left-side point
			if fluctuating&0x1f == 1 {
				// One step left of magnitude 1 is magnitude 1 on the other
				// path, not magnitude 0.
				if fluctuating&0x20 != 0 {
					fluctuating = fluctuating&0xc0 | 0x01
				} else {
					fluctuating = fluctuating&0xc0 | 0x21
				}
			} else {
				fluctuating -= 2
			}
		}
	}
	return nil
}

// columnSearch is the body shared by R848_Section and R848_F_IMR: it measures
// the columns left of, at and right of the point, and keeps the best.
func (tuner *Tuner) columnSearch(point imrPoint) (imrPoint, error) {
	var best [3]imrPoint

	for column := range 3 {
		var trio [3]imrPoint
		switch column {
		case 0: // one step left, crossing paths at magnitude 0
			if point.gainX&0x1f == 0x00 {
				trio[0].gainX = point.gainX&0xdf + 1
			} else {
				trio[0].gainX = point.gainX - 1
			}
		case 1:
			trio[0].gainX = point.gainX
		case 2: // one step right, likewise
			if point.gainX&0x1f == 0x00 {
				trio[0].gainX = (point.gainX | 0x20) + 1
			} else {
				trio[0].gainX = point.gainX + 1
			}
		}
		trio[0].phaseY = point.phaseY
		if err := tuner.iqTree(trio[0].gainX, trio[0].phaseY, 0x10, &trio); err != nil {
			return point, err
		}
		compareCorrelation(&trio)
		best[column] = trio[0]
	}
	compareCorrelation(&best)
	return best[0], nil
}

// section is R848_Section. It returns the best point columnSearch found rather
// than the vendor's unconditional failure, which would make the first,
// non-refining image-rejection pass — and so initialisation — impossible.
func (tuner *Tuner) section(point imrPoint) (imrPoint, error) {
	return tuner.columnSearch(point)
}

// imrCross probes the nine points one and two steps out from the origin along
// both axes and both paths, and reports which axis to search along plus the
// three points to seed that search with.
func (tuner *Tuner) imrCross() (points [3]imrPoint, alongGain bool, err error) {
	gainOrigin := *tuner.shadow(0x10) & 0xc0
	phaseOrigin := *tuner.shadow(0x11) & 0xc0

	var cross [9]imrPoint
	for i := range cross {
		switch i {
		case 0:
			cross[i].gainX, cross[i].phaseY = gainOrigin, phaseOrigin
		case 1: // phase, Q path, one step
			cross[i].gainX, cross[i].phaseY = gainOrigin, phaseOrigin+1
		case 2: // phase, I path, one step
			cross[i].gainX, cross[i].phaseY = gainOrigin, (phaseOrigin|0x20)+1
		case 3: // gain, Q path, one step
			cross[i].gainX, cross[i].phaseY = gainOrigin+1, phaseOrigin
		case 4: // gain, I path, one step
			cross[i].gainX, cross[i].phaseY = (gainOrigin|0x20)+1, phaseOrigin
		case 5:
			cross[i].gainX, cross[i].phaseY = gainOrigin, phaseOrigin+2
		case 6:
			cross[i].gainX, cross[i].phaseY = gainOrigin, (phaseOrigin|0x20)+2
		case 7:
			cross[i].gainX, cross[i].phaseY = gainOrigin+2, phaseOrigin
		case 8:
			cross[i].gainX, cross[i].phaseY = (gainOrigin|0x20)+2, phaseOrigin
		}
		if err := tuner.write(0x10, cross[i].gainX); err != nil {
			return points, false, err
		}
		if err := tuner.write(0x11, cross[i].phaseY); err != nil {
			return points, false, err
		}
		value, err := tuner.readIMR()
		if err != nil {
			return points, false, err
		}
		cross[i].value = value
	}

	best := cross[0]
	for _, candidate := range cross[1:] {
		if candidate.value < best.value {
			best = candidate
		}
	}

	// Whichever axis the minimum moved along is the one worth searching.
	switch {
	case best.phaseY&0x3f == 0x01 || best.phaseY&0x3f == 0x02:
		return [3]imrPoint{cross[0], cross[1], cross[5]}, false, nil
	case best.phaseY&0x3f == 0x21 || best.phaseY&0x3f == 0x22:
		return [3]imrPoint{cross[0], cross[2], cross[6]}, false, nil
	case best.gainX&0x3f == 0x01 || best.gainX&0x3f == 0x02:
		return [3]imrPoint{cross[0], cross[3], cross[7]}, true, nil
	case best.gainX&0x3f == 0x21 || best.gainX&0x3f == 0x22:
		return [3]imrPoint{cross[0], cross[4], cross[8]}, true, nil
	default: // the origin already was the minimum
		return [3]imrPoint{cross[0], cross[3], cross[4]}, true, nil
	}
}

// compareStep is R848_CompreStep: it walks one axis a step at a time while the
// measurement improves, stopping once it has clearly got worse.
func (tuner *Tuner) compareStep(points *[3]imrPoint, axis byte) error {
	step := points[0]
	for step.gainX&0x1f < imrTrial && step.phaseY&0x1f < imrTrial {
		if axis == 0x10 {
			step.gainX++
		} else {
			step.phaseY++
		}
		if err := tuner.write(0x10, step.gainX); err != nil {
			return err
		}
		if err := tuner.write(0x11, step.phaseY); err != nil {
			return err
		}
		value, err := tuner.readIMR()
		if err != nil {
			return err
		}
		step.value = value

		if step.value <= points[0].value {
			points[0].gainX, points[0].phaseY, points[0].value = step.gainX, step.phaseY, step.value
		} else if step.value-2 > points[0].value {
			break
		}
	}
	return nil
}

// imrIQCap is R848_IMR_Iqcap: with gain and phase settled, it tries the three
// IQ capacitor settings and keeps the best.
func (tuner *Tuner) imrIQCap(point *imrPoint) error {
	if err := tuner.write(0x10, point.gainX); err != nil {
		return err
	}
	if err := tuner.write(0x11, point.phaseY); err != nil {
		return err
	}
	for cap := byte(0); cap < 3; cap++ {
		if err := tuner.setBits(0x0b, 0xfc, cap); err != nil {
			return err
		}
		value, err := tuner.readIMR()
		if err != nil {
			return err
		}
		if value < point.value {
			point.value, point.iqcap = value, cap
		}
	}
	return nil
}

// searchIQ is R848_IQ: the full two-axis search from the origin.
func (tuner *Tuner) searchIQ() (imrPoint, error) {
	var point imrPoint

	if _, err := tuner.rampVGA(11, 16, 40); err != nil {
		return point, err
	}

	points, alongGain, err := tuner.imrCross()
	if err != nil {
		return point, err
	}

	// Search the axis the cross picked, then the other one, then re-check.
	first, second := byte(0x11), byte(0x10)
	if alongGain {
		first, second = 0x10, 0x11
	}
	compareCorrelation(&points)
	if err := tuner.compareStep(&points, first); err != nil {
		return point, err
	}

	if alongGain {
		if err := tuner.iqTree(points[0].gainX, points[0].phaseY, 0x10, &points); err != nil {
			return point, err
		}
	} else {
		if err := tuner.iqTree(points[0].phaseY, points[0].gainX, 0x11, &points); err != nil {
			return point, err
		}
	}
	compareCorrelation(&points)
	if err := tuner.compareStep(&points, second); err != nil {
		return point, err
	}

	if alongGain {
		err = tuner.iqTree(points[0].phaseY, points[0].gainX, 0x11, &points)
	} else {
		err = tuner.iqTree(points[0].gainX, points[0].phaseY, 0x10, &points)
	}
	if err != nil {
		return point, err
	}
	compareCorrelation(&points)

	point, err = tuner.section(points[0])
	if err != nil {
		return point, err
	}
	point.iqcap = *tuner.shadow(0x0b) & 0xfc
	if err := tuner.imrIQCap(&point); err != nil {
		return point, err
	}

	// Leave the trim registers neutral; setMux writes the stored result per
	// channel instead.
	if err := tuner.setBits(0x10, 0xc0, 0x00); err != nil {
		return point, err
	}
	if err := tuner.setBits(0x11, 0xc0, 0x00); err != nil {
		return point, err
	}
	if err := tuner.setBits(0x0b, 0xfc, 0x00); err != nil {
		return point, err
	}
	return point, nil
}

// refineIMR is R848_F_IMR: the cheaper search, starting from an already
// measured point rather than the origin.
func (tuner *Tuner) refineIMR(point imrPoint) (imrPoint, error) {
	if _, err := tuner.rampVGA(11, 16, 40); err != nil {
		return point, err
	}
	best, err := tuner.columnSearch(point)
	if err != nil {
		return point, err
	}
	best.iqcap = *tuner.shadow(0x0b) & 0xfc
	if err := tuner.imrIQCap(&best); err != nil {
		return best, err
	}
	return best, nil
}

// imageRejection points the ring oscillator at one of five reference frequencies
// across the band, runs the search, and stores the result for setMux to apply to
// nearby channels. refine reuses slot 3's result as a starting point instead of
// searching from the origin, which is what the three interior nodes want.
func (tuner *Tuner) imageRejection(slot byte, refine bool) error {
	const ringVCO = 3200000

	*tuner.shadow(0x27) &= 0x3f // clear ring_div1
	*tuner.shadow(0x21) &= 0xfc // clear ring_div2
	*tuner.shadow(0x21) &= 0xdf // clear vco_band
	*tuner.shadow(0x27) &= 0xc3 // clear ring_div_num

	var ringFreq uint32
	var mixerGain byte
	switch slot {
	case 0: // 66.66 MHz
		ringFreq, mixerGain = ringVCO/48, 8
		*tuner.shadow(0x27) |= 0x80 // ring_div1 /6
		*tuner.shadow(0x21) |= 0x03 // ring_div2 /8
	case 1: // 200 MHz
		ringFreq, mixerGain = ringVCO/16, 6
		*tuner.shadow(0x21) |= 0x02 // ring_div2 /4
	case 2: // 400 MHz
		ringFreq, mixerGain = ringVCO/8, 6
		*tuner.shadow(0x21) |= 0x01 // ring_div2 /2
	case 3: // 533.33 MHz
		ringFreq, mixerGain = ringVCO/6, 8
		*tuner.shadow(0x27) |= 0x80 // ring_div1 /6
	default: // 800 MHz
		ringFreq, mixerGain = ringVCO/4, 8
	}
	*tuner.shadow(0x27) |= 0x24 // ring_div_num = 9

	if err := tuner.setBits(0x0f, 0xe0, mixerGain); err != nil {
		return err
	}
	if err := tuner.write(0x27, *tuner.shadow(0x27)); err != nil {
		return err
	}
	if err := tuner.write(0x21, *tuner.shadow(0x21)); err != nil {
		return err
	}

	// Ring PLL drive: the low-power setting outside the middle of the band.
	ringPower := byte(0x00)
	if ringFreq < ringPowerLow || ringFreq > ringPowerHigh {
		ringPower = 0x08
	}
	if err := tuner.setBits(0x21, 0xf3, ringPower); err != nil {
		return err
	}

	if err := tuner.setMux(ringFreq-imrIFkHz, ringFreq, stdSize); err != nil {
		return err
	}
	if err := tuner.setPLL(ringFreq-imrIFkHz, stdSize); err != nil {
		return err
	}
	if err := tuner.setTrackingFilter(ringFreq, tfBead); err != nil {
		return err
	}

	var point imrPoint
	var err error
	if refine {
		start := tuner.imr[3]
		start.iqcap = *tuner.shadow(0x0b) & 0xfc
		point, err = tuner.refineIMR(start)
	} else {
		point, err = tuner.searchIQ()
	}
	if err != nil {
		return err
	}

	if slot >= imrMemCount {
		slot = imrMemCount - 1
	}
	tuner.imr[slot] = point
	return nil
}
