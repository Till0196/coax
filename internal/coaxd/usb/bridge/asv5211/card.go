// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package asv5211

import (
	"errors"
	"fmt"
)

// CardBus is register access to the card interface IC on the bridge's I2C bus.
// The IC answers on one fixed address, so a bus carries that address rather than
// taking it per transfer; transfers are ordinary I2C.
type CardBus struct {
	bus     bus
	address byte
}

// OpenCardBus claims the card interface IC at an I2C address.
func (bridge *Bridge) OpenCardBus(address byte) *CardBus {
	return &CardBus{bus: bus{bridge: bridge, target: address}, address: address}
}

// Address reports the I2C address the card interface IC answers on.
func (card *CardBus) Address() byte { return card.address }

// Read reads count bytes starting at reg.
func (card *CardBus) Read(reg byte, count int) ([]byte, error) {
	if count <= 0 {
		return nil, errors.New("ASV5211 card read length must be positive")
	}
	return card.bus.WriteRead(card.address, []byte{reg}, count)
}

// Write writes data starting at reg.
func (card *CardBus) Write(reg byte, data []byte) error {
	return card.bus.Write(card.address, append([]byte{reg}, data...))
}

// guardTable is the fixed half of how request 0x1c encodes its byte (below).
var guardTable = [16]byte{0x39, 0x72, 0x30, 0xc3, 0xad, 0xd4, 0x46, 0x36, 0xba, 0xe5, 0x5f, 0x48, 0xae, 0x14, 0x06, 0xc1}

// WriteGuarded writes data to the IC one register at a time through request
// 0x1c rather than an ordinary I2C write. The firmware takes some writes only this way: registers 0x10 to 0x1f
// written plainly read back as written but change nothing, while the same bytes
// through 0x1c take effect (see px3's streamsetup.go).
//
// Each byte goes as index = e | (r^e)<<8 with e = byte ^ table[r&15] ^
// nonce[r>>4], for any r, where the nonce is the sixteen bytes request 0x1a
// hands out. A new nonce is taken for every call, and the I2C lock is held
// across the whole of it so that another transaction cannot take one in
// between.
func (card *CardBus) WriteGuarded(reg byte, data []byte) error {
	bridge := card.bus.bridge
	bridge.i2cMu.Lock()
	defer bridge.i2cMu.Unlock()
	// The nonce comes back bare, without the status byte other requests lead with.
	nonce, err := bridge.rawCommand(0x1a, 0, 0, len(guardTable))
	if err != nil {
		return fmt.Errorf("read the ASV5211 write nonce: %w", err)
	}
	for offset, value := range data {
		r := byte(offset*0x3b) ^ 0xcd
		encoded := value ^ guardTable[r&0x0f] ^ nonce[r>>4]
		register := reg + byte(offset)
		if _, err := bridge.command(0x1c, uint16(card.address)|uint16(register)<<8,
			uint16(encoded)|uint16(r^encoded)<<8, 1); err != nil {
			return fmt.Errorf("guarded write of register %#02x of the card interface at %#02x: %w", register, card.address, err)
		}
	}
	return nil
}
