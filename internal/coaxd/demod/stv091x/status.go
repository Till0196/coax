// Copyright (c) Ralph Metzler; Marcus Metzler; Digital Devices GmbH; TurboSight; 2026 Till0196
// SPDX-License-Identifier: GPL-2.0-only

package stv091x

import "time"

// Status is one status query's result.
type Status struct {
	Locked bool
	// CNRdB is the carrier-to-noise ratio in dB. HasCNR is false until the FEC
	// locks, which is when the chip's noise estimator means anything.
	CNRdB  float64
	HasCNR bool
}

// The bit patterns read_status looks for: DMDSTATE says whether a search has
// finished, DSTATUS what was found.
const (
	demodSearchDone = 0x40 // DMDSTATE: the demodulator has stopped searching
	demodIsDVBS     = 0x20 // DMDSTATE: DVB-S rather than DVB-S2
	// DSTATUS: the demodulator's own lock, meaning a standard was recognized.
	// The carrier lock in bit 7 is deliberately not read -- it says a carrier
	// was found without saying it carries anything.
	statusLock = 0x08
	// The two standards' FEC locks, each in a register of its own.
	packetDelineatorLock = 0x02 // PDELSTATUS
	viterbiLock          = 0x08 // VSTATUSVIT
)

// berSource and berScale are what ERRCTRL1 is set to: the error count taken
// before the outer decoder, and the exponent it is measured over. The reference
// driver walks the scale between two and six to keep the count in range; no BER
// is read here, so it stays at two.
const (
	berSource = 0x20
	berScale  = 2
)

// Status is read_status's lock and carrier-to-noise portion. The first time a
// channel is seen locked it also retunes the demodulator for what it found --
// carrier loop narrowed to the announced modcod, framing matched to whether the
// transponder marks its streams, output resynchronized -- none of which is
// known during the search.
func (demod *Demod) Status() (Status, error) {
	if !demod.warm {
		return Status{}, ErrNotInitialized
	}
	demodState, err := demod.regRead(demod.path(regDMDSTATE))
	if err != nil {
		return Status{}, err
	}

	current := modeNone
	if demodState&demodSearchDone != 0 {
		signalState, err := demod.regRead(demod.path(regDSTATUS))
		if err != nil {
			return Status{}, err
		}
		if signalState&statusLock != 0 {
			if demodState&demodIsDVBS != 0 {
				current = modeDVBS
			} else {
				current = modeDVBS2
			}
		}
	}
	if current == modeNone {
		return Status{}, nil
	}

	if demod.mode == modeNone {
		demod.mode = current
		demod.firstTimeLock = true
		if err := demod.readSignalParameters(); err != nil {
			return Status{}, err
		}
		if err := demod.optimizeTracking(); err != nil {
			return Status{}, err
		}
		// Let the transport stream out, then pulse the merger's reset so the
		// first packet the demux sees starts at a packet boundary.
		if err := demod.regWrite(demod.path(regTSCFGH), demod.tscfgh); err != nil {
			return Status{}, err
		}
		time.Sleep(3 * time.Millisecond)
		if err := demod.regWrite(demod.path(regTSCFGH), demod.tscfgh|0x01); err != nil {
			return Status{}, err
		}
		if err := demod.regWrite(demod.path(regTSCFGH), demod.tscfgh); err != nil {
			return Status{}, err
		}
	}

	fecLocked := false
	if demodState&demodSearchDone != 0 {
		if demod.mode == modeDVBS2 {
			value, err := demod.regRead(demod.path(regPDELSTATUS))
			if err != nil {
				return Status{}, err
			}
			fecLocked = value&packetDelineatorLock != 0
		} else {
			value, err := demod.regRead(demod.path(regVSTATUSVIT))
			if err != nil {
				return Status{}, err
			}
			fecLocked = value&viterbiLock != 0
		}
	}
	if !fecLocked {
		return Status{}, nil
	}

	if demod.firstTimeLock {
		demod.firstTimeLock = false
		if err := demod.settleFirstLock(); err != nil {
			return Status{}, err
		}
	}

	snr, err := demod.signalToNoise()
	if err != nil {
		return Status{Locked: true}, err
	}
	// The lookup tables are calibrated in tenths of a decibel.
	return Status{Locked: true, CNRdB: float64(snr) / 10, HasCNR: true}, nil
}

