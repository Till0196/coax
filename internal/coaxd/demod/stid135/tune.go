// Copyright (c) STMicroelectronics (Mathias Hilaire, Thierry Delahaye); CrazyCat; TurboSight; 2026 Till0196
// SPDX-License-Identifier: GPL-2.0-only

package stid135

import (
	"errors"
	"fmt"
	"time"

	"github.com/Till0196/coax/internal/coaxd/tuning"
)

// Params is a satellite channel for one of the eight demodulators to lock onto.
type Params struct {
	// DeliverySystem is "DVBS" or "DVBS2".
	DeliverySystem string
	// FrequencyKHz is the absolute frequency on the aerial lead; the chip is
	// told its difference from the front end's local oscillator.
	FrequencyKHz uint32
	// SymbolRate is in symbols per second.
	SymbolRate uint32
	// StreamID is the Linux DVB API's stream_id property: the low byte is the
	// input stream identifier, bits 8 to 25 the scrambling code, bits 26 and 27
	// the scrambling mode. Negative is NO_STREAM_ID_FILTER, meaning none.
	StreamID int32
}

const noStreamIDFilter = -1

// Symbol rate limits the demodulator can search within.
const (
	minSymbolRate = 100_000
	maxSymbolRate = 520_000_000
)

// Frequency limits: the intermediate frequency band a low-noise block puts on
// the aerial lead, not a satellite band.
const (
	minFrequencyKHz = 950_000
	maxFrequencyKHz = 2_150_000
)

// Tune acquires a channel: the search reduced to the cold-start narrowband
// case. The delivery system and symbol rate are known so there is no blind
// search, and symbol rates at or above half the master clock would need the
// unported wideband modes.
func (demod *Demod) Tune(params Params) error {
	if !demod.warm {
		return ErrNotInitialized
	}
	system := tuning.Normalize(params.DeliverySystem)
	if system != "DVBS" && system != "DVBS2" {
		return fmt.Errorf("STiD135 does not support delivery system %q", params.DeliverySystem)
	}
	if params.SymbolRate < minSymbolRate || params.SymbolRate > maxSymbolRate {
		return fmt.Errorf("STiD135 takes symbol rates from %d to %d, not %d",
			minSymbolRate, maxSymbolRate, params.SymbolRate)
	}
	if params.FrequencyKHz < minFrequencyKHz || params.FrequencyKHz > maxFrequencyKHz {
		return fmt.Errorf("STiD135 covers %d to %d kHz, not %d",
			minFrequencyKHz, maxFrequencyKHz, params.FrequencyKHz)
	}
	if params.SymbolRate >= demod.masterClockHz/2 {
		return fmt.Errorf("STiD135 needs its wideband mode above %d symbols per second, which is not ported",
			demod.masterClockHz/2)
	}
	tunerFrequency := int32(params.FrequencyKHz)*1000 - int32(demod.loFrequencyHz)

	// Stop the demodulator before touching its registers.
	if err := demod.regWrite(demod.demodReg(regDMDISTATE), 0x1c); err != nil {
		return err
	}
	// The crossbar is re-pointed on every tune, because a quattro installation
	// moves a demodulator between inputs as polarisation and band change.
	if err := demod.setRFMuxPath(); err != nil {
		return err
	}
	if err := demod.setPLS(params.StreamID); err != nil {
		return err
	}
	if err := demod.setRegInitValues(); err != nil {
		return err
	}
	// The narrowband arm of the symbol rate ladder.
	if err := demod.regWrite(demod.demodReg(regCORRELCFG), 0x01); err != nil {
		return err
	}
	// Demodulators 2 and 4 are the auxiliary halves of the wideband pairs, and
	// are put back into narrowband mode on every search whether or not the
	// pair is being used that way.
	if demod.path() == 2 || demod.path() == 4 {
		if err := demod.fieldWrite(demod.demodReg(regHDEBITCFG2), fldMODEHAUTDEBIT, 2); err != nil {
			return err
		}
		if err := demod.regWrite(demod.demodReg(regPEGALCFG), 0x00); err != nil {
			return err
		}
	}
	return demod.algo(system, tunerFrequency, params.SymbolRate, params.StreamID)
}

