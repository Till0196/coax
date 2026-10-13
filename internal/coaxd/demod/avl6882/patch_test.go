// Copyright (c) 2015 Luis Alves; TurboSight; 2026 Till0196
// SPDX-License-Identifier: GPL-2.0-or-later

package avl6882

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
)

// captureBus records every write and answers every read with zeroes.
type captureBus struct {
	writes [][]byte
}

func (bus *captureBus) Write(_ byte, data []byte) error {
	bus.writes = append(bus.writes, append([]byte(nil), data...))
	return nil
}

func (bus *captureBus) Read(_ byte, length int) ([]byte, error) {
	return make([]byte, length), nil
}

// TestRunPatchExecutesTheImmediateLoadCommand drives the interpreter through a
// hand-built script: a header, one unconditional PATCH_CMD_LD_TO_DEVICE_IMM
// writing a known byte to a known address, and nothing else.
func TestRunPatchExecutesTheImmediateLoadCommand(t *testing.T) {
	script := []uint32{
		patchCmdLoadToDeviceImm, // the command
		1,                       // length: one byte
		0x123456,                // destination address
		0xab,                    // value
		0,                       // return value count
	}
	// Each command is prefixed by its own word count and its condition count.
	command := append([]uint32{uint32(len(script)) + 1, 0}, script...)
	// Header: version word, total length, standard, args address, data section
	// offset, reserved length, script length. runPatch starts reading at word 3.
	patch := append([]uint32{
		0x10000001, // version 1, with the 0x1 family nibble loadFirmwareFile checks
		0, 0,       // total length, standard: unread
		0x1000, // args address
		0,      // data section offset
		0,      // reserved length
		uint32(len(command)),
	}, command...)

	bus := &captureBus{}
	demod := New(bus, "", Config{Address: 0x14})
	if err := demod.runPatch(patch); err != nil {
		t.Fatalf("runPatch: %v", err)
	}

	want := []byte{0x12, 0x34, 0x56, 0xab}
	found := false
	for _, write := range bus.writes {
		if len(write) == len(want) {
			match := true
			for i := range want {
				if write[i] != want[i] {
					match = false
					break
				}
			}
			if match {
				found = true
			}
		}
	}
	if !found {
		t.Errorf("the immediate load never reached the bus; writes = %#v", bus.writes)
	}
}

// TestRunPatchSkipsACommandWhoseConditionIsFalse checks the conditional
// branch: a command guarded by a false condition must be jumped over using its
// own word count, not partially executed.
func TestRunPatchSkipsACommandWhoseConditionIsFalse(t *testing.T) {
	// One condition word: LOAD the immediate value 0, so the condition is
	// false. Addressing mode 1 (immediate) keeps it out of the variable array.
	const loadImmediate = 1<<16 | patchBinaryLoad
	body := []uint32{
		loadImmediate, 0, // condition: load 0
		patchCmdLoadToDeviceImm, 1, 0x123456, 0xab, 0,
	}
	command := append([]uint32{uint32(len(body)) + 2, 1}, body...)
	patch := append([]uint32{0x10000001, 0, 0, 0x1000, 0, 0, uint32(len(command))}, command...)

	bus := &captureBus{}
	demod := New(bus, "", Config{Address: 0x14})
	if err := demod.runPatch(patch); err != nil {
		t.Fatalf("runPatch: %v", err)
	}
	if len(bus.writes) != 0 {
		t.Errorf("a false condition still produced writes: %#v", bus.writes)
	}
}

// TestRunPatchRejectsATruncatedImage guards the bounds checks on the patch
// script: a truncated firmware file must produce an error, not a panic.
func TestRunPatchRejectsATruncatedImage(t *testing.T) {
	bus := &captureBus{}
	demod := New(bus, "", Config{Address: 0x14})
	// A header that claims a longer script than the array actually holds.
	patch := []uint32{0x10000001, 0, 0, 0x1000, 0, 0, 999}
	if err := demod.runPatch(patch); err == nil {
		t.Fatal("runPatch accepted a script running past the end of the image")
	}
}

// TestLoadFirmwareFileSplitsThePackedImages pins the packed firmware layout:
// three little-endian (offset, size) pairs, then each image's body as
// big-endian 32-bit words, each starting with the 0x1 family nibble.
func TestLoadFirmwareFileSplitsThePackedImages(t *testing.T) {
	dir := t.TempDir()

	const headerSize = 24
	bodies := [3][]uint32{
		{0x10000001, 0xdeadbeef},
		{0x10000002},
		{0x10000003, 0x11223344, 0x55667788},
	}
	var body []byte
	var header []byte
	offset := uint32(headerSize)
	for _, words := range bodies {
		header = binary.LittleEndian.AppendUint32(header, offset)
		header = binary.LittleEndian.AppendUint32(header, uint32(len(words))*4)
		offset += uint32(len(words)) * 4
		for _, word := range words {
			body = binary.BigEndian.AppendUint32(body, word)
		}
	}
	path := filepath.Join(dir, firmwareName)
	if err := os.WriteFile(path, append(header, body...), 0o600); err != nil {
		t.Fatal(err)
	}

	demod := New(&captureBus{}, dir, Config{Address: 0x14})
	if err := demod.loadFirmwareFile(); err != nil {
		t.Fatalf("loadFirmwareFile: %v", err)
	}
	for i, want := range bodies {
		got := demod.images[i]
		if len(got) != len(want) {
			t.Fatalf("image %d has %d words, want %d", i, len(got), len(want))
		}
		for w := range want {
			if got[w] != want[w] {
				t.Errorf("image %d word %d = %#08x, want %#08x", i, w, got[w], want[w])
			}
		}
	}
}

// TestSystemMapsBothGenerationsOntoOneImage checks that systemFor folds both
// generations of a family onto one System — the value SetDeliverySystem
// compares to decide whether an upload is needed — and rejects ISDB-T.
func TestSystemMapsBothGenerationsOntoOneImage(t *testing.T) {
	for _, test := range []struct {
		delivery string
		want     System
	}{
		{"DVB-T", SystemDVBT}, {"DVB-T2", SystemDVBT},
		{"DVB-S", SystemDVBS}, {"DVB-S2", SystemDVBS},
		{"DVB-C", SystemDVBC}, {"DVB-C/ANNEX_A", SystemDVBC},
	} {
		got, err := systemFor(test.delivery)
		if err != nil {
			t.Fatalf("systemFor(%q): %v", test.delivery, err)
		}
		if got != test.want {
			t.Errorf("systemFor(%q) = %v, want %v", test.delivery, got, test.want)
		}
	}
	if _, err := systemFor("ISDB-T"); err == nil {
		t.Error("systemFor accepted ISDB-T, which the AVL6882 has no image for")
	}
}
