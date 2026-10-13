// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package cxd2856er

import (
	"testing"

	"github.com/Till0196/coax/internal/coaxd/demod/sony"
)

// idBus answers the two identity registers on the system address and records
// which bank was selected before they were read.
type idBus struct {
	bank     byte
	register byte
	high     byte
	low      byte
	readBank byte
}

func (bus *idBus) Write(_ byte, data []byte) error {
	switch {
	case len(data) == 2 && data[0] == 0x00:
		bus.bank = data[1]
	case len(data) == 1:
		bus.register = data[0]
		bus.readBank = bus.bank
	}
	return nil
}

func (bus *idBus) Read(_ byte, length int) ([]byte, error) {
	response := make([]byte, length)
	switch bus.register {
	case 0xfb:
		response[0] = bus.high
	case 0xfd:
		response[0] = bus.low
	}
	return response, nil
}

// TestChipIDReadsBothHalves: the identifier is ten bits, and reading the low
// register alone cannot tell a CXD2856ER (0x090) from a CXD6802GL (0x197) or a
// CXD2878A (0x39a) -- the byte that differs is the one it would drop.
func TestChipIDReadsBothHalves(t *testing.T) {
	for _, want := range []sony.Chip{
		sony.ChipCXD2856, sony.ChipCXD2857, sony.ChipCXD6802,
		sony.ChipCXD2879, sony.ChipCXD2878, sony.ChipCXD2878A,
	} {
		bus := &idBus{high: byte(uint16(want) >> 8), low: byte(want)}
		got, err := New(bus, 0xdc, 0xd8, sony.Config{}).ChipID()
		if err != nil {
			t.Fatalf("ChipID(): %v", err)
		}
		if got != want {
			t.Errorf("ChipID() = %#03x (%s), want %#03x (%s)", uint16(got), got, uint16(want), want)
		}
	}
}

// TestChipIDSelectsBankZero: the identity registers are in bank 0, and a read
// taken in whatever bank the last tune left selected returns something else
// without failing.
func TestChipIDSelectsBankZero(t *testing.T) {
	bus := &idBus{high: 0x00, low: 0x90}
	if _, err := New(bus, 0xdc, 0xd8, sony.Config{}).ChipID(); err != nil {
		t.Fatal(err)
	}
	if bus.readBank != 0x00 {
		t.Errorf("the identity registers were read in bank %#02x", bus.readBank)
	}
}

// TestChipIDKeepsOnlyTheTenBits, because the rest of the high register is
// something else and would turn a known chip into an unknown one.
func TestChipIDKeepsOnlyTheTenBits(t *testing.T) {
	bus := &idBus{high: 0xfc | 0x00, low: 0x90}
	got, err := New(bus, 0xdc, 0xd8, sony.Config{}).ChipID()
	if err != nil {
		t.Fatal(err)
	}
	if got != sony.ChipCXD2856 {
		t.Errorf("ChipID() = %#03x with the upper bits set, want %#03x", uint16(got), uint16(sony.ChipCXD2856))
	}
}
