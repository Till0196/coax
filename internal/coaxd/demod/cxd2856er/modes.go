// Copyright (c) 2018-2021 nns779; 2026 Till0196
// SPDX-License-Identifier: GPL-2.0-only
//
// The ISDB-T and ISDB-S sequences are from px4_drv (cxd2856er.c) by nns779.

package cxd2856er

import (
	"errors"
	"fmt"

	"github.com/Till0196/coax/internal/coaxd/demod/sony"
)

// demodMode is the mode-select register value for a system; switching it is
// what changes which demodulator core runs.
func demodMode(system sony.System) (byte, error) {
	switch system {
	case sony.SystemISDBT:
		return 0x06, nil
	case sony.SystemISDBS:
		return 0x0c, nil
	case sony.SystemDVBC:
		return 0x04, nil
	case sony.SystemDVBT:
		return 0x07, nil
	case sony.SystemDVBT2:
		return 0x09, nil
	case sony.SystemDVBS:
		return 0x0a, nil
	case sony.SystemDVBS2:
		return 0x0b, nil
	case sony.SystemJ83B:
		return 0x08, nil
	default:
		return 0, sony.ErrNoSystem
	}
}

// j83 reports whether a system is one of the ITU-T J.83 single-carrier QAM
// systems, which share one demodulator core. DVB-C is Annex A, and Annex C --
// what ISDB-C is -- runs on the same core; Annex B is the trellis-coded one.
func j83(system sony.System) bool {
	return system == sony.SystemDVBC || system == sony.SystemJ83B
}

// defaultIFHz is the IF the tuner is expected to deliver, in Hz. The CXD2858ER
// does not report one, so these are the fallbacks; a board with a different IF
// says so via Config.
func defaultIFHz(system sony.System, bandwidthMHz uint32) uint32 {
	switch {
	case system == sony.SystemISDBT:
		return 3_600_000
	case j83(system):
		// The tuner's 6 MHz cable setting (IF offset 0x1A, bandwidth offset
		// 0x1C) puts the channel at 3.7 MHz, the IF the family's own DVB-C
		// table pairs with it.
		return 3_700_000
	case system == sony.SystemDVBT || system == sony.SystemDVBT2:
		switch bandwidthMHz {
		case 8:
			return 4_800_000
		case 7:
			return 4_200_000
		default:
			return 3_600_000
		}
	}
	return 0
}

// ifFrequencyRegisters converts an intermediate frequency to the three bytes
// the chip's IF register takes.
func ifFrequencyRegisters(ifHz, xtalKHz uint32) [3]byte {
	sampling := uint64(41_000_000)
	if xtalKHz == 24000 {
		sampling = 48_000_000
	}
	value := uint64(ifHz) * 16777216 / sampling
	return [3]byte{byte(value >> 16 & 0xff), byte(value >> 8 & 0xff), byte(value & 0xff)}
}

// terrestrialBand holds one terrestrial system's per-bandwidth filter settings.
// The intermediate frequency is absent on purpose: it is computed.
type terrestrialBand struct {
	nominalRate  []byte
	itbCoef      []byte
	channelWidth byte
	// trl is the timing recovery setting at register 0xd9, absent for DVB-T2.
	trl []byte
	// bank17 is the DVB-T pair written to bank 0x17 register 0x38.
	bank17 []byte
}

// isdbtBands defines the only channel width ISDB-T uses, 6 MHz.
//
// The nominal rate register is "TRCG Nominal Rate [37:0]", a 38-bit fixed-point
// number equal to 8 * crystal / symbol rate, both in MHz. ISDB-T on a 24 MHz
// crystal is 8 * 24 / (512/63) = 23.625 exactly, which is 0x17A0000000. The
// nearby 0x17A0800000 turns up in the wild; the difference is not rounding but
// 82.7 ppm of timing error.
var isdbtBands = map[uint32]terrestrialBand{
	6: {
		nominalRate: []byte{0x17, 0xa0, 0x00, 0x00, 0x00},
		itbCoef: []byte{
			0x31, 0xa8, 0x29, 0x9b, 0x27, 0x9c, 0x28,
			0x9e, 0x29, 0xa4, 0x29, 0xa2, 0x29, 0xa8,
		},
		channelWidth: 0x04,
		trl:          []byte{0x1f, 0x79},
	},
}

