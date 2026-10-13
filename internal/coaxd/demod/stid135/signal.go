// Copyright (c) STMicroelectronics (Mathias Hilaire, Thierry Delahaye); CrazyCat; TurboSight; 2026 Till0196
// SPDX-License-Identifier: GPL-2.0-only

package stid135

// trackingStandard is enum fe_sat_tracking_standard.
type trackingStandard int

const (
	standardUnknown trackingStandard = iota
	standardDVBS1
	standardDVBS2
	standardDSS
)

// rolloff is enum fe_sat_rolloff, whose ordering is the baseband header's
// roll-off field rather than a monotonic one.
type rolloff byte

const (
	rolloff35 rolloff = 0
	rolloff25 rolloff = 1
	rolloff20 rolloff = 2
	rolloff10 rolloff = 3
	rolloff05 rolloff = 4
	rolloff15 rolloff = 5
)

// initialRollOff is the filter shape forced on a DVB-S carrier: DVB-S cannot
// signal its shape, and 0.35 is what every DVB-S transponder uses.
const initialRollOff = rolloff35

// signalInfo is the part of struct fe_sat_search_result needed to decide
// whether the carrier found is the one asked for, and to optimize tracking.
type signalInfo struct {
	standard     trackingStandard
	modcode      modcode
	pilots       bool
	symbolRate   uint32
	frequencyKHz uint32
	rolloff      rolloff
}

// getStandard reports which delivery system the demodulator settled on. The
// five-turn loop settles a race between the search state and the flywheel
// counter: DVB-S2 is believed only when the counter reads fifteen at the same
// time as the state says DVB-S2.
func (demod *Demod) getStandard() (trackingStandard, error) {
	var flywheel, state byte
	var err error
	for attempt := 0; attempt < 5; attempt++ {
		flywheel, err = demod.fieldRead(demod.demodReg(regDMDFLYW), fldFLYWHEELCPT)
		if err != nil {
			return standardUnknown, err
		}
		state, err = demod.fieldRead(demod.demodReg(regDMDSTATE), fldHEADERMODE)
		if err != nil {
			return standardUnknown, err
		}
		if flywheel == 0x0f && state == stateDVBS2Found {
			break
		}
	}
	if flywheel == 0x0f && state == stateDVBS2Found {
		return standardDVBS2, nil
	}
	if state == stateDVBSFound {
		dss, err := demod.fieldRead(demod.viterbiReg(regFECM), fldDSSDVB)
		if err != nil {
			return standardUnknown, err
		}
		if dss == 1 {
			return standardDSS, nil
		}
		return standardDVBS1, nil
	}
	return standardUnknown, nil
}

// symbolRateFromRegister scales the symbol rate readback: the register holds
// the rate as a fraction of twelve times the master clock, split into high and
// low halves so two 32-bit numbers multiply without a 64-bit intermediate.
func symbolRateFromRegister(masterClockHz, register uint32) uint32 {
	clock := masterClockHz * 12
	highClock, highRate := clock>>16, register>>16
	lowClock, lowRate := clock%0x10000, register%0x10000
	return highClock*highRate + (highClock*lowRate)>>16 + (highRate*lowClock)>>16
}

func (demod *Demod) getSymbolRate() (uint32, error) {
	raw, err := demod.regReadMulti(demod.demodReg(regSFR3), 4)
	if err != nil {
		return 0, err
	}
	register := uint32(raw[0])<<24 | uint32(raw[1])<<16 | uint32(raw[2])<<8 | uint32(raw[3])
	return symbolRateFromRegister(demod.masterClockHz, register), nil
}

// twosComplement is Get2Comp: sign-extend a value of the given width.
func twosComplement(value int32, width uint) int32 {
	if value >= 1<<(width-1) {
		return value - 1<<width
	}
	return value
}

