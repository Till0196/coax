// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

//go:build windows

package pt1

import (
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/Till0196/coax/internal/coaxd/demod/tc90522"
	"github.com/Till0196/coax/internal/coaxd/i2c"
	"github.com/Till0196/coax/internal/coaxd/pcie/windriver"
	"github.com/Till0196/coax/internal/coaxd/tuning"
)

// card is one PT1 or PT2 and everything on it the four receivers share: one
// register window, one I2C sequencer, one supply on the aerial lead and one
// transfer engine, so each takes a lock here.
type card struct {
	device *windriver.Device
	model  Model
	// window is the address space number of the card's one memory window.
	window uint32

	// registerMutex serialises the command register, which is several
	// registers behind one number: a read answers about whatever was last
	// written.
	registerMutex sync.Mutex

	i2cMutex sync.Mutex
	// i2cWoken records that the bus has been put into a known state, which is
	// done once per card and never again. See program.wake.
	i2cWoken bool

	frontends [receivers]*frontend

	// The transfer is the card's rather than any receiver's: one engine
	// carries all four interleaved, and it exists while anything is reading.
	// See stream_windows.go.
	streamMutex sync.Mutex
	ring        *ring
	demux       *demultiplexer
	streams     [receivers]*Stream
	stopping    chan struct{}
	polled      chan struct{}

	// What the four receivers share beyond the hardware itself.
	sharing
}

func newCard(device *windriver.Device, model Model) (*card, error) {
	spaces := device.Spaces()
	if len(spaces) != 1 {
		return nil, fmt.Errorf("the card offers %d memory windows, and this one has exactly one", len(spaces))
	}
	held := &card{device: device, model: model, window: spaces[0]}
	// Nothing has asked for the aerial lead yet. The zero value of a supply is
	// a request for one, which would put eleven volts on the lead as soon as
	// the power register is first written, satellite in use or not.
	for receiver := range held.supply {
		held.supply[receiver] = tuning.Supply{Off: true}
	}
	return held, nil
}

// read and write reach one of the card's numbered registers.
func (c *card) read(register int) (uint32, error) {
	return c.device.ReadU32(c.window, registerAt(register))
}

func (c *card) write(register int, value uint32) error {
	return c.device.WriteU32(c.window, registerAt(register), value)
}

// command asks the card to do something. It writes the same register every
// status read comes from, hence the lock.
func (c *card) command(command uint32) error {
	c.registerMutex.Lock()
	defer c.registerMutex.Unlock()
	return c.write(regCommand, command)
}

func (c *card) status() (uint32, error) {
	c.registerMutex.Lock()
	defer c.registerMutex.Unlock()
	return c.read(regCommand)
}

// waitFor polls the command register until every bit of wanted is set.
func (c *card) waitFor(wanted uint32, budget time.Duration, what string) error {
	deadline := time.Now().Add(budget)
	for {
		status, err := c.status()
		if err != nil {
			return err
		}
		if status&wanted == wanted {
			return nil
		}
		if !time.Now().Before(deadline) {
			return fmt.Errorf("the card did not %s within %v", what, budget)
		}
		time.Sleep(pollInterval)
	}
}

// pollInterval is how often the card is asked again while being waited on. A
// millisecond throughout: nothing here waits long enough for finer to matter.
const pollInterval = time.Millisecond

// ErrI2C reports that a transfer did not get the answer it expected.
var ErrI2C = errors.New("the card's I2C sequencer did not complete")

// i2cBudget bounds a program so a wedged sequencer cannot hang a caller. The
// longest program built here is a four byte read, about 260 instructions,
// which is a few milliseconds at any bus speed these chips take; this is a
// hundred times that.
const i2cBudget = 200 * time.Millisecond

// runProgram loads a transfer into the sequencer's memory, starts it, and
// waits for it.
func (c *card) runProgram(p *program) error {
	words, err := p.instructions()
	if err != nil {
		return err
	}
	// One instruction per write: there is no block move into this memory, and
	// the register word carries both the instruction and where to put it.
	for _, word := range words {
		if err := c.write(regI2C, word); err != nil {
			return err
		}
	}
	if err := c.command(cmdRunI2C); err != nil {
		return err
	}
	deadline := time.Now().Add(i2cBudget)
	for {
		status, err := c.status()
		if err != nil {
			return err
		}
		if status&statusI2CBusy == 0 {
			return nil
		}
		if !time.Now().Before(deadline) {
			return fmt.Errorf("%w: still running after %v", ErrI2C, i2cBudget)
		}
		time.Sleep(pollInterval)
	}
}

// captured takes what a program's read instructions collected. They arrive in
// one register, shifted up as they came in, so the first byte read is the most
// significant; the register holds four bytes, which is what bounds a read.
func (c *card) captured(length int) ([]byte, error) {
	word, err := c.read(regStream)
	if err != nil {
		return nil, err
	}
	data := make([]byte, length)
	for index := length - 1; index >= 0; index-- {
		data[index] = byte(word)
		word >>= 8
	}
	return data, nil
}

// maxRead is how many bytes one transfer can bring back, which is the width of
// the register they arrive in.
const maxRead = 4

// bus presents the sequencer as an ordinary I2C bus. The demodulator and tuner
// drivers above it are shared with boards that have a real I2C master and need
// not know that a transfer here is a program.
type bus struct{ card *card }

// begin starts a program, waking the bus the first time anything uses it.
func (c *card) begin() *program {
	p := newProgram()
	if !c.i2cWoken {
		p.wake()
		c.i2cWoken = true
	}
	return p
}

func (b bus) Write(address byte, data []byte) error {
	b.card.i2cMutex.Lock()
	defer b.card.i2cMutex.Unlock()
	p := b.card.begin()
	p.write(address, data)
	p.end()
	return b.card.runProgram(p)
}

// Read is refused: the sequencer can only read as the second half of a
// transfer that began with a write. Nothing on this card needs it -- both
// tuners sit behind their demodulator's repeater, so a bare tuner read arrives
// here as a write of the repeater register followed by a read.
func (b bus) Read(address byte, length int) ([]byte, error) {
	return nil, fmt.Errorf("this card's I2C sequencer cannot read from %#02x without writing first", address)
}

// WriteRead writes to a slave and reads its answer without releasing the bus
// in between, which is what a register read is.
func (b bus) WriteRead(address byte, write []byte, length int) ([]byte, error) {
	if length <= 0 {
		return nil, b.Write(address, write)
	}
	if length > maxRead {
		return nil, fmt.Errorf("this card's I2C sequencer brings back at most %d bytes, and %d were asked for",
			maxRead, length)
	}
	b.card.i2cMutex.Lock()
	defer b.card.i2cMutex.Unlock()
	p := b.card.begin()
	p.write(address, write)
	p.read(address, length)
	p.end()
	if err := b.card.runProgram(p); err != nil {
		return nil, err
	}
	return b.card.captured(length)
}

// Bus hands the sequencer out as an I2C bus.
func (c *card) Bus() i2c.Bus { return bus{card: c} }

func (c *card) demodulator(receiver int) *tc90522.Demod {
	return c.frontends[receiver].demod
}
