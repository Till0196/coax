// Copyright (c) Sony; TurboSight; 2026 Till0196
// SPDX-License-Identifier: GPL-2.0-or-later
//
// How the chip is brought up in each satellite system, and how a retune within
// one is handled. The two Japanese standards and the two DVB ones share the
// whole satellite path and differ only in the byte selecting the demodulator
// core, in what must be told a symbol rate, and in ISDB-S3 carrying TLV. The
// terrestrial and cable systems live in the sony package.

package cxd2857er

import (
	"fmt"
	"time"

	"github.com/Till0196/coax/internal/coaxd/demod/sony"
)

// The core selector, SLVX register 0x17.
const (
	coreDVBS   = 0x0a
	coreDVBS2  = 0x0b
	coreISDBS  = 0x0c
	coreISDBS3 = 0x0d
)

// symbolRateRegister renders a symbol rate for bank 0xAE register 0x20: Msym/s
// times 16384, in twenty bits. The arithmetic rounds at the kilosymbol, and the
// ISDB-S constant in this package comes out of it exactly.
func symbolRateRegister(symbolRateSPS uint32) ([]byte, error) {
	if symbolRateSPS == 0 {
		return nil, fmt.Errorf("DVB-S2 needs a symbol rate; the channel gave none")
	}
	value := (uint64(symbolRateSPS/1000)*16384 + 500) / 1000
	if value > 0x0fffff {
		return nil, fmt.Errorf("a symbol rate of %d is too high for this chip", symbolRateSPS)
	}
	return []byte{byte(value >> 16 & 0x0f), byte(value >> 8), byte(value)}, nil
}

// wakeupDVBS is the SLtoADS transition and wakeupDVBS2 the SLtoADS2 one,
// differing only in the core selector and the symbol rate.
func (demod *Demod) wakeupDVBS(system sony.System, symbolRateSPS uint32) error {
	core := byte(coreDVBS)
	rate := []byte{0x07, 0x37, 0x0a} // what SLtoADS writes, unchanged
	if system == sony.SystemDVBS2 {
		core = coreDVBS2
		written, err := symbolRateRegister(symbolRateSPS)
		if err != nil {
			return err
		}
		rate = written
	}
	if err := demod.SetTSClockMode(system); err != nil {
		return err
	}
	return demod.Run([]sony.Op{
		{Where: sony.SLVX, Register: 0x00, Value: 0x00},
		{Where: sony.SLVX, Register: 0x17, Value: core},
		{Where: sony.SLVT, Register: 0x00, Value: 0x00},
		{Where: sony.SLVT, Register: 0x2d, Value: 0x00},
		{Where: sony.SLVT, Register: 0xa9, Value: 0x00},
		{Where: sony.SLVT, Register: 0x2c, Value: 0x01},
		{Where: sony.SLVX, Register: 0x28, Value: 0x31},
		{Where: sony.SLVT, Register: 0x4b, Value: 0x31},
		{Where: sony.SLVT, Register: 0x6a, Value: 0x00},
		{Where: sony.SLVX, Register: 0x18, Value: 0x00},
		{Where: sony.SLVT, Register: 0x00, Value: 0x00},
		{Where: sony.SLVT, Register: 0x20, Value: 0x01},
		{Where: sony.SLVT, Register: 0xce, Data: []byte{0x00, 0x00}},
		{Where: sony.SLVT, Register: 0x00, Value: 0xae},
		{Where: sony.SLVT, Register: 0x20, Data: rate},
		{Where: sony.SLVT, Register: 0x00, Value: 0xa0},
		{Where: sony.SLVT, Register: 0xd7, Value: 0x00},
		{Where: sony.SLVT, Register: 0x00, Value: 0x00},
		// Written plainly rather than masked, as SLtoADS and SLtoADS2 want
		// here; the ISDB-S sequence beside this one masks instead.
		{Where: sony.SLVT, Register: 0x80, Value: 0x30},
		{Where: sony.SLVT, Register: 0x81, Value: 0x00},
	})
}

