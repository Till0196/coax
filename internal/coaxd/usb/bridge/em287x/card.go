// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later
package em287x

import (
	"errors"
	"fmt"
	"sync"
	"time"
)

// The bridge carries the card slot itself. Five registers drive it and a vendor
// request moves the data: writes to a window at cardWriteWindow, reads to one at
// zero, both in chunks of at most cardWindowChunk bytes.
const (
	registerCASStatus  = 0x70
	registerCASDataLen = 0x71
	registerCASReset   = 0x73
	registerCASMode2   = 0x75

	// cardRequest is the vendor request the data windows answer on.
	cardRequest = 0x14
	// cardWriteWindow offsets the transmit window from the receive one.
	cardWriteWindow = 0x200
	// cardWindowChunk is the most one transfer moves.
	cardWindowChunk = 64
)

// cardStatusReady is what the status register reads while a card is seated and
// the interface is idle. Zero means the card faulted or went away.
const (
	cardStatusReady = 0x05
	cardStatusGone  = 0x00
)

// The interface is given 40 polls of 8 ms to go ready, and 30 ms to settle
// between sending a block and waiting for the answer.
const (
	cardPollInterval = 8 * time.Millisecond
	cardPolls        = 40
	cardSettle       = 30 * time.Millisecond
)

// maxCardBlock is the largest T=1 block the slot can report, bounded by the
// length register being one byte wide.
const maxCardBlock = 255

// maxAPDU bounds a command APDU. The length register is handed the whole
// block, so the four framing bytes have to come out of the same 255.
const maxAPDU = maxCardBlock - 4

// ErrNoCard reports that the slot is empty or the card stopped answering.
var ErrNoCard = errors.New("no smart card is seated in the slot")

// errNoSession reports an APDU sent before the card was reset.
var errNoSession = errors.New("the card reader has no session; reset the card first")

// cardState is the reader's half of the bridge: one slot, one conversation.
type cardState struct {
	mutex sync.Mutex
	// open is set once a reset has completed, and cleared whenever the
	// conversation breaks so a stale block number is never reused.
	open bool
	// used records that the interface was switched on at least once, so Close
	// only parks it where something actually woke it.
	used bool
	// pcb is the T=1 send-sequence bit, carried in bit 6 of the block type.
	pcb byte
}

// CardPresent reports whether a card is seated, without disturbing a session.
func (bridge *Bridge) CardPresent() (bool, error) {
	bridge.card.mutex.Lock()
	defer bridge.card.mutex.Unlock()
	return bridge.cardPresent()
}

// cardPresent is CardPresent for callers already holding the reader's lock.
func (bridge *Bridge) cardPresent() (bool, error) {
	status, err := bridge.readCardRegister(registerCASStatus)
	if err != nil {
		return false, err
	}
	return status == cardStatusReady, nil
}

// ResetCard powers the card up and begins a fresh conversation.
//
// The bytes returned are whatever the slot held once the card had answered its
// reset; the hardware frames the exchange itself and does not say how the
// answer is laid out, so treat them as advisory. What confirms the reset is the
// block size negotiation that follows.
func (bridge *Bridge) ResetCard() ([]byte, error) {
	bridge.card.mutex.Lock()
	defer bridge.card.mutex.Unlock()

	bridge.card.open = false
	bridge.card.pcb = 0

	if present, err := bridge.cardPresent(); err != nil {
		return nil, err
	} else if !present {
		return nil, ErrNoCard
	}
	bridge.card.used = true
	// The length register still holds the size of the last block the previous
	// session wrote, and a reset asked for on top of that never finishes: the
	// interface stops part-way, its status settling at 3 where a reset that took
	// reaches 5. Its absence is why every second reset in a row failed -- the
	// failed one left the interface switched off.
	if err := bridge.writeCardRegister(registerCASDataLen, 0x00); err != nil {
		return nil, err
	}
	if err := bridge.writeCardRegisters([][2]byte{
		{registerCASMode1, 0x01},
		{registerCASReset, 0x01},
	}); err != nil {
		return nil, err
	}
	// A reset that does not finish must not leave the interface switched on,
	// so anything that goes wrong from here undoes the enable above.
	answer, err := bridge.reset()
	if err != nil {
		_ = bridge.writeCardRegister(registerCASMode1, 0x00)
		return nil, err
	}
	bridge.card.open = true
	return answer, nil
}

