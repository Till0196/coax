// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

//go:build windows

package pt3

import (
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/Till0196/coax/internal/coaxd/pcie/windriver"
)

// The window the I2C master keeps its program and its captured bytes in. It is
// 2048 bytes into the card's second memory window, and the window WinDriver hands
// over already begins there, so the offsets below count from zero. Not by
// anyone's arrangement: WinDriver maps that memory at an address which is not the
// start of a page, so only a little over two kilobytes is reachable, and that is
// exactly the region the master uses.
const (
	// i2cRoom is how much of that window there is, in bytes. A program packs two
	// instructions to a byte, so this also bounds a single transfer.
	i2cRoom = 2048
	// i2cStart is the instruction address every program here is run from.
	i2cStart = 0
)

// How long to wait for the master. The longest legitimate program is a tuner
// initialisation with about twenty of the card's own one-millisecond sleeps in
// it, so hundreds of milliseconds is generous; an unbounded wait for a wedged
// card would block a program holding four tuners entirely.
const (
	i2cBudget = 500 * time.Millisecond
	i2cPoll   = 200 * time.Microsecond
)

// ErrI2C reports that a transfer did not get the answer it expected, which is
// the ordinary way to find out that nothing is at an address.
var ErrI2C = errors.New("the card's I2C master reported no answer")

// card is one PT3 and what every channel on it shares. There is one I2C master
// and one piece of memory for its programs, so every transfer on the card takes
// the same lock, whichever tuner asked for it.
type card struct {
	device *windriver.Device
	// registers and instructions are the address space numbers of the two
	// windows, which are the base address registers they came from.
	registers    uint32
	instructions uint32
	frontends    [channels]*frontend

	i2cMutex sync.Mutex

	// One channel can be read once: it has one transfer engine and one ring.
	streamMutex sync.Mutex
	streams     [channels]*Stream

	// What the four receivers share. See sharing_windows.go.
	sharing

	// lowMemory takes the transfer buffer the way a card that cannot reach past
	// four gigabytes has to. This card can, and never sets it; it exists so this
	// card can answer what the library does with such a lock, for the PT1 and PT2
	// which have no choice. See TestStreamingIntoLowMemory.
	lowMemory bool
}

// newCard wraps an opened card, working out which window is which.
func newCard(device *windriver.Device) (*card, error) {
	spaces := device.Spaces()
	if len(spaces) < 2 {
		return nil, fmt.Errorf("the card offers %d memory windows, and this one has two", len(spaces))
	}
	return &card{
		device:       device,
		registers:    spaces[registerWindow],
		instructions: spaces[instructionWindow],
	}, nil
}

// runROM starts one of the programs the card holds in its own memory and waits
// for it. Nothing is uploaded and nothing captured: these are the board's
// demodulator and tuner initialisation, and they are not readable. See bringUp.
func (c *card) runROM(address uint32) error {
	c.i2cMutex.Lock()
	defer c.i2cMutex.Unlock()
	return c.start(address)
}

// run uploads a program, runs it, and returns whatever its reads captured.
func (c *card) run(p *program) ([]byte, error) {
	if size := len(p.bytes()); size > i2cRoom {
		return nil, fmt.Errorf("a transfer of %d bytes does not fit in the card's %d", size, i2cRoom)
	}
	c.i2cMutex.Lock()
	defer c.i2cMutex.Unlock()

	// Wait for whatever ran last: whoever had the card before may have left it
	// running.
	if _, err := c.waitForI2C(); err != nil {
		return nil, err
	}

	// A byte at a time: this memory takes no block move, and a four-byte write
	// writes three bytes not meant.
	for offset, value := range p.bytes() {
		if err := c.device.WriteU8(c.instructions, instructionBase+uint64(offset), value); err != nil {
			return nil, err
		}
	}

	if err := c.start(i2cStart); err != nil {
		return nil, err
	}

	if p.captured == 0 {
		return nil, nil
	}
	// The card wrote what it captured to the start of the same memory, which is
	// only safe because the wait above established that the program finished.
	return c.readCaptured(p.captured)
}

