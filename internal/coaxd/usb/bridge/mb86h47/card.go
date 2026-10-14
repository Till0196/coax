// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package mb86h47

import (
	"fmt"
	"time"

	"github.com/Till0196/coax/pkg/iso7816"
)

// The CECH-ZD1 J's card slot hangs off a smart-card UART in the MB86H47A at
// 0xFEA000. The host drives it register by register, and iso7816 does the
// protocol.
const (
	cardEnable  = 0xfea000
	cardLines   = 0xfea004 // bit 1 clock, bit 2 reset released, bit 3 VCC
	cardBaud    = 0xfea008
	cardSend    = 0xfea00c // 1 sends what the transmit FIFO holds
	cardControl = 0xfea010 // speed setting, then the transmit command word
	cardFIFO    = 0xfea014 // bits 16-31: bytes waiting to be read
	cardIRQMask = 0xfea018
	cardIRQ     = 0xfea01c // bit 10: no card in the slot
	cardCWT     = 0xfea020
	cardBWT     = 0xfea024
	cardTX      = 0xfea028
	cardRX      = 0xfea02c // a read takes the next byte
	cardPower   = 0xfea034
	cardSentLRC = 0xfea038
	cardRecvLRC = 0xfea03c

	cardNoCard = 1 << 10
)

// Line settings per speed, from the answer to reset, with N = F/D clock
// cycles per bit:
//
//	baud    = N<<7 | min(N>>1, 0x7F)
//	control = TC1<<16 (extra guard time; 1 when TC1 is 0xFF)
//	CWT     = 3 * 2^CWI + 11, CWI the low nibble of TB3
//	BWT     = 6,800,000 * D / F
//
// The card starts at 9600 (N = 372) for its answer to reset. The values for
// the faster speeds are worked out for F = 372, TC1 = 0xFF and TB3 = 0x45,
// with D = 2 for 19200 and D = 4 for 38400. At 38400 the BWT, 73118, does not
// fit the 16-bit register and is held at the maximum.
var cardSpeeds = map[int]struct{ baud, cwt, bwt, control uint32 }{
	9600:  {baud: 0xba7f, cwt: 9600, bwt: 0, control: 0},
	19200: {baud: 0x5d5d, cwt: 107, bwt: 36559, control: 1 << 16},
	38400: {baud: 0x2eae, cwt: 107, bwt: 0xffff, control: 1 << 16},
}

// CardLine is the card slot as the byte pipe iso7816 speaks T=1 over. It is
// usable once Start has run.
func (bridge *Bridge) CardLine() iso7816.Line { return (*cardLine)(bridge) }

type cardLine Bridge

var _ iso7816.Line = (*cardLine)(nil)

func (card *cardLine) locked(fn func(bridge *Bridge)) error {
	bridge := (*Bridge)(card)
	bridge.mu.Lock()
	defer bridge.mu.Unlock()
	bridge.err = nil
	fn(bridge)
	bridge.flushLocked()
	return bridge.err
}

// The UART's registers go through the command channel when it is up
// (command.go): writes are held back and sent together, a read is one round
// trip.
func (bridge *Bridge) cardWrite(address, value uint32) { bridge.registerWriteLocked(address, value) }
func (bridge *Bridge) cardRead(address uint32) uint32  { return bridge.registerReadLocked(address) }

func (bridge *Bridge) cardSet(address, bits uint32) {
	bridge.cardWrite(address, bridge.cardRead(address)|bits)
}

func (bridge *Bridge) cardClear(address, bits uint32) {
	bridge.cardWrite(address, bridge.cardRead(address)&^bits)
}

// cardPause waits once what is held back has reached the chip.
func (bridge *Bridge) cardPause(duration time.Duration) {
	bridge.flushLocked()
	time.Sleep(duration)
}

// Present reports whether a card is seated.
func (card *cardLine) Present() (bool, error) {
	var irq uint32
	err := card.locked(func(bridge *Bridge) { irq = bridge.cardRead(cardIRQ) })
	return irq&cardNoCard == 0, err
}

// Reset powers the card down and up again, leaving it about to send its
// answer to reset at 9600 baud.
func (card *cardLine) Reset() error {
	return card.locked(func(bridge *Bridge) {
		bridge.powerDownCardLocked()
		bridge.cardPause(10 * time.Millisecond)
		bridge.cardWrite(cardIRQ, 0x3ff)
		bridge.cardWrite(cardPower, 1)
		bridge.cardClear(cardLines, 4)
		bridge.cardWrite(cardFIFO, 0)
		bridge.cardWrite(cardEnable, 0)
		bridge.cardWrite(cardEnable, 7)
		bridge.cardSet(cardLines, 8) // VCC
		bridge.cardWrite(cardBaud, cardSpeeds[9600].baud)
		bridge.cardPause(2 * time.Millisecond)
		bridge.cardSet(cardLines, 2) // clock
		bridge.cardWrite(cardRecvLRC, 0x3b)
		bridge.cardWrite(cardCWT, cardSpeeds[9600].cwt)
		bridge.cardWrite(cardBWT, 0)
		bridge.cardWrite(cardIRQMask, 0x1ff)
		bridge.cardPause(time.Millisecond)
		bridge.cardSet(cardLines, 4) // reset released
	})
}

func (bridge *Bridge) powerDownCardLocked() {
	bridge.cardClear(cardLines, 4)
	bridge.cardWrite(cardFIFO, 0)
	bridge.cardClear(cardEnable, 4)
	bridge.cardClear(cardLines, 0xa)
	bridge.cardWrite(cardIRQMask, 1)
	bridge.cardWrite(cardPower, 0)
}

// SetBaudrate switches the line speed.
func (card *cardLine) SetBaudrate(rate int) error {
	speed, ok := cardSpeeds[rate]
	if !ok {
		return fmt.Errorf("MB86H47A card interface: %d baud is not supported", rate)
	}
	return card.locked(func(bridge *Bridge) {
		bridge.cardWrite(cardBaud, speed.baud)
		bridge.cardWrite(cardControl, speed.control)
		bridge.cardWrite(cardCWT, speed.cwt)
		bridge.cardWrite(cardBWT, speed.bwt)
	})
}

// Ready reports whether received bytes are waiting.
func (card *cardLine) Ready() (bool, error) {
	var fifo uint32
	err := card.locked(func(bridge *Bridge) { fifo = bridge.cardRead(cardFIFO) })
	return fifo>>16 != 0, err
}

// Read takes up to len(buffer) of the bytes that have arrived.
func (card *cardLine) Read(buffer []byte) (int, error) {
	count := 0
	err := card.locked(func(bridge *Bridge) {
		waiting := int(bridge.cardRead(cardFIFO) >> 16)
		if bridge.err != nil || waiting == 0 {
			return
		}
		count = copy(buffer, bridge.fifoReadLocked(cardRX, min(waiting, len(buffer))))
	})
	return count, err
}

// Write sends a block, check byte included.
func (card *cardLine) Write(data []byte) error {
	return card.locked(func(bridge *Bridge) {
		bridge.cardWrite(cardRecvLRC, 0)
		bridge.cardWrite(cardSentLRC, 0)
		bridge.fifoWriteLocked(cardTX, data)
		bridge.cardWrite(cardIRQ, 0x3ff)
		// The transmit command word, written before every send; frames of
		// 256 bytes and more take 0x280.
		command := uint32(0x200)
		if len(data) >= 0x100 {
			command = 0x280
		}
		bridge.cardWrite(cardControl, command)
		bridge.cardWrite(cardSend, 1)
	})
}
