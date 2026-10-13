// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package cxd2857er

import (
	"errors"
	"time"

	"github.com/Till0196/coax/internal/coaxd/demod/sony"
)

// It is a Freia, but not wired the way the CXD2878ER family's is: it has a
// satellite side too, and both the power-up table and the per-tune register
// prologue differ. So it gets its own bring-up and terrestrial tune here rather
// than sharing freia.go's.

// terrParams is g_terr_param_table_cxd2857: the same shape as the CXD2878ER's
// table (the columns and the enumeration keying them live in the sony package),
// differing only in the IF filter gain and the RF overload levels. The two are
// kept separate rather than merged behind a per-chip patch.
//
// The satellite systems have no row: that side is pointed by its own sequence,
// which takes a frequency rather than a table row.
var terrParams = map[sony.FreiaSystem]sony.FreiaParam{
	sony.FreiaISDBT6:  sony.FreiaRow(0xff, 0x07, [3]byte{0x0d, 0x0d, 0x0d}, [3]byte{0x03, 0x03, 0x03}, 0x00, 0x00, 0x17, 0x1b),
	sony.FreiaISDBT7:  sony.FreiaRow(0xff, 0x07, [3]byte{0x0d, 0x0d, 0x0d}, [3]byte{0x03, 0x03, 0x03}, 0x00, 0x01, 0x19, 0x1a),
	sony.FreiaISDBT8:  sony.FreiaRow(0xff, 0x07, [3]byte{0x0d, 0x0d, 0x0d}, [3]byte{0x03, 0x03, 0x03}, 0x00, 0x02, 0x1b, 0x19),
	sony.FreiaDVBT6:   sony.FreiaRow(0xff, 0x07, [3]byte{0x0d, 0x0d, 0x0d}, [3]byte{0x03, 0x03, 0x03}, 0x00, 0x00, 0x17, 0x1b),
	sony.FreiaDVBT7:   sony.FreiaRow(0xff, 0x07, [3]byte{0x0d, 0x0d, 0x0d}, [3]byte{0x03, 0x03, 0x03}, 0x00, 0x01, 0x19, 0x1a),
	sony.FreiaDVBT8:   sony.FreiaRow(0xff, 0x07, [3]byte{0x0d, 0x0d, 0x0d}, [3]byte{0x03, 0x03, 0x03}, 0x00, 0x02, 0x1b, 0x19),
	sony.FreiaDVBT2_6: sony.FreiaRow(0xff, 0x07, [3]byte{0x0d, 0x0d, 0x0d}, [3]byte{0x03, 0x03, 0x03}, 0x00, 0x00, 0x17, 0x1b),
	sony.FreiaDVBT2_7: sony.FreiaRow(0xff, 0x07, [3]byte{0x0d, 0x0d, 0x0d}, [3]byte{0x03, 0x03, 0x03}, 0x00, 0x01, 0x19, 0x1a),
	sony.FreiaDVBT2_8: sony.FreiaRow(0xff, 0x07, [3]byte{0x0d, 0x0d, 0x0d}, [3]byte{0x03, 0x03, 0x03}, 0x00, 0x02, 0x1b, 0x19),
	sony.FreiaCable6:  sony.FreiaRow(0xff, 0x03, [3]byte{0x09, 0x09, 0x09}, [3]byte{0x03, 0x03, 0x03}, 0x00, 0x00, 0x03, 0x08),
	sony.FreiaSKPOpt:  sony.FreiaRow(0xff, 0x03, [3]byte{0x09, 0x09, 0x09}, [3]byte{0x03, 0x03, 0x03}, 0x00, 0x00, 0x04, 0x08),
}

// initTuner brings the tuner up. The one board-dependent part is the clock: on
// a card with two of these chips only the first drives the crystal's reference
// output, so past the first (TunerIndex) the REFOUT and GPIO bytes of the
// power-up table are left at zero.
func (demod *Demod) initTuner() error {
	tuner := demod.Tuner()

	// Reading the identity register confirms a tuner answers at all; the
	// value itself is not used.
	if _, err := tuner.Read(0x7f, 1); err != nil {
		return err
	}

	for _, one := range []struct{ register, value byte }{
		{0x01, 0x00}, {0x67, 0x00}, {0x43, 0x06}, {0x45, 0x02},
	} {
		if err := tuner.Write(one.register, one.value); err != nil {
			return err
		}
	}
	if err := tuner.WriteMulti(0x5e, []byte{0x15, 0x00, 0x00}); err != nil {
		return err
	}
	if err := tuner.Write(0x0c, 0x14); err != nil {
		return err
	}
	if err := tuner.Write(0x0d, 0x00); err != nil {
		return err
	}
	if err := tuner.WriteMulti(0x99, []byte{0x7a, 0x01}); err != nil {
		return err
	}

	crystal, err := sony.FreiaXtalCode(demod.Config().TunerXtal)
	if err != nil {
		return err
	}
	powerUp := []byte{
		crystal,
		0x84, 0xa4, 0x80, 0x01, // REFOUT and the two GPIO ports
		0x10, 0xc4, 0x40, 0x10,
		0x00, 0x45, 0x75, // internal RF AGC
		0x07,                                     // analogue block
		0x08, 0x00, 0x00, 0x10, 0x20, 0x0a, 0x00, // internal analogue block
	}
	if demod.Config().TunerIndex > 1 {
		copy(powerUp[1:5], []byte{0x00, 0x00, 0x00, 0x00})
	}
	if err := tuner.WriteMulti(0x81, powerUp); err != nil {
		return err
	}
	if err := tuner.Write(0x9b, 0x00); err != nil {
		return err
	}

	time.Sleep(10 * time.Millisecond)
	cpuStatus, err := tuner.Read(0x1a, 1)
	if err != nil {
		return err
	}
	if cpuStatus[0] != 0x00 {
		return sony.ErrFreiaNotReady
	}

	for _, one := range []struct{ register, value byte }{
		{0x74, 0x12}, {0x67, 0x00}, {0x88, 0x00}, {0x87, 0xc0},
		{0x80, 0x01}, {0x41, 0x07}, {0x42, 0x00}, {0x46, 0x00},
	} {
		if err := tuner.Write(one.register, one.value); err != nil {
			return err
		}
	}
	return tuner.WriteMulti(0x7b, []byte{0x02, 0x01})
}