// setRegInitValues puts the registers back to known values, so each
// acquisition starts where the last one did.
func (demod *Demod) setRegInitValues() error {
	writes := []struct {
		address uint16
		value   byte
	}{
		{demod.demodReg(regDEMOD), 0x00},
		{demod.demodReg(regDMDCFGMD), 0xc8},
		// CARFREQ is what makes a DVB-S carrier lock on a cold start.
		{demod.demodReg(regCARFREQ), 0x45},
		{demod.demodReg(regCFR1BCFG), 0x67},
		{demod.demodReg(regPLSMSCRAMBB), 0x70},
		{demod.hwareReg(regTSDLYSET2), 0x31},
		{demod.hwareReg(regTSDLYSET1), 0x2b},
		{demod.hwareReg(regTSDLYSET0), 0x00},
		{demod.demodReg(regCORRELMANT), 0x78},
		// Correlator settings for narrowband carriers at low C/N.
		{demod.demodReg(regCORRELABS), 0x70},
		{demod.demodReg(regCORRELCOEF), 0x20},
		{demod.demodReg(regCORRELFREQ), 0x70},
	}
	for _, write := range writes {
		if err := demod.regWrite(write.address, write.value); err != nil {
			return err
		}
	}
	// Release any forced baseband header type left by a previous acquisition.
	if err := demod.fieldWrite(demod.pktdelinReg(regBBHCTRL2), fldFORCEMATYPEMSB, 0); err != nil {
		return err
	}
	if err := demod.fieldWrite(demod.pktdelinReg(regPDELCTRL0), fldHEMMODESELECT, 0); err != nil {
		return err
	}
	// Stop packing generic streams into MPEG packets.
	if err := demod.fieldWrite(demod.hwareReg(regTSCFG0), fldTSFIFOEMBINDVB, 0); err != nil {
		return err
	}
	if err := demod.fieldWrite(demod.hwareReg(regTSSTATE1), fldTSOUTNOSYNC, 0); err != nil {
		return err
	}
	more := []struct {
		address uint16
		value   byte
	}{
		{demod.demodReg(regCARHDR), 0x08},
		{demod.demodReg(regDFECFG), 0xc1},
		{demod.demodReg(regXFECFG2), 0x03},
		{demod.demodReg(regCFR2CFR1), 0x25},
	}
	for _, write := range more {
		if err := demod.regWrite(write.address, write.value); err != nil {
			return err
		}
	}
	// Back to its initial value for the multistream-plus-ISSY case.
	return demod.fieldWrite(demod.hwareReg(regTSPCRPID1), fldSOFFIFOPCRADJ, 0)
}

// setPLS programs the physical layer scrambling sequence. NO_STREAM_ID_FILTER
// means the broadcast default of mode zero and root one, and a root of zero in
// mode zero is taken as one because zero is not a valid root.
func (demod *Demod) setPLS(streamID int32) error {
	mode, code := byte(0), uint32(1)
	if streamID != noStreamIDFilter {
		mode = byte(streamID>>26) & 0x3
		code = uint32(streamID>>8) & 0x3ffff
		if mode == 0 && code == 0 {
			code = 1
		}
	}
	if err := demod.fieldsWrite(demod.demodReg(regPLROOT2),
		field{fldPLSCRAMBMODE, mode},
		field{fldPLSCRAMBROOTHigh, byte(code>>16) & 0x3}); err != nil {
		return err
	}
	if err := demod.regWrite(demod.demodReg(regPLROOT1), byte(code>>8)); err != nil {
		return err
	}
	return demod.regWrite(demod.demodReg(regPLROOT0), byte(code))
}

