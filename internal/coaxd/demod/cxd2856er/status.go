// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later
//
// What the demodulator says about the signal it is receiving: lock,
// carrier-to-noise ratio and carrier strength. Readings assembled from several
// registers are taken under the chip's freeze, so they describe one moment.

package cxd2856er

import "github.com/Till0196/coax/internal/coaxd/demod/sony"

// CNR reports the carrier-to-noise ratio in dB for the system the chip is in.
func (demod *Demod) CNR() (float64, error) {
	demod.mu.Lock()
	defer demod.mu.Unlock()
	var mdB int32
	var err error
	switch demod.System {
	case sony.SystemISDBT:
		mdB, err = demod.CNRISDBT()
	case sony.SystemDVBT:
		mdB, err = demod.CNRDVBT(0x10, 4996, 5350, 28500)
	case sony.SystemDVBT2:
		mdB, err = demod.CNRDVBT(0x20, 10876, 12600, 32000)
	case sony.SystemDVBC, sony.SystemJ83B:
		mdB, err = demod.CNRCable()
	case sony.SystemISDBS, sony.SystemDVBS2:
		mdB, err = demod.cnrSatellite(dvbs2ISDBSCN)
	case sony.SystemDVBS:
		mdB, err = demod.cnrSatellite(dvbsCN)
	default:
		return 0, sony.ErrNoSystem
	}
	if err != nil {
		return 0, err
	}
	return float64(mdB) / 1000, nil
}

// cnrSatellite reads a satellite curve, which is a table rather than a formula.
func (demod *Demod) cnrSatellite(curve sony.CNCurve) (int32, error) {
	if err := demod.Freeze(true); err != nil {
		return 0, err
	}
	defer demod.Freeze(false)
	if err := demod.Bank(0xa1); err != nil {
		return 0, err
	}
	data, err := demod.Read(sony.SLVT, 0x10, 3)
	if err != nil {
		return 0, err
	}
	if data[0]&0x01 == 0 {
		return 0, sony.ErrCNRUnavailable
	}
	// A 13-bit reading: five bits in the first byte, eight in the second.
	return curve.MdB(uint32(data[1]&0x1f)<<8 | uint32(data[2])), nil
}

// Lock reports how far acquisition has got in the system the chip is in.
func (demod *Demod) Lock() (sony.Lock, error) {
	demod.mu.Lock()
	defer demod.mu.Unlock()
	switch demod.System {
	case sony.SystemISDBT:
		return demod.lockTerrestrial(0x60)
	case sony.SystemDVBT:
		return demod.lockDVBT(0x10)
	case sony.SystemDVBT2:
		return demod.lockDVBT(0x20)
	case sony.SystemDVBC, sony.SystemJ83B:
		return demod.lockJ83()
	case sony.SystemISDBS:
		return demod.lockISDBS()
	case sony.SystemDVBS, sony.SystemDVBS2:
		return demod.lockDVBS()
	default:
		return sony.Lock{}, sony.ErrNoSystem
	}
}

// lockTerrestrial reads the ISDB-T lock register. Sync, lock and give-up each
// have their own bit.
func (demod *Demod) lockTerrestrial(bank byte) (sony.Lock, error) {
	data, err := demod.readBanked(bank, 0x10)
	if err != nil {
		return sony.Lock{}, err
	}
	return sony.Lock{
		Synced: data&0x02 != 0,
		Locked: data&0x01 != 0,
		Failed: data&0x10 != 0,
	}, nil
}

// lockDVBT reads the DVB-T or DVB-T2 lock register, which differ only in bank.
// Sync is a three-bit field rather than a flag, and the all-ones value is an
// invalid hardware state rather than "synchronised", so it reports a zero Lock.
func (demod *Demod) lockDVBT(bank byte) (sony.Lock, error) {
	data, err := demod.readBanked(bank, 0x10)
	if err != nil {
		return sony.Lock{}, err
	}
	if data&0x07 == 0x07 {
		return sony.Lock{}, nil
	}
	return sony.Lock{
		Synced: data&0x07 == 0x06,
		Locked: data&0x20 != 0,
		Failed: data&0x10 != 0,
	}, nil
}

// lockJ83 reads the J.83 lock, which takes two registers: the first says
// whether the demodulator has the signal at all, and only then does the second
// one's stream lock mean anything.
func (demod *Demod) lockJ83() (sony.Lock, error) {
	demodulated, err := demod.readBanked(0x40, 0x88)
	if err != nil {
		return sony.Lock{}, err
	}
	if demodulated&0x01 == 0 {
		return sony.Lock{}, nil
	}
	stream, err := demod.ReadBanked(0x10, 1)
	if err != nil {
		return sony.Lock{}, err
	}
	return sony.Lock{Synced: true, Locked: stream[0]&0x20 != 0}, nil
}

