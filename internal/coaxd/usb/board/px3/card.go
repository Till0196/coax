// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package px3

import (
	"fmt"
	"sync"
	"time"

	"github.com/Till0196/coax/internal/coaxd/device"
	"github.com/Till0196/coax/internal/coaxd/smartcard/asie5606"
)

// The board carries an on-board reader, so its handle fulfils the optional
// card capability of the usbdev contract.
var _ device.CardReader = (*board)(nil)

// cardAddresses are the I2C addresses the card interface IC may answer on.
// They are tried in this order and whichever replies is kept, because the same
// enclosure shipped with the IC strapped either way.
var cardAddresses = []byte{0x5a, 0x4a}

// cardIdentityRegister is read to tell an IC that is there from one that is
// not; its answer also carries the revision.
const cardIdentityRegister = 0x09

// cardResetSequence pulses the card interface IC's reset line, which shares a
// GPIO bank with the front ends.
var cardResetSequence = []gpioStep{
	{value: 0xff, mask: 0x11, wait: 25 * time.Millisecond},
	{value: 0x00, mask: 0x11, wait: 25 * time.Millisecond},
	{value: 0xff, mask: 0x11, wait: 50 * time.Millisecond},
}

// card is the enclosure's one reader, however many tuners the board carries.
// It is serialised because a T=1 session cannot be interleaved with another.
type card struct {
	mutex  sync.Mutex
	reader *asie5606.Reader
	probed bool
	err    error
}

// cardReader brings the card interface IC up on first use. The probe is
// deferred because most work with these boards never touches the card, and a
// missing IC must not stop a scan.
func (hardware *hardware) cardReader() (*asie5606.Reader, error) {
	if hardware.card.probed {
		return hardware.card.reader, hardware.card.err
	}
	hardware.card.probed = true
	hardware.card.reader, hardware.card.err = hardware.probeCard()
	return hardware.card.reader, hardware.card.err
}

// probeCard resets the card interface IC and finds the address it answers on.
func (hardware *hardware) probeCard() (*asie5606.Reader, error) {
	// The IC a stream already runs through is not reset again: that would cut
	// the stream.
	hardware.chips.Lock()
	chip := hardware.chips.found[0]
	hardware.chips.Unlock()
	if chip != nil {
		return asie5606.New(hardware.control.OpenCardBus(chip.address))
	}
	for _, step := range cardResetSequence {
		if err := hardware.control.SetGPIO(step.value, step.mask); err != nil {
			return nil, fmt.Errorf("reset the card interface: %w", err)
		}
		time.Sleep(step.wait)
	}
	for _, address := range cardAddresses {
		cardBus := hardware.control.OpenCardBus(address)
		if _, err := cardBus.Read(cardIdentityRegister, 1); err != nil {
			continue
		}
		reader, err := asie5606.New(cardBus)
		if err != nil {
			return nil, err
		}
		return reader, nil
	}
	return nil, fmt.Errorf("no card interface answered on this board")
}

// CardStatus reports whether a card is seated in the enclosure's slot.
func (board *board) CardStatus() (device.CardStatus, error) {
	board.hardware.card.mutex.Lock()
	defer board.hardware.card.mutex.Unlock()
	reader, err := board.hardware.cardReader()
	if err != nil {
		return device.CardStatus{}, err
	}
	present, err := reader.Present()
	if err != nil {
		return device.CardStatus{}, err
	}
	return device.CardStatus{
		Present: present,
		Reader:  fmt.Sprintf("ASIE5606 rev %d", reader.Version()),
	}, nil
}

// ResetCard powers the card up and returns its ATR.
func (board *board) ResetCard() ([]byte, error) {
	board.hardware.card.mutex.Lock()
	defer board.hardware.card.mutex.Unlock()
	reader, err := board.hardware.cardReader()
	if err != nil {
		return nil, err
	}
	return reader.Reset()
}

// TransmitAPDU sends one command APDU to the card and returns its answer.
func (board *board) TransmitAPDU(apdu []byte) ([]byte, error) {
	board.hardware.card.mutex.Lock()
	defer board.hardware.card.mutex.Unlock()
	reader, err := board.hardware.cardReader()
	if err != nil {
		return nil, err
	}
	return reader.Transmit(apdu)
}
