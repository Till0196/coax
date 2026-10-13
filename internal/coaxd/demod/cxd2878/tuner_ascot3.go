// Copyright (c) Sony; TurboSight; 2026 Till0196
// SPDX-License-Identifier: GPL-2.0-or-later
//
// Ported from cxd2878.c's ascot3_init/ascot3_tune/ascot3_read_rssi, the older
// of the two tuner cores this family is found with (chip ID CXD6802 only —
// see sony.Chip.usesASCOT3). On a CXD6802GL it is a die in the demodulator's
// own package; the same core is also sold on its own as a CXD2871/2872/2875.

package cxd2878

import (
	"errors"
	"time"

	"github.com/Till0196/coax/internal/coaxd/demod/sony"
)

// ascot3Param is one row of the per-system tuning table.
type ascot3Param struct {
	outputLimit  byte
	rfGain       byte // 0xff means automatic
	ifBPFGain    byte
	rfOverload   [3]byte // <=172MHz, <=464MHz, >464MHz
	ifOverload   [3]byte
	ifBPFCenter  byte
	bandwidth    byte
	ifOffset     byte
	bandwidthOff byte
	lowerLocal   byte
}

// ascot3System is the tuner core's own system enum, finer-grained than the
// demodulator's because the bandwidth is folded into it.
type ascot3System int

const (
	ascot3DVBC6 ascot3System = iota
	ascot3DVBC8
	ascot3DVBT5
	ascot3DVBT6
	ascot3DVBT7
	ascot3DVBT8
	ascot3DVBT2_1_7
	ascot3DVBT2_5
	ascot3DVBT2_6
	ascot3DVBT2_7
	ascot3DVBT2_8
	ascot3ISDBT6
	ascot3ISDBT7
	ascot3ISDBT8
	ascot3J83B5_6
	ascot38VSB
)

// isCable reports whether a system value is one of the three that take the
// cable IF and reference-divider path: DVB-C at 6 or 8 MHz, and J.83B at
// 5.6 Msym/s.
func (s ascot3System) isCable() bool {
	return s == ascot3DVBC6 || s == ascot3DVBC8 || s == ascot3J83B5_6
}

