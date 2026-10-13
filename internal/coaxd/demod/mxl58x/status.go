// Copyright (c) Ralph Metzler; Marcus Metzler; Digital Devices; MaxLinear; TurboSight; 2026 Till0196
// SPDX-License-Identifier: GPL-2.0-only

package mxl58x

type Status struct {
	Locked bool
	// CNRdB is the carrier-to-noise ratio in dB. HasCNR is false until the
	// chip has a lock, which is when the firmware's reading means anything.
	CNRdB  float64
	HasCNR bool
}

// hundredthsOfADecibel converts a firmware level reading: a signed
// sixteen-bit count of hundredths of a decibel, so negative levels arrive
// with the top of the 32-bit word set.
func hundredthsOfADecibel(raw uint32) float64 {
	return float64(int16(uint16(raw))) / 100
}

// readStatusWord reads one word of this frontend's slice of the firmware's
// status area. The lock either side is not optional: the firmware rewrites the
// slice as it tracks, and a read across an update would mix two.
func (demod *Demod) readStatusWord(register uint32) (uint32, error) {
	offset := demod.statusOffset()
	if err := demod.regWrite(regDemodStatusLock+offset, 1); err != nil {
		return 0, err
	}
	value, err := demod.regRead(register + offset)
	if unlock := demod.regWrite(regDemodStatusLock+offset, 0); unlock != nil && err == nil {
		return 0, unlock
	}
	return value, err
}

// Status is read_status's lock and carrier-to-noise portion.
func (demod *Demod) Status() (Status, error) {
	if !demod.warm {
		return Status{}, ErrNotInitialized
	}
	lock, err := demod.readStatusWord(regDemodLockStatus)
	if err != nil {
		return Status{}, err
	}
	// The firmware writes exactly one for a lock, so compare rather than mask.
	if lock != 1 {
		return Status{}, nil
	}
	snr, err := demod.readStatusWord(regDemodSNR)
	if err != nil {
		return Status{}, err
	}
	return Status{Locked: true, CNRdB: hundredthsOfADecibel(snr), HasCNR: true}, nil
}

// SignalStrength is the level on this frontend's input, in dBm. It is read
// whether or not the chip has a lock, as in the reference driver.
func (demod *Demod) SignalStrength() (float64, error) {
	if !demod.warm {
		return 0, ErrNotInitialized
	}
	power, err := demod.readStatusWord(regDemodInputPower)
	if err != nil {
		return 0, err
	}
	return hundredthsOfADecibel(power), nil
}
