// Copyright (c) Luis Alves; TurboSight; 2026 Till0196
// SPDX-License-Identifier: GPL-2.0-or-later

package tas2101

import (
	"errors"
	"fmt"
	"time"
)

// SetVoltage selects the low-noise block supply: high is 18 V (horizontal or
// left-hand circular), false 13 V (vertical or right-hand). off is accepted
// and does nothing -- the regulator's enable is a board signal, not a
// register, and the three cards this drives expose no way to reach it.
func (demod *Demod) SetVoltage(high, off bool) error {
	if !demod.warm {
		return ErrNotInitialized
	}
	if off {
		return nil
	}
	if high {
		return demod.regMask(registerLNBControl, voltageSelect, 0)
	}
	return demod.regMask(registerLNBControl, 0, voltageSelect)
}

// SetTone puts the continuous 22 kHz tone on the aerial lead, or takes it
// off. The tone shares register 0x10's low three bits with the DiSEqC
// commands; holding them at the tone's value is what keeps the tone there.
func (demod *Demod) SetTone(on bool) error {
	if !demod.warm {
		return ErrNotInitialized
	}
	command := byte(toneOff)
	if on {
		command = toneOn
	}
	return demod.regMask(registerLNBControl, command, diseqcCommandMask)
}

const (
	diseqcAttempts = 10
	diseqcInterval = 20 * time.Millisecond
)

// SendDiSEqC sends one master command, then puts the lead back the way it
// was. The wait at the end reads the chip's busy flag the right way round;
// read inverted, it reports a timeout on every message that was in fact sent.
func (demod *Demod) SendDiSEqC(message []byte) error {
	if !demod.warm {
		return ErrNotInitialized
	}
	if len(message) < 1 || len(message) > 8 {
		return fmt.Errorf("a DiSEqC message is 1 to 8 bytes, not %d", len(message))
	}
	previous, err := demod.regRead(registerLNBControl)
	if err != nil {
		return err
	}
	restore := func(cause error) error {
		if err := demod.regWrite(registerLNBControl, previous); err != nil && cause == nil {
			return err
		}
		return cause
	}
	if err := demod.regMask(register34, 0, 0x40); err != nil {
		return restore(err)
	}
	if err := demod.regWriteMulti(append([]byte{registerDiSEqC}, message...)); err != nil {
		return restore(err)
	}
	command := previous&^(diseqcLengthMask|diseqcCommandMask) |
		diseqcSendMsg | byte(len(message)-1)<<3
	if err := demod.regWrite(registerLNBControl, command); err != nil {
		return restore(err)
	}
	// A DiSEqC message takes about this long on the wire, so there is nothing
	// to see before it.
	time.Sleep(54 * time.Millisecond)
	return restore(demod.waitIdle())
}

// SendBurst sends the one-bit tone burst that selects between two low-noise
// blocks on a simple switch. b selects burst B; false selects burst A.
func (demod *Demod) SendBurst(b bool) error {
	if !demod.warm {
		return ErrNotInitialized
	}
	previous, err := demod.regRead(registerLNBControl)
	if err != nil {
		return err
	}
	restore := func(cause error) error {
		if err := demod.regWrite(registerLNBControl, previous); err != nil && cause == nil {
			return err
		}
		return cause
	}
	if err := demod.regMask(register34, 0, 0x40); err != nil {
		return restore(err)
	}
	burst := byte(diseqcBurstA)
	if b {
		burst = diseqcBurstB
	}
	if err := demod.regWrite(registerLNBControl, previous&^diseqcCommandMask|burst); err != nil {
		return restore(err)
	}
	return restore(demod.waitIdle())
}

func (demod *Demod) waitIdle() error {
	for attempt := 0; attempt < diseqcAttempts; attempt++ {
		status, err := demod.regRead(registerLNBStatus)
		if err != nil {
			return err
		}
		if status&diseqcBusy == 0 {
			return nil
		}
		time.Sleep(diseqcInterval)
	}
	return errors.New("TAS2101 is still busy with the aerial lead")
}