// ascot3Params is g_param_table_ascot3i, one row of 16 numbers per system.
var ascot3Params = map[ascot3System]ascot3Param{
	ascot38VSB:      {0x00, 0xff, 0x08, [3]byte{0x0b, 0x0b, 0x0b}, [3]byte{0x03, 0x03, 0x03}, 0x00, 0x00, 0x1a, 0x1d, 0x00},
	ascot3ISDBT6:    {0x00, 0xff, 0x08, [3]byte{0x0b, 0x0b, 0x0b}, [3]byte{0x02, 0x02, 0x02}, 0x00, 0x00, 0x17, 0x1b, 0x00},
	ascot3ISDBT7:    {0x00, 0xff, 0x08, [3]byte{0x0b, 0x0b, 0x0b}, [3]byte{0x02, 0x02, 0x02}, 0x00, 0x01, 0x19, 0x1a, 0x00},
	ascot3ISDBT8:    {0x00, 0xff, 0x08, [3]byte{0x0b, 0x0b, 0x0b}, [3]byte{0x02, 0x02, 0x02}, 0x00, 0x02, 0x1b, 0x19, 0x00},
	ascot3DVBT5:     {0x00, 0xff, 0x08, [3]byte{0x0b, 0x0b, 0x0b}, [3]byte{0x02, 0x02, 0x02}, 0x00, 0x00, 0x18, 0x1d, 0x00},
	ascot3DVBT6:     {0x00, 0xff, 0x08, [3]byte{0x0b, 0x0b, 0x0b}, [3]byte{0x02, 0x02, 0x02}, 0x00, 0x00, 0x18, 0x1d, 0x00},
	ascot3DVBT7:     {0x00, 0xff, 0x08, [3]byte{0x0b, 0x0b, 0x0b}, [3]byte{0x02, 0x02, 0x02}, 0x00, 0x01, 0x1a, 0x1b, 0x00},
	ascot3DVBT8:     {0x00, 0xff, 0x08, [3]byte{0x0b, 0x0b, 0x0b}, [3]byte{0x02, 0x02, 0x02}, 0x00, 0x02, 0x1c, 0x1a, 0x00},
	ascot3DVBT2_1_7: {0x00, 0xff, 0x08, [3]byte{0x0b, 0x0b, 0x0b}, [3]byte{0x02, 0x02, 0x02}, 0x00, 0x03, 0x16, 0x16, 0x00},
	ascot3DVBT2_5:   {0x00, 0xff, 0x08, [3]byte{0x0b, 0x0b, 0x0b}, [3]byte{0x02, 0x02, 0x02}, 0x00, 0x00, 0x18, 0x1d, 0x00},
	ascot3DVBT2_6:   {0x00, 0xff, 0x08, [3]byte{0x0b, 0x0b, 0x0b}, [3]byte{0x02, 0x02, 0x02}, 0x00, 0x00, 0x18, 0x1d, 0x00},
	ascot3DVBT2_7:   {0x00, 0xff, 0x08, [3]byte{0x0b, 0x0b, 0x0b}, [3]byte{0x02, 0x02, 0x02}, 0x00, 0x01, 0x1a, 0x1b, 0x00},
	ascot3DVBT2_8:   {0x00, 0xff, 0x08, [3]byte{0x0b, 0x0b, 0x0b}, [3]byte{0x02, 0x02, 0x02}, 0x00, 0x02, 0x1c, 0x1a, 0x00},
	ascot3DVBC6:     {0x00, 0xff, 0x04, [3]byte{0x02, 0x02, 0x02}, [3]byte{0x01, 0x01, 0x01}, 0x00, 0x00, 0x1a, 0x1c, 0x00},
	ascot3DVBC8:     {0x00, 0xff, 0x04, [3]byte{0x02, 0x02, 0x02}, [3]byte{0x01, 0x01, 0x01}, 0x00, 0x02, 0x1e, 0x1d, 0x00},
	ascot3J83B5_6:   {0x00, 0xff, 0x04, [3]byte{0x02, 0x02, 0x02}, [3]byte{0x01, 0x01, 0x01}, 0x00, 0x00, 0x1b, 0x02, 0x00},
}

// ascot3SystemFor maps the demodulator's current system and bandwidth onto
// the tuner's own system enum, the way ascot3_tune's switch does.
func (demod *Demod) ascot3SystemFor() (ascot3System, error) {
	switch demod.System {
	case sony.SystemDVBC:
		if demod.Bandwidth == 6 {
			return ascot3DVBC6, nil
		}
		return ascot3DVBC8, nil // 7 MHz takes the 8 MHz setting
	case sony.SystemDVBT:
		switch demod.Bandwidth {
		case 5:
			return ascot3DVBT5, nil
		case 6:
			return ascot3DVBT6, nil
		case 7:
			return ascot3DVBT7, nil
		default:
			return ascot3DVBT8, nil
		}
	case sony.SystemDVBT2:
		switch demod.Bandwidth {
		case 1:
			return ascot3DVBT2_1_7, nil
		case 5:
			return ascot3DVBT2_5, nil
		case 6:
			return ascot3DVBT2_6, nil
		case 7:
			return ascot3DVBT2_7, nil
		default:
			return ascot3DVBT2_8, nil
		}
	case sony.SystemISDBT:
		switch demod.Bandwidth {
		case 7:
			return ascot3ISDBT7, nil
		case 8:
			return ascot3ISDBT8, nil
		default:
			return ascot3ISDBT6, nil
		}
	case sony.SystemATSC:
		return ascot38VSB, nil
	case sony.SystemJ83B:
		return ascot3J83B5_6, nil
	default:
		return 0, sony.ErrNoSystem
	}
}