// setSymbolRateRegister scales a symbol rate into the register, which holds it
// as a fraction of the master clock. The three-step division keeps the
// intermediates inside 32 bits; reordering it would change the result.
func setSymbolRateRegister(masterClockHz, symbolRate uint32) uint32 {
	value := uint32(1<<27) / (masterClockHz / 10000)
	value = value * (symbolRate / 10000)
	return value / (3 * (1 << 5))
}

func (demod *Demod) setSymbolRate(symbolRate uint32) error {
	value := setSymbolRateRegister(demod.masterClockHz, symbolRate)
	return demod.regWriteMulti(demod.demodReg(regSFRINIT2),
		byte(value>>16), byte(value>>8), byte(value))
}

// pllFvcoMHz is the PLL's design output frequency. It stays a constant even
// though readClocks reads the loop's actual output back, because the carrier
// arithmetic below is written against the constant.
const pllFvcoMHz = 6200

// cfrFactor is 2^20/(10^6/2^6)*100, rounded to 6711.
const cfrFactor = 6711

// carrierBoundaryRegister is the CFR_UP and CFR_LOW arithmetic. The two
// multiplications by 2^11 bracket the division rather than being folded into
// one shift, because the division truncates between them.
func carrierBoundaryRegister(masterClockHz uint32, frequencyHz int32) int32 {
	value := (frequencyHz / 1000) * (1 << 11)
	value = value / ((int32(masterClockHz) / 1000) * 3)
	return value * (1 << 11)
}

// carrierInitRegister is the CFR_INIT arithmetic: the wanted offset from the
// local oscillator, scaled into the 24-bit signed derotator word.
func carrierInitRegister(frequencyHz int32) int32 {
	return ((frequencyHz / pllFvcoMHz) * cfrFactor) / 100
}

// setCarrierFrequencyInit programs the bounds carrier recovery may wander
// between, and the offset it starts from.
func (demod *Demod) setCarrierFrequencyInit(tunerFrequency int32) error {
	up := carrierBoundaryRegister(demod.masterClockHz, tunerFrequency+int32(demod.searchRangeHz)/2)
	low := carrierBoundaryRegister(demod.masterClockHz, tunerFrequency-int32(demod.searchRangeHz)/2)

	// Manual carrier bounds, derotator on, phase-detect algorithm.
	if err := demod.regWrite(demod.demodReg(regCARCFG), 0x46); err != nil {
		return err
	}
	if err := demod.regWriteMulti(demod.demodReg(regCFRUP2),
		byte(up>>16), byte(up>>8), byte(up)); err != nil {
		return err
	}
	if err := demod.regWriteMulti(demod.demodReg(regCFRLOW2),
		byte(low>>16), byte(low>>8), byte(low)); err != nil {
		return err
	}
	initial := carrierInitRegister(tunerFrequency)
	return demod.regWriteMulti(demod.demodReg(regCFRINIT2),
		byte(initial>>16), byte(initial>>8), byte(initial))
}

// Values of enum fe_sat_search_state, read from DMDSTATE's header mode field.
const (
	stateSearching   = 0
	statePLHDetected = 1
	stateDVBS2Found  = 2
	stateDVBSFound   = 3
)

// lockTimeouts is the cold-start timeout ladder, already carrying the
// reference driver's fourfold multipliers on both figures.
func lockTimeouts(symbolRate uint32) (demodMs, fecMs uint32) {
	switch {
	case symbolRate <= 1_000_000:
		demodMs, fecMs = 3000, 2000
	case symbolRate <= 2_000_000:
		demodMs, fecMs = 2500, 1300
	case symbolRate <= 5_000_000:
		demodMs, fecMs = 1000, 650
	case symbolRate <= 10_000_000:
		demodMs, fecMs = 700, 350
	case symbolRate <= 20_000_000:
		demodMs, fecMs = 400, 200
	default:
		demodMs, fecMs = 300, 200
	}
	return demodMs * 4, fecMs * 4
}