// dvbtBands and dvbt2Bands hold the 20.5 MHz column, not the 24 MHz one this
// part runs at: the 6 MHz nominal rate here is 0x17EAAAAAAA where a 24 MHz part
// wants 0x1C00000000, and the channel filters are the 20.5 MHz ones too. A 24
// MHz crystal also needs writes with no counterpart here.
//
// Nothing uses these paths: the PX-MLT boards this driver serves are used for
// ISDB, and correcting the tables alone would not make DVB-T/T2 work. ISDB-T is
// checked against the formula in modes_test.go.
var dvbtBands = map[uint32]terrestrialBand{
	6: {
		nominalRate: []byte{0x17, 0xea, 0xaa, 0xaa, 0xaa},
		itbCoef: []byte{
			0x27, 0xa7, 0x28, 0xb3, 0x02, 0xf0, 0x01,
			0xe8, 0x00, 0xcf, 0x00, 0xe6, 0x23, 0xa4,
		},
		channelWidth: 0x04,
		trl:          []byte{0x1f, 0xdc},
		bank17:       []byte{0x00, 0x03},
	},
	7: {
		nominalRate: []byte{0x14, 0x80, 0x00, 0x00, 0x00},
		itbCoef: []byte{
			0x2c, 0xbd, 0x02, 0xcf, 0x04, 0xf8, 0x23,
			0xa6, 0x29, 0xb0, 0x26, 0xa9, 0x21, 0xa5,
		},
		channelWidth: 0x02,
		trl:          []byte{0x12, 0xf8},
		bank17:       []byte{0x00, 0x03},
	},
	8: {
		nominalRate: []byte{0x11, 0xf0, 0x00, 0x00, 0x00},
		itbCoef: []byte{
			0x26, 0xaf, 0x06, 0xcd, 0x13, 0xbb, 0x28,
			0xba, 0x23, 0xa9, 0x1f, 0xa8, 0x2c, 0xc8,
		},
		channelWidth: 0x00,
		trl:          []byte{0x01, 0xe0},
		bank17:       []byte{0x01, 0x02},
	},
}

// dvbt2Bands differ from DVB-T in where the nominal rate goes: bank 0x20
// rather than bank 0x10.
var dvbt2Bands = map[uint32]terrestrialBand{
	6: {
		nominalRate: []byte{0x17, 0xea, 0xaa, 0xaa, 0xaa},
		itbCoef: []byte{
			0x27, 0xa7, 0x28, 0xb3, 0x02, 0xf0, 0x01,
			0xe8, 0x00, 0xcf, 0x00, 0xe6, 0x23, 0xa4,
		},
		channelWidth: 0x04,
	},
	7: {
		nominalRate: []byte{0x14, 0x80, 0x00, 0x00, 0x00},
		itbCoef: []byte{
			0x2c, 0xbd, 0x02, 0xcf, 0x04, 0xf8, 0x23,
			0xa6, 0x29, 0xb0, 0x26, 0xa9, 0x21, 0xa5,
		},
		channelWidth: 0x02,
	},
	8: {
		nominalRate: []byte{0x11, 0xf0, 0x00, 0x00, 0x00},
		itbCoef: []byte{
			0x26, 0xaf, 0x06, 0xcd, 0x13, 0xbb, 0x28,
			0xba, 0x23, 0xa9, 0x1f, 0xa8, 0x2c, 0xc8,
		},
		channelWidth: 0x00,
	},
}

