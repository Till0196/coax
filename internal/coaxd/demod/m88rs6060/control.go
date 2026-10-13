// Copyright (c) Montage; TurboSight; 2026 Till0196
// SPDX-License-Identifier: GPL-2.0-or-later

package m88rs6060

// SetVoltage drives the chip's own LNB supply regulator through register
// 0xA2 -- unlike TBS5520SE's AV201x, which has no regulator and is switched
// by the bridge's GPIOs. high selects 18 V (horizontal/left), false 13 V
// (vertical/right); off cuts LNB power entirely and wins over high.
func (demod *Demod) SetVoltage(high, off bool) error {
	var value byte
	if !off {
		value |= 0x02 // lnb_power
		if high {
			value |= 0x01 // voltage_sel
		}
	}
	return demod.regUpdateBits(0xa2, 0x03, value)
}

// SetTone puts the continuous 22 kHz tone on the aerial lead, or takes it off:
// it selects a universal LNB's higher local oscillator, and on the 124/128
// degrees east DVB-S2 feeds, which satellite reaches the tuner. Two quirks are
// deliberate: the tone bit is inverted (zero turns it on), and the mask on the
// second write is zero when the tone goes off, making that write a no-op.
func (demod *Demod) SetTone(on bool) error {
	if demod.config.NoTone {
		return nil
	}
	tone, mask := byte(1), byte(0x00)
	if on {
		tone, mask = 0, 0x47
	}
	// The unused envelope mode is the other half of the field; it stays zero.
	if err := demod.regUpdateBits(0xa2, 0xe0, tone<<7); err != nil {
		return err
	}
	return demod.regUpdateBits(0xa1, mask, 1<<2)
}
