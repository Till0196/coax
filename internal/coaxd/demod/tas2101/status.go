// Copyright (c) Luis Alves; TurboSight; 2026 Till0196
// SPDX-License-Identifier: GPL-2.0-or-later

package tas2101

type Status struct {
	Locked bool
	// CNRdB is the carrier-to-noise ratio in dB. HasCNR is false until the
	// chip has a lock, which is when the reading means anything.
	CNRdB  float64
	HasCNR bool
}

// Status is tas2101_read_status's lock and carrier-to-noise portion. The one
// write in it is deliberate: on a lock, bit 3 of register 0x04 is dropped --
// the bit the pre-tune table set to hold the transport stream output off while
// the search ran.
func (demod *Demod) Status() (Status, error) {
	if !demod.warm {
		return Status{}, ErrNotInitialized
	}
	raw, err := demod.regRead(registerStatus)
	if err != nil {
		return Status{}, err
	}
	status := Status{Locked: raw&statusMask == statusLocked}
	if status.Locked {
		held, err := demod.regRead(register04)
		if err != nil {
			return Status{}, err
		}
		if held&0x08 != 0 {
			if err := demod.regWrite(register04, held&^0x08); err != nil {
				return Status{}, err
			}
		}
		reading, err := demod.regReadMulti(registerSNR0, 2)
		if err != nil {
			return Status{}, err
		}
		measured := uint16(reading[1]&0x0f)<<8 | uint16(reading[0])
		status.CNRdB = float64(interpolate(snrTable, measured)) / 10
		status.HasCNR = true
	}
	return status, nil
}

// SignalStrength is the level on the aerial lead, in dBm. The frequency is
// not part of it: unlike the M88RS6060's, this chip's curve is calibrated
// once for the whole band.
func (demod *Demod) SignalStrength() (float64, error) {
	if !demod.warm {
		return 0, ErrNotInitialized
	}
	reading, err := demod.regReadMulti(registerStrength0, 2)
	if err != nil {
		return 0, err
	}
	// The reading is twelve bits, and the upper four arrive in the high
	// nibble of the second byte rather than the low one.
	measured := uint16(reading[1]&0xf0)<<4 | uint16(reading[0])
	return float64(interpolate(strengthTable, measured)) / 10, nil
}