// retuneDVBS is what a move inside DVB-S2 needs, which is the same nudge the
// Japanese standards take plus the new symbol rate.
func (demod *Demod) retuneDVBS(system sony.System, symbolRateSPS uint32) error {
	if system == sony.SystemDVBS2 {
		rate, err := symbolRateRegister(symbolRateSPS)
		if err != nil {
			return err
		}
		if err := demod.Run([]sony.Op{
			{Where: sony.SLVT, Register: 0x00, Value: 0xae},
			{Where: sony.SLVT, Register: 0x20, Data: rate},
		}); err != nil {
			return err
		}
	}
	return demod.Run([]sony.Op{
		{Where: sony.SLVT, Register: 0x00, Value: 0x00},
		{Where: sony.SLVT, Register: 0xc3, Value: 0x01},
	})
}

// The satellite half of the chip, which the terrestrial cards never reach.
// Switching the Freia tuner between its terrestrial and satellite sides is not
// just a different frequency: the tuner goes through a handover sequence and
// the demodulator is woken into a different mode with its own register set.
// ISDB-S3 additionally carries MMT/TLV.
//
// Ported from the driver for the TBS 6812 (a.k.a. PT4K).

// tunerSide is which half of the two-sided Freia tuner is in use. The handover
// between them has to be done once, on the way across, not on every tune.
type tunerSide int

const (
	tunerSideNone tunerSide = iota
	tunerSideTerrestrial
	tunerSideSatellite
)

// isdbsSymbolRate and isdbs3SymbolRate are fixed by the standards: ISDB-S is
// 28.86 MBaud, ISDB-S3 33.75 MBaud. The chip takes them at bank 0xAE register
// 0x20, three bytes, megasymbols per second times 16384. Only DVB-S2 has to be
// told its rate.
const (
	isdbsSymbolRate  = 28_860_000
	isdbs3SymbolRate = 33_750_000
)

// wakeupISDBS is the SLtoAIS transition.
func (demod *Demod) wakeupISDBS() error {
	if err := demod.SetTSClockMode(sony.SystemISDBS); err != nil {
		return err
	}
	return demod.Run([]sony.Op{
		{Where: sony.SLVX, Register: 0x00, Value: 0x00},
		{Where: sony.SLVX, Register: 0x17, Value: 0x0c},
		{Where: sony.SLVT, Register: 0x00, Value: 0x00},
		{Where: sony.SLVT, Register: 0x2d, Value: 0x00},
		{Where: sony.SLVT, Register: 0xa9, Value: 0x00},
		{Where: sony.SLVT, Register: 0x2c, Value: 0x01},
		{Where: sony.SLVX, Register: 0x28, Value: 0x31},
		{Where: sony.SLVT, Register: 0x4b, Value: 0x31},
		{Where: sony.SLVT, Register: 0x6a, Value: 0x00},
		{Where: sony.SLVX, Register: 0x18, Value: 0x00},
		{Where: sony.SLVT, Register: 0x00, Value: 0x00},
		{Where: sony.SLVT, Register: 0x20, Value: 0x01},
		{Where: sony.SLVT, Register: 0xce, Data: []byte{0x00, 0x00}},
		{Where: sony.SLVT, Register: 0x00, Value: 0xae},
		{Where: sony.SLVT, Register: 0x20, Data: []byte{0x07, 0x37, 0x0a}},
		{Where: sony.SLVT, Register: 0x00, Value: 0xa0},
		{Where: sony.SLVT, Register: 0xd7, Value: 0x00},
		{Where: sony.SLVT, Register: 0x00, Value: 0x00},
		{Where: sony.SLVT, Register: 0x80, Value: 0x10, Mask: 0x1f},
	})
}

