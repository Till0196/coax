// Copyright (c) 2014 Budi Rachmanto; 2026 Till0196
// SPDX-License-Identifier: GPL-2.0-only

package nm120

import (
	"errors"
	"testing"
)

// The demodulator this tuner sits behind, as the PX-W3U3 V2's board
// addresses it.
const testDemod = 0x22

// fakeBus records what the driver writes and answers the identity register.
type fakeBus struct {
	// identity is what the chip answers when its identity register is read.
	identity uint32
	writes   [][]byte
	// selected is the sixteen-bit register the last write selected.
	selected uint16
}

func (bus *fakeBus) Write(address byte, data []byte) error {
	if address != testDemod {
		return errors.New("a transfer went somewhere other than the demodulator")
	}
	if len(data) < 4 || data[0] != 0xfe || data[1] != 0xce {
		if len(data) == 2 {
			// A demodulator register, written directly rather than tunnelled.
			bus.writes = append(bus.writes, append([]byte(nil), data...))
			return nil
		}
		return errors.New("a tunnelled transfer was malformed")
	}
	bus.selected = uint16(data[2])<<8 | uint16(data[3])
	bus.writes = append(bus.writes, append([]byte(nil), data...))
	return nil
}

func (bus *fakeBus) Read(byte, int) ([]byte, error) {
	return nil, errors.New("this driver reads through WriteRead")
}

func (bus *fakeBus) WriteRead(address byte, write []byte, readLength int) ([]byte, error) {
	if address != testDemod || len(write) != 2 || write[0] != 0xfe || write[1] != 0xcf {
		return nil, errors.New("a read did not turn the bus around properly")
	}
	out := make([]byte, readLength)
	if bus.selected == identityRegister && readLength == 4 {
		for index := range out {
			out[index] = byte(bus.identity >> (8 * index))
		}
	}
	return out, nil
}

// tunnelled returns the (register, value) pairs written through the tunnel
// with a one-byte payload.
func (bus *fakeBus) tunnelled() [][2]uint16 {
	var pairs [][2]uint16
	for _, write := range bus.writes {
		if len(write) == 5 && write[0] == 0xfe && write[1] == 0xce {
			pairs = append(pairs, [2]uint16{uint16(write[2])<<8 | uint16(write[3]), uint16(write[4])})
		}
	}
	return pairs
}

// Each part must get its own closing table; the identities here exercise both
// mask widths.
func TestTheClosingTableFollowsTheChipIdentity(t *testing.T) {
	for _, one := range []struct {
		name     string
		identity uint32
		// first and pairs are the first register of the part's closing table
		// and how many pairs it has.
		first uint16
		pairs int
	}{
		{"NM120", 0x00012045, 0x0e, 9},
		{"NM130", 0x00013012, 0x26, 7},
		{"NM130V", 0x00813077, 0x28, 3},
		{"NM131", 0x00013188, 0x28, 1},
	} {
		bus := &fakeBus{identity: one.identity}
		tuner := New(bus, testDemod)
		if err := tuner.finish(); err != nil {
			t.Errorf("%s: %v", one.name, err)
			continue
		}
		written := bus.tunnelled()
		if len(written) != one.pairs {
			t.Errorf("%s wrote %d registers, want %d", one.name, len(written), one.pairs)
			continue
		}
		if written[0][0] != one.first {
			t.Errorf("%s starts at register %#02x, want %#02x", one.name, written[0][0], one.first)
		}
		// Every one of the four writes register 0x28 as zero somewhere.
		var found bool
		for _, pair := range written {
			if pair[0] == 0x28 {
				found = true
				if pair[1] != 0 {
					t.Errorf("%s writes register 0x28 as %#02x, want 0", one.name, pair[1])
				}
			}
		}
		if !found {
			t.Errorf("%s never writes register 0x28", one.name)
		}
	}
}

// A part this driver has no table for is reported rather than given somebody
// else's.
func TestAnUnknownChipIsRefused(t *testing.T) {
	// 0x00812000 is deliberately absent: the NM120 is tested with the narrower
	// mask, so an identity with 0x2000 in the middle matches it whatever the byte
	// above holds.
	for _, identity := range []uint32{0, 0x00014000, 0x00015500, 0xffffffff} {
		bus := &fakeBus{identity: identity}
		if err := New(bus, testDemod).finish(); err == nil {
			t.Errorf("identity %#08x was accepted", identity)
		}
		if len(bus.tunnelled()) != 0 {
			t.Errorf("identity %#08x was written to anyway", identity)
		}
	}
}

// Why the mask is carried per part: an NM130V's identity passes the NM130's
// test under the narrower mask, and the wider one is the right test for both.
func TestTheMasksAreNotInterchangeable(t *testing.T) {
	const nm130v = 0x00813000
	var nm130, wide variant
	for _, part := range variants {
		switch part.name {
		case "NM130":
			nm130 = part
		case "NM130V":
			wide = part
		}
	}
	if nm130.mask != wide.mask {
		t.Fatal("this test is about the two sharing a mask")
	}
	if nm130v&nm130.mask == nm130.id {
		t.Error("an NM130V should not be taken for an NM130 under the wider mask")
	}
	// With the narrower mask the two would be indistinguishable.
	if nm130v&0x000fff00 != nm130.id&0x000fff00 {
		t.Error("this test is about the narrow mask confusing the two")
	}
}
