// Copyright (c) Sony; TurboSight; 2026 Till0196
// SPDX-License-Identifier: GPL-2.0-or-later

package sony

import (
	"fmt"

	"github.com/Till0196/coax/pkg/i2c"
)

// Target selects which of the chip's four I2C addresses a transfer goes to.
// They are derived from the demodulator's own configured address rather than
// from independent config fields.
type Target int

const (
	SLVT Target = iota // the demodulator's own address
	SLVX               // system part: SLVT + 2
	SLVR               // ATSC core: SLVT - 0x20
	SLVM               // ATSC core, second bank: SLVT - 0x54
)

// Op is one step of a register sequence: a plain write, a masked
// read-modify-write, or a multi-byte write.
type Op struct {
	Where    Target
	Register byte
	Value    byte
	Mask     byte
	Data     []byte
}

// Core is one chip of the family reached over an I2C bus, plus the state a
// driver keeps about it. A driver embeds it rather than wrapping it, so the
// register helpers read the same from either side. Not safe for concurrent
// use: a caller sharing one between goroutines must serialise access.
type Core struct {
	bus       i2c.Bus
	config    Config
	addresses [4]byte // indexed by Target

	// Chip is which silicon revision answered, filled in by ReadChipID.
	Chip Chip
	// State is how far the chip has been brought up.
	State State
	// System is what it was last woken for.
	System System
	// Bandwidth is that system's channel width in MHz, or zero for the
	// systems whose width is fixed.
	Bandwidth uint32
}

// NewCore addresses a chip of the family on the given bus.
func NewCore(bus i2c.Bus, config Config) *Core {
	core := &Core{bus: bus, config: config}
	systemAddress := config.SystemAddress
	if systemAddress == 0 {
		systemAddress = config.DemodAddress + 2
	}
	core.addresses = [4]byte{
		SLVT: config.DemodAddress,
		SLVX: systemAddress,
		SLVR: config.DemodAddress - 0x20,
		SLVM: config.DemodAddress - 0x54,
	}
	return core
}

// Config is how the board wired the chip.
func (core *Core) Config() Config { return core.config }

// Bus is the I2C bus the chip is on, for a driver that has to reach something
// else behind the same bus -- the tuner, past the chip's repeater.
func (core *Core) Bus() i2c.Bus { return core.bus }

func (core *Core) Write(where Target, register, value byte) error {
	address := core.addresses[where]
	if err := core.bus.Write(address, []byte{register, value}); err != nil {
		return fmt.Errorf("%s write %#02x/%#02x: %w", core.Chip, address, register, err)
	}
	return nil
}

func (core *Core) WriteMulti(where Target, register byte, data []byte) error {
	address := core.addresses[where]
	if err := core.bus.Write(address, append([]byte{register}, data...)); err != nil {
		return fmt.Errorf("%s write %#02x/%#02x: %w", core.Chip, address, register, err)
	}
	return nil
}

func (core *Core) Read(where Target, register byte, length int) ([]byte, error) {
	address := core.addresses[where]
	data, err := i2c.WriteRead(core.bus, address, []byte{register}, length)
	if err != nil {
		return nil, fmt.Errorf("%s read %#02x/%#02x: %w", core.Chip, address, register, err)
	}
	if len(data) != length {
		return nil, fmt.Errorf("%s short read %#02x/%#02x: %d of %d bytes",
			core.Chip, address, register, len(data), length)
	}
	return data, nil
}

// WriteMasked does a read-modify-write. A zero mask is a no-op, which is what
// lets a sequence carry a step it does not always want to take.
func (core *Core) WriteMasked(where Target, register, value, mask byte) error {
	if mask == 0 {
		return nil
	}
	if mask != 0xff {
		current, err := core.Read(where, register, 1)
		if err != nil {
			return err
		}
		value = i2c.Mask(current[0], value, mask)
	}
	return core.Write(where, register, value)
}

// Bank selects a register bank on the demodulator address before the
// operations that follow it. Nearly every sequence starts with one.
func (core *Core) Bank(n byte) error { return core.Write(SLVT, 0x00, n) }

// ReadBanked reads a run of registers from the demodulator address, where the
// banked register space lives. It satisfies the Banked interface in errors.go.
func (core *Core) ReadBanked(register byte, length int) ([]byte, error) {
	return core.Read(SLVT, register, length)
}

// Freeze holds the chip's monitor registers still, so a reading taken from
// several of them describes one moment. The register is the same on every part
// of the family.
//
// The bank is selected first, which is not a formality: the register space is
// banked, and a caller reaching this from a bank other than zero was writing
// into the monitor block instead of freezing it. The monitors then answered as
// if the demodulator had never locked, so a channel carrying a stream reported
// no lock and no ratio.
func (core *Core) Freeze(hold bool) error {
	value := byte(0x00)
	if hold {
		value = 0x01
	}
	if err := core.Bank(0x00); err != nil {
		return err
	}
	return core.Write(SLVT, 0x01, value)
}

// Run applies a sequence of register operations in order, stopping at the
// first failure.
func (core *Core) Run(sequence []Op) error {
	for _, step := range sequence {
		var err error
		switch {
		case len(step.Data) != 0:
			err = core.WriteMulti(step.Where, step.Register, step.Data)
		case step.Mask != 0:
			err = core.WriteMasked(step.Where, step.Register, step.Value, step.Mask)
		default:
			err = core.Write(step.Where, step.Register, step.Value)
		}
		if err != nil {
			return err
		}
	}
	return nil
}