// wakeupISDBS3 is the SLtoAIS3 transition, with the output left in TLV mode.
// TLV is not optional in practice: ISDB-S3 carries MMT/TLV, which TS framing
// does not describe.
func (demod *Demod) wakeupISDBS3() error {
	// The mode transition is refused unless this bit is set first.
	if err := demod.Run([]sony.Op{
		{Where: sony.SLVT, Register: 0x00, Value: 0xa0},
		{Where: sony.SLVT, Register: 0xb9, Value: 0x01, Mask: 0x01},
	}); err != nil {
		return err
	}
	if err := demod.setTLVClockMode(); err != nil {
		return err
	}
	return demod.Run([]sony.Op{
		{Where: sony.SLVX, Register: 0x00, Value: 0x00},
		{Where: sony.SLVX, Register: 0x17, Value: 0x0d},
		{Where: sony.SLVT, Register: 0x00, Value: 0x00},
		{Where: sony.SLVT, Register: 0x2d, Value: 0x00},
		// 0xA9 is OREG_TSTLVSEL: the output carries TLV rather than TS.
		{Where: sony.SLVT, Register: 0xa9, Value: 0x01},
		{Where: sony.SLVT, Register: 0x2c, Value: 0x01},
		{Where: sony.SLVX, Register: 0x28, Value: 0x31},
		{Where: sony.SLVT, Register: 0x4b, Value: 0x31},
		{Where: sony.SLVT, Register: 0x6a, Value: 0x00},
		{Where: sony.SLVX, Register: 0x18, Value: 0x00},
		{Where: sony.SLVT, Register: 0x00, Value: 0x00},
		{Where: sony.SLVT, Register: 0x20, Value: 0x01},
		{Where: sony.SLVT, Register: 0x00, Value: 0xa3},
		{Where: sony.SLVT, Register: 0x43, Data: []byte{0x0b, 0x0b}},
		{Where: sony.SLVT, Register: 0x00, Value: 0xae},
		{Where: sony.SLVT, Register: 0x46, Data: []byte{0x04, 0x91}},
		{Where: sony.SLVT, Register: 0x00, Value: 0xb6},
		{Where: sony.SLVT, Register: 0x74, Value: 0x59},
		{Where: sony.SLVT, Register: 0x00, Value: 0xd5},
		{Where: sony.SLVT, Register: 0x61, Data: []byte{0x48, 0xfe, 0x4e, 0x6e, 0xfe}},
		{Where: sony.SLVT, Register: 0x67, Data: []byte{0x4e, 0x6e}},
		{Where: sony.SLVT, Register: 0x90, Data: []byte{0x10, 0x03, 0x10}},
		{Where: sony.SLVT, Register: 0x00, Value: 0xd6},
		{Where: sony.SLVT, Register: 0x18, Data: []byte{0x33, 0xeb}},
		{Where: sony.SLVT, Register: 0x1c, Value: 0x59},
		{Where: sony.SLVT, Register: 0x1e, Value: 0x6e},
		{Where: sony.SLVT, Register: 0x21, Data: []byte{0x4e, 0x80, 0x80, 0x10}},
		{Where: sony.SLVT, Register: 0x51, Data: []byte{0xb3, 0xb3, 0xc3}},
		{Where: sony.SLVT, Register: 0x00, Value: 0xda},
		{Where: sony.SLVT, Register: 0xbc, Value: 0x02},
		{Where: sony.SLVT, Register: 0xd6, Data: []byte{0x60, 0x70}},
		{Where: sony.SLVT, Register: 0x00, Value: 0xae},
		{Where: sony.SLVT, Register: 0x20, Data: []byte{0x08, 0x70, 0x64}},
		{Where: sony.SLVT, Register: 0x00, Value: 0xda},
		{Where: sony.SLVT, Register: 0xcb, Value: 0x01},
		{Where: sony.SLVT, Register: 0xc1, Value: 0x01},
		{Where: sony.SLVT, Register: 0x00, Value: 0xa0},
		{Where: sony.SLVT, Register: 0xd7, Value: 0x00},
		{Where: sony.SLVT, Register: 0x00, Value: 0x00},
		{Where: sony.SLVT, Register: 0x80, Value: 0x10, Mask: 0x1f},
	})
}

