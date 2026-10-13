// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package px3

import (
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/Till0196/coax/internal/coaxd/trace"
)

// The transport stream does not go from the demodulator to the bridge
// directly: it passes through the card interface IC (ASIE5606 and its
// successors), which has to be brought up before the first stream: without it
// no packets reach the bridge. It is reset and read when the board is opened,
// and only register 5 is written as a stream starts. On the V2 all of it goes
// through the controlling bridge, since its second bridge carries nothing but
// its streams.
//
//   - a reset pulse on a GPIO line (0x11 for the first unit, 0x80 for any
//     other, unless that one shares the first's IC);
//   - the IC's revision from register 0x09, (value >> 1) & 0x1f, at 0x5a or
//     else 0x4a (0x5c or else 0x4c for a V2 tuner with an IC of its own);
//   - for revisions 0x03, 0x0f and 0x10, register 5 written from the
//     demodulator bring-up's answer, which is always 0xe9: bit 1 would give
//     0x10, bit 3 gives 0x20. Revisions 0x0f and 0x10 serve both tuners of a
//     bridge and are written once; the V2's revisions 0x12 to 0x14 get no
//     write at all.

// tsChipShared are the revisions of an IC that serves both tuners of a bridge.
var tsChipShared = []byte{0x0f, 0x10, 0x12, 0x13, 0x14}

// tsChipOutput is the register the TS output is configured in, and the value
// that turns it on.
const (
	tsChipOutputRegister = 0x05
	tsChipOutput         = 0x20
)

// tsChip is the card interface IC in a tuner's stream path, once found.
type tsChip struct {
	address  byte
	revision byte
}

// tsChips is what has been found in one enclosure, by unit: the tuner index on
// the older boards, 2*position + index on the V2, whose two bridges share one
// set of ICs behind the controlling bridge.
type tsChips struct {
	sync.Mutex
	found [4]*tsChip
}

// unit numbers a tuner across the enclosure: on the V2, both bridges' tuners.
func (hardware *hardware) unit(index int) int {
	if hardware.v2 {
		return 2*hardware.at + index
	}
	return index
}

// initTSChip resets and finds the IC in a tuner's stream path, when the board
// is opened, after the front ends and before the first tune. One already found is left alone: resetting it again would cut a
// stream running through it.
func (hardware *hardware) initTSChip(index int) error {
	hardware.chips.Lock()
	defer hardware.chips.Unlock()
	_, err := hardware.lookUpTSChip(index)
	return err
}

// prepareTSChip writes the output register of the IC in a tuner's stream path
// on each stream, looking for the IC again if the open did not find it.
func (hardware *hardware) prepareTSChip(index int) error {
	hardware.chips.Lock()
	defer hardware.chips.Unlock()
	chip, err := hardware.lookUpTSChip(index)
	if err != nil {
		return err
	}
	switch chip.revision {
	case 0x03:
	case 0x0f, 0x10:
		// One IC for both tuners of a bridge: it is written for the first.
		if index != 0 && hardware.chips.found[hardware.unit(0)] == chip {
			return nil
		}
	default:
		return nil
	}
	return hardware.control.OpenCardBus(chip.address).Write(tsChipOutputRegister, []byte{tsChipOutput})
}

// lookUpTSChip returns the IC in a tuner's stream path, finding it first if it
// has not been. The caller holds hardware.chips.
func (hardware *hardware) lookUpTSChip(index int) (*tsChip, error) {
	unit := hardware.unit(index)
	if chip := hardware.chips.found[unit]; chip != nil {
		return chip, nil
	}
	chip, err := hardware.findTSChip(unit)
	// The reset pulse shares its GPIO bank with the V2's amplifier lines, so put
	// them back where they were asked to be.
	for tuner, on := range hardware.amplifierOn {
		if err := hardware.setExternalAmplifier(tuner, on); err != nil {
			return nil, err
		}
	}
	if err != nil {
		return nil, err
	}
	hardware.chips.found[unit] = chip
	trace.Printf("px3 %s: tuner %d (unit %d) streams through the card interface at %#02x, revision %#02x",
		hardware.bridge.Name(), index, unit, chip.address, chip.revision)
	return chip, nil
}

// findTSChip resets and finds the IC in one unit's stream path. The caller
// holds hardware.chips.
func (hardware *hardware) findTSChip(unit int) (*tsChip, error) {
	if unit == 0 {
		return hardware.resetTSChip(0x11, []byte{0x5a, 0x4a})
	}
	// A unit whose IC is the first's shares it and does not reset it again: that
	// would cut the first unit's stream.
	if other := hardware.chips.found[0]; other != nil && slices.Contains(tsChipShared, other.revision) {
		return other, nil
	}
	addresses := []byte{0x5a, 0x4a}
	if hardware.v2 {
		addresses = []byte{0x5c, 0x4c}
	}
	chip, err := hardware.resetTSChip(0x80, addresses)
	if err == nil || hardware.chips.found[0] != nil {
		return chip, err
	}
	// No IC of its own, and the first unit's not brought up yet: bring that one
	// up, and take it if it is one that serves more than one tuner.
	first, firstErr := hardware.resetTSChip(0x11, []byte{0x5a, 0x4a})
	if firstErr != nil {
		return nil, err
	}
	hardware.chips.found[0] = first
	trace.Printf("px3 %s: unit 0's card interface at %#02x, revision %#02x, brought up for unit %d",
		hardware.bridge.Name(), first.address, first.revision, unit)
	if !slices.Contains(tsChipShared, first.revision) {
		return nil, err
	}
	return first, nil
}

// resetTSChip pulses the reset line under mask and finds the IC at the first
// of the addresses that answers.
func (hardware *hardware) resetTSChip(mask byte, addresses []byte) (*tsChip, error) {
	for _, step := range []gpioStep{
		{value: 0xff, mask: mask, wait: 25 * time.Millisecond},
		{value: 0x00, mask: mask, wait: 25 * time.Millisecond},
		{value: 0xff, mask: mask, wait: 50 * time.Millisecond},
	} {
		if err := hardware.control.SetGPIO(step.value, step.mask); err != nil {
			return nil, fmt.Errorf("reset the card interface: %w", err)
		}
		time.Sleep(step.wait)
	}
	return hardware.probeTSChip(addresses)
}

// probeTSChip finds the IC at the first of the addresses that answers.
func (hardware *hardware) probeTSChip(addresses []byte) (*tsChip, error) {
	for _, address := range addresses {
		revision, err := hardware.tsChipRevision(address)
		if err != nil {
			continue
		}
		if revision > 0x14 {
			return nil, fmt.Errorf("the card interface at %#02x reports revision %#02x, which is not a known one", address, revision)
		}
		return &tsChip{address: address, revision: revision}, nil
	}
	return nil, fmt.Errorf("no card interface answered at % #02x; the stream passes through it", addresses)
}

// tsChipRevision reads an IC's revision.
func (hardware *hardware) tsChipRevision(address byte) (byte, error) {
	identity, err := hardware.control.OpenCardBus(address).Read(cardIdentityRegister, 1)
	if err != nil {
		return 0, err
	}
	return identity[0] >> 1 & 0x1f, nil
}
