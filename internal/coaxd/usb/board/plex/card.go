// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package plex

import (
	"fmt"
	"sync"

	"github.com/Till0196/coax/internal/coaxd/device"
	"github.com/Till0196/coax/internal/coaxd/smartcard/iso7816"
	"github.com/Till0196/coax/internal/coaxd/usb/boardkit"
)

// cardSlot is the enclosure's smart card reader. There is one slot however
// many tuners the enclosure carries, so it lives on the frame.
//
// It is brought up on the first request rather than at open time, because most
// work with these boards never touches the card and a missing or unpowered
// reader must not stop a scan from starting.
type cardSlot struct {
	mutex   sync.Mutex
	session *iso7816.Session
	probed  bool
	// powered records that the slot is holding the analogue supply up, so it
	// can be let go of once when the board closes.
	powered bool
}

// CardSession brings the slot up on first use and returns the conversation. The
// supply is raised once and kept up while the board is open: raising and
// dropping it per exchange would power-cycle an enclosure with no tuner running
// on every APDU.
func (frame *Frame) CardSession() (*iso7816.Session, error) {
	frame.card.mutex.Lock()
	defer frame.card.mutex.Unlock()
	if frame.card.probed {
		return frame.card.session, nil
	}
	session, err := frame.openCard()
	if err != nil {
		// Raising the analogue supply can fail for reasons that say nothing
		// about whether a reader is there, so the failure is reported but not
		// remembered and the next request tries again.
		return nil, err
	}
	frame.card.probed = true
	frame.card.session = session
	return session, nil
}

// openCard raises the supply and puts the bridge's UART into card mode.
func (frame *Frame) openCard() (*iso7816.Session, error) {
	if err := frame.Power.Acquire(); err != nil {
		return nil, fmt.Errorf("power up the card reader: %w", err)
	}
	frame.card.powered = true
	line, err := frame.Bridge.OpenCardLine()
	if err != nil {
		frame.card.powered = false
		_ = frame.Power.Release()
		return nil, err
	}
	return iso7816.New(line), nil
}

// releaseCard drops the supply reference the slot took, and is called once the
// board and all its tuners are done.
func (frame *Frame) releaseCard() {
	frame.card.mutex.Lock()
	defer frame.card.mutex.Unlock()
	if frame.card.session != nil {
		frame.card.session.Invalidate()
	}
	if !frame.card.powered {
		return
	}
	frame.card.powered = false
	_ = frame.Power.Release()
}

// CardBoard adds the enclosure's card reader to a board handle when the model
// carries one, and hands the handle back untouched when it does not. Whether
// there is a slot cannot be probed -- the bridge has no register for it and the
// detect pin reads the same on an empty slot as on no slot -- so each board
// package passes the answer in.
func (frame *Frame) CardBoard(inner device.Board, fitted bool, reader string) device.Board {
	if !fitted {
		return inner
	}
	return boardkit.CardBoard(inner, frame, reader)
}

var _ boardkit.CardHardware = (*Frame)(nil)

// CardPresent reports whether a card is seated in the enclosure's slot.
func (frame *Frame) CardPresent() (bool, error) {
	session, err := frame.CardSession()
	if err != nil {
		return false, err
	}
	return session.Present()
}

// ResetCard powers the card up and returns its answer to reset.
func (frame *Frame) ResetCard() ([]byte, error) {
	session, err := frame.CardSession()
	if err != nil {
		return nil, err
	}
	return session.Reset()
}

// TransmitAPDU sends one command APDU to the card and returns its answer.
func (frame *Frame) TransmitAPDU(apdu []byte) ([]byte, error) {
	session, err := frame.CardSession()
	if err != nil {
		return nil, err
	}
	return session.Transmit(apdu)
}
