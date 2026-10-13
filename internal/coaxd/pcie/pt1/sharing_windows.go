// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

//go:build windows

package pt1

import (
	"fmt"
	"sync"

	"github.com/Till0196/coax/internal/coaxd/tuning"
)

// What the four receivers share beyond the register window and the sequencer.
//
// The power register is one write carrying everything at once: whether the
// tuners are powered, whether the card is held in reset, what the aerial lead
// is fed with, and four bits per receiver whose meaning is unknown. No receiver
// can change its own part without the card assembling the whole word.
type sharing struct {
	claimMutex sync.Mutex
	claimed    [receivers]bool

	powerMutex sync.Mutex
	// powered and inReset are the card's own state, and supply what each
	// receiver last asked for on the lead.
	powered bool
	inReset bool
	supply  [receivers]tuning.Supply
	// applied is the word last written, so that a request changing nothing
	// does not write.
	applied      uint32
	appliedValid bool
}

// claim marks a receiver as taken: one receiver, one user, because two callers
// tuning the same demodulator would each move the other's channel.
func (c *card) claim(receiver int) error {
	c.claimMutex.Lock()
	defer c.claimMutex.Unlock()
	if c.claimed[receiver] {
		return fmt.Errorf("receiver %d of this card is already in use", receiver)
	}
	c.claimed[receiver] = true
	return nil
}

func (c *card) unclaim(receiver int) {
	c.claimMutex.Lock()
	defer c.claimMutex.Unlock()
	c.claimed[receiver] = false
}

// setPower says whether the tuners are powered and whether the card is held in
// reset, and rewrites the register.
func (c *card) setPower(powered, inReset bool) error {
	c.powerMutex.Lock()
	defer c.powerMutex.Unlock()
	c.powered, c.inReset = powered, inReset
	return c.writePower()
}

// requestSupply says what one receiver wants on the aerial lead and reports
// whether the lead was raised as a result. One lead feeds both satellite
// receivers with no way to feed one and not the other, so what is applied is
// the most any receiver asks for.
func (c *card) requestSupply(receiver int, supply tuning.Supply) (bool, error) {
	c.powerMutex.Lock()
	defer c.powerMutex.Unlock()
	before := c.supplyBits()
	c.supply[receiver] = supply
	after := c.supplyBits()
	if before == after && c.appliedValid {
		return false, nil
	}
	if err := c.writePower(); err != nil {
		return false, err
	}
	return supplyRank(after) > supplyRank(before), nil
}

// supplyBits is what the lead should carry, given what every receiver asked
// for.
func (c *card) supplyBits() uint32 {
	bits := uint32(0)
	for _, one := range c.supply {
		if one.Off {
			continue
		}
		if one.High {
			bits |= powerSupply15V | powerSupply11V
			continue
		}
		bits |= powerSupply11V
	}
	return bits
}

// supplyRank orders the three settings by how much they put on the lead.
func supplyRank(bits uint32) int {
	switch {
	case bits&powerSupply15V != 0:
		return 2
	case bits&powerSupply11V != 0:
		return 1
	default:
		return 0
	}
}

// writePower assembles the whole register and writes it. The caller holds the
// lock.
func (c *card) writePower() error {
	bits := c.supplyBits()
	if c.powered {
		bits |= powerTunersOn
	}
	if !c.inReset {
		bits |= powerNotInReset
	}
	// Every receiver's bits, always. See sleepBits.
	for _, one := range sleepBits {
		bits |= one
	}
	if c.appliedValid && bits == c.applied {
		return nil
	}
	if err := c.write(regPower, bits); err != nil {
		return err
	}
	c.applied, c.appliedValid = bits, true
	return nil
}
