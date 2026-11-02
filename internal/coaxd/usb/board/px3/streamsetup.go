// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package px3

import "fmt"

// streamSetup is written to card interface registers 0x10 to 0x1f before a
// stream starts. Tuners sharing an IC must use the same seed.
//
// The write has to go through the controlling bridge's guarded write (request
// 0x1c; see asv5211.CardBus.WriteGuarded). An ordinary I2C write of the same
// registers can read back as written without configuring the stream transform.
var streamSetup = []byte{
	0x00, 0x00, 0x08, 0x00, 0x00, 0x01, 0xc0, 0x04,
	0x00, 0x00, 0xc0, 0xa6, 0x00, 0x00, 0x01, 0x00,
}

// streamSetupRegister is the first of the card interface registers streamSetup
// goes to.
const streamSetupRegister = 0x10

// writeStreamSetup writes streamSetup for this tuner's stream path. It goes
// through the bridge controlling the card interface IC in the tuner's path.
func (hardware *hardware) writeStreamSetup(chip *tsChip) error {
	if err := hardware.control.OpenCardBus(chip.address).WriteGuarded(streamSetupRegister, streamSetup); err != nil {
		return fmt.Errorf("write the stream setup: %w", err)
	}
	return nil
}
