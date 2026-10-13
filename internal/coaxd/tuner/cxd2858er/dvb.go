// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later
//
// The DVB and J.83 delivery systems for the CXD2858ER. ISDB-T and ISDB-S have
// their own tune sequences, in cxd2858er.go.

package cxd2858er

import (
	"fmt"
	"time"

	"github.com/Till0196/coax/internal/coaxd/demod/sony"
)

// terrestrialParams are the per-system filter and gain settings. bandwidth is
// the chip's field value, not a number of megahertz.
type terrestrialParams struct {
	ifBPFGain       byte
	rfOverload      byte
	ifOverload      byte
	bandwidth       byte
	ifOffset        byte
	bandwidthOffset byte
}

// bandwidth field values.
const (
	bandwidth6MHz byte = 0x00
	bandwidth7MHz byte = 0x01
	bandwidth8MHz byte = 0x02
)

// dvbTerrestrialParams is keyed by system and channel width in MHz. The 1.7 MHz
// DVB-T2 channel is absent because the demodulator driver has no filter for it.
var dvbTerrestrialParams = map[sony.System]map[uint32]terrestrialParams{
	sony.SystemDVBT: {
		5: {0x09, 0x0b, 0x02, bandwidth6MHz, 0x18, 0x1d},
		6: {0x09, 0x0b, 0x02, bandwidth6MHz, 0x18, 0x1d},
		7: {0x09, 0x0b, 0x02, bandwidth7MHz, 0x1a, 0x1b},
		8: {0x09, 0x0b, 0x02, bandwidth8MHz, 0x1c, 0x1a},
	},
	sony.SystemDVBT2: {
		5: {0x09, 0x0b, 0x02, bandwidth6MHz, 0x18, 0x1d},
		6: {0x09, 0x0b, 0x02, bandwidth6MHz, 0x18, 0x1d},
		7: {0x09, 0x0b, 0x02, bandwidth7MHz, 0x1a, 0x1b},
		8: {0x09, 0x0b, 0x02, bandwidth8MHz, 0x1c, 0x1a},
	},
	sony.SystemDVBC: {
		6: {0x05, 0x02, 0x01, bandwidth6MHz, 0x1a, 0x1c},
		8: {0x05, 0x02, 0x01, bandwidth8MHz, 0x1e, 0x1d},
	},
	// J.83B has one 6 MHz setting, centred for the 5.6 Msym/s signal at the
	// 3.75 MHz IF the demodulator expects for it.
	sony.SystemJ83B: {
		6: {0x05, 0x02, 0x01, bandwidth6MHz, 0x1b, 0x02},
	},
}

// j83Reference is the PLL reference divider the cable systems take: the crystal
// frequency in whole megahertz, a 1 MHz phase reference, where the digital
// broadcast systems divide by eight.
var j83Reference = map[uint32]byte{
	16000: 0x10,
	20500: 0x14,
	24000: 0x18,
	41000: 0x28,
}