// carrierFrequencyFromRegister scales the carrier frequency readback: the
// offset in hertz is 12*mclk*derotator/2^24. The magnitude is taken first so
// the split multiplication never truncates a negative number.
func carrierFrequencyFromRegister(masterClockHz uint32, derotator int32) int32 {
	negative := derotator < 0
	if negative {
		derotator = -derotator
	}
	clock := int32(masterClockHz * 12)
	highClock, highRotator := clock>>12, derotator>>12
	lowClock, lowRotator := clock%0x1000, derotator%0x1000
	result := highClock*highRotator + (highClock*lowRotator)>>12 + (highRotator*lowClock)>>12
	if negative {
		return -result
	}
	return result
}

func (demod *Demod) getCarrierFrequency() (int32, error) {
	raw, err := demod.regReadMulti(demod.demodReg(regCFR12), 3)
	if err != nil {
		return 0, err
	}
	derotator := int32(raw[0])<<16 | int32(raw[1])<<8 | int32(raw[2])
	return carrierFrequencyFromRegister(demod.masterClockHz, twosComplement(derotator, 24)), nil
}

// timingOffsetFromRegister scales the timing loop register into hertz, to add
// to the reported symbol rate. The reciprocal division is deliberate: dividing
// 2^24 by the register first, then the scaled symbol rate by that, is not the
// same in integer arithmetic as one multiplication and one division.
func timingOffsetFromRegister(symbolRate uint32, register int32) int32 {
	if register == 0 {
		register = 1
	}
	offset := (int32(symbolRate) * 10) / (int32(0x1000000) / register)
	return offset / 320
}

func (demod *Demod) getTimingOffset(symbolRate uint32) (int32, error) {
	raw, err := demod.regReadMulti(demod.demodReg(regTMGREG2), 3)
	if err != nil {
		return 0, err
	}
	register := int32(raw[0])<<16 | int32(raw[1])<<8 | int32(raw[2])
	return timingOffsetFromRegister(symbolRate, twosComplement(register, 24)), nil
}

// getModeCode unpacks the physical layer header the demodulator latched. One
// reference-driver oddity is kept: for the three pilots-off DVB-S2X aggregate
// codes only the pilot flag is adjusted and the stored modcode stays the
// pilots-off value; correcting it would change the carrier loop register
// chosen for them.
func (demod *Demod) getModeCode(standard trackingStandard) (code modcode, pilots bool, err error) {
	raw, err := demod.regRead(demod.demodReg(regDMDMODCOD))
	if err != nil {
		return 0, false, err
	}
	switch standard {
	case standardDVBS2:
		var candidate modcode
		if raw&0x80 != 0 {
			candidate = modcode(raw>>1) & 0x7f
		} else {
			candidate = modcode(raw>>2) & 0x1f
		}
		pilots = raw&0x01 != 0
		if candidate <= maxLegacyModcode {
			code = candidate
		}
		if candidate >= minDVBS2XModcode && candidate <= maxDVBS2XModcode {
			code = candidate
		}
		if candidate >= minPilotOffMode && candidate <= maxPilotOffMode {
			pilots = true
		}
	case standardDVBS1, standardDSS:
		candidate := modcode(raw>>2) & 0x3f
		if candidate >= minDVBS1Modcode && candidate <= maxDVBS1Modcode {
			code = candidate
		}
	}
	return code, pilots, nil
}

func carrierWidth(symbolRate uint32, shape rolloff) uint32 {
	var percent uint32
	switch shape {
	case rolloff05:
		percent = 5
	case rolloff10:
		percent = 10
	case rolloff15:
		percent = 15
	case rolloff20:
		percent = 20
	case rolloff25:
		percent = 25
	case rolloff35:
		percent = 35
	}
	return symbolRate + (symbolRate*percent)/100
}

