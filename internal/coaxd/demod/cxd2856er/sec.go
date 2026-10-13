// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package cxd2856er

// The satellite equipment control block: the 22 kHz tone and DiSEqC.
//
// No board this driver serves can use it. The PX-MLT boards feed their aerial
// lead from an on/off GPIO, with no way to pick a voltage and no tone circuit
// on the print, and Japan's ISDB-S wants neither -- the two circular
// polarisations are separated by IF.
//
// The block itself lives in the sony package, which this driver's core embeds;
// what is here is only this port's lock. The registers were read on a CXD2857ER
// rather than on this part; nothing in the family's own driver treats the two
// differently.

// SetTone puts the continuous 22 kHz tone on the aerial lead, or takes it off.
func (demod *Demod) SetTone(on bool) error {
	demod.mu.Lock()
	defer demod.mu.Unlock()
	return demod.Core.SetTone(on)
}

// SendDiSEqC sends one DiSEqC message and waits for the chip to say it has
// gone. Nothing in this program builds one for these boards yet; see the top
// of this file.
func (demod *Demod) SendDiSEqC(message []byte) error {
	demod.mu.Lock()
	defer demod.mu.Unlock()
	return demod.Core.SendDiSEqC(message)
}
