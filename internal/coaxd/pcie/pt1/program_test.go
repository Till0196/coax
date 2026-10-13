// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package pt1

import "testing"

// decoded is one instruction taken apart, so a test can say what it means
// rather than what it is.
type decoded struct {
	at           int
	busy, sample bool
	clock, data  bool
	next         int
}

func decode(word uint32) decoded {
	return decoded{
		at:     int(word >> instructionAddressShift),
		busy:   word&instructionBusy != 0,
		sample: word&instructionSample != 0,
		// The card is told what is not on each line, so both read back
		// inverted.
		clock: word&instructionClockLow == 0,
		data:  word&instructionDataLow == 0,
		next:  int(word & instructionNextMask),
	}
}

func decodeAll(t *testing.T, p *program) []decoded {
	t.Helper()
	words, err := p.instructions()
	if err != nil {
		t.Fatal(err)
	}
	list := make([]decoded, 0, len(words))
	for _, word := range words {
		list = append(list, decode(word))
	}
	return list
}

// Instruction zero is the stop: the only one without the busy flag, pointing
// at itself so that reaching it holds the card there.
func TestTheProgramBeginsWithAStopAtZero(t *testing.T) {
	list := decodeAll(t, newProgram())
	if len(list) != 1 {
		t.Fatalf("a new program holds %d instructions, want 1", len(list))
	}
	stop := list[0]
	if stop.at != 0 || stop.next != 0 {
		t.Errorf("the stop is at %d pointing at %d, want 0 and 0", stop.at, stop.next)
	}
	if stop.busy {
		t.Error("the stop keeps the busy flag raised, so nothing would ever finish")
	}
	if !stop.clock || !stop.data {
		t.Error("the stop leaves a line held down")
	}
}

// Everything after the stop is laid down consecutively from instruction one,
// each pointing at the next, because that is where the card begins.
func TestInstructionsRunOnFromOne(t *testing.T) {
	p := newProgram()
	p.write(0x36, []byte{0x00})
	p.end()
	list := decodeAll(t, p)
	for index, one := range list[1:] {
		if one.at != index+entryAddress {
			t.Fatalf("instruction %d is stored at %d, want %d", index, one.at, index+entryAddress)
		}
		if !one.busy {
			t.Errorf("instruction at %d does not keep the busy flag raised", one.at)
		}
	}
	// And the last one goes back to the stop.
	if last := list[len(list)-1]; last.next != haltAddress {
		t.Errorf("the program ends pointing at %d, want the stop at %d", last.next, haltAddress)
	}
}

// A start is the data line falling while the clock is high. Getting the two
// inverted bits the wrong way round makes this a stop instead, and every slave
// would ignore what follows.
func TestAStartIsTheDataLineFallingUnderAHighClock(t *testing.T) {
	p := newProgram()
	p.start()
	list := decodeAll(t, p)[1:]
	want := []struct{ clock, data bool }{{true, true}, {true, false}, {false, false}}
	if len(list) != len(want) {
		t.Fatalf("a start is %d instructions, want %d", len(list), len(want))
	}
	for index, one := range want {
		if list[index].clock != one.clock || list[index].data != one.data {
			t.Errorf("instruction %d has clock=%v data=%v, want %v and %v",
				index, list[index].clock, list[index].data, one.clock, one.data)
		}
	}
}

// A stop is the other way round: the data line rising under a high clock.
func TestAStopIsTheDataLineRisingUnderAHighClock(t *testing.T) {
	p := newProgram()
	p.end()
	list := decodeAll(t, p)[1:]
	want := []struct{ clock, data bool }{{false, false}, {true, false}, {true, true}}
	for index, one := range want {
		if list[index].clock != one.clock || list[index].data != one.data {
			t.Errorf("instruction %d has clock=%v data=%v, want %v and %v",
				index, list[index].clock, list[index].data, one.clock, one.data)
		}
	}
}

// Every bit of a written byte reaches the line, most significant first, and
// the ninth bit is the line released for the slave to acknowledge on.
func TestEveryBitOfAWrittenByteIsCarried(t *testing.T) {
	p := newProgram()
	p.writeByte(0xa5)
	list := decodeAll(t, p)[1:]
	if len(list) != 9*3 {
		t.Fatalf("a written byte is %d instructions, want %d", len(list), 9*3)
	}
	// The middle instruction of each three is the one with the clock high,
	// which is where a slave reads the line.
	var got byte
	for bit := range 8 {
		clocked := list[bit*3+1]
		if !clocked.clock {
			t.Fatalf("bit %d is not clocked", bit)
		}
		if clocked.data {
			got |= 1 << uint(7-bit)
		}
	}
	if got != 0xa5 {
		t.Errorf("the byte on the line is %#02x, want 0xa5", got)
	}
	if !list[8*3+1].data {
		t.Error("the acknowledge bit holds the line down, so the slave cannot pull it")
	}
}

