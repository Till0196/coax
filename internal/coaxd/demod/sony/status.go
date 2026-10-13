// Copyright (c) Sony; TurboSight; 2026 Till0196
// SPDX-License-Identifier: GPL-2.0-or-later

package sony

import "errors"

// Lock is how far the demodulator has got with the channel it was pointed at.
type Lock struct {
	// Synced means the demodulator has found the signal's structure. It comes
	// before Locked and is what tells a weak channel from an empty frequency.
	Synced bool
	// Locked means the output stream is running.
	Locked bool
	// Failed means the demodulator has given up, so a caller can stop waiting
	// early. Only members whose lock register carries a give-up bit set it; the
	// rest leave it clear, which reads as "still trying".
	Failed bool
}

func (core *Core) LockDVBT() (Lock, error) {
	if err := core.Bank(0x10); err != nil {
		return Lock{}, err
	}
	data, err := core.Read(SLVT, 0x10, 1)
	if err != nil {
		return Lock{}, err
	}
	sync := data[0] & 0x07
	locked := data[0]&0x20 != 0
	return Lock{Synced: sync >= 6, Locked: sync >= 6 && locked}, nil
}

func (core *Core) LockDVBT2() (Lock, error) {
	if err := core.Bank(0x20); err != nil {
		return Lock{}, err
	}
	data, err := core.Read(SLVT, 0x10, 1)
	if err != nil {
		return Lock{}, err
	}
	sync := data[0] & 0x07
	tsLock := data[0]&0x20 != 0
	unlocked := data[0]&0x10 != 0
	return Lock{Synced: sync == 0x07, Locked: tsLock && !unlocked && sync >= 6}, nil
}

func (core *Core) LockCable() (Lock, error) {
	if err := core.Bank(0x40); err != nil {
		return Lock{}, err
	}
	data, err := core.Read(SLVT, 0x88, 1)
	if err != nil {
		return Lock{}, err
	}
	synced := data[0]&0x01 != 0
	status, err := core.Read(SLVT, 0x10, 1)
	if err != nil {
		return Lock{}, err
	}
	tsLock := status[0]&0x20 != 0
	return Lock{Synced: synced, Locked: synced && tsLock}, nil
}

func (core *Core) LockISDBT() (Lock, error) {
	if err := core.Bank(0x60); err != nil {
		return Lock{}, err
	}
	data, err := core.Read(SLVT, 0x10, 1)
	if err != nil {
		return Lock{}, err
	}
	unlocked := data[0]&0x10 != 0
	synced := data[0]&0x02 != 0
	tsLock := data[0]&0x01 != 0
	return Lock{Synced: synced && !unlocked, Locked: !unlocked && synced && tsLock}, nil
}

func (core *Core) LockATSC() (Lock, error) {
	if err := core.Write(SLVM, 0x00, 0x0d); err != nil {
		return Lock{}, err
	}
	vq, err := core.Read(SLVM, 0x86, 1)
	if err != nil {
		return Lock{}, err
	}
	if err := core.Write(SLVM, 0x00, 0x0f); err != nil {
		return Lock{}, err
	}
	ts, err := core.Read(SLVM, 0x11, 1)
	if err != nil {
		return Lock{}, err
	}
	locked := vq[0]&0x01 != 0 && ts[0]&0x01 != 0
	return Lock{Synced: locked, Locked: locked}, nil
}

// ErrCNRUnavailable reports that the demodulator has no measurement yet, which
// is a different thing from a bus failure and from a reading of 0 dB.
var ErrCNRUnavailable = errors.New("the demodulator has no carrier-to-noise measurement yet")

// The carrier-to-noise readings, meaningful only once the chip has locked (past
// FE_HAS_VITERBI). A reading of zero is the counter saying it has nothing yet,
// not a ratio of zero -- through the ISDB-T formula it would answer -90.31 dB
// -- so every one of these refuses it. Multi-register reads are taken under the
// chip's freeze.

