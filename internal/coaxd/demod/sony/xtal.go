// Copyright (c) Sony; TurboSight; 2026 Till0196
// SPDX-License-Identifier: GPL-2.0-or-later
//
// The tuner crystal tables. Both tuner cores take the crystal the same way --
// a code in the first byte of the 0x81 power-up block and a reference divider
// inside the 0x5e tuning block -- but serve different sets: ASCOT3 has a code
// for all four crystals the family names, Freia for two. A crystal a core
// cannot serve is an error rather than a wrong number written to a real chip.

package sony

import "fmt"

// ASCOT3XtalCode is the crystal code the ASCOT3 core's power-up block takes,
// register 0x81 byte 0.
func ASCOT3XtalCode(xtal TunerXtal) (byte, error) {
	switch xtal {
	case TunerXtal16000KHz:
		return 0x10, nil
	case TunerXtal20500KHz:
		return 0xd4, nil
	case TunerXtal24000KHz:
		return 0x18, nil
	case TunerXtal41000KHz:
		return 0x69, nil
	default:
		return 0, fmt.Errorf("the ASCOT3 tuner core has no setting for a %s crystal", xtal)
	}
}

// ASCOT3Reference is the PLL reference divider the ASCOT3 core's tuning block
// takes, register 0x5e byte 4. This is the 8 MHz table, the one every board in
// this tree uses; a second 1 MHz table exists for the cable systems but only
// on the ASCOT3I sub-variant (chip ID 2871A), which this port does not tell
// apart -- see the note beside the caller.
func ASCOT3Reference(xtal TunerXtal) (byte, error) {
	switch xtal {
	case TunerXtal16000KHz, TunerXtal20500KHz:
		return 0x02, nil
	case TunerXtal24000KHz:
		return 0x03, nil
	case TunerXtal41000KHz:
		return 0x05, nil
	default:
		return 0, fmt.Errorf("the ASCOT3 tuner core has no reference divider for a %s crystal", xtal)
	}
}

// ASCOT3LocalTuning is the first of the four bytes following the reference
// divider in the ASCOT3 core's 0x5e block. The cable systems take one value
// whatever the crystal; the rest take another, except on a 20.5 MHz crystal.
func ASCOT3LocalTuning(xtal TunerXtal, cable bool) byte {
	switch {
	case cable:
		return 0x1c
	case xtal == TunerXtal20500KHz:
		return 0x8c
	default:
		return 0xb4
	}
}

// FreiaXtalCode is the crystal code the Freia core's power-up block takes,
// register 0x81 byte 0. The Freia serves only two of the family's four
// crystals: falling back to the 16 MHz code for anything not 24 MHz would
// program a 20.5 or 41 MHz board for a crystal it does not have, so an
// unserved crystal is an error instead.
func FreiaXtalCode(xtal TunerXtal) (byte, error) {
	switch xtal {
	case TunerXtal16000KHz:
		return 0x10, nil
	case TunerXtal24000KHz:
		return 0x18, nil
	default:
		return 0, fmt.Errorf("the Freia tuner core has no setting for a %s crystal", xtal)
	}
}

// FreiaReference is the PLL reference divider the Freia core's tuning block
// takes, register 0x5e byte 4 and register 0x04 byte 2. It serves the same two
// crystals FreiaXtalCode does.
func FreiaReference(xtal TunerXtal) (byte, error) {
	switch xtal {
	case TunerXtal16000KHz:
		return 0x02, nil
	case TunerXtal24000KHz:
		return 0x03, nil
	default:
		return 0, fmt.Errorf("the Freia tuner core has no reference divider for a %s crystal", xtal)
	}
}
