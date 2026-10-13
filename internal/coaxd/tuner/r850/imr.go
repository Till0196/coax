// Copyright (c) 2018-2021 nns779; 2026 Till0196
// SPDX-License-Identifier: GPL-2.0-only
//
// Ported from px4_drv (r850.c) by nns779.

package r850

import (
	"fmt"
	"time"
)

// readADC samples the chip's calibration ADC.
func (tuner *Tuner) readADC() (byte, error) {
	time.Sleep(2 * time.Millisecond)
	var value [1]byte
	if err := tuner.readRegisters(0x01, value[:]); err != nil {
		return 0, err
	}
	return value[0] & 0x3f, nil
}

// writeIQ writes the gain and phase trim registers together.
func (tuner *Tuner) writeIQ() error { return tuner.writeRegisters(0x14, 2) }

// checkIQCross samples a cross of nine trim points around the origin and keeps
// the quietest, which also tells the caller which axis to walk next.
func (tuner *Tuner) checkIQCross(point *imr) (imrDirection, error) {
	cross := [9]struct{ gain, phase byte }{
		{}, {phase: 1}, {phase: 0x20 | 1}, {gain: 1}, {gain: 0x20 | 1},
		{phase: 2}, {phase: 0x20 | 2}, {gain: 2}, {gain: 0x20 | 2},
	}
	best := imr{value: 0xff}
	for _, candidate := range cross {
		tuner.registers[0x14] = tuner.registers[0x14]&0xd0 | candidate.gain&0x2f
		tuner.registers[0x15] = tuner.registers[0x15]&0xd0 | candidate.phase&0x2f
		if err := tuner.writeIQ(); err != nil {
			return imrGain, err
		}
		value, err := tuner.readADC()
		if err != nil {
			return imrGain, err
		}
		if best.value > value {
			best = imr{gain: candidate.gain, phase: candidate.phase, value: value}
		}
	}
	*point = best
	if best.phase != 0 {
		return imrPhase, nil
	}
	return imrGain, nil
}

// checkIQTree samples three or five neighbouring points along one axis.
func (tuner *Tuner) checkIQTree(point *imr, direction imrDirection, count int) error {
	if count != 3 && count != 5 {
		return fmt.Errorf("R850 IMR tree size %d is unsupported", count)
	}
	var register byte
	var values [5]byte
	best := imr{}
	switch direction {
	case imrGain:
		register = 0x14
		values[0] = point.gain
		tuner.registers[0x15] = tuner.registers[0x15]&0xd0 | point.phase&0x2f
		best.phase = point.phase
	case imrPhase:
		register = 0x15
		values[0] = point.phase
		tuner.registers[0x14] = tuner.registers[0x14]&0xd0 | point.gain&0x2f
		best.gain = point.gain
	}
	values[1] = values[0] + 1
	if count == 3 {
		if values[0]&0x0f == 0 {
			values[2] = values[0] ^ 0x20 + 1
		} else {
			values[2] = values[0] - 1
		}
	} else {
		values[2] = values[0] + 2
		switch values[0] & 0x0f {
		case 0:
			values[3] = values[0] ^ 0x20 + 1
			values[4] = values[3] + 1
		case 1:
			values[3] = values[0] - 1
			values[4] = values[3] ^ 0x20 + 1
		default:
			values[3] = values[0] - 1
			values[4] = values[3] - 1
		}
	}
	best.value = 0xff
	for index := 0; index < count; index++ {
		tuner.registers[register] = tuner.registers[register]&0xd0 | values[index]&0x2f
		if err := tuner.writeIQ(); err != nil {
			return err
		}
		value, err := tuner.readADC()
		if err != nil {
			return err
		}
		if best.value > value {
			if direction == imrGain {
				best.gain = values[index]
			} else {
				best.phase = values[index]
			}
			best.value = value
		}
	}
	*point = best
	return nil
}

// checkIQStep walks one axis upwards until the reading stops improving.
func (tuner *Tuner) checkIQStep(point *imr, direction imrDirection) error {
	var register, value byte
	switch direction {
	case imrGain:
		register = 0x14
		value = point.gain
		tuner.registers[0x15] = tuner.registers[0x15]&0xd0 | point.phase&0x2f
	case imrPhase:
		register = 0x15
		value = point.phase
		tuner.registers[0x14] = tuner.registers[0x14]&0xd0 | point.gain&0x2f
	}
	best := *point
	for value&0x0f <= 8 {
		value++
		tuner.registers[register] = tuner.registers[register]&0xd0 | value&0x2f
		if err := tuner.writeIQ(); err != nil {
			return err
		}
		sample, err := tuner.readADC()
		if err != nil {
			return err
		}
		if best.value > sample {
			if direction == imrGain {
				best.gain = value
			} else {
				best.phase = value
			}
			best.value = sample
		} else if best.value+2 < sample {
			break
		}
	}
	*point = best
	return nil
}

// checkSection re-walks the phase axis at three neighbouring gains.
func (tuner *Tuner) checkSection(point *imr) error {
	var points [3]imr
	points[1].gain = point.gain
	if point.gain != 0 {
		points[0].gain = point.gain - 1
		points[2].gain = point.gain + 1
	} else {
		points[0].gain = point.gain&0xdf + 1
		points[2].gain = point.gain | 0x20 + 1
	}
	for index := range points {
		points[index].phase = point.phase
	}
	best := byte(0xff)
	chosen := 0
	for index := range points {
		if err := tuner.checkIQTree(&points[index], imrPhase, 3); err != nil {
			return err
		}
		if best > points[index].value {
			best = points[index].value
			chosen = index
		}
	}
	*point = points[chosen]
	return nil
}