// A read samples the line once per bit, on the high clock, and answers the
// last byte by leaving the line high.
func TestAReadSamplesEveryBitAndRefusesTheLastByte(t *testing.T) {
	p := newProgram()
	p.read(0x36, 2)
	list := decodeAll(t, p)[1:]
	samples := 0
	for _, one := range list {
		if one.sample {
			samples++
			if !one.clock {
				t.Error("a sample is taken while the clock is low")
			}
		}
	}
	if samples != 16 {
		t.Errorf("%d bits are sampled, want 16", samples)
	}
	if p.captured != 2 {
		t.Errorf("the program says it captured %d bytes, want 2", p.captured)
	}
	// The final three instructions are the not-acknowledge of the second byte;
	// the middle one is where the clock is high and the line is left there.
	last := list[len(list)-2]
	if !last.data {
		t.Error("the last byte is acknowledged, which asks the slave for another")
	}
}

// A program too large for the card's memory is refused when the words are
// asked for, rather than quietly wrapping over instruction zero.
func TestATooLargeProgramIsRefused(t *testing.T) {
	p := newProgram()
	for range 64 {
		p.write(0x36, make([]byte, 16))
	}
	p.end()
	if _, err := p.instructions(); err == nil {
		t.Error("a program past the end of the card's memory was accepted")
	}
}

// The whole of an ordinary register read, which is what almost every transfer
// on this card is: say which register, then take the value without letting go
// of the bus in between.
func TestARegisterReadIsTheWholeConversation(t *testing.T) {
	p := newProgram()
	p.wake()
	p.write(0x36, []byte{0xc3})
	p.read(0x36, 1)
	p.end()
	list := decodeAll(t, p)

	if list[0].busy {
		t.Error("the stop is not the first instruction")
	}
	// Two starts: one for the write and one for the read, with no stop
	// between them.
	starts := 0
	for index := 1; index+1 < len(list); index++ {
		if list[index].clock && list[index].data &&
			list[index+1].clock && !list[index+1].data {
			starts++
		}
	}
	if starts < 2 {
		t.Errorf("found %d start conditions, want at least the write's and the read's", starts)
	}
	if p.captured != 1 {
		t.Errorf("captured %d bytes, want 1", p.captured)
	}
}

// The exact words the card is sent. Everything else here checks what a program
// means; this checks what it is. The encoding has two inverted bits, an
// address in the middle of the word and a pointer at the bottom, and a mistake
// in any of them still yields a well formed program that does something else.
func TestTheWordsAreWhatTheCardExpects(t *testing.T) {
	p := newProgram()
	p.start()
	words, err := p.instructions()
	if err != nil {
		t.Fatal(err)
	}
	want := []uint32{
		// The stop at zero: no busy, both lines released, pointing at itself.
		0,
		// At 1, busy, clock high, data high, on to 2.
		1<<18 | 1<<13 | 2,
		// At 2, busy, clock high, data low -- so the data bit is set.
		2<<18 | 1<<13 | 1<<10 | 3,
		// At 3, busy, both low, so both bits are set.
		3<<18 | 1<<13 | 1<<11 | 1<<10 | 4,
	}
	if len(words) != len(want) {
		t.Fatalf("wrote %d words, want %d", len(words), len(want))
	}
	for index := range want {
		if words[index] != want[index] {
			t.Errorf("word %d is %#08x, want %#08x", index, words[index], want[index])
		}
	}
}

// How many instructions each building routine comes to, which is what a
// program is measured against when working out whether one fits the card.
func TestTheBuildingRoutineSizes(t *testing.T) {
	for _, one := range []struct {
		name  string
		build func(*program)
		words int
	}{
		{"a start", func(p *program) { p.start() }, 3},
		{"a stop and the jump back", func(p *program) { p.end() }, 3},
		{"a written byte and its acknowledge", func(p *program) { p.writeByte(0) }, 9 * 3},
		{"a read byte and its answer", func(p *program) { p.readByte(true) }, 8*4 + 3},
		{"the once-only wake", func(p *program) { p.wake() }, 2},
	} {
		t.Run(one.name, func(t *testing.T) {
			p := newProgram()
			before := len(p.words)
			one.build(p)
			if got := len(p.words) - before; got != one.words {
				t.Errorf("%d instructions, want %d", got, one.words)
			}
		})
	}
}
