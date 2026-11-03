// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package asicen

import (
	"crypto/aes"
	"crypto/cipher"
	"errors"
	"fmt"
	"io"
	"time"
)

// A command goes to the card through propCardSend and its answer comes back
// through propCardReceive, each in 16-byte blocks of AES-128 under a key made
// when the device is opened: cardKeyBase XORed with the random propRandom
// reports.
const (
	propCardSend    = 16
	propCardReceive = 17
	// propWrite writes up to two bytes to a device on the I2C bus.
	propWrite = 6
	// cardBuffer is the size both items take: a state byte, a length byte, then
	// the blocks.
	cardBuffer = 0x142
	// cardLimit is the longest command and answer the driver carries.
	cardLimit = 0xff
	// cardPoll is how often an answer is looked for.
	cardPoll = 10 * time.Millisecond
)

// The card session's states. In cardResetting the driver brings the card up,
// which it does about two seconds after the device is opened, and again when
// the card stops answering. In cardReady it takes a command and holds the
// answer to the last one; any other state is a command under way.
const (
	cardResetting = 0
	cardReady     = 1
)

// The card interface IC, at the first of cardInterfaces that takes a write,
// deactivates the card when cardDeactivate is written to cardControl. The next
// command then goes unanswered, and the driver resets the card.
var cardInterfaces = []byte{0x5a, 0x4a}

const (
	cardControl    = 0x00
	cardDeactivate = 0x80
)

var cardKeyBase = [16]byte{
	0x3c, 0x51, 0x93, 0x13, 0x9b, 0xa2, 0x41, 0xc9,
	0xb3, 0xfe, 0xb5, 0xda, 0xfe, 0xa0, 0x05, 0xe2,
}

// ErrCardNotReady reports that the driver has no card session to take a
// command: no card, or one not brought up in time.
var ErrCardNotReady = errors.New("the ASICEN driver has no card ready")

// ErrCardReset reports that the card was reset before it answered: the
// command was lost and can be sent again.
var ErrCardReset = errors.New("the card was reset before it answered")

// CardPresent reports whether the driver has a card up, or one being taken
// through a command. With no card in the slot, and while one is reset, it has
// not.
func (c *Control) CardPresent() (bool, error) {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	if c.closed {
		return false, io.ErrClosedPipe
	}
	block, err := c.cardCipherLocked()
	if err != nil {
		return false, err
	}
	state, _, err := c.receiveCardLocked(block)
	return state != cardResetting, err
}

// Transmit sends one command to the card and returns its answer, status words
// included. The timeout covers waiting for the card to be brought up as well.
func (c *Control) Transmit(command []byte, timeout time.Duration) ([]byte, error) {
	limit := c.cardCommandLimit
	if limit == 0 {
		limit = cardLimit
	}
	if len(command) == 0 || len(command) > limit {
		return nil, fmt.Errorf("ASICEN card command of %d bytes; want 1..%d", len(command), limit)
	}
	c.mutex.Lock()
	defer c.mutex.Unlock()
	if c.closed {
		return nil, io.ErrClosedPipe
	}
	block, err := c.cardCipherLocked()
	if err != nil {
		return nil, err
	}
	deadline := time.Now().Add(timeout)
	if err := c.awaitCardLocked(block, deadline); err != nil {
		return nil, err
	}
	if err := c.sendCardLocked(block, command); err != nil {
		return nil, err
	}

	// The driver marks the command under way as it takes it and ready again
	// once the card has answered. A command identical to the last one is not
	// sent again, and the answer to it stays where it was. A reset in between
	// leaves the answer to the driver's own command.
	for {
		state, answer, err := c.receiveCardLocked(block)
		if err != nil {
			return nil, err
		}
		switch {
		case state == cardResetting:
			return nil, ErrCardReset
		case state == cardReady && len(answer) > 0:
			return answer, nil
		}
		if !time.Now().Before(deadline) {
			return nil, fmt.Errorf("the card did not answer within %v (state %d)", timeout, state)
		}
		time.Sleep(cardPoll)
	}
}

