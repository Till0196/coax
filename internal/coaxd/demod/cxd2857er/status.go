// Copyright (c) Sony; TurboSight; 2026 Till0196
// SPDX-License-Identifier: GPL-2.0-or-later

package cxd2857er

import (
	"time"

	"github.com/Till0196/coax/internal/coaxd/demod/sony"
)

// lockSatellite reads the satellite lock flag, in the last of the bytes read
// from bank 0xA0 -- three for ISDB-S, two for ISDB-S3. There is no separate
// sync flag on this side, so locked or not is the only distinction offered.
func (demod *Demod) lockSatellite(length int) (sony.Lock, error) {
	if err := demod.Write(sony.SLVT, 0x00, 0xa0); err != nil {
		return sony.Lock{}, err
	}
	data, err := demod.Read(sony.SLVT, 0x10, length)
	if err != nil {
		return sony.Lock{}, err
	}
	locked := data[length-1]&0x40 != 0
	return sony.Lock{Synced: locked, Locked: locked}, nil
}

// ifBPFGainTable is the intermediate frequency band-pass filter's gain in
// decibels, indexed by the setting the tuner reports in register 0x69.
var ifBPFGainTable = [16]int32{-6, -4, -2, 0, 2, 4, 6, 8, 10, 12, 14, 16, 18, 20, 20, 20}

// readRSSI reads the terrestrial received level, in hundredths of a dB, from
// the tuner's own gain registers.
//
// This is not the sequence the family's other chips use, and the difference is
// not cosmetic: the opening registers, how far the gain compensation nibble is
// read, the AGC scale factor, and both gain curves all differ. Reading a
// CXD2857ER with the other chips' version gives a plausible-looking number
// several decibels out.
func (demod *Demod) readRSSI(frequencyKHz uint32) (int32, error) {
	tuner := demod.Tuner()
	if err := tuner.WriteMulti(0x87, []byte{0xc4, 0x41}); err != nil {
		return 0, err
	}
	if err := tuner.WriteMulti(0x17, []byte{0x7e, 0x06}); err != nil {
		return 0, err
	}
	time.Sleep(4 * time.Millisecond)

	status, err := tuner.Read(0x1a, 1)
	if err != nil {
		return 0, err
	}
	if status[0] != 0x00 {
		return 0, sony.ErrFreiaNotReady
	}
	// The gain compensation is the low nibble here, signed in four bits.
	compensation, err := tuner.Read(0x19, 1)
	if err != nil {
		return 0, err
	}
	trim := sony.TwosComplement(compensation[0]&0x0f, 4)

	if err := tuner.WriteMulti(0x59, []byte{0x05, 0x01}); err != nil {
		return 0, err
	}
	ifRead, err := tuner.Read(0x5b, 1)
	if err != nil {
		return 0, err
	}
	if err := tuner.WriteMulti(0x59, []byte{0x03, 0x01}); err != nil {
		return 0, err
	}
	rfRead, err := tuner.Read(0x5b, 1)
	if err != nil {
		return 0, err
	}
	for _, one := range []struct{ register, value byte }{
		{0x59, 0x04}, {0x88, 0x00}, {0x87, 0xc0},
	} {
		if err := tuner.Write(one.register, one.value); err != nil {
			return 0, err
		}
	}
	filter, err := tuner.Read(0x69, 1)
	if err != nil {
		return 0, err
	}

	ifAGC, rfAGC := int32(ifRead[0]), int32(rfRead[0])
	return rssiFromGains(frequencyKHz, ifAGC, rfAGC, filter[0]&0x0f, trim), nil
}

