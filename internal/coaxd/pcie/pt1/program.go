// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package pt1

import "fmt"

// The card's I2C sequencer: no byte-oriented master and no bit-banging from the host, but a
// 1024-instruction memory and a machine that walks it, an instruction being a state of the
// two lines plus the address to go to next. A transfer is therefore a program of a few
// hundred instructions.
//
// One word carries both where to store the instruction and what it is:
//
//	bits 27..18  where this instruction goes
//	bit  13      keep the busy flag raised
//	bit  12      sample the data line on this instruction
//	bit  11      the clock line, inverted
//	bit  10      the data line, inverted
//	bits  9..0   the instruction to go to next
//
// The two line bits are inverted, which cannot be checked by reading the card: a program
// with both the wrong way round is still well formed and would clock nonsense at every
// slave on the bus.
//
// Execution always begins at instruction one. Instruction zero is a stop: the only
// instruction written without the busy flag, and it points at itself, so every program
// ends by pointing at zero.
const (
	instructionCount = 1024
	// haltAddress is instruction zero, the stop.
	haltAddress = 0
	// entryAddress is where the card begins.
	entryAddress = 1
)

// The fields of an instruction word.
const (
	instructionAddressShift = 18
	instructionBusy         = 1 << 13
	instructionSample       = 1 << 12
	instructionClockLow     = 1 << 11
	instructionDataLow      = 1 << 10
	instructionNextMask     = 1<<10 - 1
)

// program is a transfer being assembled, built without touching the card: what
// the card is sent is exactly the list of words below.
type program struct {
	words []uint32
	// next is the instruction the next emit will be stored at.
	next int
	// captured is how many bytes the read instructions will collect.
	captured int
	// overflowed records that the program outgrew the card's memory, reported
	// when the words are asked for rather than at each step.
	overflowed bool
}

// newProgram begins a transfer. The stop at instruction zero is rewritten
// every time: it costs one word, and assuming it survived would be trusting
// memory anything else holding this card may have written.
func newProgram() *program {
	p := &program{next: haltAddress}
	p.emit(false, false, true, true, haltAddress)
	p.next = entryAddress
	return p
}

// emit stores one instruction at the next address and says where to go after
// it.
func (p *program) emit(busy, sample, clock, data bool, next int) {
	if p.next >= instructionCount || next >= instructionCount {
		p.overflowed = true
		return
	}
	word := uint32(p.next)<<instructionAddressShift | uint32(next)&instructionNextMask
	if busy {
		word |= instructionBusy
	}
	if sample {
		word |= instructionSample
	}
	// The card is told what is *not* on each line.
	if !clock {
		word |= instructionClockLow
	}
	if !data {
		word |= instructionDataLow
	}
	p.words = append(p.words, word)
	p.next++
}

// emitNext is an instruction that falls through to the one after it.
func (p *program) emitNext(sample, clock, data bool) {
	p.emit(true, sample, clock, data, p.next+1)
}

// wake puts the bus into a known state, once per card. The lines are left
// wherever the last program stopped them; raising the clock and dropping the
// data line under it leaves the bus, from any state at all, somewhere a start
// can be issued from.
func (p *program) wake() {
	p.emitNext(false, true, true)
	p.emitNext(false, true, false)
}

// start is the data line falling while the clock is high, then the clock down
// ready for the first bit.
func (p *program) start() {
	p.emitNext(false, true, true)
	p.emitNext(false, true, false)
	p.emitNext(false, false, false)
}

func (p *program) writeBit(bit bool) {
	p.emitNext(false, false, bit)
	p.emitNext(false, true, bit)
	p.emitNext(false, false, bit)
}

// readBit lets the slave hold the line and samples it on the high clock.
func (p *program) readBit() {
	p.emitNext(false, false, true)
	p.emitNext(false, true, true)
	p.emitNext(true, true, true) // the sample
	p.emitNext(false, false, true)
}

// writeByte sends a byte and releases the line for the slave to acknowledge
// on. The card cannot check it: there is no instruction to branch on.
func (p *program) writeByte(value byte) {
	for bit := 7; bit >= 0; bit-- {
		p.writeBit(value>>uint(bit)&1 != 0)
	}
	p.writeBit(true)
}

// readByte takes a byte and answers it. last leaves the line high, the
// not-acknowledge that tells the slave to stop.
func (p *program) readByte(last bool) {
	for range 8 {
		p.readBit()
	}
	p.writeBit(last)
	p.captured++
}

// write is a start, the slave's address, and the bytes.
func (p *program) write(address byte, data []byte) {
	p.start()
	p.writeByte(address &^ 0x01)
	for _, value := range data {
		p.writeByte(value)
	}
}

// read is a start, the slave's address with the read bit, and the bytes
// wanted.
func (p *program) read(address byte, length int) {
	p.start()
	p.writeByte(address | 0x01)
	for index := range length {
		p.readByte(index == length-1)
	}
}

// end is a stop condition, and then back to the instruction that halts.
func (p *program) end() {
	p.emitNext(false, false, false)
	p.emitNext(false, true, false)
	p.emit(true, false, true, true, haltAddress)
}

// instructions is the program, as words to be written to the card one at a
// time in this order.
func (p *program) instructions() ([]uint32, error) {
	if p.overflowed {
		return nil, fmt.Errorf("a transfer of this size does not fit the card's %d instructions", instructionCount)
	}
	return p.words, nil
}