// j83ITBCoef is the J.83 channel filter, which does not vary with the channel
// width the way the terrestrial ones do.
var j83ITBCoef = []byte{
	0x27, 0xa7, 0x28, 0xb3, 0x02, 0xf0, 0x01,
	0xe8, 0x00, 0xcf, 0x00, 0xe6, 0x23, 0xa4,
}

func (demod *Demod) setBandwidthDVBT(bandwidthMHz uint32) error {
	band, ok := dvbtBands[bandwidthMHz]
	if !ok {
		return fmt.Errorf("CXD2856ER does not support a %d MHz DVB-T channel", bandwidthMHz)
	}
	ifFreq := ifFrequencyRegisters(defaultIFHz(sony.SystemDVBT, bandwidthMHz), demod.Config().XtalKHz())
	return demod.Run([]sony.Op{
		{Where: sony.SLVT, Register: 0x00, Value: 0x13},
		{Where: sony.SLVT, Register: 0x9c, Value: 0x01},
		{Where: sony.SLVT, Register: 0x9d, Value: 0x14},
		{Where: sony.SLVT, Register: 0x00, Value: 0x10},
		{Where: sony.SLVT, Register: 0x9f, Data: band.nominalRate},
		{Where: sony.SLVT, Register: 0xa6, Data: band.itbCoef},
		{Where: sony.SLVT, Register: 0xb6, Data: ifFreq[:]},
		{Where: sony.SLVT, Register: 0xd7, Value: band.channelWidth, Mask: 0x07},
		{Where: sony.SLVT, Register: 0xd9, Data: band.trl},
		{Where: sony.SLVT, Register: 0x00, Value: 0x17},
		{Where: sony.SLVT, Register: 0x38, Data: band.bank17},
	})
}

func (demod *Demod) setBandwidthDVBT2(bandwidthMHz uint32) error {
	band, ok := dvbt2Bands[bandwidthMHz]
	if !ok {
		return fmt.Errorf("CXD2856ER does not support a %d MHz DVB-T2 channel", bandwidthMHz)
	}
	ifFreq := ifFrequencyRegisters(defaultIFHz(sony.SystemDVBT2, bandwidthMHz), demod.Config().XtalKHz())
	return demod.Run([]sony.Op{
		{Where: sony.SLVT, Register: 0x00, Value: 0x20},
		{Where: sony.SLVT, Register: 0x9f, Data: band.nominalRate},
		{Where: sony.SLVT, Register: 0x00, Value: 0x27},
		{Where: sony.SLVT, Register: 0x7a, Value: 0x00, Mask: 0x0f},
		{Where: sony.SLVT, Register: 0x00, Value: 0x10},
		{Where: sony.SLVT, Register: 0xa6, Data: band.itbCoef},
		{Where: sony.SLVT, Register: 0xb6, Data: ifFreq[:]},
		{Where: sony.SLVT, Register: 0xd7, Value: band.channelWidth, Mask: 0x07},
	})
}

func (demod *Demod) setBandwidthJ83(system sony.System) error {
	ifFreq := ifFrequencyRegisters(defaultIFHz(system, 6), demod.Config().XtalKHz())
	return demod.Run([]sony.Op{
		{Where: sony.SLVT, Register: 0x00, Value: 0x10},
		{Where: sony.SLVT, Register: 0xa6, Data: j83ITBCoef},
		{Where: sony.SLVT, Register: 0xb6, Data: ifFreq[:]},
		{Where: sony.SLVT, Register: 0x00, Value: 0x11},
		{Where: sony.SLVT, Register: 0xa3, Value: 0x14},
		{Where: sony.SLVT, Register: 0x00, Value: 0x40},
		{Where: sony.SLVT, Register: 0x26, Value: 0x08, Mask: 0x0f},
		{Where: sony.SLVT, Register: 0x27, Value: 0x6e},
	})
}