// tuneDVBTerrestrial points the tuner at a DVB-T, DVB-T2 or J.83 channel. The
// caller holds the lock and the gate.
func (tuner *Tuner) tuneDVBTerrestrial(system sony.System, frequencyKHz, bandwidthMHz uint32) error {
	widths, ok := dvbTerrestrialParams[system]
	if !ok {
		return fmt.Errorf("CXD2858ER has no terrestrial settings for %s", system)
	}
	params, ok := widths[bandwidthMHz]
	if !ok {
		return fmt.Errorf("CXD2858ER has no %s settings for a %d MHz channel", system, bandwidthMHz)
	}
	if tuner.system.IsSatellite() {
		if err := tuner.stopSatellite(); err != nil {
			return err
		}
	}
	// J.83 runs the analogue block differently: a different PLL reference divider
	// and no internal loop filter at all.
	j83 := system == sony.SystemDVBC || system == sony.SystemJ83B
	analogue := []byte{0x10, 0x20}
	// The reference divider follows the crystal, as it does for ISDB-T.
	var reference byte
	if !j83 {
		code, err := tuner.xtalCode(0x02, 0x03)
		if err != nil {
			return err
		}
		reference = code
	}
	tuning := []byte{0xb4, 0x78, 0x08, 0x30}
	loopFilter := byte(0x90)
	trailer := []byte{0x99, 0x00, 0x24, 0x87}
	if j83 {
		analogue = []byte{0x16, 0x26}
		tuning = []byte{0x1c, 0x78, 0x08, 0x1c}
		loopFilter = 0x00
		trailer = []byte{0xd9, 0x0f, 0x24, 0x87}
		found, ok := j83Reference[tuner.config.Xtal.KHz()]
		if !ok {
			return fmt.Errorf("CXD2858ER: no J.83 reference divider for a %s crystal", tuner.config.Xtal)
		}
		reference = found
	}
	if err := tuner.regs.Write(0x01, 0x00); err != nil {
		return err
	}
	if err := tuner.regs.Write(0x74, 0x02); err != nil {
		return err
	}
	if err := tuner.regs.WriteMulti(0x87, []byte{0xc4, 0x40}); err != nil {
		return err
	}
	if err := tuner.regs.WriteMulti(0x91, analogue); err != nil {
		return err
	}
	// 0x9c carries the loop filter setting and 0x9d the local polarity, which
	// is upper for every system in the table.
	if err := tuner.regs.WriteMulti(0x9c, []byte{loopFilter, 0x00}); err != nil {
		return err
	}
	if err := tuner.regs.WriteMulti(0x5e, append([]byte{0xee, 0x02, 0x1e, 0x67, reference}, tuning...)); err != nil {
		return err
	}
	if err := tuner.regs.WriteMasked(0x67, 0x00, 0x02); err != nil {
		return err
	}
	// The chip's overload detection levels are per band, but every system here
	// uses one value for all three bands.
	body := []byte{
		0x00,
		0x80 | params.ifBPFGain, // RF gain automatic
		0x00,
		params.rfOverload,
		params.ifOverload | 0x20,
		params.bandwidth,
		params.ifOffset,
		params.bandwidthOffset,
		byte(frequencyKHz), byte(frequencyKHz >> 8), byte(frequencyKHz>>16) & 0x0f,
		0xff, // tuning command
		0x01, // enable the IF output and AGC
	}
	if err := tuner.regs.WriteMulti(0x68, append(body, trailer...)); err != nil {
		return err
	}
	time.Sleep(50 * time.Millisecond)
	if err := tuner.regs.Write(0x88, 0x00); err != nil {
		return err
	}
	if err := tuner.regs.Write(0x87, 0xc0); err != nil {
		return err
	}
	tuner.system = system
	return nil
}

// satelliteCutoffMHz is the low-pass filter cutoff for a satellite channel, in
// megahertz. ISDB-S is a fixed 22 MHz; the DVB systems widen the filter with the
// symbol rate along one curve each, and the chip takes 5 to 36. The register is
// a plain count of megahertz, so ISDB-S writes 22, not 0x22 (= 34, out of
// range).
func satelliteCutoffMHz(system sony.System, symbolRateKS uint32) (byte, error) {
	if system == sony.SystemISDBS {
		return 22, nil
	}
	var cutoff uint32
	switch {
	case symbolRateKS <= 4000:
		cutoff = 5
	case system == sony.SystemDVBS && symbolRateKS <= 10000:
		cutoff = (symbolRateKS*47 + 39999) / 40000
	case system == sony.SystemDVBS:
		cutoff = (symbolRateKS*27+39999)/40000 + 5
	case symbolRateKS <= 10000:
		cutoff = (symbolRateKS*11 + 9999) / 10000
	default:
		cutoff = (symbolRateKS*3+4999)/5000 + 5
	}
	if cutoff > 36 {
		cutoff = 36
	}
	if cutoff < 5 {
		return 0, fmt.Errorf("CXD2858ER: a cutoff of %d MHz is below the chip's range", cutoff)
	}
	return byte(cutoff), nil
}
