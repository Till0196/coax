// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package mb86h47

import (
	"encoding/binary"
	"testing"
)

// commandChip stands in for core 1 behind the command channel: it keeps the
// packets sent to it and answers the ones that ask, with their sequence number.
type commandChip struct {
	packets [][]byte
	answers [][]byte
}

func (chip *commandChip) Control(byte, byte, uint16, uint16, []byte, uint32) (int, error) {
	return 0, nil
}

func (chip *commandChip) Bulk(endpoint byte, buffer []byte, _ uint32) (int, error) {
	switch endpoint {
	case endpointCommand:
		chip.packets = append(chip.packets, append([]byte(nil), buffer...))
		if binary.BigEndian.Uint32(buffer[4:])&commandAnswer != 0 {
			answer := make([]byte, 8)
			copy(answer, buffer[:4])
			chip.answers = append(chip.answers, answer)
		}
		return len(buffer), nil
	case endpointAnswer:
		if len(chip.answers) == 0 {
			return 0, nil
		}
		n := copy(buffer, chip.answers[0])
		chip.answers = chip.answers[1:]
		return n, nil
	}
	return 0, nil
}

func TestDrainAsksOnlyWhenSomethingIsInHand(t *testing.T) {
	chip := &commandChip{}
	bridge := &Bridge{device: chip, command: true, commandSequence: 1}

	if err := bridge.Drain(); err != nil {
		t.Fatal(err)
	}
	if len(chip.packets) != 0 {
		t.Fatalf("a drain with nothing in hand sent %d packets", len(chip.packets))
	}

	bridge.mu.Lock()
	bridge.registerWriteLocked(0x1000, 1)
	bridge.mu.Unlock()
	if err := bridge.Flush(); err != nil {
		t.Fatal(err)
	}
	if len(chip.packets) != 1 || binary.BigEndian.Uint32(chip.packets[0][4:])&commandAnswer != 0 {
		t.Fatalf("the flush sent %d packets, want one that asks for no answer", len(chip.packets))
	}

	if err := bridge.Drain(); err != nil {
		t.Fatal(err)
	}
	if len(chip.packets) != 2 || binary.BigEndian.Uint32(chip.packets[1][4:])&commandAnswer == 0 {
		t.Fatalf("after the flush a drain sent %d packets, want a second that asks for an answer", len(chip.packets))
	}

	if err := bridge.Drain(); err != nil {
		t.Fatal(err)
	}
	if len(chip.packets) != 2 {
		t.Fatalf("a drain after an answer sent %d packets, want none more", len(chip.packets)-2)
	}
}