// iqPowerThreshold is DEMOD_IQPOWER_THRESHOLD: below it, with the gain
// integrator at zero, there is nothing on the input worth searching.
const iqPowerThreshold = 10

// ErrNoSignal means the front end reports no power on this RF input.
var ErrNoSignal = errors.New("STiD135 sees no signal on this RF input")

// ErrNotLocked means the search ran but settled on no carrier in range.
var ErrNotLocked = errors.New("STiD135 did not lock")

// algo runs one acquisition: set the search up, start it, settle what it found.
func (demod *Demod) algo(system string, tunerFrequency int32, symbolRate uint32, streamID int32) error {
	// Release the delineator's reset, then stop the demodulator.
	if err := demod.fieldWrite(demod.pktdelinReg(regPDELCTRL1), fldALGOSWRST, 0); err != nil {
		return err
	}
	if err := demod.regWrite(demod.demodReg(regDMDISTATE), 0x5c); err != nil {
		return err
	}
	demodMs, fecMs := lockTimeouts(symbolRate)

	if err := demod.setSymbolRate(symbolRate); err != nil {
		return err
	}
	if err := demod.setCarrierFrequencyInit(tunerFrequency); err != nil {
		return err
	}
	// The gain integrator needs a moment after the carrier registers move.
	time.Sleep(10 * time.Millisecond)
	gain, err := demod.readGainIntegrator()
	if err != nil {
		return err
	}
	if gain == 0 {
		// With the integrator pinned at zero the only remaining evidence of a
		// signal is the raw I/Q power, averaged over five readings.
		power := int32(0)
		for reading := 0; reading < 5; reading++ {
			raw, err := demod.regReadMulti(demod.agcrfReg(regAGCRFPOWERI), 2)
			if err != nil {
				return err
			}
			power += (int32(raw[0]) + int32(raw[1])) / 2
		}
		if power/5 < iqPowerThreshold {
			return ErrNoSignal
		}
	}

	// Spectrum inversion is always searched for both ways round.
	if err := demod.fieldWrite(demod.demodReg(regDEMOD), fldSPECINVCONTROL, 0); err != nil {
		return err
	}
	if err := demod.setSearchStandard(system); err != nil {
		return err
	}
	// Cold start: reset the demodulator, then trigger.
	if err := demod.regWrite(demod.demodReg(regDMDISTATE), 0x1f); err != nil {
		return err
	}
	if err := demod.regWrite(demod.demodReg(regDMDISTATE), 0x15); err != nil {
		return err
	}

	locked, err := demod.waitDemodLock(demodMs)
	if err != nil {
		return err
	}
	if !locked {
		return ErrNotLocked
	}
	found, inRange, err := demod.signalParams(tunerFrequency, symbolRate)
	if err != nil {
		return err
	}
	if !inRange {
		return ErrNotLocked
	}
	if found.standard == standardDVBS2 {
		// The baseband header is only readable once the delineator has locked.
		if err := demod.waitFirstLock(); err != nil {
			return err
		}
	}

	// Cycle the stream merger's reset so that it restarts on the new carrier.
	if err := demod.fieldWrite(demod.hwareReg(regTSCFG2), fldRSTHWARE, 0); err != nil {
		return err
	}
	time.Sleep(3 * time.Millisecond)
	if err := demod.fieldWrite(demod.hwareReg(regTSCFG2), fldRSTHWARE, 1); err != nil {
		return err
	}
	if err := demod.fieldWrite(demod.hwareReg(regTSCFG2), fldRSTHWARE, 0); err != nil {
		return err
	}

	settled, err := demod.waitForLock(demodMs, fecMs)
	if err != nil {
		return err
	}
	if !settled {
		return ErrNotLocked
	}
	if err := demod.resetErrorCounters(found.standard); err != nil {
		return err
	}
	if err := demod.trackingOptimization(found); err != nil {
		return err
	}
	if err := demod.resetObservationRegisters(); err != nil {
		return err
	}
	return demod.setMISFiltering(streamID)
}

