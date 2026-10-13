// Copyright (c) 2015 Luis Alves; Rafael Micro; TurboSight; 2026 Till0196
// SPDX-License-Identifier: GPL-2.0-or-later

package r848

// freqSelect works out the LNA band, IMR memory slot, polyfilter band and
// low-pass filter cap/notch for one LO/RF frequency pair.
func freqSelect(loKHz, rfKHz uint32, system standard) freqInfo {
	var info freqInfo

	// The LNA band thresholds always use the bead row, regardless of which
	// tracking-filter type is in use.
	switch {
	case rfKHz < lnaLowLowest[tfBead]:
		info.lnaBand = 0x60 // ultra low
	case rfKHz < lnaMidLow[tfBead]:
		info.lnaBand = 0x40 // low
	case rfKHz < lnaHighMid[tfBead]:
		info.lnaBand = 0x20 // mid
	default:
		info.lnaBand = 0x00 // high
	}

	switch {
	case loKHz < 133000:
		info.imrMem = 0
	case loKHz < 221000:
		info.imrMem = 1
	case loKHz < 450000:
		info.imrMem = 2
	case loKHz < 775000:
		info.imrMem = 3
	default:
		info.imrMem = 4
	}
	// The polyfilter band's thresholds do not line up with the IMR slots':
	// 221000-775000 is one polyfilter band but two IMR slots.
	switch {
	case loKHz < 133000:
		info.rfPoly = 0x80 // low
	case loKHz < 221000:
		info.rfPoly = 0x40 // mid
	case loKHz < 775000:
		info.rfPoly = 0x00 // highest
	default:
		info.rfPoly = 0xc0 // ultra high
	}

	switch system {
	case stdDvbC8m, stdDvbC6m, stdJ83b, stdDvbC8mIf5m, stdDvbC6mIf5m, stdJ83bIf5m:
		switch {
		case loKHz < 77000:
			info.lpfCap, info.lpfNotch = 16, 10
		case loKHz < 85000:
			info.lpfCap, info.lpfNotch = 16, 4
		case loKHz < 115000:
			info.lpfCap, info.lpfNotch = 13, 3
		case loKHz < 125000:
			info.lpfCap, info.lpfNotch = 11, 1
		case loKHz < 141000:
			info.lpfCap, info.lpfNotch = 9, 0
		case loKHz < 157000:
			info.lpfCap, info.lpfNotch = 8, 0
		case loKHz < 181000:
			info.lpfCap, info.lpfNotch = 6, 0
		case loKHz < 205000:
			info.lpfCap, info.lpfNotch = 3, 0
		default:
			info.lpfCap, info.lpfNotch = 0, 0
		}
	default:
		switch {
		case loKHz < 73000:
			info.lpfCap, info.lpfNotch = 8, 10
		case loKHz < 81000:
			info.lpfCap, info.lpfNotch = 8, 4
		case loKHz < 89000:
			info.lpfCap, info.lpfNotch = 8, 3
		case loKHz < 121000:
			info.lpfCap, info.lpfNotch = 6, 1
		case loKHz < 145000:
			info.lpfCap, info.lpfNotch = 4, 0
		case loKHz < 153000:
			info.lpfCap, info.lpfNotch = 3, 0
		case loKHz < 177000:
			info.lpfCap, info.lpfNotch = 2, 0
		case loKHz < 201000:
			info.lpfCap, info.lpfNotch = 1, 0
		default:
			info.lpfCap, info.lpfNotch = 0, 0
		}
	}
	return info
}

// sysInfoFor is the per-standard table lookup plus R848_Sys_Sel's fixed tail.
// It also derives the config's tracking-filter type from what the
// tracking-filter check measured, which happens here rather than at check time.
func (tuner *Tuner) sysInfoFor(system standard) sysInfo {
	info := sysInfoTable[system]

	info.swcapClk = 0x02 // 8 kHz, the AGC's 500 Hz rate mapped onto it
	info.naPwrDet = 0x00 // on
	info.tfCur = 0x40    // low
	info.swbufCur = 0x04 // low

	if system == stdDvbC8mIf5m {
		info.filt3dB = 0x08
	}
	info.filtComp = 0x20
	info.filtCur = 0x00 // highest

	if system == stdDvbT217m || system == stdFm {
		info.v17M = 0x80
	}

	// Every standard picks the same pair, so what would otherwise be a
	// per-standard switch collapses to this.
	if tuner.config.DetectTfType == ulUsingBead {
		tuner.config.SetTfType = tf82nBead
	} else {
		tuner.config.SetTfType = tf82n270n
	}
	return info
}