// readCNRWord reads the two-byte estimate a terrestrial curve is computed
// from, refusing a zero.
func (core *Core) readCNRWord(bank, register byte) (uint32, error) {
	if err := core.Bank(bank); err != nil {
		return 0, err
	}
	data, err := core.Read(SLVT, register, 2)
	if err != nil {
		return 0, err
	}
	reg := uint32(data[0])<<8 | uint32(data[1])
	if reg == 0 {
		return 0, ErrCNRUnavailable
	}
	return reg, nil
}

// CNRDVBT reads the DVB-T or DVB-T2 curve. They differ only in the bank, the
// clamp, the constant the reading is subtracted from, and the offset.
func (core *Core) CNRDVBT(bank byte, clamp, subtrahend uint32, offset int32) (int32, error) {
	reg, err := core.readCNRWord(bank, 0x28)
	if err != nil {
		return 0, err
	}
	reg = min(reg, clamp)
	return 100*(int32(MathLog10(reg))-int32(MathLog10(subtrahend-reg))) + offset, nil
}

func (core *Core) CNRISDBT() (int32, error) {
	reg, err := core.readCNRWord(0x60, 0x28)
	if err != nil {
		return 0, err
	}
	return 100*int32(MathLog10(reg)) - 9031, nil
}

// CNRCable covers both DVB-C and J.83B: the same registers are read, and one of
// two curves is picked by the demodulated constellation.
//
// For 32/128-QAM the additive constant is +86999, matching cxd2856er.go's port
// of the same formula; +8699 is the same number missing a digit and would put
// the answer an order of magnitude out.
//
// This is Sony's own formula. A "simplified DVB-C SNR" of -950*log10(reg)+36500
// is sometimes substituted and is wrong by two orders of magnitude.
func (core *Core) CNRCable() (int32, error) {
	if err := core.Freeze(true); err != nil {
		return 0, err
	}
	defer core.Freeze(false)
	if err := core.Bank(0x40); err != nil {
		return 0, err
	}
	constellation, err := core.Read(SLVT, 0x19, 1)
	if err != nil {
		return 0, err
	}
	data, err := core.Read(SLVT, 0x4c, 2)
	if err != nil {
		return 0, err
	}
	reg := uint32(data[0]&0x1f)<<8 | uint32(data[1])
	if reg == 0 {
		return 0, ErrCNRUnavailable
	}
	switch constellation[0] & 0x07 {
	case 1, 3: // 32-QAM, 128-QAM
		reg = max(reg, 69)
		return -88*int32(MathLog(reg)) + 86999, nil
	default: // 16/64/256-QAM
		reg = max(reg, 126)
		return -95*int32(MathLog(reg)) + 95941, nil
	}
}

func (core *Core) CNRATSC() (int32, error) {
	if err := core.Write(SLVM, 0x00, 0x0d); err != nil {
		return 0, err
	}
	data, err := core.Read(SLVM, 0x70, 3)
	if err != nil {
		return 0, err
	}
	reg := uint32(data[2]&0x7f)<<16 | uint32(data[1])<<8 | uint32(data[0])
	if reg == 0 {
		return 0, ErrCNRUnavailable
	}
	return 70418 - 100*int32(MathLog10(reg)), nil
}

// systemIFOut is the IF output level each delivery system expects, in
// hundredths of a dB, subtracted from the tuner's RSSI by SignalStrength.
var systemIFOut = map[System]int32{
	SystemDVBT: 400, SystemDVBT2: 400, SystemDVBC: 150, SystemJ83B: 150,
	SystemISDBT: 450, SystemATSC: 500,
}

// DBmFromRSSI scales the tuner's own received-level reading to an input level
// in dBm: subtract the IF level the delivery system expects, add a fixed
// 2.56 dB calibration offset, and divide by a hundred.
func (core *Core) DBmFromRSSI(rssi int32) float64 {
	return float64(rssi-systemIFOut[core.System]+256) / 100
}
