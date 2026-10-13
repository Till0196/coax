// Copyright (c) 2018-2021 nns779; 2026 Till0196
// SPDX-License-Identifier: GPL-2.0-only
//
// Ported from px4_drv (r850.c) by nns779.

package r850

import (
	"time"
)

// setPLL programs the synthesiser for a local oscillator frequency in kHz.
func (tuner *Tuner) setPLL(loFrequency, ifFrequency uint32, system System) error {
	xtal := tuner.config.XtalKHz
	vcoMin := uint32(2200000)
	if tuner.chip == 0 {
		vcoMin += 70000
	}
	vcoMax := vcoMin * 2
	mixDiv := uint32(2)
	vcoFrequency := loFrequency * mixDiv

	tuner.registers[0x20] &= 0xfc
	tuner.registers[0x2e] |= 0x40
	tuner.registers[0x0c] &= 0x3c
	tuner.registers[0x09] &= 0xf9
	tuner.registers[0x22] &= 0x3f
	tuner.registers[0x0b] = tuner.registers[0x0b]&0xc3 | 0x10
	tuner.registers[0x25] = tuner.registers[0x25]&0xef | 0x20

	var band byte
	switch {
	case loFrequency < 100000:
		if tuner.xtalPower > 1 {
			band = 3 - tuner.xtalPower
		} else {
			band = 2
		}
	case loFrequency < 130000:
		if tuner.xtalPower > 2 {
			band = 3 - tuner.xtalPower
		} else {
			band = 1
		}
	default:
		band = 0
	}

	tuner.setXtalCap(0x27)
	tuner.registers[0x22] = tuner.registers[0x22]&0xcf | band<<4&0x30

	// The divider choice below assumes a 24 MHz crystal, as every board uses.
	divJudge := (loFrequency + ifFrequency) / 1000 / 12

	tuner.registers[0x1e] &= 0x1f
	tuner.registers[0x25] &= 0xfd
	switch divJudge {
	case 4, 10, 22, 24, 28:
		tuner.registers[0x25] |= 0x02
	}

	if tuner.chip != 0 {
		tuner.registers[0x2f] &= 0xfd
	} else {
		tuner.registers[0x2f] &= 0xfc
	}

	division := byte(0)
	for division < 6 {
		if vcoMin <= vcoFrequency && vcoFrequency < vcoMax {
			break
		}
		mixDiv *= 2
		vcoFrequency = loFrequency * mixDiv
		division++
	}

	xtalDiv := 0
	tuner.registers[0x22] &= 0xfc
	if system != SystemUndefined {
		switch {
		case loFrequency < 380500:
			if divJudge&1 == 0 {
				xtal /= 2
				tuner.registers[0x22] |= 0x02
				xtalDiv = 1
			}
		case loFrequency+ifFrequency-478000 < 4000 && system == SystemISDBT:
			xtal /= 4
			tuner.registers[0x22] |= 0x03
			xtalDiv = 3
		}
	}

	tuner.registers[0x0b] &= 0xfe

	tuner.registers[0x2d] &= 0xf3
	switch {
	case mixDiv == 8:
		tuner.registers[0x2d] |= 0x04
	case mixDiv == 16:
		tuner.registers[0x2d] |= 0x08
	case mixDiv >= 32:
		tuner.registers[0x2d] |= 0x0c
	}

	tuner.registers[0x2e] &= 0xfc
	tuner.registers[0x20] &= 0xec
	if mixDiv == 2 || mixDiv == 4 {
		tuner.registers[0x2e] |= 0x01
	} else {
		tuner.registers[0x2e] |= 0x02
		tuner.registers[0x20] |= 0x01
	}

	tuner.registers[0x11] &= 0x7f
	if mixDiv == 8 {
		tuner.registers[0x11] |= 0x80
	}

	tuner.registers[0x1e] = tuner.registers[0x1e]&0xe3 | division<<2&0x1c

	integer := uint16(vcoFrequency / 2 / xtal)
	fraction := vcoFrequency - xtal*2*uint32(integer)
	switch {
	case fraction < xtal/64:
		fraction = 0
	case fraction > xtal*127/64:
		fraction = 0
		integer++
	case fraction > xtal*127/128 && xtal > fraction:
		fraction = xtal * 127 / 128
	case xtal < fraction && fraction < xtal*129/128:
		fraction = xtal * 129 / 128
	}

	ni := byte((integer - 13) / 4)
	si := byte(integer - 13 - uint16(ni)*4)
	tuner.registers[0x1b] = tuner.registers[0x1b]&0x80 | ni&0x7f
	tuner.registers[0x1e] = tuner.registers[0x1e]&0xfc | si&0x03
	tuner.registers[0x20] &= 0x3f

	// Accumulate the sigma-delta fraction one halving at a time.
	nsdm := uint32(2)
	sdm := uint16(0)
	for fraction > 1 {
		if xtal*2/nsdm < fraction {
			fraction -= xtal * 2 / nsdm
			sdm += uint16(0x8000 / (nsdm / 2))
			if nsdm&0x8000 != 0 {
				break
			}
		}
		nsdm += nsdm
	}
	tuner.registers[0x1c] = byte(sdm)
	tuner.registers[0x1d] = byte(sdm >> 8)

	if err := tuner.writeRegisters(0x08, 0x28); err != nil {
		return err
	}
	switch xtalDiv {
	case 0:
		time.Sleep(10 * time.Millisecond)
	case 1:
		time.Sleep(20 * time.Millisecond)
	default:
		time.Sleep(40 * time.Millisecond)
	}

	if tuner.chip == 0 {
		tuner.registers[0x2f] &= 0xfc
	}
	tuner.registers[0x2f] |= 0x02
	return tuner.writeRegisters(0x2f, 1)
}

