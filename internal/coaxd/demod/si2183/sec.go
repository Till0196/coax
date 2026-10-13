// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package si2183

import "fmt"

// The satellite equipment control command carries the 22 kHz tone, the tone
// burst and DiSEqC alike -- this chip has no registers of its own for them. It
// is always eight bytes on the wire whatever the message length, with the flags
// in the second byte.
const (
	secCommand = 0x8c

	secEnable    = 1 << 0
	secToneOn    = 1 << 1
	secToneBurst = 1 << 2
	secBurstB    = 1 << 3
	secEndOfSeq  = 1 << 4
	secLengthAt  = 5 // the message length sits in the top three bits

	// secBytes is what the command occupies on the wire, message or not.
	secBytes = 8
	// secMessageBytes is how much of it a DiSEqC message may take: the two
	// bytes of command and flags leave this much.
	secMessageBytes = secBytes - 2
)

// SetTone puts the continuous 22 kHz tone on the aerial lead, or takes it off.
// There being no separate tone command on this chip, it is a DiSEqC command
// carrying no message, with the tone bit set the way it should end up and the
// sequence marked finished -- as the reference driver asks for it.
func (demod *Demod) SetTone(on bool) error {
	flags := byte(secEnable | secEndOfSeq)
	if on {
		flags |= secToneOn
	}
	return demod.sec(flags, nil)
}

// SendDiSEqC sends one DiSEqC message, leaving the tone where it was.
func (demod *Demod) SendDiSEqC(message []byte, tone bool) error {
	if len(message) == 0 || len(message) > secMessageBytes {
		return fmt.Errorf("a DiSEqC message is 1 to %d bytes on this chip, not %d",
			secMessageBytes, len(message))
	}
	flags := byte(secEnable | secEndOfSeq | byte(len(message))<<secLengthAt)
	if tone {
		flags |= secToneOn
	}
	return demod.sec(flags, message)
}

// sec sends the command, padded to the width the chip expects.
func (demod *Demod) sec(flags byte, message []byte) error {
	write := make([]byte, secBytes)
	write[0] = secCommand
	write[1] = flags
	copy(write[2:], message)
	if _, err := demod.command(write, 1); err != nil {
		return fmt.Errorf("Si2183 satellite equipment control: %w", err)
	}
	return nil
}