// signalParams reads back what the demodulator found, with its range check: a
// carrier is accepted only inside the search range, or -- below ten megasymbols,
// where the timing loop pulls in from further out -- inside its own occupied
// bandwidth.
func (demod *Demod) signalParams(tunerFrequency int32, requestedRate uint32) (signalInfo, bool, error) {
	var found signalInfo
	standard, err := demod.getStandard()
	if err != nil {
		return found, false, err
	}
	found.standard = standard

	offset, err := demod.getCarrierFrequency()
	if err != nil {
		return found, false, err
	}
	rate, err := demod.getSymbolRate()
	if err != nil {
		return found, false, err
	}
	timing, err := demod.getTimingOffset(rate)
	if err != nil {
		return found, false, err
	}
	if timing < 0 {
		rate -= uint32(-timing)
	} else {
		rate += uint32(timing)
	}
	found.symbolRate = rate

	loKHz := demod.loFrequencyHz / 1000
	found.frequencyKHz = loKHz
	if offset < 0 {
		found.frequencyKHz -= uint32(-offset) / 1000
	} else {
		found.frequencyKHz += uint32(offset) / 1000
	}

	code, pilots, err := demod.getModeCode(standard)
	if err != nil {
		return found, false, err
	}
	found.modcode, found.pilots = code, pilots

	shape, err := demod.fieldRead(demod.demodReg(regTMGOBS), fldROLLOFFSTATUS)
	if err != nil {
		return found, false, err
	}
	found.rolloff = rolloff(shape)

	// Via the reported absolute frequency rather than the derotator reading, so
	// the kilohertz truncation above is part of it.
	residual := int32(found.frequencyKHz)*1000 - int32(loKHz)*1000 - tunerFrequency
	if residual < 0 {
		residual = -residual
	}
	inRange := uint32(residual) <= demod.searchRangeHz/2
	if !inRange && requestedRate < 10_000_000 {
		inRange = uint32(residual) <= carrierWidth(found.symbolRate, found.rolloff)/2
	}
	return found, inRange, nil
}

// trackingOptimization settles the tracking registers for the standard found.
// Unlike the reference driver it does not re-read the symbol rate to index the
// carrier loop table, which does not vary with symbol rate: see carrierLoopACLC.
func (demod *Demod) trackingOptimization(found signalInfo) error {
	switch found.standard {
	case standardDVBS2:
		if err := demod.fieldsWrite(demod.demodReg(regDMDCFGMD),
			field{fldDVBS1ENABLE, 0}, field{fldDVBS2ENABLE, 1}); err != nil {
			return err
		}
		// Count DVB-S2 packet errors rather than Viterbi bit errors.
		if err := demod.regWrite(demod.hwareReg(regERRCTRL1), 0x67); err != nil {
			return err
		}
		return demod.setCarrierLoop(found.modcode)
	case standardDVBS1, standardDSS:
		// DVB-S carries no roll-off indication, so the demodulator is told
		// initialRollOff and put into manual mode.
		if err := demod.fieldsWrite(demod.demodReg(regDEMOD),
			field{fldROLLOFFCONTROL, byte(initialRollOff)},
			field{fldMANUALSXROLLOFF, 1}); err != nil {
			return err
		}
		return demod.regWrite(demod.hwareReg(regERRCTRL1), 0x75)
	}
	return nil
}

// carrierLoopQPSKHold is where the QPSK carrier loop register is parked when
// the carrier turned out denser, so it is not left tracking a constellation it
// is not tuned for.
const carrierLoopQPSKHold = 0x48

// setCarrierLoop writes the loop coefficient into the constellation's
// register.
func (demod *Demod) setCarrierLoop(code modcode) error {
	aclc := optimCarrierLoop(code)
	family := classify(code)
	if family == modulationQPSK {
		return demod.regWrite(demod.demodReg(regACLC2S2Q), aclc)
	}
	if err := demod.regWrite(demod.demodReg(regACLC2S2Q), carrierLoopQPSKHold); err != nil {
		return err
	}
	var register uint16
	switch family {
	case modulation8PSK:
		register = regACLC2S28
	case modulation16APSK:
		register = regACLC2S216A
	case modulation32APSK:
		register = regACLC2S232A
	case modulation64APSK:
		register = regACLC2S264A
	case modulation128APSK:
		register = regACLC2S2128A
	case modulation256APSK:
		register = regACLC2S2256A
	default:
		// VLSNR and 1024APSK have no coefficient register; parking the QPSK
		// loop above is all that is done for them.
		return nil
	}
	return demod.regWrite(demod.demodReg(register), aclc)
}