// setTLVClockMode is the TLV counterpart of setTSClockMode, for the parallel
// output these boards are wired for. The clock configuration is indexed by
// wiring and clock rate; the parallel entry is a clock period of 2 at the
// highest interface rate. The serial and two-bit-parallel branches are not
// ported.
func (demod *Demod) setTLVClockMode() error {
	const tlvClkPeriod, clkSelTLVIf = 0x02, 0x04

	return demod.Run([]sony.Op{
		{Where: sony.SLVT, Register: 0x00, Value: 0x01},
		// Parallel TLV: the serial enable and the two-bit parallel select are
		// both cleared, and 0xE7 is set because the output is not serial.
		{Where: sony.SLVT, Register: 0xe7, Value: 0x01, Mask: 0x01},
		{Where: sony.SLVT, Register: 0xc1, Value: 0x00, Mask: 0x80},
		{Where: sony.SLVT, Register: 0xcf, Value: 0x00, Mask: 0x01},
		{Where: sony.SLVT, Register: 0x00, Value: 0x56},
		{Where: sony.SLVT, Register: 0x83, Value: clkSelTLVIf, Mask: 0x07},
		// The interface clock is stopped while its rate changes.
		{Where: sony.SLVT, Register: 0x00, Value: 0x00},
		{Where: sony.SLVT, Register: 0x32, Value: 0x00, Mask: 0x01},
		{Where: sony.SLVT, Register: 0x33, Value: tlvClkPeriod, Mask: 0x03},
		{Where: sony.SLVT, Register: 0x32, Value: 0x01, Mask: 0x01},
	})
}

// setISDBSStreamID names the transport stream to demodulate out of an ISDB-S
// carrier, which multiplexes several. The third byte is the relative stream
// number: a value below 8 is taken as a relative number rather than a TSID.
//
// Written and not read back, which is safe here in a way it is not on a
// TC90522. Measured on a TBS 6812: asked for a TSID the transponder does not
// carry, it reported carrier and never lock and emitted nothing. A stream not
// on the carrier is thus a lock failure.
func (demod *Demod) setISDBSStreamID(streamID uint32) error {
	relative := byte(0)
	if streamID < 8 {
		relative = 1
	}
	return demod.Run([]sony.Op{
		{Where: sony.SLVT, Register: 0x00, Value: 0xc0},
		{Where: sony.SLVT, Register: 0xe9, Data: []byte{
			byte(streamID >> 8), byte(streamID), relative,
		}},
	})
}

// setISDBS3StreamID names the stream to demodulate out of an ISDB-S3 carrier.
func (demod *Demod) setISDBS3StreamID(streamID uint32) error {
	return demod.Run([]sony.Op{
		{Where: sony.SLVT, Register: 0x00, Value: 0xd0},
		{Where: sony.SLVT, Register: 0x87, Data: []byte{byte(streamID >> 8), byte(streamID)}},
	})
}

// IsISDBS3StreamID reports whether a stream identifier names an ISDB-S3 rather
// than an ISDB-S carrier. The two share a numbering space and are told apart by
// the network in the top nibble, which decides which mode to wake into when
// there is no system to go with it.
func IsISDBS3StreamID(streamID uint32) bool {
	network := (streamID >> 12) & 0xf
	return network == 0xb || network == 0xc
}

