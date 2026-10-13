// Copyright (c) Ralph Metzler; Marcus Metzler; Digital Devices GmbH; TurboSight; 2026 Till0196
// SPDX-License-Identifier: GPL-2.0-only

package stv091x

import (
	"fmt"
	"time"
)

// No SetVoltage here: the chip has no LNB supply of its own. The TBS cards
// switch theirs from two of the bridge's GPIO pins -- one enables the
// regulator, one selects between its two voltages -- so a card driver does it.

// DISTXCFG values. The register holds both the transmitter's mode and its
// reset, so every operation is a whole-register write, not read-modify-write.
const (
	distxToneOn  = 0x00 // idle, output low: tone on the lead
	distxToneOff = 0x80 // transmitter in reset: tone off
	// The modes that accept bytes: a DiSEqC message, and burst A, the
	// unmodulated of the two tone bursts.
	distxLoadMessage = 0x06
	distxLoadBurstA  = 0x07
	distxSend        = 0x02
)

// DISTXSTATUS bits the waits below watch: byte queue full, transmission done.
const (
	distxFIFOFull = 0x40
	distxIdle     = 0x20
)

// SetTone puts the continuous 22 kHz tone on the aerial lead, or takes it off,
// telling a universal LNB which local oscillator to use. Turning it on pulses
// the reset first: a DiSEqC command may have left the transmitter mid-message.
func (demod *Demod) SetTone(on bool) error {
	if !demod.warm {
		return ErrNotInitialized
	}
	register := demod.diseqc(regDISTXCFG)
	if !on {
		return demod.regWrite(register, distxToneOff)
	}
	if err := demod.regWrite(register, distxToneOn); err != nil {
		return err
	}
	if err := demod.regWrite(register, distxToneOff); err != nil {
		return err
	}
	return demod.regWrite(register, distxToneOn)
}

// SendDiSEqC sends one master command down the aerial lead, a byte at a time
// through the transmitter's queue.
func (demod *Demod) SendDiSEqC(message []byte) error {
	if !demod.warm {
		return ErrNotInitialized
	}
	if len(message) < 1 || len(message) > 8 {
		return fmt.Errorf("a DiSEqC message is 1 to 8 bytes, not %d", len(message))
	}
	if err := demod.regWrite(demod.diseqc(regDISTXCFG), distxLoadMessage); err != nil {
		return err
	}
	for _, value := range message {
		if err := demod.waitDiSEqC(distxFIFOFull, 0x00); err != nil {
			return err
		}
		if err := demod.regWrite(demod.diseqc(regDISTXFIFO), value); err != nil {
			return err
		}
	}
	if err := demod.regWrite(demod.diseqc(regDISTXCFG), distxSend); err != nil {
		return err
	}
	return demod.waitDiSEqC(distxIdle, distxIdle)
}

// SendBurst sends the one-bit tone burst that selects between two LNBs on a
// simple switch. b selects burst B; false selects burst A. The two differ in
// both mode and byte: burst A is an unmodulated carrier, an all-zero byte in
// the chip's own burst mode, while burst B is modulated and goes out as an
// all-ones byte in the ordinary message mode.
func (demod *Demod) SendBurst(b bool) error {
	if !demod.warm {
		return ErrNotInitialized
	}
	mode, value := byte(distxLoadBurstA), byte(0x00)
	if b {
		mode, value = distxLoadMessage, 0xff
	}
	if err := demod.regWrite(demod.diseqc(regDISTXCFG), mode); err != nil {
		return err
	}
	if err := demod.waitDiSEqC(distxFIFOFull, 0x00); err != nil {
		return err
	}
	if err := demod.regWrite(demod.diseqc(regDISTXFIFO), value); err != nil {
		return err
	}
	if err := demod.regWrite(demod.diseqc(regDISTXCFG), distxSend); err != nil {
		return err
	}
	return demod.waitDiSEqC(distxIdle, distxIdle)
}

// waitDiSEqC is wait_dis: watch one bit of DISTXSTATUS until it reads want.
func (demod *Demod) waitDiSEqC(flag, want byte) error {
	for attempt := 0; attempt < waitDiSEqCAttempts; attempt++ {
		status, err := demod.regRead(demod.diseqc(regDISTXSTATUS))
		if err != nil {
			return err
		}
		if status&flag == want {
			return nil
		}
		time.Sleep(waitDiSEqCInterval)
	}
	return fmt.Errorf("STV0910 demodulator %d is still busy with the aerial lead", demod.config.Index)
}
