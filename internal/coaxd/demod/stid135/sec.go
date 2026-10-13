// Copyright (c) STMicroelectronics (Mathias Hilaire, Thierry Delahaye); CrazyCat; TurboSight; 2026 Till0196
// SPDX-License-Identifier: GPL-2.0-only

package stid135

import (
	"errors"
	"fmt"
	"time"
)

// The chip has four DiSEqC transmitters, one per RF input rather than per
// demodulator, so the one this instance drives follows its current input.
//
// Transmitter initialization is not ported: it reaches into the SoC pin
// multiplexer and starts the front end's FSK block, both part of the bring-up
// left to the driver that already did it. Reception is not ported either --
// only the first RF input can receive a reply, and nothing here asks a switch a
// question.

// Values of enum fe_sat_diseqc_txmode. The transmitter's mode field doubles as
// the continuous tone switch: zero keeps the 22 kHz tone on the lead, and the
// pulse-width mode carries a message.
const (
	diseqcModeContinuous22kHz = 0
	diseqcModePWM             = 2
)

// In Quattro mode SetVoltage and SetTone move the two low bits of the RF input
// index, since the four inputs are already held at the four combinations:
// inputs 0 and 1 at 13 V, 2 and 3 at 18 V, with the tone on 1 and 3. The
// polarities are the opposite way round from MxL58x.
const (
	// rfInputHighVoltage is set on the two inputs a quattro low-noise block
	// feeds at 18 V, and cleared on the two it feeds at 13 V.
	rfInputHighVoltage = 2
	// rfInputToneOn is set on the two inputs held with the 22 kHz tone.
	rfInputToneOn = 1
)

// SetVoltage selects the low-noise block supply: high is 18 V, false 13 V, off
// no supply at all. The STiD135 never switches the supply itself. In Quattro,
// asking for 18 V moves this demodulator onto an input already at 18 V; off is
// treated as 13 V. In Direct mode the request goes to the card through
// Config.Voltage; with none configured this errors rather than succeeding
// silently, since a caller believing the polarisation had changed would tune
// the wrong half of the transponder plan.
func (demod *Demod) SetVoltage(high, off bool) error {
	if !demod.warm {
		return ErrNotInitialized
	}
	if demod.config.Multiswitch == Quattro {
		if high && !off {
			demod.rfInput |= rfInputHighVoltage
		} else {
			demod.rfInput &^= rfInputHighVoltage
		}
		return nil
	}
	if demod.config.Voltage == nil {
		return fmt.Errorf("STiD135 demodulator %d cannot switch its supply: the low-noise block"+
			" regulator is the card's, and no way to reach it was configured", demod.config.Demod)
	}
	return demod.config.Voltage.SetInputVoltage(demod.rfInput, high, off)
}

// SetTone puts the continuous 22 kHz tone on the aerial lead, or takes it off.
// In Quattro mode it switches nothing: the tone is already on two of the four
// inputs, so asking for it moves this demodulator onto one. In Direct mode the
// tone is the chip's, and taking it off leaves the transmitter in the
// pulse-width mode a DiSEqC message needs.
func (demod *Demod) SetTone(on bool) error {
	if !demod.warm {
		return ErrNotInitialized
	}
	if demod.config.Multiswitch == Quattro {
		if on {
			demod.rfInput |= rfInputToneOn
		} else {
			demod.rfInput &^= rfInputToneOn
		}
		return nil
	}
	if demod.config.Suppress22kHz {
		return nil
	}
	mode := byte(diseqcModePWM)
	if on {
		mode = diseqcModeContinuous22kHz
	}
	return demod.fieldWrite(demod.diseqcReg(regDISTXCFG), fldDISEQCMODE, mode)
}

// diseqcAttempts and diseqcInterval bound the wait for the transmitter to
// finish at ten turns of ten milliseconds.
const (
	diseqcAttempts = 10
	diseqcInterval = 10 * time.Millisecond
)

// fifoAttempts bounds the wait for FIFO room, so a wedged transmitter cannot
// hang the caller.
const fifoAttempts = 100

// SendDiSEqC sends one master command on the aerial lead this demodulator is
// currently on. Unlike SetVoltage and SetTone it runs in Quattro mode too, on
// whichever input the last band request composed. The one-bit tone burst is not
// ported.
func (demod *Demod) SendDiSEqC(message []byte) error {
	if !demod.warm {
		return ErrNotInitialized
	}
	if len(message) < 1 || len(message) > 8 {
		return fmt.Errorf("a DiSEqC message is 1 to 8 bytes, not %d", len(message))
	}
	// Only the first input can receive, and not while transmitting.
	if demod.rfInput == 0 {
		if err := demod.fieldWrite(demod.diseqcReg(regDISRXCFG), fldDISRXON, 0); err != nil {
			return err
		}
		defer demod.fieldWrite(demod.diseqcReg(regDISRXCFG), fldDISRXON, 1)
	}
	// Precharge holds the line at the tone level while the FIFO fills, so the
	// message goes out as one burst.
	if err := demod.fieldWrite(demod.diseqcReg(regDISTXCFG), fldDISPRECHARGE, 1); err != nil {
		return err
	}
	for _, value := range message {
		if err := demod.waitFIFORoom(); err != nil {
			return err
		}
		// A whole byte, never a read-modify-write: reading consumes the FIFO.
		if err := demod.regWrite(demod.diseqcReg(regDISTXFIFO), value); err != nil {
			return err
		}
	}
	if err := demod.fieldWrite(demod.diseqcReg(regDISTXCFG), fldDISPRECHARGE, 0); err != nil {
		return err
	}
	for attempt := 0; attempt < diseqcAttempts; attempt++ {
		idle, err := demod.fieldRead(demod.diseqcReg(regDISTXSTATUS), fldTXIDLE)
		if err != nil {
			return err
		}
		if idle == 1 {
			return nil
		}
		time.Sleep(diseqcInterval)
	}
	return errors.New("STiD135 is still sending a DiSEqC message")
}

func (demod *Demod) waitFIFORoom() error {
	for attempt := 0; attempt < fifoAttempts; attempt++ {
		full, err := demod.fieldRead(demod.diseqcReg(regDISTXSTATUS), fldTXFIFOFULL)
		if err != nil {
			return err
		}
		if full == 0 {
			return nil
		}
		time.Sleep(time.Millisecond)
	}
	return errors.New("STiD135 DiSEqC transmit buffer stayed full")
}
