// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package mb86h47

import (
	"bytes"
	"encoding/binary"
	"testing"
)

// cardChip stands in for the chip behind the widened memory request: plain
// registers, plus the card UART's receive FIFO, whose count shows in the top
// half of 0xFEA014, and its transmit FIFO.
type cardChip struct {
	registers map[uint32]uint32
	received  []byte
	sent      []byte
}

func (chip *cardChip) address(value, index uint16) uint32 {
	return uint32(value&0xff)<<24 | uint32(value&0xff00)<<8 | uint32(index)
}

func (chip *cardChip) Control(requestType, request byte, value, index uint16, buffer []byte, _ uint32) (int, error) {
	address := chip.address(value, index)
	if requestType == vendorOut {
		word := binary.BigEndian.Uint32(buffer)
		if address == cardTX {
			chip.sent = append(chip.sent, byte(word))
		}
		chip.registers[address] = word
		return len(buffer), nil
	}
	word := chip.registers[address]
	switch address {
	case cardFIFO:
		word = uint32(len(chip.received))<<16 | word&0xffff
	case cardRX:
		if len(chip.received) > 0 {
			word = uint32(chip.received[0])
			chip.received = chip.received[1:]
		}
	}
	binary.BigEndian.PutUint32(buffer, word)
	return len(buffer), nil
}

func (chip *cardChip) Bulk(byte, []byte, uint32) (int, error) { return 0, nil }

func TestCardLineReadsWhatWaits(t *testing.T) {
	chip := &cardChip{registers: map[uint32]uint32{}, received: []byte{0x3b, 0xf0, 0x12, 0x00, 0xff}}
	line := New(chip).CardLine()
	if ready, err := line.Ready(); err != nil || !ready {
		t.Fatalf("Ready = %v, %v with 5 bytes waiting", ready, err)
	}
	buffer := make([]byte, 3)
	if n, err := line.Read(buffer); err != nil || n != 3 || !bytes.Equal(buffer, []byte{0x3b, 0xf0, 0x12}) {
		t.Fatalf("Read = %d %x, %v", n, buffer[:n], err)
	}
	// A larger buffer takes only what has arrived.
	buffer = make([]byte, 33)
	if n, err := line.Read(buffer); err != nil || n != 2 || !bytes.Equal(buffer[:n], []byte{0x00, 0xff}) {
		t.Fatalf("second Read = %d %x, %v", n, buffer[:n], err)
	}
	if ready, _ := line.Ready(); ready {
		t.Error("Ready with nothing waiting")
	}
}

func TestCardLineWritesBlock(t *testing.T) {
	chip := &cardChip{registers: map[uint32]uint32{}}
	line := New(chip).CardLine()
	block := []byte{0x00, 0xc1, 0x01, 0xfe, 0x3e}
	if err := line.Write(block); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(chip.sent, block) {
		t.Errorf("sent %x, want %x", chip.sent, block)
	}
	if chip.registers[cardControl] != 0x200 || chip.registers[cardSend] != 1 {
		t.Errorf("command %#x send %#x, want 0x200 and 1", chip.registers[cardControl], chip.registers[cardSend])
	}
}

func TestCardLinePresence(t *testing.T) {
	chip := &cardChip{registers: map[uint32]uint32{cardIRQ: cardNoCard}}
	line := New(chip).CardLine()
	if present, err := line.Present(); err != nil || present {
		t.Errorf("Present = %v, %v on an empty slot", present, err)
	}
	chip.registers[cardIRQ] = 0
	if present, err := line.Present(); err != nil || !present {
		t.Errorf("Present = %v, %v with a card", present, err)
	}
	if err := line.SetBaudrate(38400); err != nil {
		t.Errorf("38400 baud: %v", err)
	}
	if chip.registers[cardBaud] != 0x2eae {
		t.Errorf("38400 baud divider %#x, want 0x2eae", chip.registers[cardBaud])
	}
	if err := line.SetBaudrate(57600); err == nil {
		t.Error("57600 baud accepted")
	}
}