// setBandwidthJ83B loads the Annex B band setting: a symbol-rate search range
// and an IF rather than a channel width. The two configurations are different
// signals -- American cable runs at 5.056941 or 5.360537 Msym/s, while Japan's
// optical distribution of the 124/128 degree satellite service runs at a fixed
// 5.6 Msym/s. sony.J83BBandFor decides.
func (demod *Demod) setBandwidthJ83B(symbolRateSPS uint32) error {
	band := sony.J83BBandFor(symbolRateSPS)
	ifFreq := ifFrequencyRegisters(uint32(band.IFMHz*1_000_000), demod.Config().XtalKHz())
	return demod.Run([]sony.Op{
		{Where: sony.SLVT, Register: 0x00, Value: 0x10},
		{Where: sony.SLVT, Register: 0xa6, Data: j83ITBCoef},
		{Where: sony.SLVT, Register: 0xb6, Data: ifFreq[:]},
		{Where: sony.SLVT, Register: 0x00, Value: 0x40},
		{Where: sony.SLVT, Register: 0x14, Value: band.Register14},
		{Where: sony.SLVT, Register: 0x26, Data: band.SymbolRate},
	})
}

// wakeupJ83B brings the chip up in Annex B, the SLtoACC transition: the J.83
// wake-up above plus the trellis core's own settings. Untested on hardware.
func (demod *Demod) wakeupJ83B(symbolRateSPS uint32) error {
	if err := demod.setTSClock(sony.SystemJ83B); err != nil {
		return err
	}
	preamble, err := demod.terrestrialPreamble(sony.SystemJ83B)
	if err != nil {
		return err
	}
	if err := demod.Run(append(preamble, []sony.Op{
		{Where: sony.SLVT, Register: 0x00, Value: 0x11},
		{Where: sony.SLVT, Register: 0x6a, Value: 0x48},
		{Where: sony.SLVT, Register: 0x00, Value: 0x10},
		{Where: sony.SLVT, Register: 0xa5, Value: 0x01},
		{Where: sony.SLVT, Register: 0x00, Value: 0x40},
		{Where: sony.SLVT, Register: 0x21, Value: 0x04},
		{Where: sony.SLVT, Register: 0xb3, Value: 0x05},
		{Where: sony.SLVT, Register: 0x1e, Value: 0x80},
		{Where: sony.SLVT, Register: 0x8e, Value: 0x58},
		{Where: sony.SLVT, Register: 0x00, Value: 0x41},
		{Where: sony.SLVT, Register: 0xcf, Value: 0x99},
		{Where: sony.SLVT, Register: 0x00, Value: 0x11},
		{Where: sony.SLVT, Register: 0xa3, Value: 0x14},
		{Where: sony.SLVT, Register: 0x33, Data: []byte{0x00, 0x03, 0x3b}},
		{Where: sony.SLVT, Register: 0x00, Value: 0x00},
		{Where: sony.SLVT, Register: 0xce, Data: []byte{0x01, 0x01}},
	}...)); err != nil {
		return err
	}
	if err := demod.setBandwidthJ83B(symbolRateSPS); err != nil {
		return err
	}
	if err := demod.Run([]sony.Op{
		{Where: sony.SLVT, Register: 0x00, Value: 0x00},
		{Where: sony.SLVT, Register: 0x80, Value: 0x08, Mask: 0x1f},
	}); err != nil {
		return err
	}
	return demod.setTSPinState(true)
}

// terrestrialPreamble is the wake-up prefix the terrestrial and J.83 systems
// share.
func (demod *Demod) terrestrialPreamble(system sony.System) ([]sony.Op, error) {
	mode, err := demodMode(system)
	if err != nil {
		return nil, err
	}
	return []sony.Op{
		{Where: sony.SLVX, Register: 0x00, Value: 0x00},
		{Where: sony.SLVX, Register: 0x17, Value: mode},
		{Where: sony.SLVT, Register: 0x00, Value: 0x00},
		{Where: sony.SLVT, Register: 0xa9, Value: 0x00},
		{Where: sony.SLVT, Register: 0x2c, Value: 0x01},
		{Where: sony.SLVT, Register: 0x4b, Value: 0x74},
		{Where: sony.SLVT, Register: 0x49, Value: 0x00},
		{Where: sony.SLVX, Register: 0x18, Value: 0x00},
	}, nil
}

