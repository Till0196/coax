// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package pt3

import (
	"bytes"
	"testing"
)

// pack is what the card's memory should hold for a list of instructions: two to a
// byte, the earlier one in the low half. Written out here rather than taken from
// the code under test.
func pack(instructions ...byte) []byte {
	var packed []byte
	for index, instruction := range instructions {
		if index%2 == 0 {
			packed = append(packed, instruction&0x0f)
		} else {
			packed[len(packed)-1] |= instruction << 4
		}
	}
	return packed
}

func TestAStartIsTheDataLineFallingUnderAHighClock(t *testing.T) {
	var p program
	p.start()
	want := pack(instDataHigh, instClockHigh, instDataLow, instClockLow)
	if !bytes.Equal(p.bytes(), want) {
		t.Errorf("start assembled to % x, want % x", p.bytes(), want)
	}
	if p.instructions() != 4 {
		t.Errorf("start is %d instructions, want 4", p.instructions())
	}
}

func TestAStopIsTheDataLineRisingUnderAHighClock(t *testing.T) {
	var p program
	p.stop()
	want := pack(instDataLow, instClockHigh, instDataHigh)
	if !bytes.Equal(p.bytes(), want) {
		t.Errorf("stop assembled to % x, want % x", p.bytes(), want)
	}
}

func TestAWrittenByteIsEightBitsAndAnAcknowledgement(t *testing.T) {
	var p program
	p.write(0xa5) // 1010 0101
	want := pack(
		instDataHighNop, instDataLowNop, instDataHighNop, instDataLowNop,
		instDataLowNop, instDataHighNop, instDataLowNop, instDataHighNop,
		instDataAck)
	if !bytes.Equal(p.bytes(), want) {
		t.Errorf("writing 0xa5 assembled to % x, want % x", p.bytes(), want)
	}
}

func TestEveryBitOfAWrittenByteIsCarried(t *testing.T) {
	for _, value := range []byte{0x00, 0x01, 0x80, 0xff, 0x5a} {
		var p program
		p.write(value)
		// Rebuild the byte from the instructions to check the bit order.
		var got byte
		for bit := range 8 {
			half := p.bytes()[bit/2]
			instruction := half & 0x0f
			if bit%2 == 1 {
				instruction = half >> 4
			}
			got <<= 1
			if instruction == instDataHighNop {
				got |= 1
			} else if instruction != instDataLowNop {
				t.Fatalf("bit %d of %#02x is instruction %#x", bit, value, instruction)
			}
		}
		if got != value {
			t.Errorf("writing %#02x reads back as %#02x", value, got)
		}
	}
}

func TestTheLastByteReadIsNotAcknowledged(t *testing.T) {
	var p program
	if at := p.read(2); at != 0 {
		t.Errorf("the first read lands at %d, want 0", at)
	}
	want := pack(
		instDataRead, instDataRead, instDataRead, instDataRead,
		instDataRead, instDataRead, instDataRead, instDataRead,
		instDataLowNop,
		instDataRead, instDataRead, instDataRead, instDataRead,
		instDataRead, instDataRead, instDataRead, instDataRead,
		instDataHighNop)
	if !bytes.Equal(p.bytes(), want) {
		t.Errorf("reading two bytes assembled to % x, want % x", p.bytes(), want)
	}
}

// Where captured bytes land is decided as the program is built: the card writes
// them in the order they were asked for.
func TestCapturedBytesAreCountedInOrder(t *testing.T) {
	var p program
	if at := p.read(3); at != 0 {
		t.Errorf("first read at %d, want 0", at)
	}
	if at := p.read(2); at != 3 {
		t.Errorf("second read at %d, want 3", at)
	}
	if p.captured != 5 {
		t.Errorf("captured %d bytes, want 5", p.captured)
	}
}

func TestSleepingIsOneInstructionPerMillisecond(t *testing.T) {
	var p program
	p.sleep(3)
	if p.instructions() != 3 {
		t.Errorf("three milliseconds is %d instructions", p.instructions())
	}
	want := pack(instSleep, instSleep, instSleep)
	if !bytes.Equal(p.bytes(), want) {
		t.Errorf("sleeping assembled to % x, want % x", p.bytes(), want)
	}
}

// Instructions are half a byte and the card reads whole ones, so a program
// stopping halfway through a byte would leave the card running whatever was in
// the other half from last time. end adds a second end instruction when the
// count is odd.
func TestAProgramAlwaysEndsOnAWholeByte(t *testing.T) {
	for _, before := range []int{0, 1, 2, 3, 8, 47} {
		var p program
		for range before {
			p.add(instSleep)
		}
		p.end()

		if p.instructions()%2 != 0 {
			t.Errorf("%d instructions then end left %d, which is odd", before, p.instructions())
		}
		if len(p.bytes()) != p.instructions()/2 {
			t.Errorf("%d instructions packed into %d bytes", p.instructions(), len(p.bytes()))
		}
		last := p.bytes()[len(p.bytes())-1]
		if before%2 == 0 {
			// The end landed in the low half, so both halves are ends.
			if last != 0x00 {
				t.Errorf("%d instructions then end gave a last byte of %#02x", before, last)
			}
		} else if last>>4 != instEnd {
			// The end landed in the high half, after the last real instruction.
			t.Errorf("%d instructions then end gave a last byte of %#02x", before, last)
		}
	}
}

// A register read: address the chip, say which register, address it again to
// read, take a byte.
func TestARegisterReadIsTheWholeConversation(t *testing.T) {
	const address = 0x10
	var p program
	p.start()
	p.write(address<<1, 0x00)
	p.start()
	p.write(address<<1 | 1)
	at := p.read(1)
	p.stop()
	p.end()

	if at != 0 {
		t.Errorf("the byte lands at %d, want 0", at)
	}
	// Four for the start, nine per written byte, four for the repeated start,
	// nine for the read address, nine for the byte taken, three for the stop,
	// and the end.
	const want = 4 + 9 + 9 + 4 + 9 + 9 + 3 + 1
	if p.instructions() != want+want%2 {
		t.Errorf("the conversation is %d instructions, want %d", p.instructions(), want+want%2)
	}
	if len(p.bytes()) != p.instructions()/2 {
		t.Errorf("%d instructions packed into %d bytes", p.instructions(), len(p.bytes()))
	}
}