// ascot3Init brings the ASCOT3 tuner core up: chip identification, the fixed
// power-up register table (keyed by the demodulator's own crystal setting),
// then the standby state Tune wakes it from.
func (demod *Demod) ascot3Init() error {
	tuner := demod.Tuner()

	xtalByte, err := sony.ASCOT3XtalCode(demod.Config().TunerXtal)
	if err != nil {
		return err
	}

	if err := tuner.WriteMulti(0x99, []byte{0x7a, 0x01}); err != nil {
		return err
	}
	if err := tuner.WriteMulti(0x81, []byte{
		xtalByte, 0x84, 0xa8, 0x82, 0x00, 0x00, 0xc4, 0x40, 0x10,
		0x00, 0x45, 0x75, 0x07, 0x1c, 0x3f, 0x02, 0x10, 0x20, 0x0a, 0x00,
	}); err != nil {
		return err
	}
	if err := tuner.Write(0x9b, 0x00); err != nil {
		return err
	}
	time.Sleep(10 * time.Millisecond)
	status, err := tuner.Read(0x1a, 1)
	if err != nil {
		return err
	}
	if status[0] != 0 {
		return errASCOT3NotReady
	}

	// sony.Chip variant auto-detection and VCO current setting. Nothing acts
	// on the detected sub-variant beyond that write.
	if err := tuner.WriteMulti(0x17, []byte{0x8d, 0x06}); err != nil {
		return err
	}
	time.Sleep(time.Millisecond)
	data, err := tuner.Read(0x19, 1)
	if err != nil {
		return err
	}
	if err := tuner.Write(0x95, data[0]>>4); err != nil {
		return err
	}

	if err := tuner.WriteMasked(0x67, 0x00, 0xfe); err != nil {
		return err
	}
	if err := tuner.WriteMasked(0x74, 0x02, 0x03); err != nil {
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
	return tuner.Write(0x80, 0x01)
}

var errASCOT3NotReady = errors.New("ASCOT3 tuner did not come out of boot")

// ascot3Tune points the ASCOT3 tuner at a channel. kHz is the unit this tuner
// works in.
func (demod *Demod) ascot3Tune(frequencyKHz uint32) error {
	system, err := demod.ascot3SystemFor()
	if err != nil {
		return err
	}
	param := ascot3Params[system]
	tuner := demod.Tuner()

	if err := tuner.WriteMasked(0x74, 0x02, 0x03); err != nil {
		return err
	}
	if err := tuner.WriteMulti(0x87, []byte{0xc4, 0x40}); err != nil {
		return err
	}
	analogue := []byte{0x10, 0x20}
	if system.isCable() {
		analogue = []byte{0x16, 0x26}
	}
	if err := tuner.WriteMulti(0x91, analogue); err != nil {
		return err
	}
	if err := tuner.WriteMulti(0x9c, []byte{0x00, param.lowerLocal & 0x01}); err != nil {
		return err
	}

	// Register 0x61: the reference divider. Digital (terrestrial) systems use
	// an 8 MHz reference; the cable systems use 1 MHz on the 2871A
	// sub-variant, which this port does not distinguish -- every board it
	// targets uses the digital table.
	reference, err := sony.ASCOT3Reference(demod.Config().TunerXtal)
	if err != nil {
		return err
	}
	// The byte after it is the crystal's again: the cable systems take one
	// value whatever is fitted, and the rest take another that a 20.5 MHz
	// crystal alone changes.
	local := sony.ASCOT3LocalTuning(demod.Config().TunerXtal, system.isCable())
	tuning := []byte{local, 0x78, 0x08, 0x30}
	if system.isCable() {
		tuning[3] = 0x1c
	}
	if err := tuner.WriteMulti(0x5e, append([]byte{0xee, 0x02, 0x1e, 0x67, reference}, tuning...)); err != nil {
		return err
	}
	if err := tuner.WriteMasked(0x67, 0x00, 0x06); err != nil {
		return err
	}

	overload := sony.TunerOverloadIndex(frequencyKHz)
	body := []byte{
		param.outputLimit & 0x03,
		sony.TunerRFGainByte(param.rfGain, param.ifBPFGain),
		0x00,
		param.rfOverload[overload] & 0x0f,
		param.ifOverload[overload]&0x07 | 0x20,
		param.ifBPFCenter<<4&0x30 | param.bandwidth&0x03,
		param.ifOffset & 0x1f,
		param.bandwidthOff & 0x1f,
		byte(frequencyKHz), byte(frequencyKHz >> 8), byte(frequencyKHz>>16) & 0x0f,
		0xff, 0x11,
	}
	trailer := []byte{0x99, 0x00, 0x24, 0x87}
	if system.isCable() {
		trailer = []byte{0xd9, 0x0f, 0x24, 0x87}
	}
	if err := tuner.WriteMulti(0x68, append(body, trailer...)); err != nil {
		return err
	}
	time.Sleep(50 * time.Millisecond)
	if err := tuner.Write(0x88, 0x00); err != nil {
		return err
	}
	if err := tuner.Write(0x87, 0xc0); err != nil {
		return err
	}
	time.Sleep(10 * time.Millisecond)
	return nil
}

// ascot3ReadRSSI reads the tuner's IF and RF AGC registers and converts them to
// hundredths of a dB relative to the tuner's own reference, not yet corrected
// for the delivery system's IF — the caller applies that.
func (demod *Demod) ascot3ReadRSSI(frequencyKHz uint32) (int32, error) {
	tuner := demod.Tuner()
	if err := tuner.WriteMulti(0x87, []byte{0xc4, 0x41}); err != nil {
		return 0, err
	}
	if err := tuner.WriteMulti(0x59, []byte{0x05, 0x01}); err != nil {
		return 0, err
	}
	ifagc, err := tuner.Read(0x5b, 1)
	if err != nil {
		return 0, err
	}
	if err := tuner.WriteMulti(0x59, []byte{0x03, 0x01}); err != nil {
		return 0, err
	}
	rfagc, err := tuner.Read(0x5b, 1)
	if err != nil {
		return 0, err
	}
	if err := tuner.Write(0x59, 0x04); err != nil {
		return 0, err
	}
	if err := tuner.Write(0x88, 0x04); err != nil {
		return 0, err
	}
	if err := tuner.Write(0x87, 0xc0); err != nil {
		return 0, err
	}

	agc := int32(ifagc[0]) * 140
	bpfGain, err := tuner.Read(0x69, 1)
	if err != nil {
		return 0, err
	}
	bpfGainX100 := int32(ascot3IFBPFGainTable[bpfGain[0]&0x0f]) * 100

	var ifGain int32
	if agc > 9945 {
		ifGain = 870 + bpfGainX100
	} else {
		ifGain = 870 + bpfGainX100 + (769*(9945-agc)+1275)/2550
	}

	maxAGC := agc
	if rfagc[0] > ifagc[0] {
		maxAGC = int32(rfagc[0]) * 140
	}
	rfGainMax := ascot3RFGainMax(frequencyKHz)

	var rfGain int32
	switch {
	case maxAGC < 4896:
		rfGain = rfGainMax
	case maxAGC < 5457:
		rfGain = rfGainMax - (70*(maxAGC-4896)+127)/255
	case maxAGC < 8823:
		rfGain = rfGainMax - 154
	case maxAGC < 24786:
		rfGain = rfGainMax - 154 - (70*(maxAGC-8823)+127)/255
	case maxAGC < 30090:
		rfGain = rfGainMax - 4536 - (57*(maxAGC-24786)+127)/255
	default:
		rfGain = rfGainMax - 4536 - 1186 - (160*(maxAGC-30090)+127)/255
	}
	return -ifGain - rfGain, nil
}

var ascot3IFBPFGainTable = [16]int32{0, 0, 0, 0, 2, 4, 6, 8, 10, 12, 14, 16, 18, 20, 20, 20}

func ascot3RFGainMax(frequencyKHz uint32) int32 {
	switch {
	case frequencyKHz > 700000:
		return 4150
	case frequencyKHz > 600000:
		return 4130
	case frequencyKHz > 532000:
		return 4170
	case frequencyKHz > 464000:
		return 4050
	case frequencyKHz > 400000:
		return 4280
	case frequencyKHz > 350000:
		return 4260
	case frequencyKHz > 320000:
		return 4110
	case frequencyKHz > 285000:
		return 4310
	case frequencyKHz > 215000:
		return 4250
	case frequencyKHz > 184000:
		return 4020
	case frequencyKHz > 172000:
		return 3920
	case frequencyKHz > 150000:
		return 4080
	case frequencyKHz > 86000:
		return 4180
	case frequencyKHz > 65000:
		return 4200
	default:
		return 4020
	}
}
