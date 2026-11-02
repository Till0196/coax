// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package px3

import (
	"fmt"
	"sync"

	"github.com/Till0196/coax/internal/coaxd/device"
	"github.com/Till0196/coax/internal/coaxd/smartcard/asie5606"
)

// The board carries an on-board reader, so its handle fulfils the optional
// card capability of the usbdev contract.
var _ device.CardReader = (*board)(nil)

// card is the enclosure's one reader, however many tuners the board carries.
// It is serialised because a T=1 session cannot be interleaved with another.
type card struct {
	mutex  sync.Mutex
	reader *asie5606.Reader
}

// cardReader brings the card interface IC up on first use. The probe is
// deferred because most work with these boards never touches the card, and a
// missing IC must not stop a scan.
func (hardware *hardware) cardReader() (*asie5606.Reader, error) {
	if hardware.card.reader == nil {
		reader, err := hardware.probeCard()
		if err != nil {
			return nil, err
		}
		hardware.card.reader = reader
	}
	return hardware.card.reader, nil
}

// probeCard resets the card interface IC and finds the address it answers on.
func (hardware *hardware) probeCard() (*asie5606.Reader, error) {
	// The IC a stream already runs through is not reset again: that would cut
	// the stream.
	hardware.chips.Lock()
	defer hardware.chips.Unlock()
	chip, err := hardware.lookUpTSUnit(0)
	if err != nil {
		return nil, err
	}
	return asie5606.New(hardware.control.OpenCardBus(chip.address))
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
