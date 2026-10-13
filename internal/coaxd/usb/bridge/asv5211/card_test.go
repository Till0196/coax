// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package asv5211

import (
	"bytes"
	"fmt"
	"testing"
)

// cardDevice models the bridge's I2C commands closely enough to carry a
// register transfer end to end: 0x0d fills the outgoing buffer three bytes at
// a time, 0x0e puts it on the bus, and 0x19 reads the answer back. The modelled
// IC auto-increments its register pointer, which is what makes a multi-byte
// transfer address consecutive registers.
type cardDevice struct {
	// registers is what the modelled card IC holds.
	registers map[byte]byte
	// pending is the buffer 0x0d fills and 0x0e sends.
	pending []byte
	// selected is the register 0x19 will read from.
	selected byte
	// requests records every vendor request issued.
	requests []byte
	// address is the I2C target the last transfer named.
	address byte
	// nonce is what request 0x1a hands out, and nonces how many times it has.
	nonce  [16]byte
	nonces int
}

// unguard is how the firmware reads a request 0x1c byte back.
func unguard(index uint16, nonce [16]byte) byte {
	encoded, r := byte(index), byte(index>>8)^byte(index)
	return encoded ^ guardTable[r&0x0f] ^ nonce[r>>4]
}

func newCardDevice() *cardDevice {
	return &cardDevice{registers: map[byte]byte{}, pending: make([]byte, 3)}
}

func (device *cardDevice) Control(_, request byte, value, index uint16, data []byte, _ uint32) (int, error) {
	device.requests = append(device.requests, request)
	switch request {
	case 0x0d:
		offset := int(byte(value))
		chunk := []byte{byte(value >> 8), byte(index), byte(index >> 8)}
		for len(device.pending) < offset+3 {
			device.pending = append(device.pending, 0)
		}
		copy(device.pending[offset:], chunk)
		data[0] = 1
	case 0x0e:
		device.address = byte(value)
		length := len(data) - 1
		if length < 1 || length > len(device.pending) {
			return 0, fmt.Errorf("bad transfer length %d", length)
		}
		sent := device.pending[:length]
		device.selected = sent[0]
		// A write with no data only selects a register; the rest is data for
		// consecutive registers.
		for offset, value := range sent[1:] {
			device.registers[device.selected+byte(offset)] = value
		}
		data[0] = 1
	case 0x19:
		device.address = byte(value)
		data[0] = 1
		for offset := range data[1:] {
			data[offset+1] = device.registers[device.selected+byte(offset)]
		}
	case 0x1a:
		// A fresh nonce each time, and no status byte in front of it.
		device.nonces++
		for offset := range device.nonce {
			device.nonce[offset] = byte(device.nonces*31 + offset*17)
		}
		copy(data, device.nonce[:])
	case 0x1c:
		device.address = byte(value)
		device.registers[byte(value>>8)] = unguard(index, device.nonce)
		data[0], data[1] = 1, 0
	default:
		return 0, fmt.Errorf("unmodelled request %#02x", request)
	}
	return len(data), nil
}

func (device *cardDevice) Bulk(byte, []byte, uint32) (int, error) { return 0, nil }
func (device *cardDevice) Close() error                           { return nil }
func (device *cardDevice) Name() string                           { return "test" }
func (device *cardDevice) ProductID() uint16                      { return 0x0006 }

// openTestCardBus wires a bridge onto the fake device.
func openTestCardBus(device *cardDevice) *CardBus {
	return (&Bridge{usb: device}).OpenCardBus(0x5a)
}

func TestTransfersRoundTripThroughTheCardBus(t *testing.T) {
	device := newCardDevice()
	card := openTestCardBus(device)
	for _, value := range []byte{0x00, 0x01, 0x55, 0x80, 0xaa, 0xff} {
		if err := card.Write(0x40, []byte{value}); err != nil {
			t.Fatal(err)
		}
		if got := device.registers[0x40]; got != value {
			t.Fatalf("IC holds %#02x; want %#02x", got, value)
		}
		read, err := card.Read(0x40, 1)
		if err != nil {
			t.Fatal(err)
		}
		if read[0] != value {
			t.Fatalf("read back %#02x; want %#02x", read[0], value)
		}
	}
}

func TestLongerTransfersAddressConsecutiveRegisters(t *testing.T) {
	device := newCardDevice()
	card := openTestCardBus(device)
	written := []byte{1, 2, 3, 4, 5, 6, 7, 8}
	if err := card.Write(0x40, written); err != nil {
		t.Fatal(err)
	}
	read, err := card.Read(0x40, len(written))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(read, written) {
		t.Fatalf("read back % x; want % x", read, written)
	}
}

func TestTransfersAddressTheBusTheCardWasOpenedOn(t *testing.T) {
	device := newCardDevice()
	card := openTestCardBus(device)
	if _, err := card.Read(0x09, 1); err != nil {
		t.Fatal(err)
	}
	if device.address != card.Address() {
		t.Fatalf("addressed %#02x; want %#02x", device.address, card.Address())
	}
}

// TestGuardedWritesArriveAsWritten: every byte of a guarded write lands in its
// register once the firmware has taken the nonce off, over one nonce taken for
// the whole write.
func TestGuardedWritesArriveAsWritten(t *testing.T) {
	device := newCardDevice()
	card := openTestCardBus(device)
	written := []byte{0x00, 0x00, 0x08, 0x00, 0x00, 0x01, 0xc0, 0x04, 0x00, 0x00, 0xc0, 0xa6, 0x00, 0x00, 0x01, 0x00}
	for pass := 0; pass < 2; pass++ {
		if err := card.WriteGuarded(0x10, written); err != nil {
			t.Fatal(err)
		}
		for offset, value := range written {
			if got := device.registers[0x10+byte(offset)]; got != value {
				t.Fatalf("pass %d: register %#02x holds %#02x; want %#02x", pass, 0x10+offset, got, value)
			}
		}
	}
	if device.nonces != 2 {
		t.Errorf("%d nonces taken for two writes; want one each", device.nonces)
	}
	if device.address != card.Address() {
		t.Errorf("addressed %#02x; want %#02x", device.address, card.Address())
	}
}

// TestGuardedIndicesDecodeToTheirBytes: known requests, with the nonce they
// were encoded under, read back as the bytes they carry.
func TestGuardedIndicesDecodeToTheirBytes(t *testing.T) {
	nonce := [16]byte{0xbb, 0xf3, 0x44, 0xfc, 0x69, 0xcd, 0x31, 0xbe, 0x2b, 0x50, 0xaa, 0xa1, 0x2e, 0xb7, 0x09, 0x99}
	for _, known := range []struct {
		index uint16
		want  byte
	}{{0x41b4, 0xf9}, {0xcde7, 0xfc}, {0xa4c9, 0xec}, {0x2051, 0x9d}} {
		if got := unguard(known.index, nonce); got != known.want {
			t.Errorf("index %#04x reads as %#02x; want %#02x", known.index, got, known.want)
		}
	}
}

func TestReadRejectsAnEmptyTransfer(t *testing.T) {
	device := newCardDevice()
	card := openTestCardBus(device)
	for _, count := range []int{0, -1} {
		if _, err := card.Read(0x09, count); err == nil {
			t.Errorf("accepted a read of %d bytes", count)
		}
	}
	if device.requests != nil {
		t.Fatalf("touched the bus: % x", device.requests)
	}
}
