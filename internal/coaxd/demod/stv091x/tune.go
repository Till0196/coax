// Copyright (c) Ralph Metzler; Marcus Metzler; Digital Devices GmbH; TurboSight; 2026 Till0196
// SPDX-License-Identifier: GPL-2.0-only

package stv091x

import (
	"fmt"
	"time"

	"github.com/Till0196/coax/internal/coaxd/tuning"
)

// Params is a satellite channel to lock onto.
type Params struct {
	// DeliverySystem is "DVBS" or "DVBS2". Validated and then unused: the search
	// always tries both standards and Status reports which was found, so naming
	// the wrong one still locks.
	DeliverySystem string
	FrequencyKHz   uint32
	SymbolRate     uint32 // symbols per second

	// StreamID is the DVB API's packed multistream selector: ISI in bits 0-7,
	// the physical layer scrambling code in bits 8-25, its mode in bits 26-27.
	// Negative is NO_STREAM_ID_FILTER: lock the first stream found.
	StreamID int32
}

// The symbol rates the chip searches between, from the frontend's limits.
const (
	minSymbolRate = 100_000
	maxSymbolRate = 70_000_000
)

// modcodeAll is the DVB core's MODCODE_ALL. A partial list would leave a
// channel silently unusable, so this is the only value ever passed.
const modcodeAll = ^uint32(0)

// tuningDelay is TUNING_DELAY, roughly the demodulator's settling time after
// the search is triggered; used here as the lock poll interval.
const tuningDelay = 200 * time.Millisecond

// Tune is set_parameters: stop the demodulator, move the tuner, start a fresh
// search. Unlike the reference driver it then waits out the sum of Start's two
// timeouts, so the caller gets a locked frontend or an error, not one to poll.
func (demod *Demod) Tune(params Params) error {
	if !demod.warm {
		return ErrNotInitialized
	}
	system := tuning.Normalize(params.DeliverySystem)
	if system != "DVBS" && system != "DVBS2" {
		return fmt.Errorf("STV0910 does not support delivery system %q", params.DeliverySystem)
	}
	if params.SymbolRate < minSymbolRate || params.SymbolRate > maxSymbolRate {
		return fmt.Errorf("STV0910 searches between %d and %d symbols per second, not %d",
			minSymbolRate, maxSymbolRate, params.SymbolRate)
	}

	if err := demod.stop(); err != nil {
		return err
	}
	if demod.tuner != nil {
		if err := demod.tuner.Tune(params.FrequencyKHz*1000, params.SymbolRate); err != nil {
			return err
		}
	}
	demod.symbolRate = params.SymbolRate
	if err := demod.start(params); err != nil {
		return err
	}

	deadline := time.Now().Add(searchTimeout(params.SymbolRate))
	for {
		status, err := demod.Status()
		if err != nil {
			return err
		}
		if status.Locked {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("STV0910 did not lock onto %d kHz", params.FrequencyKHz)
		}
		time.Sleep(tuningDelay)
	}
}

// searchTimeout is Start's DemodTimeout plus FecTimeout. Both shrink as the
// symbol rate grows, because a wider carrier is found sooner.
func searchTimeout(symbolRate uint32) time.Duration {
	var demodMS, fecMS int
	switch {
	case symbolRate <= 1_000_000:
		demodMS, fecMS = 3000, 2000
	case symbolRate <= 2_000_000:
		demodMS, fecMS = 2500, 1300
	case symbolRate <= 5_000_000:
		demodMS, fecMS = 1000, 650
	case symbolRate <= 10_000_000:
		demodMS, fecMS = 700, 350
	case symbolRate < 20_000_000:
		demodMS, fecMS = 400, 200
	default:
		demodMS, fecMS = 300, 200
	}
	return time.Duration(demodMS+fecMS) * time.Millisecond
}

// start is Start: everything between the tuner moving and the search trigger.
func (demod *Demod) start(params Params) error {
	demod.mode = modeNone

	if demod.started {
		if err := demod.regWrite(demod.path(regDMDISTATE), 0x5c); err != nil {
			return err
		}
	}

	if err := demod.setMIS(params.StreamID); err != nil {
		return err
	}
	if err := demod.setModcode(modcodeAll); err != nil {
		return err
	}

	// The starting symbol rate, as a fraction of the master clock in units of
	// 1/65536. Only fifteen bits of it reach the chip.
	initial := mulDiv32(params.SymbolRate, 65536, demod.mclkHz)
	if err := demod.regWrite(demod.path(regSFRINIT1), byte(initial>>8)&0x7f); err != nil {
		return err
	}
	if err := demod.regWrite(demod.path(regSFRINIT1)+1, byte(initial)); err != nil {
		return err
	}

	// Bit 7 puts the demodulator into its search state; the inversion bits
	// below it are already settled.
	demod.demodReg |= 0x80
	if err := demod.regWrite(demod.path(regDEMOD), demod.demodReg); err != nil {
		return err
	}

	// Search for both standards; which was found is read back afterwards.
	if err := demod.regUpdate(demod.path(regDMDCFGMD), 0xc0, 0xc0); err != nil {
		return err
	}

	for _, step := range []struct {
		register uint16
		value    byte
	}{
		// DSS off, and the Viterbi decoder given every puncture rate to try.
		{demod.path(regFECM), 0x00},
		{demod.path(regPRVIT), 0x2f},

		// Workaround for tracking 8PSK 3/5 and 2/3 without pilots, which the
		// carrier loop otherwise loses.
		{demod.path(regACLC2S2Q), 0x0b},
		{demod.path(regACLC2S28), 0x0a},
		{demod.path(regBCLC2S2Q), 0x84},
		{demod.path(regBCLC2S28), 0x84},
		{demod.path(regCARHDR), 0x1c},

		{demod.path(regDMDISTATE), 0x1f},
		{demod.path(regCARCFG), 0x46},
	} {
		if err := demod.regWrite(step.register, step.value); err != nil {
			return err
		}
	}

	upper, lower := searchWindow(demod.mclkHz, params.SymbolRate)
	if err := demod.regWrite(demod.path(regCFRUP1), byte(upper>>8)); err != nil {
		return err
	}
	if err := demod.regWrite(demod.path(regCFRUP1)+1, byte(upper)); err != nil {
		return err
	}
	if err := demod.regWrite(demod.path(regCFRLOW1), byte(lower>>8)); err != nil {
		return err
	}
	if err := demod.regWrite(demod.path(regCFRLOW1)+1, byte(lower)); err != nil {
		return err
	}

	// Whatever the last search settled on is not this channel's offset.
	if err := demod.regWrite(demod.path(regCFRINIT1), 0x00); err != nil {
		return err
	}
	if err := demod.regWrite(demod.path(regCFRINIT1)+1, 0x00); err != nil {
		return err
	}

	if err := demod.regWrite(demod.path(regDMDISTATE), 0x1f); err != nil {
		return err
	}
	// Trigger the acquisition.
	if err := demod.regWrite(demod.path(regDMDISTATE), 0x15); err != nil {
		return err
	}
	demod.started = true
	return nil
}

