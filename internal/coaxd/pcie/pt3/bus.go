// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package pt3

// The card's I2C master runs a program: a transfer is a sequence of 4-bit
// instructions -- data line high, clock low, capture a bit, wait a millisecond --
// written into the card's memory and run, after which the host reads back what
// the capture instructions collected. A whole conversation with a demodulator,
// its tuner behind it and the delays between them costs one upload and one wait
// rather than hundreds of trips over the bus.
//
// This file assembles the program and nothing else, so its tests run anywhere.

// The instruction set the card's I2C master runs.
const (
	instEnd       = 0x0
	instAddress   = 0x1
	instClockLow  = 0x2
	instClockHigh = 0x3
	instDataLow   = 0x4
	instDataHigh  = 0x5
	instReset     = 0x6
	instSleep     = 0x7 // one millisecond, run by the card
	// The four that carry a bit. "nop" drives the data line and works the clock
	// as usual; "read" samples the line instead.
	instDataLowNop  = 0x08
	instDataHighNop = 0x0c
	instDataRead    = 0x0d
	instDataAck     = 0x0e
)

// program is a transfer being assembled. The zero value is an empty program at
// instruction address zero, where every transfer this package runs starts.
type program struct {
	// packed is the program as the card wants it: two instructions to a byte.
	packed []byte
	// count says whether the next instruction goes in the low half of a byte or
	// the high half.
	count int
	// captured is how many bytes the read instructions will collect. The card
	// puts them at the start of its memory in the order they were asked for.
	captured int
}

// add appends one instruction. Two go in a byte and the earlier one is the low
// half; getting that backwards still gives a valid-length program that runs and
// does something else entirely.
func (p *program) add(instruction byte) {
	if p.count%2 == 0 {
		p.packed = append(p.packed, instruction&0x0f)
	} else {
		p.packed[len(p.packed)-1] |= instruction << 4
	}
	p.count++
}

// start: the data line falls while the clock is high.
func (p *program) start() {
	p.add(instDataHigh)
	p.add(instClockHigh)
	p.add(instDataLow)
	p.add(instClockLow)
}

// stop: the data line rises while the clock is high.
func (p *program) stop() {
	p.add(instDataLow)
	p.add(instClockHigh)
	p.add(instDataHigh)
}

// write sends bytes most significant bit first, each followed by the slot the
// far end acknowledges in.
func (p *program) write(data ...byte) {
	for _, value := range data {
		for bit := 7; bit >= 0; bit-- {
			if value&(1<<uint(bit)) != 0 {
				p.add(instDataHighNop)
			} else {
				p.add(instDataLowNop)
			}
		}
		p.add(instDataAck)
	}
}

// read collects bytes and reports where in the captured data they will land. Not
// acknowledging the last byte is how the far end is told to stop talking.
func (p *program) read(count int) int {
	at := p.captured
	for index := range count {
		for range 8 {
			p.add(instDataRead)
		}
		if index == count-1 {
			p.add(instDataHighNop)
		} else {
			p.add(instDataLowNop)
		}
	}
	p.captured += count
	return at
}

// sleep makes the card wait, in whole milliseconds. Waiting on the card is the
// point of the arrangement: a sequence with delays in it still costs one upload
// and one wait.
func (p *program) sleep(milliseconds int) {
	for range milliseconds {
		p.add(instSleep)
	}
}

// end finishes the program. The second end is not a mistake: instructions are
// half a byte and the card reads whole ones, so an odd number of them would
// leave the last half byte holding whatever was in the card's memory before.
func (p *program) end() {
	p.add(instEnd)
	if p.count%2 == 1 {
		p.add(instEnd)
	}
}

func (p *program) bytes() []byte { return p.packed }

// instructions is also the unit the card counts its start address in.
func (p *program) instructions() int { return p.count }