// checkIQCap picks the quietest of the three IQ capacitance settings.
func (tuner *Tuner) checkIQCap(point *imr) error {
	tuner.registers[0x14] = tuner.registers[0x14]&0xd0 | point.gain&0x2f
	if err := tuner.writeRegisters(0x14, 1); err != nil {
		return err
	}
	tuner.registers[0x15] = tuner.registers[0x15]&0xd0 | point.phase&0x2f
	point.iqcap = 0
	point.value = 0xff
	for candidate := byte(0); candidate < 3; candidate++ {
		tuner.registers[0x15] = tuner.registers[0x15]&0x3f | candidate<<6&0xc0
		if err := tuner.writeRegisters(0x15, 1); err != nil {
			return err
		}
		value, err := tuner.readADC()
		if err != nil {
			return err
		}
		if value < point.value {
			point.iqcap = candidate
			point.value = value
		}
	}
	return nil
}

// calibrateIMR measures the image-rejection trim at five ring frequencies.
func (tuner *Tuner) calibrateIMR() error {
	tuner.registers = imrCalibrationRegisters
	mixerMode := tuner.mixerMode
	mixerAmpLPF := tuner.mixerAmpCal
	state := &tuner.calibration[mixerMode]

	// The order matters: the wideband points are measured first and the narrow
	// ones seeded from them.
	for _, index := range [5]int{2, 1, 0, 3, 4} {
		var ringFrequency uint32
		full := false
		previous := 2
		switch index {
		case 0:
			ringFrequency = 136000
			tuner.registers[0x24] = tuner.registers[0x24]&0xf0 | 0x0a
			previous = 1
		case 1:
			ringFrequency = 326400
			tuner.registers[0x24] = tuner.registers[0x24]&0xf0 | 0x05
		case 2:
			ringFrequency = 544000
			tuner.registers[0x24] = tuner.registers[0x24]&0xf0 | 0x02
			full = true
		case 3:
			ringFrequency = 816000
			tuner.registers[0x24] &= 0xf0
			full = mixerMode != 0
		case 4:
			ringFrequency = 204000
			tuner.registers[0x24] = tuner.registers[0x24]&0xf0 | 0x08
			previous = 1
		}

		tuner.registers[0x23] = tuner.registers[0x23]&0xa0 | 0x11

		if mixerMode == 0 {
			tuner.setMux(ringFrequency, SystemUndefined)
			if err := tuner.setPLL(ringFrequency-5300, 5300, SystemUndefined); err != nil {
				return err
			}
			tuner.registers[0x13] = tuner.registers[0x13]&0xe8 | mixerAmpLPF&0x07
			if err := tuner.writeRegisters(0x13, 1); err != nil {
				return err
			}
			if index == 4 {
				tuner.registers[0x24] = tuner.registers[0x24]&0xcf | 0x10
			} else {
				tuner.registers[0x24] |= 0x30
			}
			if err := tuner.writeRegisters(0x24, 1); err != nil {
				return err
			}
			tuner.registers[0x29] = tuner.registers[0x29]&0xf0 | 0x08
			if err := tuner.writeRegisters(0x29, 1); err != nil {
				return err
			}
		} else {
			tuner.setMux(ringFrequency, SystemUndefined)
			if err := tuner.setPLL(ringFrequency+5300, 5300, SystemUndefined); err != nil {
				return err
			}
			tuner.registers[0x13] |= 0x10
			tuner.registers[0x13] = tuner.registers[0x13]&0xf8 | mixerAmpLPF&0x07
			if err := tuner.writeRegisters(0x13, 1); err != nil {
				return err
			}
			tuner.registers[0x29] &= 0xf0
			if index == 4 {
				tuner.registers[0x29] |= 0x07
				tuner.registers[0x24] = tuner.registers[0x24]&0xcf | 0x10
			} else {
				tuner.registers[0x29] |= 0x06
				tuner.registers[0x24] |= 0x30
			}
			if err := tuner.writeRegisters(0x29, 1); err != nil {
				return err
			}
			if err := tuner.writeRegisters(0x24, 1); err != nil {
				return err
			}
		}

		tuner.registers[0x29] |= 0xf0
		if err := tuner.writeRegisters(0x29, 1); err != nil {
			return err
		}

		point := &state.points[index]
		if full {
			*point = imr{}
			direction, err := tuner.checkIQCross(point)
			if err != nil {
				return err
			}
			if err := tuner.checkIQStep(point, direction); err != nil {
				return err
			}
			opposite := imrGain
			if direction == imrGain {
				opposite = imrPhase
			}
			if err := tuner.checkIQTree(point, opposite, 5); err != nil {
				return err
			}
			if err := tuner.checkIQTree(point, direction, 3); err != nil {
				return err
			}
		} else {
			*point = state.points[previous]
		}

		if err := tuner.checkSection(point); err != nil {
			return err
		}
		if err := tuner.checkIQCap(point); err != nil {
			return err
		}
		// A trim that lands near the edge of its range did not really converge.
		state.usable[index] = point.gain&0x0f <= 0x06 && point.phase&0x0f <= 0x06

		if full {
			tuner.registers[0x14] &= 0xd0
			tuner.registers[0x15] &= 0x10
			if err := tuner.writeIQ(); err != nil {
				return err
			}
		}
	}

	state.done = true
	state.mixerAmpLPF = mixerAmpLPF
	return nil
}
