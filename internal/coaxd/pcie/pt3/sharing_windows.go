// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

//go:build windows

package pt3

import (
	"fmt"
	"sync"

	"github.com/Till0196/coax/internal/coaxd/tuning"
)

// What the four receivers of a card share: the I2C master, which is looked after
// in i2c_windows.go; which receivers are in use; and the supply on the aerial
// lead, which is one switch for both satellite receivers.

// claim marks a receiver as taken. One receiver, one user: two callers tuning
// the same demodulator would each be moving the other's channel, and the
// transfer engine behind it has one ring.
func (c *held) claim(channel int) error {
	c.card.claimMutex.Lock()
	defer c.card.claimMutex.Unlock()
	if c.card.claimed[channel] {
		return fmt.Errorf("receiver %d of the card at %s is already in use", channel, c.slot)
	}
	c.card.claimed[channel] = true
	return nil
}

func (c *card) unclaim(channel int) {
	c.claimMutex.Lock()
	defer c.claimMutex.Unlock()
	c.claimed[channel] = false
}

// requestSupply says what one receiver wants on the lead, and reports whether the
// lead was raised as a result. One lead feeds both satellite receivers and there
// is no way to feed one and not the other, so what is applied is the most any
// receiver asks for: the higher voltage wins, and the supply drops only when
// nothing wants it.
func (c *card) requestSupply(channel int, supply tuning.Supply) (bool, error) {
	c.supplyMutex.Lock()
	defer c.supplyMutex.Unlock()

	wanted := supplyOff
	switch {
	case supply.Off:
		wanted = supplyOff
	case supply.High:
		wanted = supplyHigh
	default:
		wanted = supplyLow
	}
	c.supplyWanted[channel] = wanted

	applied := supplyOff
	for _, one := range c.supplyWanted {
		if supplyRank(one) > supplyRank(applied) {
			applied = one
		}
	}
	if applied == c.supplyApplied {
		return false, nil
	}
	if err := c.setSupply(applied); err != nil {
		return false, err
	}
	raised := supplyRank(applied) > supplyRank(c.supplyApplied)
	c.supplyApplied = applied
	return raised, nil
}

// supplyRank orders the three settings by how much they put on the lead, which
// the values do not: they are register contents with two bits meaning something
// else.
func supplyRank(setting uint32) int {
	switch setting {
	case supplyHigh:
		return 2
	case supplyLow:
		return 1
	default:
		return 0
	}
}

// The card's share of the above, kept beside what protects it.
type sharing struct {
	claimMutex sync.Mutex
	claimed    [channels]bool

	supplyMutex sync.Mutex
	// supplyWanted is what each receiver last asked for; supplyApplied is what
	// the lead is actually carrying.
	supplyWanted  [channels]uint32
	supplyApplied uint32
}