// reset is ResetCard's body, from the interface being switched on to the card
// agreeing a block size.
func (bridge *Bridge) reset() ([]byte, error) {
	if err := bridge.cardWait(); err != nil {
		return nil, err
	}
	answer, err := bridge.cardDrain()
	if err != nil {
		return nil, err
	}
	time.Sleep(time.Millisecond)
	if err := bridge.cardNegotiateIFS(); err != nil {
		return nil, err
	}
	return answer, nil
}

// ifsRequest is a complete T=1 S(IFS request) asking for the largest
// information field a card will take: node address, block type, length, the
// size itself and the check byte.
var ifsRequest = []byte{0x00, 0xc1, 0x01, 0xfe, 0x3e}

// cardNegotiateIFS runs that exchange. A card that has not started does not
// answer it, which is what makes it the reset's real test.
func (bridge *Bridge) cardNegotiateIFS() error {
	if err := bridge.writeCardWindow(ifsRequest); err != nil {
		return err
	}
	if err := bridge.writeCardRegisters([][2]byte{
		{registerCASMode2, 0x00},
		{registerCASStatus, 0x80},
	}); err != nil {
		return err
	}
	time.Sleep(cardSettle)
	if err := bridge.writeCardRegister(registerCASDataLen, byte(len(ifsRequest))); err != nil {
		return err
	}
	if err := bridge.cardWait(); err != nil {
		return err
	}
	// Only four bytes are needed: the block type says this is the response, and
	// the information field repeats the agreed size.
	answer, err := bridge.readCardWindow(4)
	if err != nil {
		return err
	}
	if answer[1] != 0xe1 || answer[3] != 0xfe {
		return fmt.Errorf("the card refused the T=1 block size negotiation (answer %x)", answer)
	}
	return nil
}

// TransmitAPDU sends one command APDU and returns the card's response with its
// status bytes, stripped of the T=1 framing.
func (bridge *Bridge) TransmitAPDU(apdu []byte) ([]byte, error) {
	if len(apdu) == 0 || len(apdu) > maxAPDU {
		return nil, fmt.Errorf("an APDU of %d bytes is outside the 1..%d the slot can carry", len(apdu), maxAPDU)
	}
	bridge.card.mutex.Lock()
	defer bridge.card.mutex.Unlock()
	if !bridge.card.open {
		return nil, errNoSession
	}
	if present, err := bridge.cardPresent(); err != nil {
		return nil, err
	} else if !present {
		// The card went away mid-conversation, so the block number is no
		// longer about anything.
		bridge.card.open = false
		return nil, ErrNoCard
	}
	if err := bridge.writeCardRegister(registerCASMode1, 0x01); err != nil {
		return nil, err
	}

	// A T=1 information block: node address, block type carrying the send
	// sequence, length, the APDU, and the check byte over everything but the
	// zero node address, which contributes nothing anyway.
	block := make([]byte, 0, len(apdu)+4)
	block = append(block, 0x00, bridge.card.pcb, byte(len(apdu)))
	block = append(block, apdu...)
	block = append(block, lrc(block[1:]))

	if err := bridge.writeCardWindow(block); err != nil {
		bridge.card.open = false
		return nil, err
	}
	// The sequence alternates between consecutive information blocks.
	bridge.card.pcb ^= 0x40

	if err := bridge.writeCardRegister(registerCASMode2, 0x00); err != nil {
		bridge.card.open = false
		return nil, err
	}
	// The value is ignored; the read itself is part of the sequence. The
	// interface is handed a length next and does not take one reliably without
	// this.
	if _, err := bridge.readCardRegister(registerCASStatus); err != nil {
		bridge.card.open = false
		return nil, err
	}
	if err := bridge.writeCardRegister(registerCASDataLen, byte(len(block))); err != nil {
		bridge.card.open = false
		return nil, err
	}
	time.Sleep(time.Millisecond)
	time.Sleep(cardSettle)
	if err := bridge.cardWait(); err != nil {
		bridge.card.open = false
		return nil, err
	}

	answer, err := bridge.cardDrain()
	if err != nil {
		bridge.card.open = false
		return nil, err
	}
	if len(answer) < 4 {
		bridge.card.open = false
		return nil, fmt.Errorf("the card returned a %d-byte T=1 block, which is too short", len(answer))
	}
	// Drop the node address, block type and length in front, and the check
	// byte behind, leaving the response APDU.
	return answer[3 : len(answer)-1], nil
}