// settleFirstLock is read_status's FirstTimeLock branch: release the rolloff
// back to automatic, clear the packet delineator's error count, and point the
// error counters at their stage.
func (demod *Demod) settleFirstLock() error {
	if demod.mode == modeDVBS2 {
		// Drop the manual rolloff bits so the chip follows the transponder.
		demod.demodReg &^= 0x84
		if err := demod.regWrite(demod.path(regDEMOD), demod.demodReg); err != nil {
			return err
		}
		if err := demod.regUpdate(demod.path(regPDELCTRL2), 0x40, 0x40); err != nil {
			return err
		}
		if err := demod.regUpdate(demod.path(regPDELCTRL2), 0x40, 0x00); err != nil {
			return err
		}
	}
	if err := demod.regWrite(demod.path(regERRCTRL1), berSource|berScale); err != nil {
		return err
	}
	// Clear the total packet counter; set the error counter to never stop.
	if err := demod.regWrite(demod.path(regFBERCPT4), 0x00); err != nil {
		return err
	}
	return demod.regWrite(demod.path(regERRCTRL2), 0xc1)
}

// readSignalParameters is GetSignalParameters: the modcod, pilots and frame
// length the DVB-S2 header announces, needed by the carrier loop tuning below.
// The rolloff is also readable from TMGOBS, but nothing uses it.
func (demod *Demod) readSignalParameters() error {
	if demod.mode != modeDVBS2 {
		return nil
	}
	value, err := demod.regRead(demod.path(regDMDMODCOD))
	if err != nil {
		return err
	}
	demod.modCod = (value & 0x7c) >> 2
	demod.pilots = value&0x01 != 0
	demod.fecType = (value & 0x02) >> 1
	return nil
}

// fecTypeLong is DVBS2_64K, the long frame every broadcast transponder uses.
// The carrier loop table is measured for it, so a short-frame transponder keeps
// the loop bandwidth the search ran with.
const fecTypeLong = 0

// optimizeTracking is TrackingOptimization: pin the demodulator to the standard
// now known, and for DVB-S2 narrow the carrier loop and match the framing to the
// transponder. The symbol rate measurement that opens this step upstream is
// exposed as SymbolRate instead; the Viterbi puncture rate is not read.
func (demod *Demod) optimizeTracking() error {
	mode := byte(0xc0)
	switch demod.mode {
	case modeDVBS:
		mode = 0x40
	case modeDVBS2:
		mode = 0x80
	}
	if err := demod.regUpdate(demod.path(regDMDCFGMD), 0xc0, mode); err != nil {
		return err
	}
	if demod.mode != modeDVBS2 {
		return nil
	}

	if err := demod.regWrite(demod.path(regERRCTRL1), berSource|berScale); err != nil {
		return err
	}

	if demod.fecType == fecTypeLong {
		loop := optimalCarrierLoop(demod.modCod, demod.pilots, demod.symbolRate)
		// The loop bandwidth goes in the register for the constellation found;
		// the QPSK register is parked at a safe 0x2a when it is not that one.
		switch {
		case demod.modCod <= modcodQPSK910:
			if err := demod.regWrite(demod.path(regACLC2S2Q), loop); err != nil {
				return err
			}
		case demod.modCod <= modcod8PSK910:
			if err := demod.regWrite(demod.path(regACLC2S2Q), 0x2a); err != nil {
				return err
			}
			if err := demod.regWrite(demod.path(regACLC2S28), loop); err != nil {
				return err
			}
		case demod.modCod <= modcod16APSK910:
			if err := demod.regWrite(demod.path(regACLC2S2Q), 0x2a); err != nil {
				return err
			}
			if err := demod.regWrite(demod.path(regACLC2S216A), loop); err != nil {
				return err
			}
		case demod.modCod <= modcod32APSK910:
			if err := demod.regWrite(demod.path(regACLC2S2Q), 0x2a); err != nil {
				return err
			}
			if err := demod.regWrite(demod.path(regACLC2S232A), loop); err != nil {
				return err
			}
		}
	}

	// MATYPE says whether the transponder's streams carry input stream
	// synchronization. Without it the TS FIFO must not try to synchronize, or
	// adaptive coding breaks the output on every modulation change.
	matype, err := demod.regRead(demod.path(regMATSTR1))
	if err != nil {
		return err
	}
	synchronized := matype&0x18 != 0

	state, err := demod.regRead(demod.path(regTSSTATEM))
	if err != nil {
		return err
	}
	if synchronized {
		state &^= 0x01
	} else {
		state |= 0x01
	}
	if err := demod.regWrite(demod.path(regTSSTATEM), state); err != nil {
		return err
	}

	sync, err := demod.regRead(demod.path(regTSSYNC))
	if err != nil {
		return err
	}
	sync &^= 0x18
	if !synchronized {
		sync |= 0x10
	}
	return demod.regWrite(demod.path(regTSSYNC), sync)
}