// wakeupDVBT brings the chip up in DVB-T mode.
func (demod *Demod) wakeupDVBT(bandwidthMHz uint32) error {
	if err := demod.setTSClock(sony.SystemDVBT); err != nil {
		return err
	}
	preamble, err := demod.terrestrialPreamble(sony.SystemDVBT)
	if err != nil {
		return err
	}
	if err := demod.Run(append(preamble, []sony.Op{
		{Where: sony.SLVT, Register: 0x00, Value: 0x11},
		{Where: sony.SLVT, Register: 0x6a, Value: 0x50},
		{Where: sony.SLVT, Register: 0x00, Value: 0x10},
		{Where: sony.SLVT, Register: 0xa5, Value: 0x01},
		{Where: sony.SLVT, Register: 0x00, Value: 0x00},
		{Where: sony.SLVT, Register: 0xce, Data: []byte{0x00, 0x00}},
		{Where: sony.SLVT, Register: 0x00, Value: 0x10},
		{Where: sony.SLVT, Register: 0x69, Value: 0x04},
		{Where: sony.SLVT, Register: 0x6b, Value: 0x03},
		{Where: sony.SLVT, Register: 0x9d, Value: 0x50},
		{Where: sony.SLVT, Register: 0xd3, Value: 0x06},
		{Where: sony.SLVT, Register: 0xed, Value: 0x00},
		{Where: sony.SLVT, Register: 0xe2, Value: 0xce},
		{Where: sony.SLVT, Register: 0xf2, Value: 0x13},
		{Where: sony.SLVT, Register: 0xde, Value: 0x2e},
		{Where: sony.SLVT, Register: 0x00, Value: 0x15},
		{Where: sony.SLVT, Register: 0xde, Value: 0x02},
		{Where: sony.SLVT, Register: 0x00, Value: 0x17},
		{Where: sony.SLVT, Register: 0x38, Data: []byte{0x00, 0x03}},
		{Where: sony.SLVT, Register: 0x00, Value: 0x1e},
		{Where: sony.SLVT, Register: 0x73, Value: 0x68},
		{Where: sony.SLVT, Register: 0x00, Value: 0x63},
		{Where: sony.SLVT, Register: 0x81, Value: 0x00},
		{Where: sony.SLVT, Register: 0x00, Value: 0x11},
		{Where: sony.SLVT, Register: 0x33, Data: []byte{0x00, 0x03, 0x3b}},
		{Where: sony.SLVT, Register: 0x00, Value: 0x60},
		{Where: sony.SLVT, Register: 0xa8, Data: []byte{0xb7, 0x1b}},
	}...)); err != nil {
		return err
	}
	if err := demod.setBandwidthDVBT(bandwidthMHz); err != nil {
		return err
	}
	if err := demod.Run([]sony.Op{
		{Where: sony.SLVT, Register: 0x00, Value: 0x00},
		{Where: sony.SLVT, Register: 0x80, Value: 0x28},
		{Where: sony.SLVT, Register: 0x81, Value: 0x00},
	}); err != nil {
		return err
	}
	return demod.setTSPinState(true)
}

