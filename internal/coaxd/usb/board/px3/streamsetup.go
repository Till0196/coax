// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package px3

import "fmt"

// streamSetup is what the PX-W3U3 V2 takes in registers 0x10 to 0x1f of the card
// interface IC in each tuner's stream path before a stream starts. Without it the
// bytes after each packet's four-byte header do not come out as the demodulator
// made them, and nothing that reads the stream finds a table in it.
//
// The write has to go through the controlling bridge's guarded write (request
// 0x1c; see asv5211.CardBus.WriteGuarded). An ordinary I2C write of the same
// registers reads back as written and changes nothing. The same sixteen bytes
// serve the terrestrial and the satellite channel, and both bridges' streams.
var streamSetup = []byte{
	0x00, 0x00, 0x08, 0x00, 0x00, 0x01, 0xc0, 0x04,
	0x00, 0x00, 0xc0, 0xa6, 0x00, 0x00, 0x01, 0x00,
}

// streamSetupRegister is the first of the card interface registers streamSetup
// goes to.
const streamSetupRegister = 0x10

// writeStreamSetup writes streamSetup for this tuner's stream path. It goes
// through the controlling bridge, which serves both of an enclosure's bridges,
// to the card interface IC in the tuner's stream path.
func (hardware *hardware) writeStreamSetup(index int) error {
	hardware.chips.Lock()
	chip := hardware.chips.found[hardware.unit(index)]
	hardware.chips.Unlock()
	if chip == nil {
		return fmt.Errorf("no card interface is known in the stream path")
	}
	if err := hardware.control.OpenCardBus(chip.address).WriteGuarded(streamSetupRegister, streamSetup); err != nil {
		return fmt.Errorf("write the stream setup: %w", err)
	}
	return nil
}