// setTLVOutput configures the chip's output side to carry MMT/TLV. It has to be
// written once, after the tuner is up: TLV is how the pins are configured, not
// a mode the output side falls into when a satellite channel is tuned.
func (demod *Demod) setTLVOutput() error {
	if err := demod.Bank(0xa0); err != nil {
		return err
	}
	if err := demod.WriteMasked(sony.SLVT, 0xb9, 0x01, 0x01); err != nil {
		return err
	}
	if err := demod.Bank(0x01); err != nil {
		return err
	}
	if err := demod.Run([]sony.Op{
		{Where: sony.SLVT, Register: 0xc1, Value: 0x00, Mask: 0x80}, // serial output off
		{Where: sony.SLVT, Register: 0xcf, Value: 0x00, Mask: 0x01}, // 8-bit parallel
		{Where: sony.SLVT, Register: 0xc1, Value: 0x00, Mask: 0x10}, // TSDATA[7] is the MSB
	}); err != nil {
		return err
	}
	if err := demod.Bank(0x00); err != nil {
		return err
	}
	if err := demod.WriteMasked(sony.SLVT, 0xc4, 0x00, 0x80); err != nil {
		return err
	}
	if err := demod.Bank(0x01); err != nil {
		return err
	}
	if err := demod.WriteMasked(sony.SLVT, 0xc8, 0x00, 0x08); err != nil {
		return err
	}
	return demod.Run([]sony.Op{
		{Where: sony.SLVX, Register: 0x00, Value: 0x01},
		{Where: sony.SLVX, Register: 0xf3, Value: 0x02},
		{Where: sony.SLVX, Register: 0x00, Value: 0x00},
		{Where: sony.SLVX, Register: 0xa5, Value: 0x04, Mask: 0x0f},
		{Where: sony.SLVX, Register: 0x82, Value: 0x00, Mask: 0x04},
	})
}

// tuneTerrestrial points the terrestrial side of the tuner at a channel.
func (demod *Demod) tuneTerrestrial(frequencyKHz uint32) error {
	system, err := demod.FreiaSystem()
	if err != nil {
		return err
	}
	param, ok := terrParams[system]
	if !ok {
		return errNoTunerSystem
	}
	tuner := demod.Tuner()
	demod.side = tunerSideTerrestrial

	if err := tuner.Write(0x01, 0x00); err != nil {
		return err
	}
	if err := tuner.WriteMulti(0x74, []byte{0x12, 0xf9, 0x0f, 0x05, 0x44}); err != nil {
		return err
	}
	if err := tuner.WriteMulti(0x87, []byte{0xc4, 0x40}); err != nil {
		return err
	}
	for _, one := range []struct{ register, value byte }{
		{0x79, 0xa1}, {0x7d, 0x00}, {0x8d, 0x00}, {0x8e, 0x08},
	} {
		if err := tuner.Write(one.register, one.value); err != nil {
			return err
		}
	}
	if err := tuner.WriteMulti(0x91, []byte{0x0a, 0x0f}); err != nil {
		return err
	}
	if err := tuner.WriteMulti(0x9c, []byte{0x90, 0x00}); err != nil {
		return err
	}
	reference, err := sony.FreiaReference(demod.Config().TunerXtal)
	if err != nil {
		return err
	}
	if err := tuner.WriteMulti(0x5e, []byte{
		0xee, 0x02, 0x9e, 0x67, reference, 0x38, 0x1e, 0x02, 0x24,
	}); err != nil {
		return err
	}
	if err := tuner.WriteMasked(0x67, 0x00, 0x02); err != nil {
		return err
	}

	// This port sets the 0x30 bits of the overload byte whatever the system,
	// where the CXD2878ER's leaves them clear for DVB-T and DVB-T2.
	ifOverload := sony.FreiaIFOverload(param, frequencyKHz) | 0x30
	body := append(sony.FreiaTuneBody(param, frequencyKHz, ifOverload), 0xff, 0x11)
	if err := tuner.WriteMulti(0x68, body); err != nil {
		return err
	}
	time.Sleep(50 * time.Millisecond)
	if err := tuner.Write(0x88, 0x00); err != nil {
		return err
	}
	return tuner.Write(0x87, 0xc0)
}

// errNoTunerSystem is returned when the wanted standard has no entry in this
// chip's terrestrial table.
var errNoTunerSystem = errors.New("the tuner has no settings for this system")