// wakeupDVBT2 brings the chip up in DVB-T2 mode. plpID selects one physical
// layer pipe, or -1 to let the demodulator choose.
func (demod *Demod) wakeupDVBT2(bandwidthMHz uint32, plpID int) error {
	if err := demod.setTSClock(sony.SystemDVBT2); err != nil {
		return err
	}
	preamble, err := demod.terrestrialPreamble(sony.SystemDVBT2)
	if err != nil {
		return err
	}
	if err := demod.Run(append(preamble, []sony.Op{
		{Where: sony.SLVT, Register: 0x00, Value: 0x11},
		{Where: sony.SLVT, Register: 0x6a, Value: 0x50},
		{Where: sony.SLVT, Register: 0x00, Value: 0x10},
		{Where: sony.SLVT, Register: 0xa5, Value: 0x01},
		{Where: sony.SLVT, Register: 0x00, Value: 0x20},
		{Where: sony.SLVT, Register: 0x8b, Value: 0x3c},
		{Where: sony.SLVT, Register: 0x00, Value: 0x2b},
		{Where: sony.SLVT, Register: 0x76, Value: 0x20, Mask: 0x70},
		{Where: sony.SLVT, Register: 0x00, Value: 0x00},
		{Where: sony.SLVT, Register: 0xce, Data: []byte{0x00, 0x00}},
		{Where: sony.SLVT, Register: 0x00, Value: 0x13},
		{Where: sony.SLVT, Register: 0x83, Value: 0x10},
		{Where: sony.SLVT, Register: 0x86, Value: 0x34},
		{Where: sony.SLVT, Register: 0x9e, Value: 0x09, Mask: 0x0f},
		{Where: sony.SLVT, Register: 0x9f, Value: 0xd8},
		{Where: sony.SLVT, Register: 0x00, Value: 0x2a},
		{Where: sony.SLVT, Register: 0x38, Value: 0x04, Mask: 0x0f},
		{Where: sony.SLVT, Register: 0x00, Value: 0x2b},
		{Where: sony.SLVT, Register: 0x11, Value: 0x20, Mask: 0x3f},
	}...)); err != nil {
		return err
	}
	if err := demod.setBandwidthDVBT2(bandwidthMHz); err != nil {
		return err
	}
	if err := demod.setPLPLocked(plpID); err != nil {
		return err
	}
	if err := demod.Run([]sony.Op{
		{Where: sony.SLVT, Register: 0x00, Value: 0x00},
		{Where: sony.SLVT, Register: 0x80, Value: 0x28},
		{Where: sony.SLVT, Register: 0x81, Value: 0x00},
	}); err != nil {
		return err
	}
	return demod.setTSPinState(true)
}

// setPLPLocked selects a DVB-T2 physical layer pipe without taking the lock.
func (demod *Demod) setPLPLocked(plpID int) error {
	if plpID < 0 {
		return demod.Run([]sony.Op{
			{Where: sony.SLVT, Register: 0x00, Value: 0x23},
			{Where: sony.SLVT, Register: 0xad, Value: 0x00},
		})
	}
	if plpID > 255 {
		return fmt.Errorf("CXD2856ER PLP %d is outside 0..255", plpID)
	}
	return demod.Run([]sony.Op{
		{Where: sony.SLVT, Register: 0x00, Value: 0x23},
		{Where: sony.SLVT, Register: 0xaf, Value: byte(plpID)},
		{Where: sony.SLVT, Register: 0xad, Value: 0x01},
	})
}

// SetPLP selects which DVB-T2 physical layer pipe to decode, or -1 for the
// demodulator's own choice.
func (demod *Demod) SetPLP(plpID int) error {
	demod.mu.Lock()
	defer demod.mu.Unlock()
	if demod.System != sony.SystemDVBT2 {
		return errors.New("CXD2856ER can only select a PLP in DVB-T2")
	}
	return demod.setPLPLocked(plpID)
}

