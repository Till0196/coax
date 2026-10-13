// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package sony

import (
	"fmt"
	"time"
)

// The satellite equipment control block: the 22 kHz tone and DiSEqC. The
// CXD2841ER, CXD2856ER and CXD2857ER all keep it in bank 0xBB at the same
// offsets, so the register numbers live here once.
const (
	// SECBank is the bank the block sits in.
	SECBank = 0xbb

	SECTransmit      = 0x32 // writing 1 starts a DiSEqC send
	SECDiSEqCEnable  = 0x33
	SECTone          = 0x36 // 1 puts the continuous tone on
	SECRepeatFirst   = 0x37
	SECRepeatSecond  = 0x38
	SECMessageLength = 0x3d
	SECMessage       = 0x3e
	SECBusy          = 0x10 // reads non-zero while a send is in flight

	// SECMessageBytes is how much room the message registers have.
	SECMessageBytes = 12
)

// SEC poll settings: fifty looks twenty milliseconds apart, i.e. one second.
const (
	SECPollInterval = 20 * time.Millisecond
	SECPollAttempts = 50
)

// SECToneValue is what the tone register takes for on and off.
func SECToneValue(on bool) byte {
	if on {
		return 1
	}
	return 0
}

// SECPaddedMessage checks a DiSEqC message's length and returns it padded out
// to the register run, which is written whole.
func SECPaddedMessage(message []byte) ([]byte, error) {
	if len(message) == 0 || len(message) > SECMessageBytes {
		return nil, fmt.Errorf("a DiSEqC message is 1 to %d bytes, not %d", SECMessageBytes, len(message))
	}
	padded := make([]byte, SECMessageBytes)
	copy(padded, message)
	return padded, nil
}

// ErrDiSEqCStillGoing reports that the chip never lowered its busy flag.
var ErrDiSEqCStillGoing = fmt.Errorf("the DiSEqC message was still going out after %v",
	SECPollInterval*SECPollAttempts)

// SetTone puts the continuous 22 kHz tone on the aerial lead, or takes it off.
// The block only exists on the chip's satellite side, so the call is refused
// in any other state.
func (core *Core) SetTone(on bool) error {
	if !core.System.IsSatellite() {
		return fmt.Errorf("the 22 kHz tone is a satellite setting and this tuner is receiving %s", core.System)
	}
	return core.Run([]Op{
		{Where: SLVT, Register: 0x00, Value: SECBank},
		{Where: SLVT, Register: SECTone, Value: SECToneValue(on)},
	})
}

// SendDiSEqC sends one DiSEqC message and waits for the chip to say it has
// gone. Nothing in this program builds a message yet.
func (core *Core) SendDiSEqC(message []byte) error {
	if !core.System.IsSatellite() {
		return fmt.Errorf("DiSEqC is a satellite thing and this tuner is receiving %s", core.System)
	}
	padded, err := SECPaddedMessage(message)
	if err != nil {
		return err
	}
	if err := core.Run([]Op{
		{Where: SLVT, Register: 0x00, Value: SECBank},
		{Where: SLVT, Register: SECDiSEqCEnable, Value: 0x01},
		{Where: SLVT, Register: SECMessageLength, Value: byte(len(message))},
		{Where: SLVT, Register: SECMessage, Data: padded},
		{Where: SLVT, Register: SECRepeatFirst, Value: 0x01},
		{Where: SLVT, Register: SECRepeatSecond, Value: 0x00},
		{Where: SLVT, Register: SECTransmit, Value: 0x01},
	}); err != nil {
		return err
	}
	return core.WaitForDiSEqC()
}

// WaitForDiSEqC polls the busy register until the send is done.
func (core *Core) WaitForDiSEqC() error {
	for look := 0; look < SECPollAttempts; look++ {
		data, err := core.Read(SLVT, SECBusy, 1)
		if err != nil {
			return err
		}
		if data[0] == 0 {
			return nil
		}
		time.Sleep(SECPollInterval)
	}
	return ErrDiSEqCStillGoing
}
