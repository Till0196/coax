// Copyright (c) 2018-2021 nns779; 2026 Till0196
// SPDX-License-Identifier: GPL-2.0-only
//
// Ported from px4_drv (r850.c) by nns779.

package r850

import (
	"errors"
	"fmt"
)

// applySystem runs the calibrations and loads the system's filter settings, if
// the requested system changed since the last tune.
func (tuner *Tuner) applySystem() error {
	if tuner.system.System == SystemUndefined {
		return errors.New("R850: no system selected")
	}
	state := &tuner.calibration[tuner.mixerMode]
	if !tuner.config.NoIMRCalibration && (!state.done || state.mixerAmpLPF != tuner.mixerAmpCal) {
		if err := tuner.calibrateIMR(); err != nil {
			return fmt.Errorf("R850 image rejection calibration: %w", err)
		}
	}
	if tuner.system == tuner.current {
		return nil
	}

	table := systemTables[tuner.system.System][tuner.chip]
	var params *systemParams
	for index := range table {
		if table[index].bandwidth == int(tuner.system.Bandwidth) && table[index].ifFrequency == tuner.system.IFFrequency {
			params = &table[index]
			break
		}
	}
	if params == nil {
		return fmt.Errorf("R850 has no setting for IF %d kHz at bandwidth %d", tuner.system.IFFrequency, tuner.system.Bandwidth)
	}

	lpf := params.lpf
	if !tuner.config.NoLPFCalibration {
		measured, err := tuner.calibrateLPF(params.filterCalibrationIF, params.bw, 2)
		if err != nil {
			return err
		}
		lpf = measured
	}

	tuner.resetRegisters()
	tuner.registers[0x17] = lpf.lsb&0x01 | lpf.code<<1&0x1e | lpf.bandwidth<<5&0x60 | params.hpfNotch<<7&0x80
	tuner.registers[0x18] = tuner.registers[0x18]&0x0f | params.hpfCorner<<4&0xf0
	tuner.registers[0x12] = tuner.registers[0x12]&0xbf | params.filterExtend<<6&0x40
	tuner.registers[0x18] = tuner.registers[0x18]&0xf3 | params.filterCompensation<<2&0x0c
	tuner.registers[0x2f] = tuner.registers[0x2f]&0xf3 | params.agcClock<<2&0x0c
	if tuner.chip != 0 {
		tuner.registers[0x2c] = tuner.registers[0x2c]&0xfe | params.imageGain>>1&0x01
	}
	tuner.registers[0x2e] = tuner.registers[0x2e]&0xef | params.imageGain<<4&0x10

	tuner.current = tuner.system
	return nil
}