func (demod *Demod) readGainIntegrator() (uint16, error) {
	raw, err := demod.regReadMulti(demod.agcrfReg(regAGCRFIN1), 2)
	if err != nil {
		return 0, err
	}
	return uint16(raw[0])<<8 | uint16(raw[1]), nil
}

func (demod *Demod) setSearchStandard(system string) error {
	if system == "DVBS2" {
		// Enable the DVB-S2 search only; the Viterbi decoder stays out of it.
		return demod.fieldsWrite(demod.demodReg(regDMDCFGMD),
			field{fldDVBS1ENABLE, 0}, field{fldDVBS2ENABLE, 1})
	}
	if err := demod.fieldsWrite(demod.demodReg(regDMDCFGMD),
		field{fldDVBS1ENABLE, 1}, field{fldDVBS2ENABLE, 0}); err != nil {
		return err
	}
	// DVB-S, unknown puncture rate: DSS off, every rate but 6/7 allowed.
	if err := demod.regWrite(demod.viterbiReg(regFECM), 0x00); err != nil {
		return err
	}
	if err := demod.regWrite(demod.viterbiReg(regPRVIT), 0x2f); err != nil {
		return err
	}
	// Enable the super-FEC delay line.
	return demod.regWrite(demod.hwareReg(regSFDLYSET2), 0)
}

// demodLockTimeoutLimit is DmdLock_TIMEOUT_LIMIT, the floor the symbol-rate
// scaled timeout is not allowed below.
const demodLockTimeoutLimit = 5500