// cardWait polls the status register until the interface reports itself ready.
func (bridge *Bridge) cardWait() error {
	for poll := 0; poll < cardPolls; poll++ {
		// The interface is never ready the instant it is handed something.
		time.Sleep(cardPollInterval)
		status, err := bridge.readCardRegister(registerCASStatus)
		if err != nil {
			// A failed read is not a failed card; keep polling.
			continue
		}
		switch status {
		case cardStatusReady:
			return nil
		case cardStatusGone:
			return ErrNoCard
		}
	}
	return errors.New("the card interface did not become ready in time")
}

// cardDrain reads however many bytes the interface says it has.
func (bridge *Bridge) cardDrain() ([]byte, error) {
	length, err := bridge.readCardRegister(registerCASDataLen)
	if err != nil {
		return nil, err
	}
	if length == 0 {
		return nil, nil
	}
	return bridge.readCardWindow(int(length))
}

// writeCardWindow pushes a block into the transmit window, a chunk at a time.
func (bridge *Bridge) writeCardWindow(block []byte) error {
	bridge.mu.Lock()
	defer bridge.mu.Unlock()
	for offset := 0; offset < len(block); offset += cardWindowChunk {
		end := min(offset+cardWindowChunk, len(block))
		if err := bridge.control(cardWriteWindow+uint16(offset), cardRequest, true, block[offset:end]); err != nil {
			return fmt.Errorf("send a T=1 block to the card: %w", err)
		}
	}
	return nil
}

// readCardWindow takes length bytes out of the receive window.
func (bridge *Bridge) readCardWindow(length int) ([]byte, error) {
	if length <= 0 || length > maxCardBlock {
		return nil, fmt.Errorf("the card interface reported %d bytes, which is outside the 1..%d a block holds", length, maxCardBlock)
	}
	bridge.mu.Lock()
	defer bridge.mu.Unlock()
	answer := make([]byte, length)
	for offset := 0; offset < length; offset += cardWindowChunk {
		end := min(offset+cardWindowChunk, length)
		if err := bridge.control(uint16(offset), cardRequest, false, answer[offset:end]); err != nil {
			return nil, fmt.Errorf("read the card's answer: %w", err)
		}
	}
	return answer, nil
}

// readCardRegister takes the lock the private helpers leave to their callers.
func (bridge *Bridge) readCardRegister(register uint16) (byte, error) {
	bridge.mu.Lock()
	defer bridge.mu.Unlock()
	return bridge.readRegister(register)
}

func (bridge *Bridge) writeCardRegister(register uint16, value byte) error {
	return bridge.WriteRegister(register, value)
}

func (bridge *Bridge) writeCardRegisters(steps [][2]byte) error {
	for _, step := range steps {
		if err := bridge.writeCardRegister(uint16(step[0]), step[1]); err != nil {
			return err
		}
	}
	return nil
}

// parkCard switches the interface off. Close calls it only on a bridge whose
// reader was actually used.
func (bridge *Bridge) parkCard() {
	bridge.card.mutex.Lock()
	defer bridge.card.mutex.Unlock()
	if !bridge.card.used {
		return
	}
	bridge.card.open = false
	_ = bridge.writeCardRegister(registerCASMode1, 0x00)
}

// lrc is the T=1 check byte: the exclusive-or of the bytes it covers.
func lrc(data []byte) byte {
	var result byte
	for _, value := range data {
		result ^= value
	}
	return result
}
