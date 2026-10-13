// Copyright (c) Sony; TurboSight; 2026 Till0196
// SPDX-License-Identifier: GPL-2.0-or-later

// Package sony carries what every member of the Sony CXD2878ER family shares:
// the register map, the chip identifiers, and the acquisition sequences that
// are the same on all of them. It is not a driver -- a driver for one chip
// lives in its own package and composes this one.
//
// The family is sold in two shapes. An ER-suffixed part is one function on its
// own; a GL-suffixed part -- CXD6800GL, CXD6801GL, CXD6802GL -- is demodulator
// and tuner in one package. Either way the tuner is a separate I2C device
// behind the demodulator's repeater, so nothing here has to care which shape a
// card used.
//
// Which tuner it is does matter: it, not the package suffix, decides whether a
// card has a satellite side. ASCOT3 is terrestrial and cable only; the Freia
// core of a CXD2868ER also has a satellite half.
package sony

import "fmt"

// Chip is which member of the CXD2878ER/CXD6802GL family answered.
type Chip uint16

// The chip IDs the family answers with, read from registers 0xFB/0xFD. The
// blocks line up with how the parts are sold: a bare demodulator answers in
// 0x09x or 0x39x, a part with a tuner in the same package in 0x19x.
//
// ChipCXD2856 is the part on the PX-MLT boards.
const (
	ChipCXD2856  Chip = 0x090
	ChipCXD2857  Chip = 0x091
	ChipCXD6802  Chip = 0x197
	ChipCXD6822  Chip = 0x19c
	ChipCXD2879  Chip = 0x297
	ChipCXD2878  Chip = 0x396
	ChipCXD2878A Chip = 0x39a
)

func (chip Chip) String() string {
	switch chip {
	case ChipCXD2856:
		return "CXD2856ER/CXD6800GL"
	case ChipCXD2857:
		return "CXD2857ER"
	case ChipCXD2879:
		return "CXD2879ER"
	case ChipCXD2878:
		return "CXD2878ER/CXD6801GL"
	case ChipCXD6802:
		return "CXD6802GL"
	case ChipCXD6822:
		return "CXD6822GL"
	case ChipCXD2878A:
		return "CXD2878A/CXD6821"
	default:
		return fmt.Sprintf("unknown chip %#03x", uint16(chip))
	}
}

// ReadChipID identifies the chip and remembers the answer. Nothing else can
// run before it: it decides which sequences are the right ones.
func (core *Core) ReadChipID() error {
	if err := core.Bank(0x00); err != nil {
		return err
	}
	high, err := core.Read(SLVX, 0xfb, 1)
	if err != nil {
		return err
	}
	low, err := core.Read(SLVX, 0xfd, 1)
	if err != nil {
		return err
	}
	id := Chip(uint16(high[0]&0x03)<<8 | uint16(low[0]))
	switch id {
	case ChipCXD2856, ChipCXD2857, ChipCXD2879, ChipCXD2878, ChipCXD6802, ChipCXD6822, ChipCXD2878A:
		core.Chip = id
		return nil
	default:
		return fmt.Errorf("unrecognised Sony demodulator: %s", id)
	}
}