// tuneSatellite points the tuner's satellite side at a frequency, in kHz --
// unlike the terrestrial side, which converts to MHz before writing.
func (demod *Demod) tuneSatellite(frequencyKHz uint32, system sony.System) error {
	tuner := demod.Tuner()

	// Coming from the terrestrial side, the tuner is handed over first, once.
	if demod.side == tunerSideTerrestrial {
		if err := tuner.Write(0x74, 0x02); err != nil {
			return err
		}
		if err := tuner.WriteMasked(0x67, 0x00, 0xfe); err != nil {
			return err
		}
		if err := tuner.WriteMulti(0x5e, []byte{0x15, 0x00, 0x00}); err != nil {
			return err
		}
		if err := tuner.Write(0x88, 0x00); err != nil {
			return err
		}
		if err := tuner.Write(0x87, 0xc0); err != nil {
			return err
		}
	}
	demod.side = tunerSideSatellite

	if err := tuner.Write(0x15, 0x12); err != nil {
		return err
	}
	if err := tuner.WriteMulti(0x6a, []byte{0x00, 0x00}); err != nil {
		return err
	}
	if err := tuner.WriteMulti(0x74, []byte{0x12, 0xf9, 0x0f, 0x25, 0x44}); err != nil {
		return err
	}
	for _, one := range []struct{ register, value byte }{
		{0x75, 0xf9}, {0x40, 0x07}, {0x41, 0x07}, {0x45, 0x03}, {0x48, 0x07},
	} {
		if err := tuner.Write(one.register, one.value); err != nil {
			return err
		}
	}

	reference, err := sony.FreiaReference(demod.Config().TunerXtal)
	if err != nil {
		return err
	}
	settings := []byte{0xc4, 0x40, reference, 0x80, 0x70, 0x1e, 0x02, 0x24}
	if system == sony.SystemISDBS3 {
		copy(settings[3:], []byte{0x00, 0xb4, 0x78, 0x08, 0x30})
	}
	if err := tuner.WriteMulti(0x04, settings); err != nil {
		return err
	}

	// The frequency register counts in units of 4 kHz, rounded.
	step := (frequencyKHz + 2) / 4
	// Above 2150 MHz an ISDB-S3 carrier is taken through the tuner's other
	// path, which needs a different pre-load before the frequency is written.
	high := frequencyKHz > 2_150_000 && system == sony.SystemISDBS3
	preload := []byte{0xfe, 0x22, 0x9e, 0x16, 0x00, 0x00, 0x00, 0xff, 0x00, 0x01}
	if high {
		if err := tuner.Write(0x45, 0x02); err != nil {
			return err
		}
		if err := tuner.Write(0x01, 0x03); err != nil {
			return err
		}
		preload = []byte{0xfc, 0x32, 0x9e, 0x16, 0x00, 0x00, 0x00, 0xff, 0x00, 0x01}
	} else {
		if err := tuner.Write(0x43, 0x04); err != nil {
			return err
		}
		if err := tuner.Write(0x01, 0x01); err != nil {
			return err
		}
	}
	preload[4] = byte(step)
	preload[5] = byte(step >> 8)
	preload[6] = byte(step>>16) & 0x0f
	if err := tuner.WriteMulti(0x0c, preload); err != nil {
		return err
	}

	// Settling time the tuner needs before the two writes below release it.
	time.Sleep(10 * time.Millisecond)
	if err := tuner.Write(0x05, 0x00); err != nil {
		return err
	}
	return tuner.Write(0x04, 0xc0)
}

// satelliteLevel reports the satellite input level in dBm: the demodulator's IF
// AGC reading, 1.4 dB of level per 4.03 counts, measured down from the chip's
// -97 dBm reference.
func (demod *Demod) satelliteLevel() (float64, error) {
	if err := demod.Write(sony.SLVT, 0x00, 0xa0); err != nil {
		return 0, err
	}
	data, err := demod.Read(sony.SLVT, 0x1f, 2)
	if err != nil {
		return 0, err
	}
	ifAGC := int32(data[0]&0x1f)<<8 | int32(data[1])
	// Hundredths of a dB, rounded to nearest.
	hundredths := -divideRound(ifAGC*1400, 403)
	return -(float64(hundredths)/100 + 97), nil
}

// divideRound divides two positive integers and rounds the result to nearest.
func divideRound(value, by int32) int32 {
	return (value + by/2) / by
}