// rssiFromGains is the arithmetic half of readRSSI, kept apart from the
// transfers so a reading captured from real hardware can be checked against
// it.
func rssiFromGains(frequencyKHz uint32, ifAGC, rfAGC int32, filter byte, trim int32) int32 {
	filterGain := ifBPFGainTable[filter] * 100

	agc := ifAGC * 140
	var ifGain int32
	switch {
	case agc >= 10200:
		ifGain = filterGain + 820
	case agc >= 7650:
		ifGain = (filterGain + 1520) - (700*(agc-7650)+1275)/2550
	default:
		ifGain = (filterGain + 3860) - (agc*780+1275)/2550
	}

	peak := ifAGC * 140
	if rfAGC > ifAGC {
		peak = rfAGC * 140
	}
	max := rfGainMax(frequencyKHz)
	// The curve is piecewise linear in the peak AGC reading, each segment
	// starting where the one before it ended.
	var rfGain int32
	for _, segment := range rfGainCurve {
		if peak < segment.below {
			rfGain = (max - segment.drop) - ((peak-segment.from)*segment.slope+12750)/25500
			return -ifGain - rfGain - trim*100
		}
	}
	return -ifGain - (max - 8384) - trim*100
}

// rfGainCurve is the ladder of RF gain segments, in the order they are
// tested.
var rfGainCurve = []struct{ below, drop, from, slope int32 }{
	{3825, 0, 3825, 0}, // below this the gain is simply the maximum
	{6375, 0, 3825, 160},
	{7650, 16, 6375, 1340},
	{8925, 83, 7650, 3440},
	{10200, 258, 8925, 5421},
	{11475, 529, 10200, 7451},
	{15300, 902, 11475, 8253},
	{19125, 2139, 15300, 6979},
	{24225, 3186, 19125, 7468},
	{26775, 4680, 24225, 5674},
	{29325, 5247, 26775, 14592},
	{31875, 6717, 29325, 16676},
}

// rfGainMax is the tuner's maximum RF gain at a frequency, in hundredths of a
// dB. The band edges are in kHz.
func rfGainMax(frequencyKHz uint32) int32 {
	for _, band := range []struct {
		below uint32
		gain  int32
	}{
		{55000, 4690}, {65000, 4800}, {86000, 4920}, {125000, 4960},
		{142000, 4890}, {165000, 4770}, {172000, 4610}, {200000, 4580},
		{225000, 4680}, {250000, 4770}, {320000, 4840}, {350000, 4740},
		{400000, 4750}, {464000, 4750}, {532000, 4450}, {600000, 4530},
		{664000, 4580}, {766000, 4630}, {868000, 4630}, {900000, 4600},
		{950000, 4480},
	} {
		if frequencyKHz < band.below {
			return band.gain
		}
	}
	return 4300
}

// cnrSatellite reads the carrier-to-noise ratio for every satellite system but
// ISDB-S3, which the chip reports as a raw figure in bank 0xA1 with a validity
// bit in front of it. ISDB-S, DVB-S and DVB-S2 all land here: the curve is
// picked by symbol rate rather than by standard, and all three sit on the same
// side of that line.
func (demod *Demod) cnrSatellite() (int32, error) {
	if err := demod.Write(sony.SLVT, 0x00, 0xa1); err != nil {
		return 0, err
	}
	data, err := demod.Read(sony.SLVT, 0x10, 3)
	if err != nil {
		return 0, err
	}
	if data[0]&0x01 == 0 {
		return 0, sony.ErrCNRUnavailable
	}
	raw := (uint32(data[1])<<8 | uint32(data[2])) & 0x1fff
	return satelliteCN.MdB(raw), nil
}

// cnrISDBS3 is the same reading for ISDB-S3, the one satellite system with a
// curve of its own. It keeps the figure in bank 0xD0 and in one more byte.
func (demod *Demod) cnrISDBS3() (int32, error) {
	if err := demod.Write(sony.SLVT, 0x00, 0xd0); err != nil {
		return 0, err
	}
	data, err := demod.Read(sony.SLVT, 0xf3, 4)
	if err != nil {
		return 0, err
	}
	if data[0]&0x01 == 0 {
		return 0, sony.ErrCNRUnavailable
	}
	raw := (uint32(data[1])<<16 | uint32(data[2])<<8 | uint32(data[3])) & 0x1ffff
	return isdbs3CN.MdB(raw), nil
}
