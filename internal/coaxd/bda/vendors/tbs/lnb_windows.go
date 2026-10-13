// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

//go:build windows

package tbs

import (
	"encoding/binary"
	"fmt"

	"github.com/Till0196/coax/internal/coaxd/bda/vendors"
)

// Supply is the low-noise block's supply on one tuner, reached through the
// same vendor property set as the I2C bus.
//
// It is a separate item from the bus rather than a register on it: the supply
// is switched by a GPIO in the bridge FPGA, which is not on any I2C bus, and
// the vendor's driver reaches it through a property of its own.
type Supply struct {
	Properties Access
	// Name is what to call this card in a message.
	Name string
}

// Set puts the supply where a satellite channel asked for it. A tuner whose card
// has no supply wired to it answers without doing anything, so a refusal here is
// a real failure and not a card saying "not mine".
func (supply *Supply) Set(high, off bool) error {
	value := uint32(LNB13V)
	switch {
	case off:
		value = LNBOff
	case high:
		value = LNB18V
	}
	request := make([]byte, LNBStructBytes)
	binary.LittleEndian.PutUint32(request[LNBCommandAt:], LNBCommand)
	binary.LittleEndian.PutUint32(request[LNBValueAt:], value)
	if err := supply.Properties.Set(vendors.GUID(Set), PropLNB, request); err != nil {
		return fmt.Errorf("set the LNB supply on %s: %w", supply.Name, err)
	}
	return nil
}

// SendDiSEqC sends one DiSEqC message. Nothing here builds one yet; it exists
// because the property carries it in the same structure as the supply.
func (supply *Supply) SendDiSEqC(message []byte) error {
	if len(message) == 0 || len(message) > DiSEqCLengthAt-DiSEqCAt {
		return fmt.Errorf("a DiSEqC message is 1 to %d bytes, not %d",
			DiSEqCLengthAt-DiSEqCAt, len(message))
	}
	request := make([]byte, LNBStructBytes)
	binary.LittleEndian.PutUint32(request[LNBCommandAt:], DiSEqCCommand)
	copy(request[DiSEqCAt:], message)
	binary.LittleEndian.PutUint32(request[DiSEqCLengthAt:], uint32(len(message)))
	if err := supply.Properties.Set(vendors.GUID(Set), PropLNB, request); err != nil {
		return fmt.Errorf("send DiSEqC on %s: %w", supply.Name, err)
	}
	return nil
}
