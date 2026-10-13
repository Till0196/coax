// Copyright (c) Sony; TurboSight; 2026 Till0196
// SPDX-License-Identifier: GPL-2.0-or-later

package sony

import "fmt"

// The QAM cable half of the family (SLtoAC and SLtoACC) is identical on every
// member -- CXD2857, CXD2878, CXD6802, CXD6822, CXD2878A -- with no per-chip
// branch. Annex A and Annex C are one core, mode 0x04, separated only by a
// roll-off the tuner's channel filter absorbs. Annex B is the other core, mode
// 0x08: a trellis-coded demodulator with its own symbol rates and IFs.

// dvbcITBCoef8 is the QAM channel filter for 8 MHz channels, which also serves
// 7 MHz ones. The 6 MHz path reuses ISDB-T's filter, dvbcITBCoef6 in
// terrestrial.go -- the same bytes under both names.
var dvbcITBCoef8 = []byte{0x29, 0x9A, 0x28, 0x9E, 0x2F, 0xB9, 0x27, 0xA5, 0x28, 0xA9, 0x2B, 0xB0, 0x08, 0xD6}

type cableBand struct {
	itbCoef []byte
	ifMHz   float64
	// symbolRate is bank 0x40 registers 0x26..0x29: the symbol rate range the
	// demodulator searches, as two 16-bit bounds each Msym/s times 16384/48. The 8
	// MHz upper bound is 7.2012 Msym/s, the fastest channel this core accepts. It
	// is a search range, not a receive rate, which is why cable acquires without
	// being told the channel's rate.
	symbolRate []byte
	registerA3 byte
}

var dvbcBands = map[uint32]cableBand{
	8: {dvbcITBCoef8, 4.9, []byte{0x09, 0x9A, 0x00, 0xEE}, 0x00},
	7: {dvbcITBCoef8, 4.9, []byte{0x08, 0x66, 0x00, 0xEE}, 0x00},
	6: {dvbcITBCoef6, 3.7, []byte{0x07, 0x33, 0x00, 0xEE}, 0x14},
}

// J83BBand is one of J.83B's two configurations, exported so that a port which
// does not build on Core -- the CXD2856ER's -- can write the same band setting.
type J83BBand struct {
	IFMHz float64
	// Register14 is the bank-0x40 register 0x14 value.
	Register14 byte
	SymbolRate []byte
}

type j83bBand struct {
	ifMHz float64
	// register14 is the bank-0x40 register 0x14 value, the acquisition setting
	// the band choice varies.
	register14 byte
	symbolRate []byte
}

// j83bBands are the two J.83B configurations, which are two different signals
// rather than two settings for one. Entry 0 is American cable: the search range
// spans both rates Annex B allows, and the demodulator picks between them.
// Entry 1 is the 5.6 Msym/s variant Japan's fibre television uses, with its own
// IF and that one rate at both ends.
var j83bBands = map[uint32]j83bBand{
	0: {3.7, 0x14, []byte{0x07, 0x26, 0x06, 0xBE}},
	1: {3.75, 0x10, []byte{0x07, 0x77, 0x07, 0x77}},
}

// j83bBandFor takes the fixed 5.6 Msym/s setting at and above 5,600,000 sym/s,
// and the auto-selected 5.057/5.361 pair below it.
func j83bBandFor(symbolRateSPS uint32) j83bBand {
	if symbolRateSPS >= 5_600_000 {
		return j83bBands[1]
	}
	return j83bBands[0]
}

// J83BBandFor picks between the two J.83B settings for a symbol rate.
func J83BBandFor(symbolRateSPS uint32) J83BBand {
	band := j83bBandFor(symbolRateSPS)
	return J83BBand{IFMHz: band.ifMHz, Register14: band.register14, SymbolRate: band.symbolRate}
}

// WakeupDVBC brings the chip up in DVB-C mode (SLtoAC), shared by Annex A and
// Annex C: one QAM cable core, the annexes differing by a roll-off the tuner's
// channel filter absorbs.
func (core *Core) WakeupDVBC(bandwidthMHz uint32) error {
	if err := core.Run(core.TerrestrialPreamble(0x04)); err != nil {
		return err
	}
	if err := core.Run([]Op{
		{Where: SLVT, Register: 0x00, Value: 0x11},
		{Where: SLVT, Register: 0x6a, Value: 0x48},
		{Where: SLVT, Register: 0x00, Value: 0x10},
		{Where: SLVT, Register: 0xa5, Value: 0x01},
		{Where: SLVT, Register: 0x00, Value: 0x11},
		{Where: SLVT, Register: 0x33, Data: []byte{0x00, 0x03, 0x3b}},
		{Where: SLVT, Register: 0x00, Value: 0x48},
		{Where: SLVT, Register: 0x2c, Value: 0x03},
		{Where: SLVT, Register: 0x00, Value: 0x00},
		{Where: SLVT, Register: 0xce, Data: []byte{0x01, 0x01}},
	}); err != nil {
		return err
	}
	if err := core.SetBandwidthDVBC(bandwidthMHz); err != nil {
		return err
	}
	return core.FinishWakeup()
}

