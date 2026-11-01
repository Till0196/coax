// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package mb86h47

import (
	"encoding/binary"
	"fmt"
	"testing"
	"time"
)

// delayedCardCommands models a device whose processor has not executed the
// submitted packets yet. A USB write completing only puts a packet in memory;
// an answer is what guarantees the accesses have actually happened.
type delayedCardCommands struct {
	pending   [][]byte
	registers map[uint32]uint32
	off, on   time.Time
}

func (chip *delayedCardCommands) Control(byte, byte, uint16, uint16, []byte, uint32) (int, error) {
	return 0, fmt.Errorf("unexpected control transfer")
}

func (chip *delayedCardCommands) Bulk(endpoint byte, buffer []byte, _ uint32) (int, error) {
	if endpoint == endpointCommand {
		chip.pending = append(chip.pending, append([]byte(nil), buffer...))
		return len(buffer), nil
	}
	if endpoint != endpointAnswer {
		return 0, fmt.Errorf("unexpected endpoint")
	}
	for len(chip.pending) > 0 {
		packet := chip.pending[0]
		chip.pending = chip.pending[1:]
		flags := binary.BigEndian.Uint32(packet[4:])
		end := 8 + int(flags&0xff)*4
		var values []uint32
		for offset := 8; offset < end; {
			op := binary.BigEndian.Uint32(packet[offset:])
			offset += 4
			address := binary.BigEndian.Uint32(packet[offset:])
			offset += 4
			if op&memoryRead != 0 {
				values = append(values, chip.registers[address])
			} else {
				value := binary.BigEndian.Uint32(packet[offset:])
				offset += 4
				chip.registers[address] = value
				if address == cardPower {
					if value == 0 {
						chip.off = time.Now()
					} else {
						chip.on = time.Now()
					}
				}
			}
		}
		if flags&commandAnswer != 0 {
			reply := make([]byte, 8+4*len(values))
			copy(reply, packet[:4])
			binary.BigEndian.PutUint32(reply[4:], uint32(len(values)))
			for i, value := range values {
				binary.BigEndian.PutUint32(reply[8+4*i:], value)
			}
			return copy(buffer, reply), nil
		}
	}
	return 0, fmt.Errorf("no packet requested an answer")
}

func TestCardPowerOffWaitStartsAfterTheDeviceExecutedTheWrite(t *testing.T) {
	chip := &delayedCardCommands{registers: map[uint32]uint32{cardPower: 1, cardLines: 0xe, cardEnable: 7}}
	bridge := &Bridge{device: chip, command: true, commandSequence: 1}
	if err := bridge.CardLine().Reset(); err != nil {
		t.Fatal(err)
	}
	if chip.off.IsZero() || chip.on.IsZero() {
		t.Fatal("card power was not cycled")
	}
	if duration := chip.on.Sub(chip.off); duration < 10*time.Millisecond {
		t.Fatalf("device saw power off for %s, want at least 10ms", duration)
	}
}