// sysFreqInfoFor is R848_SysFreq_Sel: AGC thresholds and discharge rates, which
// depend on both the standard and where in the band the channel sits.
func (tuner *Tuner) sysFreqInfoFor(system standard, rfKHz uint32) sysFreqInfo {
	var info sysFreqInfo

	switch system {
	case stdDvbT6m, stdDvbT7m, stdDvbT8m, stdDvbT6mIf5m, stdDvbT7mIf5m, stdDvbT8mIf5m,
		stdDvbT26m, stdDvbT27m, stdDvbT28m, stdDvbT217m, stdDvbT210m,
		stdDvbT26mIf5m, stdDvbT27mIf5m, stdDvbT28mIf5m, stdDvbT217mIf5m:
		switch {
		case rfKHz >= 300000 && rfKHz <= 472000:
			info.lnaVthL, info.mixerTop, info.mixerVthL, info.nrbTop = 0xa4, 0x05, 0x95, 0xc0
		case rfKHz >= 184000 && rfKHz <= 299000:
			info.lnaVthL, info.mixerTop, info.mixerVthL, info.nrbTop = 0xa5, 0x04, 0xa6, 0x70
		default:
			info.lnaVthL, info.mixerTop, info.mixerVthL, info.nrbTop = 0xa5, 0x05, 0x95, 0xc0
		}
		info.lnaTop, info.rfTop, info.nrbBW = 0x03, 0x40, 0xc0

	case stdDvbC8m, stdDvbC6m, stdJ83b, stdDvbC8mIf5m, stdDvbC6mIf5m, stdJ83bIf5m:
		switch {
		case rfKHz < 165000:
			info.lnaTop, info.lnaVthL, info.rfTop, info.nrbTop = 0x02, 0x94, 0x80, 0x90
		case rfKHz <= 299000:
			info.lnaTop, info.lnaVthL, info.rfTop, info.nrbTop = 0x03, 0x94, 0x80, 0x90
		case rfKHz <= 345000:
			info.lnaTop, info.lnaVthL, info.rfTop, info.nrbTop = 0x03, 0xa4, 0x80, 0x90
		case rfKHz <= 472000:
			info.lnaTop, info.lnaVthL, info.rfTop, info.nrbTop = 0x04, 0x93, 0xa0, 0x20
		default:
			info.lnaTop, info.lnaVthL, info.rfTop, info.nrbTop = 0x04, 0x83, 0xa0, 0x20
		}
		info.mixerTop, info.mixerVthL = 0x05, 0x95
		// The cable branch never sets NRB_BW, so it is left at a deterministic
		// zero rather than at whatever happened to be there.

	case stdIsdbT, stdIsdbT4570, stdIsdbTIf5m:
		info.lnaVthL = 0xa5
		if rfKHz >= 300000 && rfKHz <= 472000 {
			info.lnaVthL = 0xa4
		}
		info.lnaTop, info.mixerTop, info.mixerVthL = 0x03, 0x05, 0x95
		info.rfTop, info.nrbTop, info.nrbBW = 0x60, 0xb0, 0xc0

	case stdDtmb4570, stdDtmb6000, stdDtmb6mBwIf5m, stdDtmb6mBwIf4500, stdDtmbIf5m:
		info.lnaVthL = 0xa5
		if rfKHz >= 300000 && rfKHz <= 472000 {
			info.lnaVthL = 0xa4
		}
		info.lnaTop, info.mixerTop, info.mixerVthL = 0x03, 0x05, 0x95
		info.rfTop, info.nrbTop, info.nrbBW = 0x60, 0xa0, 0xc0

	case stdAtsc, stdAtscIf5m:
		info.nrbBW = 0xc0
		if tuner.config.DetectTfType == ulUsingBead {
			switch {
			case rfKHz < 88000:
				info.lnaTop, info.lnaVthL, info.rfTop, info.nrbTop = 0x03, 0xa5, 0xc0, 0x30
			case rfKHz < 104000:
				info.lnaTop, info.lnaVthL, info.rfTop, info.nrbTop = 0x02, 0xa5, 0xa0, 0x30
			case rfKHz < 156000:
				info.lnaTop, info.lnaVthL, info.rfTop, info.nrbTop = 0x01, 0xa5, 0x60, 0x30
			case rfKHz < 464000:
				info.lnaTop, info.lnaVthL, info.rfTop, info.nrbTop = 0x01, 0xa5, 0x60, 0x90
			case rfKHz < 500000:
				info.lnaTop, info.lnaVthL, info.rfTop, info.nrbTop = 0x01, 0xb6, 0x60, 0x90
			default:
				info.lnaTop, info.lnaVthL, info.rfTop, info.nrbTop = 0x01, 0x94, 0x40, 0x90
			}
		} else { // 270 nH
			switch {
			case rfKHz < 88000:
				info.lnaTop, info.lnaVthL, info.rfTop, info.nrbTop = 0x02, 0x94, 0x80, 0xc0
			case rfKHz < 104000:
				info.lnaTop, info.lnaVthL, info.rfTop, info.nrbTop = 0x02, 0x94, 0x60, 0x90
			case rfKHz < 248000:
				info.lnaTop, info.lnaVthL, info.rfTop, info.nrbTop = 0x01, 0x94, 0x60, 0x90
			case rfKHz < 464000:
				info.lnaTop, info.lnaVthL, info.rfTop, info.nrbTop = 0x01, 0xa5, 0x60, 0x90
			case rfKHz < 500000:
				info.lnaTop, info.lnaVthL, info.rfTop, info.nrbTop = 0x01, 0xb6, 0x60, 0x90
			default:
				info.lnaTop, info.lnaVthL, info.rfTop, info.nrbTop = 0x01, 0x94, 0x40, 0x90
			}
		}
		info.mixerTop, info.mixerVthL = 0x04, 0xb7

	default: // stdFm and everything else fall back to DVB-T 8 MHz
		info.lnaVthL = 0xa5
		if rfKHz >= 300000 && rfKHz <= 472000 {
			info.lnaVthL = 0xa4
		}
		info.lnaTop, info.mixerTop, info.mixerVthL = 0x03, 0x05, 0x95
		info.rfTop, info.nrbTop, info.nrbBW = 0x40, 0x20, 0xc0
		if system == stdFm {
			info.rfTop = 0x60
		}
	}

	info.rfFastDischarge = 0x00
	info.rfSlowDischarge = 0x80
	info.rfpdPulseEnable = 0x10
	info.lnaFastDischarge = 0x0a
	info.lnaSlowDischarge = 0x00
	info.lnapdPulseEnable = 0x00
	info.agcClk = 0x00

	switch system {
	case stdDtmb4570, stdDtmb6000, stdDtmb6mBwIf5m, stdDtmb6mBwIf4500, stdDtmbIf5m:
		info.bypLPF = 0x00 // bypass

	case stdDvbT6m, stdDvbT7m, stdDvbT8m, stdDvbT6mIf5m, stdDvbT7mIf5m, stdDvbT8mIf5m,
		stdDvbT26m, stdDvbT27m, stdDvbT28m, stdDvbT217m, stdDvbT210m,
		stdDvbT26mIf5m, stdDvbT27mIf5m, stdDvbT28mIf5m, stdDvbT217mIf5m:
		// This branch never assigns BYP_LPF either; zero (bypass) stands in,
		// as above.
		if rfKHz >= 662000 && rfKHz <= 670000 {
			// A known interferer sits here; slow the AGC right down.
			info.rfSlowDischarge, info.agcClk = 0x20, 0x18 // 60 Hz
		} else {
			info.rfSlowDischarge, info.agcClk = 0x80, 0x00 // 1 kHz
			switch system {
			case stdDvbT26m, stdDvbT27m, stdDvbT28m, stdDvbT217m, stdDvbT210m,
				stdDvbT26mIf5m, stdDvbT27mIf5m, stdDvbT28mIf5m, stdDvbT217mIf5m:
				info.agcClk = 0x1c // 250 Hz
			}
		}

	default:
		if rfKHz <= 236000 {
			info.bypLPF = 0x40 // low pass
		} else {
			info.bypLPF = 0x00 // bypass
		}
	}
	return info
}