// signalToNoise is GetSignalToNoise: the noise estimate for whichever standard
// is running, read through that standard's calibration table.
func (demod *Demod) signalToNoise() (int32, error) {
	register := demod.path(regNNOSDATAT1)
	table := snrDVBS
	if demod.mode == modeDVBS2 {
		register = demod.path(regNNOSPLHT1)
		table = snrDVBS2
	}
	reading, err := demod.regReadMulti(register, 2)
	if err != nil {
		return 0, err
	}
	return lookupDescending(table, uint32(reading[0])<<8|uint32(reading[1])), nil
}

// SymbolRate is GetCurSymbolRate: the rate the demodulator measured, not the
// one it was asked for -- a fraction of the master clock plus a timing offset
// carrying the part the coarse estimate missed.
func (demod *Demod) SymbolRate() (uint32, error) {
	if !demod.warm {
		return 0, ErrNotInitialized
	}
	if !demod.started {
		return 0, nil
	}
	frequency, err := demod.regReadMulti(demod.path(regSFR3), 4)
	if err != nil {
		return 0, err
	}
	timing, err := demod.regReadMulti(demod.path(regTMGREG2), 3)
	if err != nil {
		return 0, err
	}
	raw := uint32(frequency[0])<<24 | uint32(frequency[1])<<16 |
		uint32(frequency[2])<<8 | uint32(frequency[3])
	offset := int32(uint32(timing[0])<<16 | uint32(timing[1])<<8 | uint32(timing[2]))
	// The offset is a twenty-four bit signed number.
	if offset&(1<<23) != 0 {
		offset |= ^int32(0xffffff)
	}
	rate := uint32(uint64(raw) * uint64(demod.mclkHz) >> 32)
	correction := int32(int64(rate) * int64(offset) >> 29)
	return uint32(int32(rate) + correction), nil
}

// read_status's power measurement: five readings of the demodulator's own input,
// three milliseconds apart.
const (
	strengthSamples  = 5
	strengthInterval = 3 * time.Millisecond
)

// padcOffset is the fixed correction read_status adds to the power table's
// answer, in hundredths of a dB.
const padcOffset = 352

// SignalStrength is read_status's strength portion: the power at the
// demodulator's own input less the front end's gain, leaving the level on the
// aerial lead in dBm. frequencyKHz is needed because the tuner's gain slopes
// across the band; without a tuner that can report its gain, the raw AGC
// register is subtracted instead and the result is not in dBm.
func (demod *Demod) SignalStrength(frequencyKHz uint32) (float64, error) {
	if !demod.warm {
		return 0, ErrNotInitialized
	}
	reading, err := demod.regReadMulti(demod.path(regAGCIQIN1), 2)
	if err != nil {
		return 0, err
	}
	agc := uint16(reading[0])<<8 | uint16(reading[1])
	if corrector, ok := demod.tuner.(gainCorrector); ok {
		// The tuner takes a sixteen-bit value, so a gain outside that range wraps
		// rather than saturating. Kept, to match the cards in the field.
		agc = uint16(corrector.RFGainCentiDB(agc, frequencyKHz))
	}

	var power int32
	for sample := 0; sample < strengthSamples; sample++ {
		iq, err := demod.regReadMulti(demod.path(regPOWERI), 2)
		if err != nil {
			return 0, err
		}
		power += int32(iq[0])*int32(iq[0]) + int32(iq[1])*int32(iq[1])
		time.Sleep(strengthInterval)
	}
	power /= strengthSamples

	level := lookupDescending(padcLookup, uint32(power)) + padcOffset
	// Both terms are in hundredths of a decibel.
	return float64(level-int32(agc)) / 100, nil
}
