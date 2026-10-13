// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package it9175

import (
	"errors"
	"fmt"
	"sync"
	"time"
)

// The chip carries the card slot's UART itself: a pair of mailbox 0x80 commands
// move T=1 blocks in and out of its FIFO, and registers say whether a card is
// seated, whether an answer is waiting and how much of it there is.
const (
	// cardMailbox routes a command at the card interface rather than at a
	// register bank.
	cardMailbox = 0x80
	// commandCardPower switches the slot's supply. Its payload is one byte:
	// zero drops the card, one brings it up.
	commandCardPower = 0x06

	// cardStatusRegister reads zero while a card is seated and answering.
	cardStatusRegister = 0x80fba5
	// cardPendingRegister becomes non-zero once an answer has arrived.
	cardPendingRegister = 0x8001e8
	// cardRemainingRegister counts the bytes still in the receive FIFO.
	cardRemainingRegister = 0x8001cb
	// cardArmRegister is written before each transmission to arm the receiver.
	cardArmRegister = 0x8001ec
	// cardResetRegister and cardGateRegister frame the power-cycle sequence.
	cardResetRegister = 0x8001eb
	cardGateRegister  = 0xd8b7
)

// cardChunk is the most the receive FIFO hands over in one read.
const cardChunk = 0x20

// maxCardAnswer bounds one answer: a T=1 block carries at most 254 bytes of
// information plus four of framing, so anything past that is the chip failing to
// stop.
const maxCardAnswer = 258

// cardAnswerTimeout covers the slowest answer a card gives.
const cardAnswerTimeout = time.Second

// maxAPDU bounds a command APDU so that its T=1 block still fits one frame.
const maxAPDU = maxWriteLength - 5

// ErrNoCard reports that the slot is empty or the card is not answering.
var ErrNoCard = errors.New("no smart card is seated in the IT9175's slot")

// errNoSession reports an APDU sent before the card was reset.
var errNoSession = errors.New("the IT9175 card reader has no session; reset the card first")

// cardState is the reader's half of the device: one slot, one conversation.
type cardState struct {
	mutex sync.Mutex
	// open is set once a reset has produced an ATR, and cleared whenever the
	// conversation breaks, so a stale sequence number is never reused.
	open bool
	// sequence is the T=1 send-sequence bit, carried in the block's PCB.
	sequence byte
	atr      []byte
}

// CardPresent reports whether a card is seated, without disturbing a session.
func (device *Device) CardPresent() (bool, error) {
	device.card.mutex.Lock()
	defer device.card.mutex.Unlock()
	return device.cardPresent()
}

// cardPresent is CardPresent for callers already holding the reader's lock.
func (device *Device) cardPresent() (bool, error) {
	state, err := device.ReadRegister(cardStatusRegister)
	if err != nil {
		return false, err
	}
	return state == 0, nil
}

// ATR returns the answer-to-reset of the current session, or nil when there
// is none.
func (device *Device) ATR() []byte {
	device.card.mutex.Lock()
	defer device.card.mutex.Unlock()
	if !device.card.open {
		return nil
	}
	return append([]byte(nil), device.card.atr...)
}

// ResetCard power-cycles the card and returns its answer-to-reset, beginning a
// fresh conversation.
func (device *Device) ResetCard() ([]byte, error) {
	device.card.mutex.Lock()
	defer device.card.mutex.Unlock()

	device.card.open = false
	device.card.sequence = 0
	device.card.atr = nil

	if present, err := device.cardPresent(); err != nil {
		return nil, err
	} else if !present {
		return nil, ErrNoCard
	}

	// The slot is dropped and raised twice before the answer is read. A single
	// pass leaves some cards silent, so both are kept.
	for pass := 0; pass < 2; pass++ {
		if err := device.cardPowerCycle(); err != nil {
			return nil, err
		}
	}
	if present, err := device.cardPresent(); err != nil {
		return nil, err
	} else if !present {
		return nil, ErrNoCard
	}
	// The answer is not framed as a T=1 block, so it is taken straight out of
	// the FIFO once the card has had time to send it.
	time.Sleep(200 * time.Millisecond)
	atr, err := device.cardDrain()
	if err != nil {
		return nil, err
	}
	if len(atr) == 0 {
		return nil, fmt.Errorf("%w: the card sent no answer to reset", ErrNoCard)
	}
	// Leave the slot powered for the conversation that follows.
	if err := device.cardPower(1); err != nil {
		return nil, err
	}
	if err := device.cardNegotiateIFS(); err != nil {
		return nil, err
	}
	device.card.atr = atr
	device.card.open = true
	return append([]byte(nil), atr...), nil
}

// cardPowerCycle drops the slot's supply and brings it back, with the gate
// register closed around the change.
func (device *Device) cardPowerCycle() error {
	if err := device.WriteRegister(cardGateRegister, 0x00); err != nil {
		return err
	}
	if err := device.WriteRegister(cardResetRegister, 0x02); err != nil {
		return err
	}
	if err := device.cardPower(0); err != nil {
		return err
	}
	return device.WriteRegister(cardGateRegister, 0x01)
}

func (device *Device) cardPower(on byte) error {
	device.commandMu.Lock()
	defer device.commandMu.Unlock()
	device.payload()[0] = on
	if err := device.command(cardMailbox, commandCardPower, 1, 0); err != nil {
		return fmt.Errorf("switch the IT9175 card slot: %w", err)
	}
	return nil
}