// waitDemodLock polls the search state and, once a carrier is found, the
// definitive lock flag. The timeout shortens in proportion to the symbol rate
// the timing loop has converged on, because a wide carrier locks sooner.
func (demod *Demod) waitDemodLock(timeoutMs uint32) (bool, error) {
	// SR_register = 2^16 / (12 * MCLK), the register reading one megasymbol
	// per second corresponds to.
	oneMegasymbol := uint32(1<<16) / (12 * demod.masterClockHz / 1_000_000)
	if oneMegasymbol == 0 {
		oneMegasymbol = 1
	}
	scaled := timeoutMs
	for elapsed := uint32(0); elapsed < scaled; elapsed += 10 {
		raw, err := demod.regReadMulti(demod.demodReg(regSFR2), 2)
		if err != nil {
			return false, err
		}
		rate := uint32(raw[0])<<8 | uint32(raw[1])
		if timeoutMs < demodLockTimeoutLimit {
			scaled = timeoutMs
		} else {
			if rate < oneMegasymbol {
				rate = oneMegasymbol
			} else if rate > 5*oneMegasymbol {
				rate = 5 * oneMegasymbol
			}
			scaled = timeoutMs / (rate / oneMegasymbol)
			if scaled < demodLockTimeoutLimit {
				scaled = demodLockTimeoutLimit
			} else if scaled > timeoutMs {
				scaled = timeoutMs
			}
		}
		state, err := demod.fieldRead(demod.demodReg(regDMDSTATE), fldHEADERMODE)
		if err != nil {
			return false, err
		}
		if state == stateDVBS2Found || state == stateDVBSFound {
			lock, err := demod.fieldRead(demod.demodReg(regDSTATUS), fldLOCKDEFINITIF)
			if err != nil {
				return false, err
			}
			if lock != 0 {
				return true, demod.settleSlices()
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	return false, nil
}

// settleSlices settles the slice selector after a lock. A carrier wider than
// one demodulator can take is spread across slices and must be told which to
// decode; on an ordinary carrier selector and byte oversampling go to zero.
func (demod *Demod) settleSlices() error {
	// Read only once locked: on cut 1 this field is not valid before.
	annexM, err := demod.fieldRead(demod.demodReg(regDSTATUS6), fldSIGNALANNEXEM)
	if err != nil {
		return err
	}
	// A dummy write that clears a slice range left by a previous acquisition.
	if err := demod.regWrite(demod.demodReg(regSLICEMIN), 0); err != nil {
		return err
	}
	if err := demod.regWrite(demod.demodReg(regSLICEMAX), 0); err != nil {
		return err
	}
	if annexM == 0 || annexM == 1 {
		if err := demod.regWrite(demod.demodReg(regSLICESEL), 0); err != nil {
			return err
		}
		return demod.fieldWrite(demod.hwareReg(regTSDIVN), fldBYTEOVERSAMPLING, 0)
	}
	// Sliced mode: wait for a valid slice range. 255 and 0 mean not yet.
	var minimum, maximum byte
	for elapsed := 0; ; elapsed += 5 {
		minimum, err = demod.regRead(demod.demodReg(regSLICEMIN))
		if err != nil {
			return err
		}
		maximum, err = demod.regRead(demod.demodReg(regSLICEMAX))
		if err != nil {
			return err
		}
		if minimum != 255 || maximum != 0 {
			break
		}
		if elapsed >= 40 {
			// Nothing arrived, so take the lowest slice.
			return demod.regWrite(demod.demodReg(regSLICESEL), minimum)
		}
		time.Sleep(5 * time.Millisecond)
	}
	selected, err := demod.regRead(demod.demodReg(regSLICESEL))
	if err != nil {
		return err
	}
	if selected < minimum || selected > maximum {
		return demod.regWrite(demod.demodReg(regSLICESEL), minimum)
	}
	return nil
}

// waitFirstLock waits for the delineator's first-lock flag, needed before the
// baseband header can be believed.
func (demod *Demod) waitFirstLock() error {
	for elapsed := 0; elapsed < 220; elapsed += 5 {
		lock, err := demod.fieldRead(demod.pktdelinReg(regPDELSTATUS1), fldFIRSTLOCK)
		if err != nil {
			return err
		}
		if lock != 0 {
			return nil
		}
		time.Sleep(5 * time.Millisecond)
	}
	return ErrNotLocked
}

// waitForLock waits on the demodulator, then the FEC, then the stream merger
// putting whole packets out.
func (demod *Demod) waitForLock(demodMs, fecMs uint32) (bool, error) {
	locked, err := demod.waitDemodLock(demodMs)
	if err != nil || !locked {
		return false, err
	}
	fec, err := demod.waitFECLock(fecMs)
	if err != nil || !fec {
		return false, err
	}
	for elapsed := uint32(0); elapsed < fecMs; elapsed++ {
		line, err := demod.fieldRead(demod.hwareReg(regTSSTATUS), fldTSFIFOLINEOK)
		if err != nil {
			return false, err
		}
		if line != 0 {
			return true, nil
		}
		time.Sleep(time.Millisecond)
	}
	return false, nil
}

// waitFECLock waits for the decoder. Which flag means locked depends on the
// standard found, so the state is read once and the loop watches that flag.
func (demod *Demod) waitFECLock(timeoutMs uint32) (bool, error) {
	state, err := demod.fieldRead(demod.demodReg(regDMDSTATE), fldHEADERMODE)
	if err != nil {
		return false, err
	}
	if state != stateDVBS2Found && state != stateDVBSFound {
		return false, nil
	}
	address, mask := demod.pktdelinReg(regPDELSTATUS1), byte(fldPKTDELINLOCK)
	if state == stateDVBSFound {
		address, mask = demod.viterbiReg(regVSTATUSVIT), fldLOCKEDVIT
	}
	for elapsed := uint32(0); elapsed < timeoutMs; elapsed += 10 {
		lock, err := demod.fieldRead(address, mask)
		if err != nil {
			return false, err
		}
		if lock != 0 {
			return true, nil
		}
		time.Sleep(10 * time.Millisecond)
	}
	return false, nil
}

// resetErrorCounters points the counter at DVB-S2 packet errors or Viterbi
// bit errors, and clears what the previous carrier left.
func (demod *Demod) resetErrorCounters(standard trackingStandard) error {
	if standard == standardDVBS2 {
		if err := demod.fieldWrite(demod.pktdelinReg(regPDELCTRL2), fldRESETUPKOCOUNT, 1); err != nil {
			return err
		}
		if err := demod.fieldWrite(demod.pktdelinReg(regPDELCTRL2), fldRESETUPKOCOUNT, 0); err != nil {
			return err
		}
		if err := demod.regWrite(demod.hwareReg(regERRCTRL1), 0x67); err != nil {
			return err
		}
	} else if err := demod.regWrite(demod.hwareReg(regERRCTRL1), 0x75); err != nil {
		return err
	}
	if err := demod.regWrite(demod.fecspyReg(regFBERCPT4), 0); err != nil {
		return err
	}
	// Infinite error count mode.
	return demod.regWrite(demod.hwareReg(regERRCTRL2), 0xc1)
}

// resetObservationRegisters reads or clears the sticky error and de-lock flags
// so a later status query sees only this carrier.
func (demod *Demod) resetObservationRegisters() error {
	if _, err := demod.fieldRead(demod.demodReg(regDSTATUS5), fldSHDBOVERFLOW); err != nil {
		return err
	}
	if _, err := demod.fieldRead(demod.demodReg(regDSTATUS5), fldSHDBERROR); err != nil {
		return err
	}
	if err := demod.fieldWrite(demod.demodReg(regDSTATUS2), fldDEMODDELOCK, 0); err != nil {
		return err
	}
	if err := demod.fieldWrite(demod.pktdelinReg(regPDELSTATUS1), fldPKTDELINDELOCK, 0); err != nil {
		return err
	}
	if err := demod.fieldWrite(demod.pktdelinReg(regPDELCTRL2), fldRESETUPKOCOUNT, 1); err != nil {
		return err
	}
	if err := demod.fieldWrite(demod.pktdelinReg(regPDELCTRL2), fldRESETUPKOCOUNT, 0); err != nil {
		return err
	}
	if err := demod.fieldWrite(demod.hwareReg(regTSSTATUS2), fldTSFIFOLINENOK, 0); err != nil {
		return err
	}
	if err := demod.fieldWrite(demod.hwareReg(regTSSTATUS2), fldDILNOSYNC, 0); err != nil {
		return err
	}
	if _, err := demod.fieldRead(demod.hwareReg(regTSSTATUS), fldTSFIFOERROR); err != nil {
		return err
	}
	if err := demod.fieldWrite(demod.hwareReg(regTSDEBUG0), fldTSFIFOERROREVNT, 0); err != nil {
		return err
	}
	if err := demod.fieldWrite(demod.pktdelinReg(regPDELSTATUS1), fldBCHERRORFLAG, 0); err != nil {
		return err
	}
	return demod.regWrite(demod.hwareReg(regERRCNT20), 0x00)
}

// setMISFiltering programs the multiple input stream filter: the stream
// identifier's low byte is the wanted stream, compared whole, or off.
func (demod *Demod) setMISFiltering(streamID int32) error {
	if streamID != noStreamIDFilter {
		if err := demod.fieldWrite(demod.pktdelinReg(regPDELCTRL1), fldFILTEREN, 1); err != nil {
			return err
		}
		if err := demod.regWrite(demod.pktdelinReg(regISIENTRY), byte(streamID)); err != nil {
			return err
		}
		if err := demod.regWrite(demod.pktdelinReg(regISIBITENA), 0xff); err != nil {
			return err
		}
	} else if err := demod.fieldWrite(demod.pktdelinReg(regPDELCTRL1), fldFILTEREN, 0); err != nil {
		return err
	}
	if err := demod.fieldWrite(demod.pktdelinReg(regPDELCTRL1), fldALGOSWRST, 1); err != nil {
		return err
	}
	return demod.fieldWrite(demod.pktdelinReg(regPDELCTRL1), fldALGOSWRST, 0)
}
