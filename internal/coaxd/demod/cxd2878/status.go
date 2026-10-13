// Copyright (c) Sony; TurboSight; 2026 Till0196
// SPDX-License-Identifier: GPL-2.0-or-later
//
// What the demodulator says about the signal: lock, carrier-to-noise ratio and
// carrier strength. Most of each reading lives in the sony package; what is
// here is which of those this part reaches for per system, plus the one
// measurement that depends on which tuner core the chip carries.

package cxd2878

import "github.com/Till0196/coax/internal/coaxd/demod/sony"

// Lock reports how far acquisition has got in the system the chip is in.
func (demod *Demod) Lock() (sony.Lock, error) {
	switch demod.System {
	case sony.SystemDVBT:
		return demod.LockDVBT()
	case sony.SystemDVBT2:
		return demod.LockDVBT2()
	case sony.SystemDVBC, sony.SystemJ83B:
		return demod.LockCable()
	case sony.SystemISDBT:
		return demod.LockISDBT()
	case sony.SystemATSC:
		return demod.LockATSC()
	default:
		return sony.Lock{}, sony.ErrNoSystem
	}
}

// CNR reports the carrier-to-noise ratio in dB for the system the chip is in.
// The reading is only meaningful once FE_HAS_VITERBI, which this port takes
// as being locked.
func (demod *Demod) CNR() (float64, error) {
	lock, err := demod.Lock()
	if err != nil {
		return 0, err
	}
	if !lock.Locked {
		return 0, sony.ErrCNRUnavailable
	}
	var mdB int32
	switch demod.System {
	case sony.SystemDVBT:
		mdB, err = demod.CNRDVBT(0x10, 4996, 5350, 28500)
	case sony.SystemDVBT2:
		mdB, err = demod.CNRDVBT(0x20, 10876, 12600, 32000)
	case sony.SystemDVBC, sony.SystemJ83B:
		mdB, err = demod.CNRCable()
	case sony.SystemISDBT:
		mdB, err = demod.CNRISDBT()
	case sony.SystemATSC:
		mdB, err = demod.CNRATSC()
	default:
		return 0, sony.ErrNoSystem
	}
	if err != nil {
		return 0, err
	}
	// Every path folds in the fixed -1500 mdB calibration offset before
	// converting to dB.
	return float64(mdB-1500) / 1000, nil
}

// SignalStrength reports the input level in dBm, from the RSSI the tuner
// reports: subtract the IF the delivery system expects, add a fixed 256
// (2.56 dB) calibration offset, then divide by 100.
func (demod *Demod) SignalStrength(frequencyKHz uint32) (float64, error) {
	var rssi int32
	var err error
	if demod.usesASCOT3() {
		rssi, err = demod.ascot3ReadRSSI(frequencyKHz)
	} else {
		rssi, err = demod.freiaReadRSSI(frequencyKHz)
	}
	if err != nil {
		return 0, err
	}
	return demod.DBmFromRSSI(rssi), nil
}