// setMux programs the tracking filter and image-rejection trim for a channel.
func (tuner *Tuner) setMux(loFrequency uint32, system System) {
	var imrIndex int
	switch {
	case loFrequency < 170000:
		imrIndex = 0
	case loFrequency < 240000:
		imrIndex = 4
	case loFrequency < 400000:
		imrIndex = 1
	case loFrequency < 760000:
		imrIndex = 2
	default:
		imrIndex = 3
	}

	var hpfBPF byte
	switch {
	case loFrequency < 580000:
		hpfBPF = 7
	case loFrequency < 660000:
		hpfBPF = 1
	case loFrequency < 780000:
		hpfBPF = 6
	case loFrequency < 900000:
		hpfBPF = 4
	default:
		hpfBPF = 0
	}

	var rfPoly byte
	switch {
	case loFrequency < 133000:
		rfPoly = 2
	case loFrequency < 221000:
		rfPoly = 1
	case loFrequency < 760000:
		rfPoly = 0
	default:
		rfPoly = 3
	}

	var hpfCNR byte
	switch {
	case loFrequency < 480000:
		hpfCNR = 3
	case loFrequency < 550000:
		hpfCNR = 2
	case loFrequency < 700000:
		hpfCNR = 1
	default:
		hpfCNR = 0
	}

	var lpfNotch, lpfCap byte
	if system == SystemDVBC || system == SystemJ83B {
		switch {
		case loFrequency < 77000:
			lpfNotch, lpfCap = 10, 15
		case loFrequency < 85000:
			lpfNotch, lpfCap = 4, 15
		case loFrequency < 115000:
			lpfNotch, lpfCap = 3, 13
		case loFrequency < 125000:
			lpfNotch, lpfCap = 1, 11
		case loFrequency < 141000:
			lpfNotch, lpfCap = 0, 9
		case loFrequency < 157000:
			lpfNotch, lpfCap = 0, 8
		case loFrequency < 181000:
			lpfNotch, lpfCap = 0, 6
		case loFrequency < 205000:
			lpfNotch, lpfCap = 0, 3
		default:
			lpfNotch, lpfCap = 0, 0
		}
	} else {
		switch {
		case loFrequency < 73000:
			lpfNotch, lpfCap = 10, 8
		case loFrequency < 81000:
			lpfNotch, lpfCap = 4, 8
		case loFrequency < 89000:
			lpfNotch, lpfCap = 3, 8
		case loFrequency < 121000:
			lpfNotch, lpfCap = 1, 6
		case loFrequency < 145000:
			lpfNotch, lpfCap = 0, 4
		case loFrequency < 153000:
			lpfNotch, lpfCap = 0, 3
		case loFrequency < 177000:
			lpfNotch, lpfCap = 0, 2
		case loFrequency < 201000:
			lpfNotch, lpfCap = 0, 1
		default:
			lpfNotch, lpfCap = 0, 0
		}
	}

	diplexer := byte(0)
	if loFrequency < 330000 {
		diplexer = 2
	}

	var gain, phase, iqcap byte
	state := &tuner.calibration[tuner.mixerMode]
	switch {
	case state.done && state.usable[imrIndex]:
		point := state.points[imrIndex]
		gain, phase, iqcap = point.gain, point.phase, point.iqcap
	case system != SystemUndefined:
		gain, phase, iqcap = 0x02, 0x00, 0x00
	default:
		gain, phase, iqcap = 0x00, 0x00, 0x00
	}

	tuner.registers[0x0e] = tuner.registers[0x0e]&0x03 | diplexer<<2&0x0c | lpfCap<<4&0xf0
	tuner.registers[0x0f] = tuner.registers[0x0f]&0xf0 | lpfNotch&0x0f
	tuner.registers[0x10] = tuner.registers[0x10]&0xe0 | hpfCNR<<3&0x18 | hpfBPF&0x07
	tuner.registers[0x12] = tuner.registers[0x12]&0xfc | rfPoly&0x03
	tuner.registers[0x14] = tuner.registers[0x14]&0xd0 | gain&0x2f
	tuner.registers[0x15] = tuner.registers[0x15]&0x10 | phase&0x2f | iqcap<<6&0xc0
}