// applyFrequency programs the per-channel gain and filter settings and moves the
// synthesiser. rfFrequency is in kHz.
func (tuner *Tuner) applyFrequency(rfFrequency uint32) error {
	table := frequencyTables[tuner.current.System]
	var params *frequencyParams
	for index := range table {
		entry := &table[index]
		if (entry.ifFrequency == 0 || entry.ifFrequency == tuner.current.IFFrequency) &&
			(entry.rfMin == 0 || entry.rfMin <= rfFrequency) &&
			(entry.rfMax == 0 || entry.rfMax >= rfFrequency) {
			params = entry
			break
		}
	}
	if params == nil {
		return fmt.Errorf("R850 has no setting for %d kHz", rfFrequency)
	}
	settings := *params
	switch tuner.current.System {
	case SystemDVBC, SystemJ83B, SystemISDBT:
		if tuner.chip != 0 {
			settings.filterTop = 6
		}
	}

	var loFrequency uint32
	tuner.registers[0x13] &= 0xef
	if tuner.mixerMode != 0 {
		tuner.registers[0x13] |= 0x10
		loFrequency = rfFrequency - tuner.current.IFFrequency
	} else {
		loFrequency = rfFrequency + tuner.current.IFFrequency
	}

	tuner.registers[0x0a] = tuner.registers[0x0a]&0xbf | settings.naPowerDetect<<6&0x40
	tuner.registers[0x10] = tuner.registers[0x10]&0xdf | initRegisters[0x0c]&0x20
	tuner.registers[0x0b] = tuner.registers[0x0b]&0x7f | settings.lnaNRBDetect<<7&0x80
	tuner.registers[0x26] = tuner.registers[0x26]&0xf8 | (7-settings.lnaTop)&0x07
	tuner.registers[0x27] = settings.lnaVTLH
	tuner.registers[0x11] = tuner.registers[0x11]&0xef | settings.rfLTEPSG<<4&0x10
	tuner.registers[0x26] = tuner.registers[0x26]&0x8f | (7-settings.rfTop)<<4&0x70
	tuner.registers[0x2a] = settings.rfVTLH

	if settings.rfGainLimit <= 3 {
		if settings.rfGainLimit < 2 {
			tuner.registers[0x12] &= 0xfb
		} else {
			tuner.registers[0x12] |= 0x02
		}
		if settings.rfGainLimit%2 != 0 {
			tuner.registers[0x10] |= 0x40
		} else {
			tuner.registers[0x10] &= 0xbf
		}
	}

	tuner.registers[0x13] = tuner.registers[0x13]&0xf8 | settings.mixerAmpLPF&0x07
	tuner.registers[0x28] = tuner.registers[0x28]&0xf0 | (15-settings.mixerTop)&0x0f
	if tuner.chip != 0 {
		tuner.registers[0x2c] = tuner.registers[0x2c]&0xf1 | (7-settings.filterTop)<<1&0x0e
	} else {
		tuner.registers[0x2c] = tuner.registers[0x2c]&0xf0 | (15-settings.filterTop)&0x0f
	}
	tuner.registers[0x0a] = tuner.registers[0x0a]&0xef | settings.filter3rdLPFCurrent<<4&0x10
	tuner.registers[0x18] = tuner.registers[0x18]&0xfc | settings.filter3rdLPFGain&0x03
	tuner.registers[0x29] = settings.filterVTH<<4&0xf0 | settings.mixerVTH&0x0f
	tuner.registers[0x2b] = settings.filterVTL<<4&0xf0 | settings.mixerVTL&0x0f
	tuner.registers[0x16] = tuner.registers[0x16]&0x3f | settings.mixerGainLimit<<6&0xc0
	tuner.registers[0x2e] = tuner.registers[0x2e]&0x7f | settings.mixerDetectBandwidthLPF<<7&0x80

	switch settings.lnaRFDisableMode {
	case 1:
		tuner.registers[0x2d] |= 0x03
		tuner.registers[0x1f] |= 0x01
		tuner.registers[0x20] |= 0x20
	case 2:
		tuner.registers[0x2d] |= 0x03
		tuner.registers[0x1f] &= 0xfe
		tuner.registers[0x20] &= 0xdf
	case 3:
		tuner.registers[0x2d] |= 0x03
		tuner.registers[0x1f] |= 0x01
		tuner.registers[0x20] &= 0xdf
	case 4:
		tuner.registers[0x2d] |= 0x03
		tuner.registers[0x1f] &= 0xfe
		tuner.registers[0x20] |= 0x20
	default:
		tuner.registers[0x2d] &= 0xfc
		tuner.registers[0x1f] |= 0x01
		tuner.registers[0x20] |= 0x20
	}

	tuner.registers[0x1f] = tuner.registers[0x1f]&0xfd | settings.lnaRFChargeCurrent<<1&0x02
	tuner.registers[0x0d] = tuner.registers[0x0d]&0xdf | settings.lnaRFDisableCurrent<<5&0x20
	tuner.registers[0x2d] = tuner.registers[0x2d]&0x0f | settings.rfDisableSlowFast<<4&0xf0
	tuner.registers[0x2c] = tuner.registers[0x2c]&0x0f | settings.lnaDisableSlowFast<<4&0xf0
	tuner.registers[0x19] = tuner.registers[0x19]&0xbf | settings.basebandDisableCurrent<<6&0x40
	tuner.registers[0x25] = tuner.registers[0x25]&0x3b | settings.mixerFilterDisable<<6&0xc0 | settings.basebandDetectMode<<2&0x04
	tuner.registers[0x19] = tuner.registers[0x19]&0xfd | settings.enablePolyGain<<1&0x02
	tuner.registers[0x28] = tuner.registers[0x28]&0x0f | (15-settings.nrbTop)<<4&0xf0
	tuner.registers[0x1a] = tuner.registers[0x1a]&0x33 | settings.nrbBandwidthLPF<<6&0xc0 | settings.nrbBandwidthHPF<<2&0x0c
	tuner.registers[0x2e] = tuner.registers[0x2e]&0xf3 | settings.imageNRBAdder<<2&0x0c
	tuner.registers[0x0d] = tuner.registers[0x0d]&0xf9 | settings.hpfCompensation<<1&0x06
	tuner.registers[0x15] = tuner.registers[0x15]&0xef | settings.feedbackResistor1st<<4&0x10

	// ISDB-T channel 13 sits on a crystal harmonic; slow the AGC clock there.
	if rfFrequency-478000 <= 3999 && tuner.current.System == SystemISDBT {
		tuner.registers[0x2f] &= 0xf3
	}
	tuner.registers[0x19] &= 0xdf

	if tuner.config.LoopThrough {
		tuner.registers[0x08] |= 0xc0
		tuner.registers[0x0a] |= 0x02
	} else {
		tuner.registers[0x08] = tuner.registers[0x08]&0x3f | 0x40
		tuner.registers[0x0a] &= 0xfd
	}
	if tuner.config.ClockOut {
		tuner.registers[0x22] &= 0xfb
	} else {
		tuner.registers[0x22] |= 0x04
	}

	tuner.setMux(loFrequency, tuner.current.System)
	return tuner.setPLL(loFrequency, tuner.current.IFFrequency, tuner.current.System)
}

// SetSystem selects the broadcast system to tune. The calibrations it implies
// run at the next Tune.
func (tuner *Tuner) SetSystem(system SystemConfig) error {
	var mixerMode, mixerAmpCal byte
	switch system.System {
	case SystemDVBT, SystemDVBT2, SystemDVBT21, SystemDVBC, SystemFM:
		mixerMode, mixerAmpCal = 1, 4
	case SystemJ83B, SystemDTMB, SystemATSC:
		mixerMode, mixerAmpCal = 0, 7
	case SystemISDBT:
		mixerMode, mixerAmpCal = 1, 7
	default:
		return fmt.Errorf("R850 does not support system %d", system.System)
	}
	tuner.mu.Lock()
	defer tuner.mu.Unlock()
	if !tuner.initialized {
		return errors.New("R850 is not initialised")
	}
	tuner.system = system
	tuner.mixerMode = mixerMode
	tuner.mixerAmpCal = mixerAmpCal
	// Force the system settings to be reloaded at the next tune.
	tuner.current = SystemConfig{}
	return nil
}

// Tune moves the tuner to an RF frequency given in kHz.
func (tuner *Tuner) Tune(frequency uint32) error {
	if frequency < 40000 || frequency > 1002000 {
		return fmt.Errorf("R850 frequency %d kHz is outside 40000..1002000", frequency)
	}
	tuner.mu.Lock()
	defer tuner.mu.Unlock()
	if !tuner.initialized {
		return errors.New("R850 is not initialised")
	}
	if err := tuner.applySystem(); err != nil {
		return err
	}
	return tuner.applyFrequency(frequency)
}