// wakeupJ83 brings the chip up in one of the J.83 systems, which share
// everything but the mode-select value.
func (demod *Demod) wakeupJ83(system sony.System) error {
	if err := demod.setTSClock(system); err != nil {
		return err
	}
	preamble, err := demod.terrestrialPreamble(system)
	if err != nil {
		return err
	}
	if err := demod.Run(append(preamble, []sony.Op{
		{Where: sony.SLVT, Register: 0x00, Value: 0x10},
		{Where: sony.SLVT, Register: 0xd2, Value: 0x09, Mask: 0x1f},
		{Where: sony.SLVT, Register: 0x00, Value: 0x11},
		{Where: sony.SLVT, Register: 0x6a, Value: 0x48},
		{Where: sony.SLVT, Register: 0x00, Value: 0x10},
		{Where: sony.SLVT, Register: 0xa5, Value: 0x01},
		{Where: sony.SLVT, Register: 0x00, Value: 0x40},
		{Where: sony.SLVT, Register: 0xc3, Value: 0x00, Mask: 0x04},
		{Where: sony.SLVT, Register: 0x00, Value: 0x00},
		{Where: sony.SLVT, Register: 0xce, Value: 0x01, Mask: 0x01},
		{Where: sony.SLVT, Register: 0xcf, Value: 0x01, Mask: 0x01},
	}...)); err != nil {
		return err
	}
	if err := demod.setBandwidthJ83(system); err != nil {
		return err
	}
	if err := demod.Run([]sony.Op{
		{Where: sony.SLVT, Register: 0x00, Value: 0x00},
		{Where: sony.SLVT, Register: 0x80, Value: 0x08, Mask: 0x1f},
		{Where: sony.SLVT, Register: 0x81, Value: 0x00},
	}); err != nil {
		return err
	}
	return demod.setTSPinState(true)
}

func (demod *Demod) satellitePreamble(system sony.System) ([]sony.Op, error) {
	mode, err := demodMode(system)
	if err != nil {
		return nil, err
	}
	return []sony.Op{
		{Where: sony.SLVX, Register: 0x00, Value: 0x00},
		{Where: sony.SLVX, Register: 0x17, Value: mode},
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
	}, nil
}

// wakeupDVBS brings the chip up in DVB-S or DVB-S2. symbolRateKS programs the
// symbol rate for DVB-S2; zero leaves the chip searching.
func (demod *Demod) wakeupDVBS(system sony.System, symbolRateKS uint32) error {
	if err := demod.setTSClock(system); err != nil {
		return err
	}
	preamble, err := demod.satellitePreamble(system)
	if err != nil {
		return err
	}
	if err := demod.Run(preamble); err != nil {
		return err
	}
	if system == sony.SystemDVBS2 && symbolRateKS != 0 {
		if err := demod.setSymbolRateLocked(symbolRateKS); err != nil {
			return err
		}
	}
	if err := demod.Run([]sony.Op{
		{Where: sony.SLVT, Register: 0x00, Value: 0x00},
		{Where: sony.SLVT, Register: 0x80, Value: 0x30},
		{Where: sony.SLVT, Register: 0x81, Value: 0x00},
	}); err != nil {
		return err
	}
	return demod.setTSPinState(true)
}

// setSymbolRateLocked programs the DVB-S2 symbol rate in thousands of symbols
// per second.
func (demod *Demod) setSymbolRateLocked(symbolRateKS uint32) error {
	value := (uint64(symbolRateKS)*16384 + 500) / 1000
	if value == 0 || value > 0xfffff {
		return fmt.Errorf("CXD2856ER symbol rate %d kSym/s is outside the chip's range", symbolRateKS)
	}
	return demod.Run([]sony.Op{
		{Where: sony.SLVT, Register: 0x00, Value: 0xae},
		{Where: sony.SLVT, Register: 0x20, Data: []byte{
			byte(value >> 16 & 0x0f), byte(value >> 8 & 0xff), byte(value & 0xff),
		}},
	})
}

// SetSymbolRate programs the DVB-S2 symbol rate in thousands of symbols per
// second.
func (demod *Demod) SetSymbolRate(symbolRateKS uint32) error {
	demod.mu.Lock()
	defer demod.mu.Unlock()
	if demod.System != sony.SystemDVBS2 {
		return errors.New("CXD2856ER can only program a symbol rate in DVB-S2")
	}
	return demod.setSymbolRateLocked(symbolRateKS)
}

// sleepTerrestrial parks the chip out of any terrestrial or J.83 mode, using
// the ISDB-T sequence the other cores share.
func (demod *Demod) sleepTerrestrial() error {
	return demod.sleepISDBT()
}

func (demod *Demod) sleepSatellite() error {
	return demod.sleepISDBS()
}