// lockISDBS reads the ISDB-S stream lock at bank 0xA0 register 0x12 -- not the
// register DVB-S reads on the same bank. The two are deliberately kept apart:
// reading the wrong one returns a plausible value rather than an error.
func (demod *Demod) lockISDBS() (sony.Lock, error) {
	data, err := demod.readBanked(0xa0, 0x12)
	if err != nil {
		return sony.Lock{}, err
	}
	locked := data&0x40 != 0
	return sony.Lock{Synced: locked, Locked: locked}, nil
}

// lockDVBS reads the DVB-S and DVB-S2 stream lock: bank 0xA0 register 0x11
// bit 2, "ITSLOCK".
func (demod *Demod) lockDVBS() (sony.Lock, error) {
	data, err := demod.readBanked(0xa0, 0x11)
	if err != nil {
		return sony.Lock{}, err
	}
	locked := data&0x04 != 0
	return sony.Lock{Synced: locked, Locked: locked}, nil
}

// readBanked selects a register bank and reads one register out of it.
func (demod *Demod) readBanked(bank, register byte) (byte, error) {
	if err := demod.Bank(bank); err != nil {
		return 0, err
	}
	data, err := demod.ReadBanked(register, 1)
	if err != nil {
		return 0, err
	}
	return data[0], nil
}

// SignalStrength reports the input level in dBm for the system the chip is in.
//
// The reading is the demodulator's AGC: the harder it works, the weaker the
// signal. Turning it into dBm is a per-system straight line, fitted from
// measurement rather than a specified transfer function. Good to a decibel or
// so, not a calibration.
func (demod *Demod) SignalStrength() (float64, error) {
	demod.mu.Lock()
	defer demod.mu.Unlock()
	var mdBm int64
	var err error
	switch demod.System {
	case sony.SystemISDBT:
		var agc uint32
		if agc, err = demod.agcGain(0x60, 0x26, 0x0f, 4); err == nil {
			mdBm = int64(agc)*3775/1000 - 90185
		}
	case sony.SystemDVBT:
		var agc uint32
		if agc, err = demod.agcGain(0x10, 0x26, 0x0f, 4); err == nil {
			mdBm = int64(agc)*366/100 - 89520
		}
	case sony.SystemDVBT2:
		var agc uint32
		if agc, err = demod.agcGain(0x20, 0x26, 0x0f, 4); err == nil {
			mdBm = int64(agc)*366/100 - 89520
		}
	case sony.SystemDVBC, sony.SystemJ83B:
		var agc uint32
		if agc, err = demod.agcGain(0x40, 0x49, 0x0f, 4); err == nil {
			mdBm = int64(agc)*4045/1000 - 85224
		}
	case sony.SystemISDBS, sony.SystemDVBS, sony.SystemDVBS2:
		mdBm, err = demod.satelliteLevel()
	default:
		return 0, sony.ErrNoSystem
	}
	if err != nil {
		return 0, err
	}
	return float64(mdBm) / 1000, nil
}

// agcGain reads one of the demodulator's AGC registers: a 12-bit value in the
// low bits of a register pair, shifted up to a common 16-bit scale before the
// straight line is applied.
func (demod *Demod) agcGain(bank, register, highMask byte, shift uint) (uint32, error) {
	if err := demod.Bank(bank); err != nil {
		return 0, err
	}
	data, err := demod.Read(sony.SLVT, register, 2)
	if err != nil {
		return 0, err
	}
	return (uint32(data[0]&highMask)<<8 | uint32(data[1])) << shift, nil
}

// satelliteLevel converts the satellite AGC to dBm. There is no fitted decibel
// scale for it the way there is for the other systems, only the raw gain as a
// relative figure, so this is the least certain reading in this file.
func (demod *Demod) satelliteLevel() (int64, error) {
	if err := demod.Bank(0xa0); err != nil {
		return 0, err
	}
	data, err := demod.Read(sony.SLVT, 0x1f, 2)
	if err != nil {
		return 0, err
	}
	agc := int64(data[0]&0x1f)<<8 | int64(data[1])
	// The gain in tenths of a dB, rounded away from zero.
	magnitude := agc * 1400
	quotient, remainder := magnitude/403, magnitude%403
	if remainder >= 202 {
		quotient++
	}
	return (9700 - quotient) * -10, nil
}
