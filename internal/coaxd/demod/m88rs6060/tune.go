// Copyright (c) Montage; TurboSight; 2026 Till0196
// SPDX-License-Identifier: GPL-2.0-or-later

package m88rs6060

import (
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/Till0196/coax/internal/coaxd/tuning"
)

// Params is a satellite channel to lock onto.
type Params struct {
	// DeliverySystem is "DVBS" or "DVBS2"/"DVBS2X".
	DeliverySystem string
	FrequencyKHz   uint32
	SymbolRate     uint32 // symbols per second

	// StreamID is the DVB API's packed multistream selector: ISI in bits 0-7,
	// PLS code in bits 8-25, PLS mode (0 = Gold, 1 = root) in bits 26-27.
	// Negative means no filtering — lock the first stream found, matching
	// NO_STREAM_ID_FILTER.
	StreamID int32
}

// Tune is m88rs6060_set_frontend: it resets the demodulator, retunes the
// embedded tuner's PLL and baseband filter, replays regTableDefault, then
// selects the requested stream by its physical layer scrambling sequence.
func (demod *Demod) Tune(params Params) error {
	if !demod.warm {
		return errors.New("M88RS6060 is not initialized")
	}
	system := tuning.Normalize(params.DeliverySystem)
	if system != "DVBS" && system != "DVBS2" && system != "DVBS2X" {
		return fmt.Errorf("M88RS6060 does not support delivery system %q", params.DeliverySystem)
	}

	symbolRateKSs := params.SymbolRate / 1000
	realFreqKHz := params.FrequencyKHz

	if err := demod.regWrite(0x07, 0x80); err != nil {
		return err
	}
	if err := demod.regWrite(0x07, 0x00); err != nil {
		return err
	}
	time.Sleep(2 * time.Millisecond)

	if err := demod.regWrite(0xf5, 0x00); err != nil {
		return err
	}
	if _, err := demod.regRead(0xfd); err != nil {
		return err
	}
	b2, err := demod.regRead(0xb2)
	if err != nil {
		return err
	}
	if b2 == 0x01 {
		if err := demod.regWrite(0x00, 0x00); err != nil {
			return err
		}
		if err := demod.regWrite(0xb2, 0x00); err != nil {
			return err
		}
	}

	var lpfOffsetKHz int32
	if params.SymbolRate < 5_000_000 {
		lpfOffsetKHz = 3000
		realFreqKHz = params.FrequencyKHz + 3000
	}

	if err := demod.regWrite(0x06, 0xe0); err != nil {
		return err
	}
	if err := demod.selectMclk(realFreqKHz/1000, symbolRateKSs); err != nil {
		return err
	}
	var mclkReg byte
	switch demod.mclkKHz {
	case 93000:
		mclkReg = 0x42
	case 96000:
		mclkReg = 0x44
	case 99000:
		mclkReg = 0x46
	case 110250:
		mclkReg = 0x4e
	default:
		mclkReg = 0x44
	}
	if err := demod.regWrite(0xa0, mclkReg); err != nil {
		return err
	}

	if _, err := demod.regRead(0xfd); err != nil {
		return err
	}
	if err := demod.setTSMclk(144000); err != nil {
		return err
	}
	if err := demod.regWrite(0x06, 0x00); err != nil {
		return err
	}
	time.Sleep(10 * time.Millisecond)

	if err := demod.tunerWrite(0x5b, 0x4c); err != nil {
		return err
	}
	if err := demod.tunerWrite(0x5c, 0x54); err != nil {
		return err
	}
	if err := demod.tunerWrite(0x60, 0x4b); err != nil {
		return err
	}
	if err := demod.setPLLFreq((realFreqKHz + 500) / 1000); err != nil {
		return err
	}
	if err := demod.setBB(symbolRateKSs, lpfOffsetKHz); err != nil {
		return err
	}

	if err := demod.regWrite(0x00, 0x01); err != nil {
		return err
	}
	if err := demod.regWrite(0x00, 0x00); err != nil {
		return err
	}
	if err := demod.regWrite(0xb2, 0x01); err != nil {
		return err
	}
	if err := demod.regWrite(0x00, 0x01); err != nil {
		return err
	}

	for _, row := range regTableDefault {
		if err := demod.regWrite(row[0], row[1]); err != nil {
			return err
		}
	}

	if symbolRateKSs > 47100 && symbolRateKSs < 47500 {
		if err := demod.regWrite(0xe6, 0x00); err != nil {
			return err
		}
		if err := demod.regWrite(0xe7, 0x03); err != nil {
			return err
		}
	}

	if err := demod.regUpdateBits(0x4d, 0x02, 0x00); err != nil {
		return err
	}
	if err := demod.regUpdateBits(0x08, 0x80, 0x00); err != nil {
		return err
	}
	if err := demod.regUpdateBits(0xc9, 0x08, 0x08); err != nil {
		return err
	}

	// Register 0xc8's meaning is unknown; only that the value is chosen by
	// symbol rate band, hence the name.
	var byBand byte
	switch {
	case symbolRateKSs <= 3000:
		byBand = 0x20
	case symbolRateKSs <= 10000:
		byBand = 0x10
	default:
		byBand = 0x06
	}
	if err := demod.regWrite(0xc3, 0x08); err != nil {
		return err
	}
	if err := demod.regWrite(0xc8, byBand); err != nil {
		return err
	}
	if err := demod.regWrite(0xc4, 0x08); err != nil {
		return err
	}
	if err := demod.regWrite(0xc7, 0x00); err != nil {
		return err
	}

	symbolRateReg := (symbolRateKSs<<15 + demod.mclkKHz/4) / (demod.mclkKHz / 2)
	if err := demod.regWrite(0x61, byte(symbolRateReg&0xff)); err != nil {
		return err
	}
	if err := demod.regWrite(0x62, byte((symbolRateReg>>8)&0xff)); err != nil {
		return err
	}

	if _, err := demod.regRead(0x76); err != nil {
		return err
	}
	if err := demod.regWrite(0x76, 0x30); err != nil {
		return err
	}

	if err := demod.regWrite(0x22, 0x01); err != nil {
		return err
	}
	if err := demod.regWrite(0x23, 0x00); err != nil {
		return err
	}
	if err := demod.regWrite(0x24, 0x00); err != nil {
		return err
	}

	reg08, err := demod.regRead(0x08)
	if err != nil {
		return err
	}
	switch system {
	case "DVBS":
		reg08 = (reg08 &^ 0x04) | 0x40
		if err := demod.regWrite(0x08, reg08); err != nil {
			return err
		}
		if err := demod.regWrite(0xe0, 0xf8); err != nil {
			return err
		}
	default: // DVBS2, DVBS2X
		reg08 |= 0x44
		if err := demod.regWrite(0x08, reg08); err != nil {
			return err
		}
	}

	s32tmp := int64(0x10000) * int64(lpfOffsetKHz)
	s32tmp = (2*s32tmp + int64(demod.mclkKHz)) / (2 * int64(demod.mclkKHz))
	if err := demod.regWrite(0x5e, byte(s32tmp&0xff)); err != nil {
		return err
	}
	if err := demod.regWrite(0x5f, byte((s32tmp>>8)&0xff)); err != nil {
		return err
	}

	if err := demod.regWrite(0x00, 0x00); err != nil {
		return err
	}
	if err := demod.regWrite(0xb2, 0x00); err != nil {
		return err
	}

	var isi byte
	var plsMode, plsCode uint32
	if params.StreamID >= 0 {
		isi = byte(params.StreamID & 0xff)
		plsMode = uint32(params.StreamID>>26) & 3
		plsCode = uint32(params.StreamID>>8) & 0x3ffff
		if plsMode == 0 && plsCode == 0 {
			plsCode = 1
		}
	} else {
		plsCode = 1
	}

	var pls [3]byte
	if plsMode != 0 {
		pls = calcPLSGoldCode(plsCode)
	} else {
		pls = [3]byte{byte(plsCode & 0xff), byte((plsCode >> 8) & 0xff), byte((plsCode >> 16) & 3)}
	}
	for i, value := range pls {
		if err := demod.regWrite(0x22+byte(i), value); err != nil {
			return err
		}
	}

	if err := demod.tunerWrite(0x5b, 0xcc); err != nil {
		return err
	}
	if err := demod.tunerWrite(0x5c, 0xf4); err != nil {
		return err
	}
	if err := demod.tunerWrite(0x60, 0xcb); err != nil {
		return err
	}

	var lockByte byte
	for range 150 {
		if _, err := demod.regRead(0x08); err != nil {
			return err
		}
		lockByte, err = demod.regRead(0x0d)
		if err != nil {
			return err
		}
		if lockByte == 0x8f || lockByte == 0xf7 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	if lockByte == 0x8f {
		if err := demod.selectMultistream(isi); err != nil {
			return err
		}
	}

	demod.system = system
	demod.streamID = params.StreamID
	return nil
}

// selectMultistream is the ISI-selection tail of set_frontend, run only when
// the lock byte reports a DVB-S2 multi-stream transponder (0x8F): it
// enumerates the visible input streams and outputs the one matching isi,
// falling back to the first stream found.
func (demod *Demod) selectMultistream(isi byte) error {
	reg89, err := demod.regRead(0x89)
	if err != nil {
		return err
	}
	regCA, err := demod.regRead(0xca)
	if err != nil {
		return err
	}
	if err := demod.regWrite(0xca, (regCA&0xf7)|((reg89&0x80)>>4)|0x02); err != nil {
		return err
	}
	if err := demod.regWrite(0xfa, 0x00); err != nil {
		return err
	}
	if err := demod.regWrite(0xf0, 0x00); err != nil {
		return err
	}
	if err := demod.regWrite(0xf0, 0x03); err != nil {
		return err
	}
	time.Sleep(20 * time.Millisecond)

	count, err := demod.regRead(0xf1)
	if err != nil {
		return err
	}
	count &= 0x1f

	streams := make([]byte, count)
	for i := range streams {
		if err := demod.regWrite(0xf2, byte(i)); err != nil {
			return err
		}
		streams[i], err = demod.regRead(0xf3)
		if err != nil {
			return err
		}
	}

	selected := byte(0)
	if slices.Contains(streams, isi) {
		selected = isi
	} else if len(streams) > 0 {
		selected = streams[0]
	}
	return demod.regWrite(0xf5, selected)
}