// cardNegotiateIFS asks the card for the largest block it will accept. The answer
// is only used to confirm the card speaks T=1, but the exchange has to happen
// before the first command or the card rejects it.
func (device *Device) cardNegotiateIFS() error {
	if err := device.WriteRegister(cardArmRegister, 0x01); err != nil {
		return err
	}
	// An S(IFS request) carrying the maximum information field size.
	if err := device.cardSend(0xc1, []byte{0xfe}); err != nil {
		return err
	}
	answer, err := device.cardReceive()
	if err != nil {
		return err
	}
	// The expected reply is S(IFS response). An unexpected block is accepted --
	// a card that answers something else is still talking -- but silence means
	// the card never entered T=1 and the commands that follow have nothing to
	// talk to.
	if len(answer) == 0 {
		return errors.New("the card did not answer the T=1 block size negotiation")
	}
	return nil
}

// TransmitAPDU sends one command APDU and returns the card's response with its
// status bytes, stripped of the T=1 framing.
func (device *Device) TransmitAPDU(apdu []byte) ([]byte, error) {
	if len(apdu) == 0 || len(apdu) > maxAPDU {
		return nil, fmt.Errorf("an APDU of %d bytes is outside the 1..%d the IT9175 can carry", len(apdu), maxAPDU)
	}
	device.card.mutex.Lock()
	defer device.card.mutex.Unlock()
	if !device.card.open {
		return nil, errNoSession
	}
	if present, err := device.cardPresent(); err != nil {
		return nil, err
	} else if !present {
		// The card went away mid-conversation, so the sequence number and the
		// ATR are no longer about anything.
		device.card.open = false
		device.card.atr = nil
		return nil, ErrNoCard
	}
	if err := device.WriteRegister(cardArmRegister, 0x01); err != nil {
		return nil, err
	}
	// The send-sequence bit alternates between consecutive information blocks.
	pcb := byte(0)
	if device.card.sequence&1 != 0 {
		pcb = 0x40
	}
	if err := device.cardSend(pcb, apdu); err != nil {
		device.card.open = false
		return nil, err
	}
	device.card.sequence++
	block, err := device.cardReceive()
	if err != nil {
		device.card.open = false
		return nil, err
	}
	// Drop the node address, block type and length in front, and the error
	// check byte behind, leaving the response APDU.
	if len(block) < 4 {
		device.card.open = false
		return nil, fmt.Errorf("the card returned a %d-byte T=1 block, which is too short", len(block))
	}
	return append([]byte(nil), block[3:len(block)-1]...), nil
}

// cardSend writes one T=1 block into the transmit FIFO. The node address is
// always zero: there is one card and one reader.
func (device *Device) cardSend(pcb byte, data []byte) error {
	device.commandMu.Lock()
	defer device.commandMu.Unlock()
	payload := device.payload()
	// The command opens with the block's length, then the block itself.
	payload[0] = byte(len(data) + 4)
	payload[1] = 0x00
	payload[2] = pcb
	payload[3] = byte(len(data))
	copy(payload[4:], data)
	// The error check byte is the exclusive-or of the block, which the zero
	// node address does not contribute to.
	payload[4+len(data)] = lrc(payload[2 : 4+len(data)])
	if err := device.command(cardMailbox, commandConfigWrite, 5+len(data), 0); err != nil {
		return fmt.Errorf("send a T=1 block to the card: %w", err)
	}
	return nil
}

// cardReceive waits for the card's answer and takes the whole of it.
func (device *Device) cardReceive() ([]byte, error) {
	if err := device.cardWait(cardAnswerTimeout); err != nil {
		return nil, err
	}
	return device.cardDrain()
}

// cardWait blocks until the chip reports an answer waiting.
func (device *Device) cardWait(timeout time.Duration) error {
	const interval = 10 * time.Millisecond
	for elapsed := time.Duration(0); elapsed < timeout; elapsed += interval {
		pending, err := device.ReadRegister(cardPendingRegister)
		if err != nil {
			return err
		}
		if pending != 0 {
			return nil
		}
		time.Sleep(interval)
	}
	return errors.New("the card did not answer in time")
}

// cardDrain empties the receive FIFO a chunk at a time until the chip says
// nothing is left. The total is bounded so a chip reporting bytes it never hands
// over fails rather than spinning for good.
func (device *Device) cardDrain() ([]byte, error) {
	var answer []byte
	for len(answer) <= maxCardAnswer {
		remaining, err := device.ReadRegister(cardRemainingRegister)
		if err != nil {
			return nil, err
		}
		if remaining == 0 {
			return answer, nil
		}
		chunk := min(int(remaining), cardChunk)
		data, err := device.cardRead(chunk)
		if err != nil {
			return nil, err
		}
		answer = append(answer, data...)
	}
	return nil, fmt.Errorf("the card sent more than %d bytes without stopping", maxCardAnswer)
}

// cardRead takes one chunk out of the receive FIFO.
func (device *Device) cardRead(length int) ([]byte, error) {
	device.commandMu.Lock()
	defer device.commandMu.Unlock()
	device.payload()[0] = byte(length)
	if err := device.command(cardMailbox, commandConfigRead, 1, length); err != nil {
		return nil, fmt.Errorf("read the card's answer: %w", err)
	}
	return append([]byte(nil), device.response()[:length]...), nil
}

// lrc is the T=1 error check byte: the exclusive-or of the bytes it covers.
func lrc(data []byte) byte {
	var result byte
	for _, value := range data {
		result ^= value
	}
	return result
}