// searchWindow is Start's carrier offset calculation: how far either side of the
// tuned frequency the demodulator sweeps, in units of the master clock over
// 65536. The halves are symmetric because the offset registers are signed.
func searchWindow(mclkHz, symbolRate uint32) (upper, lower int32) {
	// Half the search range in kHz, plus a margin for the tuner's own error.
	offsetKHz := int32(searchRangeHz/2000) + 600
	if symbolRate <= 5_000_000 {
		// A narrow carrier gets a narrow window, or it finds its neighbour.
		offsetKHz -= 600 + 80
	}
	upper = (offsetKHz << 16) / int32(mclkHz/1000)
	return upper, -upper
}

// setPLS is SetPLS: the physical layer scrambling sequence DVB-S2 frames are
// spread with.
func (demod *Demod) setPLS(mode byte, code uint32) error {
	// The unscrambled default is sequence one, not zero.
	if mode == 0 && code == 0 {
		code = 1
	}
	mode &= 0x03
	code &= 0x3ffff
	if err := demod.regWrite(demod.path(regPLROOT2), mode<<2|byte(code>>16)); err != nil {
		return err
	}
	if err := demod.regWrite(demod.path(regPLROOT2)+1, byte(code>>8)); err != nil {
		return err
	}
	return demod.regWrite(demod.path(regPLROOT2)+2, byte(code))
}

// setMIS is SetMIS: turn the multi-input-stream filter off, or point it at one
// stream of a multistream transponder.
func (demod *Demod) setMIS(streamID int32) error {
	if streamID < 0 {
		if err := demod.setPLS(0, 0); err != nil {
			return err
		}
		return demod.regUpdate(demod.path(regPDELCTRL1), 0x20, 0x00)
	}
	if err := demod.setPLS(byte(streamID>>26)&0x03, uint32(streamID>>8)&0x3ffff); err != nil {
		return err
	}
	if err := demod.regUpdate(demod.path(regPDELCTRL1), 0x20, 0x20); err != nil {
		return err
	}
	if err := demod.regWrite(demod.path(regISIENTRY), byte(streamID)); err != nil {
		return err
	}
	// Every bit of the identifier has to match.
	return demod.regWrite(demod.path(regISIBITENA), 0xff)
}

// The four 9/10 rates: top of each constellation's run, and the only entries
// occupying a different pair of bits from all the others.
const (
	modcodQPSK910   = 11
	modcod8PSK910   = 17
	modcod16APSK910 = 23
	modcod32APSK910 = 28
)

// setModcode is SetModcode: which of the twenty-eight DVB-S2 modcods may be
// locked onto. Each gets four bits, two per register, running down from
// MODCODLSTF towards MODCODLST1; a set mask bit enables the modcod, which the
// chip spells as clearing its entry.
func (demod *Demod) setModcode(modcode uint32) error {
	// Clear the chip's automatic symbol-rate filtering, which would override
	// the list built below.
	if err := demod.regUpdate(demod.path(regMODCODLST1), 0xfc, 0x00); err != nil {
		return err
	}

	for index := 1; index < 29; index++ {
		modcode >>= 1
		register := demod.path(regMODCODLSTF) - uint16(index/2)
		current, err := demod.regRead(register)
		if err != nil {
			return err
		}
		wanted := modcode&1 != 0
		switch index {
		case modcodQPSK910, modcod8PSK910, modcod16APSK910:
			if wanted {
				current &= 0xcf
			} else {
				current |= 0x30
			}
		case modcod32APSK910:
			if wanted {
				current &= 0xfc
			} else {
				current |= 0x03
			}
		default:
			if wanted {
				if index%2 != 0 {
					current &= 0x0f
				} else {
					current &= 0xf0
				}
			} else {
				if index%2 != 0 {
					current |= 0xf0
				} else {
					current |= 0x0f
				}
			}
		}
		if err := demod.regWrite(register, current); err != nil {
			return err
		}
	}
	return nil
}

// mulDiv32 is MulDiv32: multiply in 64 bits, divide back down to 32.
func mulDiv32(a, b, c uint32) uint32 {
	if c == 0 {
		return 0
	}
	return uint32(uint64(a) * uint64(b) / uint64(c))
}
