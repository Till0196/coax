// Copyright (c) Sony; TurboSight; 2026 Till0196
// SPDX-License-Identifier: GPL-2.0-or-later

package sony

import "errors"

// The Freia tuner core's vocabulary, shared by the two ports that meet one:
// the CXD2878ER family reaches a terrestrial-and-cable-only Freia, while a
// CXD2857ER sits beside one with a satellite side too. Their power-up tables,
// tune prologues and RSSI sequences all differ and each port keeps its own.
// What is shared -- and lives here -- is the parameter table's shape, the
// system enumeration, the mapping onto it, and the head of the tuning packet.

// FreiaParam is one row of the tuner's terrestrial/cable tuning table. Each
// port carries its own table; the numbers differ by chip, the columns do not.
type FreiaParam struct {
	RFGain       byte // 0xff means automatic
	IFBPFGain    byte
	RFOverload   [3]byte // <=172 MHz, <=464 MHz, >464 MHz
	IFOverload   [3]byte
	IFBPFCenter  byte
	Bandwidth    byte
	IFOffset     byte
	BandwidthOff byte
}

// FreiaRow builds one row. It is a call rather than a struct literal because
// these tables are eight columns wide and only legible one row to a line;
// naming every field would double the width for nothing.
func FreiaRow(rfGain, ifBPFGain byte, rfOverload, ifOverload [3]byte, ifBPFCenter, bandwidth, ifOffset, bandwidthOff byte) FreiaParam {
	return FreiaParam{
		RFGain: rfGain, IFBPFGain: ifBPFGain,
		RFOverload: rfOverload, IFOverload: ifOverload,
		IFBPFCenter: ifBPFCenter, Bandwidth: bandwidth,
		IFOffset: ifOffset, BandwidthOff: bandwidthOff,
	}
}

// FreiaSystem is the tuner core's own system enum, finer-grained than the
// demodulator's because the channel width is folded into it.
type FreiaSystem int

const (
	FreiaCable6 FreiaSystem = iota
	FreiaCable8
	FreiaDVBT5
	FreiaDVBT6
	FreiaDVBT7
	FreiaDVBT8
	FreiaDVBT2_1_7
	FreiaDVBT2_5
	FreiaDVBT2_6
	FreiaDVBT2_7
	FreiaDVBT2_8
	FreiaISDBT6
	FreiaISDBT7
	FreiaISDBT8
	FreiaSKPOpt
	Freia8VSB
)

// IsCable reports whether the system takes the cable mixer and IF path: DVB-C
// at either width, and the 5.6 Msym/s J.83B of Japan's optical distribution.
func (system FreiaSystem) IsCable() bool {
	return system == FreiaCable6 || system == FreiaCable8 || system == FreiaSKPOpt
}

// IsDVBTT2 reports the DVB-T/T2 rows, which select a different overload bit
// and analogue-block table.
func (system FreiaSystem) IsDVBTT2() bool {
	return system >= FreiaDVBT5 && system <= FreiaDVBT2_8
}

// FreiaSystemFor maps a delivery system and channel width onto the tuner's own
// enumeration. The satellite systems are absent: a Freia's satellite side is
// tuned by its own sequence, which takes the frequency rather than a table row.
func FreiaSystemFor(system System, bandwidthMHz uint32) (FreiaSystem, error) {
	switch system {
	case SystemDVBC:
		if bandwidthMHz == 6 {
			return FreiaCable6, nil
		}
		return FreiaCable8, nil
	case SystemDVBT:
		switch bandwidthMHz {
		case 5:
			return FreiaDVBT5, nil
		case 6:
			return FreiaDVBT6, nil
		case 7:
			return FreiaDVBT7, nil
		default:
			return FreiaDVBT8, nil
		}
	case SystemDVBT2:
		switch bandwidthMHz {
		case 1:
			return FreiaDVBT2_1_7, nil
		case 5:
			return FreiaDVBT2_5, nil
		case 6:
			return FreiaDVBT2_6, nil
		case 7:
			return FreiaDVBT2_7, nil
		default:
			return FreiaDVBT2_8, nil
		}
	case SystemISDBT:
		switch bandwidthMHz {
		case 7:
			return FreiaISDBT7, nil
		case 8:
			return FreiaISDBT8, nil
		default:
			return FreiaISDBT6, nil
		}
	case SystemATSC:
		return Freia8VSB, nil
	case SystemJ83B:
		return FreiaSKPOpt, nil
	default:
		return 0, ErrNoSystem
	}
}

// FreiaSystem is what the demodulator is set to, in the tuner's terms. It
// needs Bandwidth to have been filled in by Wakeup.
func (core *Core) FreiaSystem() (FreiaSystem, error) {
	return FreiaSystemFor(core.System, core.Bandwidth)
}

// ErrFreiaNotReady reports that the tuner's CPU status register did not read
// back clear, which is how both ports find a core that has not booted.
var ErrFreiaNotReady = errors.New("Freia tuner did not come out of boot")

// FreiaTuneBody composes the eleven bytes at the head of the tuning packet
// written to tuner register 0x68; each port appends a trailer of its own.
//
// ifOverload is passed in rather than taken from the parameter row because the
// ports differ: one always sets the 0x30 bits, the other leaves them clear for
// DVB-T and DVB-T2. Both start from FreiaIFOverload's three-bit value.
func FreiaTuneBody(param FreiaParam, frequencyKHz uint32, ifOverload byte) []byte {
	return []byte{
		0x00,
		TunerRFGainByte(param.RFGain, param.IFBPFGain),
		0x00, // internal RF AGC in normal operation
		param.RFOverload[TunerOverloadIndex(frequencyKHz)] & 0x0f,
		ifOverload,
		param.IFBPFCenter<<4&0x30 | param.Bandwidth&0x03,
		param.IFOffset & 0x1f,
		param.BandwidthOff & 0x1f,
		byte(frequencyKHz), byte(frequencyKHz >> 8), byte(frequencyKHz>>16) & 0x1f,
	}
}

// FreiaIFOverload is the parameter row's IF overload level for a frequency,
// masked the way the tuning packet takes it. The caller adds whichever of the
// 0x30 bits its port sets.
func FreiaIFOverload(param FreiaParam, frequencyKHz uint32) byte {
	return param.IFOverload[TunerOverloadIndex(frequencyKHz)] & 0x07
}

// TwosComplement reads a value of bitlen bits as signed; the gain compensation
// nibbles both tuner cores report come back through it.
func TwosComplement(value byte, bitlen uint) int32 {
	if bitlen == 0 || bitlen >= 8 {
		return int32(value)
	}
	if value&(1<<(bitlen-1)) != 0 {
		return int32(value) - (1 << bitlen)
	}
	return int32(value)
}