// ResetCard has the driver reset the card and waits until it is brought up
// again.
func (c *Control) ResetCard(timeout time.Duration) error {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	if c.closed {
		return io.ErrClosedPipe
	}
	block, err := c.cardCipherLocked()
	if err != nil {
		return err
	}
	deadline := time.Now().Add(timeout)
	if err := c.awaitCardLocked(block, deadline); err != nil {
		return err
	}
	if err := c.deactivateCardLocked(); err != nil {
		return err
	}
	// Any command sets the reset off, as long as the card is asked: the driver
	// answers a command identical to the last one, whoever sent it, without the
	// card, and stays ready. Of two different ones, at most one is that.
	for _, trigger := range [][]byte{{0x90, 0x32, 0x00, 0x00, 0x00}, {0x90, 0x30, 0x00, 0x00, 0x00}} {
		if err := c.sendCardLocked(block, trigger); err != nil {
			return err
		}
		state, _, err := c.receiveCardLocked(block)
		if err != nil {
			return err
		}
		if state == cardReady {
			continue
		}
		for reset := false; ; {
			if state == cardResetting {
				reset = true
			} else if reset && state == cardReady {
				return nil
			}
			if !time.Now().Before(deadline) {
				return fmt.Errorf("the card was not reset within %v (state %d)", timeout, state)
			}
			time.Sleep(cardPoll)
			if state, _, err = c.receiveCardLocked(block); err != nil {
				return err
			}
		}
	}
	return errors.New("the driver answered both commands without the card")
}

// awaitCardLocked waits until the card session takes a command. The caller
// holds c.mutex.
func (c *Control) awaitCardLocked(block cipher.Block, deadline time.Time) error {
	for {
		state, _, err := c.receiveCardLocked(block)
		if err != nil {
			return err
		}
		if state == cardReady {
			return nil
		}
		if !time.Now().Before(deadline) {
			return fmt.Errorf("%w (state %d)", ErrCardNotReady, state)
		}
		time.Sleep(cardPoll)
	}
}

// sendCardLocked hands one command to the driver. The caller holds c.mutex.
func (c *Control) sendCardLocked(block cipher.Block, command []byte) error {
	send := make([]byte, cardBuffer)
	send[1] = byte(len(command))
	padded := make([]byte, (len(command)+15)/16*16)
	copy(padded, command)
	for at := 0; at < len(padded); at += 16 {
		block.Encrypt(send[2+at:], padded[at:at+16])
	}
	if err := c.properties.Set(Set, propCardSend, send); err != nil {
		return fmt.Errorf("send the card command: %w", err)
	}
	return nil
}

// deactivateCardLocked deactivates the card through the card interface IC.
// The caller holds c.mutex.
func (c *Control) deactivateCardLocked() error {
	var err error
	for _, address := range cardInterfaces {
		if err = c.properties.Set(Set, propWrite, []byte{address, 2, cardControl, cardDeactivate}); err == nil {
			return nil
		}
	}
	return fmt.Errorf("deactivate the card: %w", err)
}

// cardCipherLocked makes the session's cipher on first use. The caller holds
// c.mutex.
func (c *Control) cardCipherLocked() (cipher.Block, error) {
	if c.card != nil {
		return c.card, nil
	}
	var random [16]byte
	n, err := c.properties.Get(Set, propRandom, random[:])
	if err != nil {
		return nil, fmt.Errorf("ASICEN random: %w", err)
	}
	if n != uint32(len(random)) {
		return nil, fmt.Errorf("ASICEN random: got %d bytes", n)
	}
	key := cardKeyBase
	for i := range key {
		key[i] ^= random[i]
	}
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return nil, err
	}
	c.card = block
	return block, nil
}

// receiveCardLocked reads the card state and the answer the driver holds. The
// caller holds c.mutex.
func (c *Control) receiveCardLocked(block cipher.Block) (byte, []byte, error) {
	received := make([]byte, cardBuffer)
	n, err := c.properties.Get(Set, propCardReceive, received)
	if err != nil {
		return 0, nil, fmt.Errorf("receive the card answer: %w", err)
	}
	if n < 2 {
		return 0, nil, fmt.Errorf("ASICEN card answer: got %d bytes", n)
	}
	state, length := received[0], int(received[1])
	blocks := (length + 15) / 16 * 16
	if length == 0 || 2+blocks > int(n) {
		return state, nil, nil
	}
	answer := make([]byte, blocks)
	for at := 0; at < blocks; at += 16 {
		block.Decrypt(answer[at:], received[2+at:2+at+16])
	}
	return state, answer[:length], nil
}