// start runs the program at an instruction address and waits for it to finish.
// The caller holds the lock; this is one shared engine.
func (c *card) start(address uint32) error {
	if address&^i2cAddressMask != 0 {
		return fmt.Errorf("instruction address %#x does not fit the card's %d bits", address, 13)
	}
	if err := c.device.WriteU32(c.registers, regI2CWrite, i2cGo|address); err != nil {
		return err
	}
	status, err := c.waitForI2C()
	if err != nil {
		return err
	}
	if status&i2cError != 0 {
		return fmt.Errorf("%w: status %#x", ErrI2C, status)
	}
	return nil
}

// readCaptured takes the bytes a program collected out of the card's memory.
// Four at a time, because this memory does not answer a byte-sized read: it
// returns zero for one and the right thing for a whole word. Writing a byte
// works, so only the reading has to be widened.
func (c *card) readCaptured(count int) ([]byte, error) {
	captured := make([]byte, 0, count)
	for at := 0; at < count; at += 4 {
		word, err := c.device.ReadU32(c.instructions, instructionBase+uint64(at))
		if err != nil {
			return nil, err
		}
		for shift := range 4 {
			if len(captured) == count {
				break
			}
			captured = append(captured, byte(word>>(8*uint(shift))))
		}
	}
	return captured, nil
}

// waitForI2C waits until the master is idle and reports the status it stopped at.
func (c *card) waitForI2C() (uint32, error) {
	deadline := time.Now().Add(i2cBudget)
	for {
		status, err := c.device.ReadU32(c.registers, regI2CRead)
		if err != nil {
			return 0, err
		}
		if status&i2cBusy == 0 {
			return status, nil
		}
		if time.Now().After(deadline) {
			return 0, fmt.Errorf("the card's I2C master was still busy after %v", i2cBudget)
		}
		time.Sleep(i2cPoll)
	}
}

// resetI2C puts the master back to a known state: a program taking a card over
// cannot assume the last user let it go tidily.
func (c *card) resetI2C() error {
	return c.device.WriteU32(c.registers, regI2CWrite, i2cReset)
}

// i2cClean reports whether the master says its memory has not been used since it
// was reset, which decides whether the card still needs its power-on sequence.
func (c *card) i2cClean() (bool, error) {
	status, err := c.device.ReadU32(c.registers, regI2CRead)
	if err != nil {
		return false, err
	}
	return status&i2cClean != 0, nil
}

// bus is the card's I2C master as the rest of this program understands one.
// Everything above it is shared with boards whose I2C master is an ordinary one
// on a USB bridge, and none of them has to know a transfer here is a program.
type bus struct{ card *card }

func (b bus) Write(address byte, data []byte) error {
	var p program
	p.start()
	p.write(append([]byte{address & 0xfe}, data...)...)
	p.stop()
	p.end()
	_, err := b.card.run(&p)
	return err
}

func (b bus) Read(address byte, length int) ([]byte, error) {
	if length <= 0 {
		return nil, nil
	}
	var p program
	p.start()
	p.write(address | 1)
	p.read(length)
	p.stop()
	p.end()
	return b.card.run(&p)
}

// WriteRead does both without letting go of the bus in between, which is what a
// register read is. One program rather than two is necessary, not just faster: a
// demodulator repeating to the tuner behind it needs the whole exchange inside
// one transfer.
func (b bus) WriteRead(address byte, write []byte, length int) ([]byte, error) {
	if length <= 0 {
		return nil, b.Write(address, write)
	}
	var p program
	p.start()
	p.write(append([]byte{address & 0xfe}, write...)...)
	p.start()
	p.write(address | 1)
	p.read(length)
	p.stop()
	p.end()
	return b.card.run(&p)
}
