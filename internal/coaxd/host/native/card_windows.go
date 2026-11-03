// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

//go:build windows

package native

import (
	"github.com/Till0196/coax/internal/coaxd/device"
	"github.com/Till0196/coax/internal/coaxd/host/pool"
)

// A PLEX enclosure's card is reached through one of its BDA tuners, so the
// card calls follow the tuner's own way: to the host process, and there onto
// the tuner's COM thread.

// cardReceiver is a receiver device that reaches a card.
type cardReceiver interface {
	CardStatus() (bool, error)
	ResetCard() ([]byte, error)
	TransmitAPDU(apdu []byte) ([]byte, error)
}

func (o *onThread) cardReceiver() (cardReceiver, error) {
	reader, ok := o.device.(cardReceiver)
	if !ok {
		return nil, pool.ErrNoCardReader
	}
	return reader, nil
}

func (o *onThread) CardStatus() (bool, error) {
	reader, err := o.cardReceiver()
	if err != nil {
		return false, err
	}
	var present bool
	err = o.thread.do(func() error {
		answer, err := reader.CardStatus()
		present = answer
		return err
	})
	return present, err
}

func (o *onThread) ResetCard() ([]byte, error) {
	reader, err := o.cardReceiver()
	if err != nil {
		return nil, err
	}
	var atr []byte
	err = o.thread.do(func() error {
		answer, err := reader.ResetCard()
		atr = answer
		return err
	})
	return atr, err
}

func (o *onThread) TransmitAPDU(apdu []byte) ([]byte, error) {
	reader, err := o.cardReceiver()
	if err != nil {
		return nil, err
	}
	var response []byte
	err = o.thread.do(func() error {
		answer, err := reader.TransmitAPDU(apdu)
		response = answer
		return err
	})
	return response, err
}

// The card calls the host answers.

type HostTransmit struct {
	ID   int
	APDU []byte
}

func (h *Host) CardStatus(id int, reply *bool) error {
	device, err := h.device(id)
	if err != nil {
		return err
	}
	present, err := device.CardStatus()
	*reply = present
	return toHost(err)
}

func (h *Host) ResetCard(id int, reply *[]byte) error {
	device, err := h.device(id)
	if err != nil {
		return err
	}
	atr, err := device.ResetCard()
	*reply = atr
	return toHost(err)
}

func (h *Host) TransmitAPDU(arguments HostTransmit, reply *[]byte) error {
	device, err := h.device(arguments.ID)
	if err != nil {
		return err
	}
	response, err := device.TransmitAPDU(arguments.APDU)
	*reply = response
	return toHost(err)
}

func (d *hostedDevice) CardStatus() (bool, error) {
	var present bool
	err := hostCall(d.client, "CardStatus", d.id, &present)
	return present, err
}

func (d *hostedDevice) ResetCard() ([]byte, error) {
	var atr []byte
	err := hostCall(d.client, "ResetCard", d.id, &atr)
	return atr, err
}

func (d *hostedDevice) TransmitAPDU(apdu []byte) ([]byte, error) {
	var response []byte
	err := hostCall(d.client, "TransmitAPDU", HostTransmit{ID: d.id, APDU: apdu}, &response)
	return response, err
}

// cardBoard is a board whose tuner reaches its enclosure's card.
type cardBoard struct {
	*board
	reader cardReceiver
}

func (b *cardBoard) CardStatus() (device.CardStatus, error) {
	present, err := b.reader.CardStatus()
	if err != nil {
		return device.CardStatus{}, err
	}
	return device.CardStatus{Present: present, Reader: "PLEX BDA driver"}, nil
}

func (b *cardBoard) ResetCard() ([]byte, error)               { return b.reader.ResetCard() }
func (b *cardBoard) TransmitAPDU(apdu []byte) ([]byte, error) { return b.reader.TransmitAPDU(apdu) }