func (core *Core) SetBandwidthDVBC(bandwidthMHz uint32) error {
	band, ok := dvbcBands[bandwidthMHz]
	if !ok {
		return fmt.Errorf("this chip has no a %d MHz DVB-C channel", bandwidthMHz)
	}
	ifFreq := IFFrequencyRegisters(band.ifMHz)
	return core.Run([]Op{
		{Where: SLVT, Register: 0x00, Value: 0x10},
		{Where: SLVT, Register: 0xa6, Data: band.itbCoef},
		{Where: SLVT, Register: 0xb6, Data: ifFreq[:]},
		{Where: SLVT, Register: 0x00, Value: 0x11},
		{Where: SLVT, Register: 0xa3, Value: band.registerA3},
		{Where: SLVT, Register: 0x00, Value: 0x40},
		{Where: SLVT, Register: 0x26, Data: band.symbolRate},
	})
}

// WakeupJ83B brings the chip up in J.83B mode (SLtoACC). symbolRateSPS chooses
// between the two settings; j83bBandFor says where the line falls.
func (core *Core) WakeupJ83B(symbolRateSPS uint32) error {
	if err := core.Run(core.TerrestrialPreamble(0x08)); err != nil {
		return err
	}
	if err := core.Run([]Op{
		{Where: SLVT, Register: 0x00, Value: 0x11},
		{Where: SLVT, Register: 0x6a, Value: 0x48},
		{Where: SLVT, Register: 0x00, Value: 0x10},
		{Where: SLVT, Register: 0xa5, Value: 0x01},
		{Where: SLVT, Register: 0x00, Value: 0x40},
		{Where: SLVT, Register: 0x21, Value: 0x04},
		{Where: SLVT, Register: 0xb3, Value: 0x05},
		{Where: SLVT, Register: 0x1e, Value: 0x80},
		{Where: SLVT, Register: 0x8e, Value: 0x58},
		{Where: SLVT, Register: 0x00, Value: 0x41},
		{Where: SLVT, Register: 0xcf, Value: 0x99},
		{Where: SLVT, Register: 0x00, Value: 0x11},
		{Where: SLVT, Register: 0xa3, Value: 0x14},
		{Where: SLVT, Register: 0x33, Data: []byte{0x00, 0x03, 0x3b}},
		{Where: SLVT, Register: 0x00, Value: 0x00},
		{Where: SLVT, Register: 0xce, Data: []byte{0x01, 0x01}},
		{Where: SLVT, Register: 0x00, Value: 0x10},
		{Where: SLVT, Register: 0xa6, Data: dvbcITBCoef6},
	}); err != nil {
		return err
	}
	if err := core.SetBandwidthJ83B(symbolRateSPS); err != nil {
		return err
	}
	return core.FinishWakeup()
}

func (core *Core) SetBandwidthJ83B(symbolRateSPS uint32) error {
	band := j83bBandFor(symbolRateSPS)
	ifFreq := IFFrequencyRegisters(band.ifMHz)
	return core.Run([]Op{
		{Where: SLVT, Register: 0x00, Value: 0x10},
		{Where: SLVT, Register: 0xb6, Data: ifFreq[:]},
		{Where: SLVT, Register: 0x00, Value: 0x40},
		{Where: SLVT, Register: 0x14, Value: band.register14},
		{Where: SLVT, Register: 0x26, Data: band.symbolRate},
	})
}

// WakeupCable brings the chip up for a QAM cable channel. Annex B (mode 0x08)
// is what Japan's optical distribution of the 124/128 degree satellite service
// carries. The symbol rate only chooses between J.83B's two configurations --
// a cable channel is acquired by search, not by being told its rate. A zero
// bandwidth falls back to 6 MHz, the only width Annex B ever has.
func (core *Core) WakeupCable(system System, bandwidthMHz, symbolRateSPS uint32) error {
	if system == SystemJ83B {
		return core.WakeupJ83B(symbolRateSPS)
	}
	return core.WakeupDVBC(cableBandwidthMHz(bandwidthMHz))
}

// SetCableBand reapplies only the band setting, for a retune that stays in the
// same system.
func (core *Core) SetCableBand(system System, bandwidthMHz, symbolRateSPS uint32) error {
	if system == SystemJ83B {
		return core.SetBandwidthJ83B(symbolRateSPS)
	}
	return core.SetBandwidthDVBC(cableBandwidthMHz(bandwidthMHz))
}

func cableBandwidthMHz(bandwidthMHz uint32) uint32 {
	if bandwidthMHz == 0 {
		return 6
	}
	return bandwidthMHz
}
