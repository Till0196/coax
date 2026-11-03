// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

//go:build windows

package bda

import (
	"errors"
	"time"

	"github.com/Till0196/coax/internal/coaxd/bda/vendors/asicen"
)

// ErrNoCardReader reports a tuner that is not the one its enclosure's card
// slot is reached through.
var ErrNoCardReader = errors.New("this tuner has no card reader")

// cardATR is what ResetCard answers with. The driver reads the card's ATR when
// it brings the card up and gives it to nobody; this is the one the cards it
// takes answer with.
var cardATR = []byte{0x3b, 0xf0, 0x12, 0x00, 0xff, 0x91, 0x81, 0xb1, 0x7c, 0x45, 0x1f, 0x03, 0x99}

const (
	// cardTransmitTimeout covers the card being brought up, about two seconds
	// after the enclosure's first filter is opened, and an answer.
	cardTransmitTimeout = 5 * time.Second
	// cardResetTimeout covers a reset, about three seconds, after a bring-up.
	cardResetTimeout = 10 * time.Second
	// cardBringUp is how long after the card is first asked for one in the slot
	// may still be coming up: the driver brings it up about two seconds after
	// the enclosure's first filter is opened.
	cardBringUp = 4 * time.Second
	cardPoll    = 50 * time.Millisecond
)

// CardStatus reports whether the driver has a card up. The first time it is
// asked, a card in the slot is waited for.
func (device *Device) CardStatus() (bool, error) {
	if device.plexCardMutex != nil {
		device.plexCardMutex.Lock()
		defer device.plexCardMutex.Unlock()
	}
	control, err := device.cardControl()
	if err != nil {
		return false, err
	}
	for {
		present, err := control.CardPresent()
		if present || err != nil || time.Since(device.plex.controlled) >= cardBringUp {
			return present, err
		}
		time.Sleep(cardPoll)
	}
}

// ResetCard has the driver reset the card and returns its ATR.
func (device *Device) ResetCard() ([]byte, error) {
	if device.plexCardMutex != nil {
		device.plexCardMutex.Lock()
		defer device.plexCardMutex.Unlock()
	}
	control, err := device.cardControl()
	if err != nil {
		return nil, err
	}
	if err := control.ResetCard(cardResetTimeout); err != nil {
		return nil, err
	}
	return append([]byte(nil), cardATR...), nil
}

// TransmitAPDU sends one command APDU and returns the card's answer.
func (device *Device) TransmitAPDU(apdu []byte) ([]byte, error) {
	if device.plexCardMutex != nil {
		device.plexCardMutex.Lock()
		defer device.plexCardMutex.Unlock()
	}
	control, err := device.cardControl()
	if err != nil {
		return nil, err
	}
	return control.Transmit(apdu, cardTransmitTimeout)
}

// cardControl reaches the card through the filter the device holds.
func (device *Device) cardControl() (*asicen.Control, error) {
	if device.plex == nil || !asicen.CarriesCard(device.tuner.Vendor()) {
		return nil, ErrNoCardReader
	}
	return device.plex.vendor()
}
