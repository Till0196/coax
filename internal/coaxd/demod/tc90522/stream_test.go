// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package tc90522

import (
	"strings"
	"testing"
)

// registerFile is a satellite core on a bus: a write sets the register
// pointer and stores what follows it, a read reads on from the pointer, and
// the stream selected comes back as the current one.
type registerFile struct {
	registers [256]byte
	pointer   byte
	selected  int
}

func (r *registerFile) Write(_ byte, data []byte) error {
	r.pointer = data[0]
	for i, value := range data[1:] {
		r.registers[int(r.pointer)+i] = value
	}
	if r.pointer == 0x8f && len(data) == 3 {
		r.selected++
		r.registers[0xe6], r.registers[0xe7] = data[1], data[2]
	}
	return nil
}

func (r *registerFile) Read(_ byte, length int) ([]byte, error) {
	return append([]byte(nil), r.registers[r.pointer:int(r.pointer)+length]...), nil
}

func TestSelectStreamSLooksSlotsUpInTheTMCC(t *testing.T) {
	bus := &registerFile{}
	for slot, tsid := range []uint16{0x4630, 0x4631, 0xffff, 0xffff, 0xffff, 0xffff, 0xffff, 0xffff} {
		bus.registers[0xce+2*slot], bus.registers[0xcf+2*slot] = byte(tsid>>8), byte(tsid)
	}
	demod := New(bus, 0x22, false)
	if err := demod.SelectStreamS(1); err != nil {
		t.Fatal(err)
	}
	if current, _ := demod.TSIDS(); current != 0x4631 {
		t.Fatalf("slot 1 selected %#04x", current)
	}
	if err := demod.SelectStreamS(0x4630); err != nil {
		t.Fatal(err)
	}
	if current, _ := demod.TSIDS(); current != 0x4630 {
		t.Fatalf("TSID 0x4630 selected %#04x", current)
	}

	selected := bus.selected
	if err := demod.SelectStreamS(2); err == nil || !strings.Contains(err.Error(), "carries no stream") {
		t.Fatalf("an empty slot: %v", err)
	}
	if err := demod.SelectStreamS(9); err == nil {
		t.Fatal("slot 9 was taken")
	}
	if bus.selected != selected {
		t.Fatal("a stream was selected for a slot with none")
	}
}
