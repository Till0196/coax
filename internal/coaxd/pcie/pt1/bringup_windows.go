// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

//go:build windows

package pt1

import (
	"fmt"
	"time"

	"github.com/Till0196/coax/internal/coaxd/pcie/windriver"
	"github.com/Till0196/coax/internal/coaxd/tuning"
)

// open claims a card and brings it far enough up to talk to.
func open(session *windriver.Session, slot windriver.Slot, model Model) (*card, error) {
	device, err := session.OpenDevice(slot)
	if err != nil {
		return nil, err
	}
	held, err := newCard(device, model)
	if err != nil {
		device.Close()
		return nil, err
	}
	// Before anything that can set a transfer going.
	if err := held.leaveSafe(); err != nil {
		device.Close()
		return nil, err
	}
	if err := held.bringUp(); err != nil {
		// Whatever bring-up got as far as, the lead and the tuners are left
		// unpowered.
		_ = held.setPower(false, true)
		device.Close()
		return nil, err
	}
	return held, nil
}

// bringUp takes the card through the sequence it needs at probe. The order and
// the waits matter throughout and are kept exactly as they are; what each step
// is for is said where it is known.
func (c *card) bringUp() error {
	if err := c.identify(); err != nil {
		return err
	}
	// A process that ended without closing the card leaves the transfer
	// walking tables in memory that is no longer that process's. The tables
	// are taken away before bus mastering is given back. Written straight to
	// the register: a card nobody has unlocked yet ignores it, and one that is
	// still running from before is unlocked already.
	if err := c.write(regCommand, cmdForgetTables); err != nil {
		return err
	}
	time.Sleep(settleAfterStop)
	// Without bus mastering the transfer carries nothing.
	if err := c.device.EnableBusMastering(); err != nil {
		return err
	}

	for receiver := range receivers {
		c.frontends[receiver] = newFrontend(c, receiver)
	}

	// Unpowered and held in reset, which is where the card is taken from.
	if err := c.setPower(false, true); err != nil {
		return err
	}

	// The sequencer's memory holds whatever was in it, and a half-written
	// program would run when the first transfer starts it.
	if err := c.clearSequencer(); err != nil {
		return err
	}
	if err := c.sync(); err != nil {
		return err
	}
	// The serial number is clocked out and thrown away: the reading is what
	// walks the line back to its resting state.
	if _, err := c.identity(); err != nil {
		return err
	}
	if err := c.unlock(); err != nil {
		return err
	}
	if err := c.resetInterface(cmdResetPCI, cmdReleasePCI, statusPCIReset, "reset its bus interface"); err != nil {
		return err
	}
	if err := c.resetInterface(cmdResetRAM, cmdReleaseRAM, statusRAMReset, "reset its memory interface"); err != nil {
		return err
	}
	if err := c.trainMemory(); err != nil {
		return err
	}

	// Every receiver's share off, so nothing flows once the tables are given.
	for receiver := range receivers {
		if err := c.setStream(receiver, false); err != nil {
			return err
		}
	}

	// Power on, then out of reset, with the waits the card needs between.
	if err := c.setPower(true, true); err != nil {
		return err
	}
	time.Sleep(20 * time.Millisecond)
	if err := c.setPower(true, false); err != nil {
		return err
	}
	time.Sleep(2 * time.Millisecond)

	return c.startFrontends()
}

// leaveSafe has the driver stop the card when it is let go, including when this
// process dies without closing it. The engine stops by itself once it has used
// up the page tables it was given, but up to four are given at a time, and that
// is several megabytes written into memory the system has had back. A card the
// driver cannot do this for is not used.
//
// The page tables are taken back first, which is what stops the transfer; then
// the memory interface is disabled and everything on the power register turned
// off and held in reset. On an ordinary close the same writes are made again
// after Close's own, and leave the sleep bits clear as well.
func (c *card) leaveSafe() error {
	return c.device.SetupCleanup([]windriver.RegisterWrite{
		{Space: c.window, Offset: registerAt(regCommand), Value: cmdForgetTables},
		{Space: c.window, Offset: registerAt(regCommand), Value: cmdDisableRAM},
		{Space: c.window, Offset: registerAt(regPower), Value: 0},
	})
}

// identify refuses a card this package does not know how to drive. There is no
// version register -- the card says what it is in configuration space and
// nowhere else -- so the model was settled by the scan and only the revision is
// checked here.
func (c *card) identify() error {
	revision, err := c.device.Revision()
	if err != nil {
		return err
	}
	if revision != cardRevision {
		return fmt.Errorf("the card at %s is revision %d, and this drives %d",
			c.device.Slot(), revision, cardRevision)
	}
	return nil
}

// clearSequencer writes a stop into every instruction of the I2C memory.
func (c *card) clearSequencer() error {
	stop := func(address int) uint32 {
		// Not busy, nothing sampled, both lines released, pointing at the halt.
		return uint32(address)<<instructionAddressShift | haltAddress
	}
	for address := range instructionCount {
		if err := c.write(regI2C, stop(address)); err != nil {
			return err
		}
	}
	// 128 more writes of the same instruction at address zero, for no stated
	// reason. Kept: either it settles something or it is harmless.
	for range 128 {
		if err := c.write(regI2C, stop(0)); err != nil {
			return err
		}
	}
	return nil
}

// sync walks the card's serial line to its starting point. The card answers a
// bit at a time and says when it has reached the beginning; 57 clocks is the
// whole length of what it has to say, so that many finds it from anywhere.
func (c *card) sync() error {
	for range identityBits {
		status, err := c.status()
		if err != nil {
			return err
		}
		if status&statusSynced != 0 {
			return nil
		}
		if err := c.command(cmdClock); err != nil {
			return err
		}
	}
	return fmt.Errorf("the card at %s never said where its serial number begins", c.device.Slot())
}

// identityBits is how long the card's serial number is.
const identityBits = 57

func (c *card) identity() (uint64, error) {
	number := uint64(0)
	for bit := range identityBits {
		status, err := c.status()
		if err != nil {
			return 0, err
		}
		if status&statusIdentity != 0 {
			number |= 1 << uint(bit)
		}
		if err := c.command(cmdClock); err != nil {
			return 0, err
		}
	}
	return number, nil
}

// unlock waits for the card to agree to take commands.
func (c *card) unlock() error {
	if err := c.command(cmdClock); err != nil {
		return err
	}
	return c.waitFor(statusUnlocked, 10*pollInterval, "unlock")
}

// resetInterface resets one of the card's two interfaces and waits for it.
func (c *card) resetInterface(hold, release, done uint32, what string) error {
	if err := c.command(hold); err != nil {
		return err
	}
	if err := c.command(release); err != nil {
		return err
	}
	return c.waitFor(done, 20*pollInterval, what)
}

// trainMemory steps the memory interface round until it is reading reliably.
// What it counts is not written down: each round asks for one step and watches
// a status bit until it changes. The round count differs per card. It reads
// like a clock phase walked round until the data window is centred.
func (c *card) trainMemory() error {
	time.Sleep(2 * time.Millisecond)
	for round := range c.model.trainingRounds() {
		if err := c.trainOnce(); err != nil {
			return fmt.Errorf("train the memory interface, round %d of %d: %w",
				round+1, c.model.trainingRounds(), err)
		}
	}
	return nil
}

// trainOnce steps the interface once and waits for the card to say so.
func (c *card) trainOnce() error {
	before, err := c.status()
	if err != nil {
		return err
	}
	before &= statusRAMPhase
	if err := c.command(cmdEnableRAM); err != nil {
		return err
	}
	// 1024 reads of the register and then a sleep, ten times over. The spin is
	// what makes this fast enough to do 166 times; the sleep stops it being a
	// busy wait when the card is not answering.
	for range 10 {
		for range trainingSpins {
			status, err := c.status()
			if err != nil {
				return err
			}
			if status&statusRAMPhase != before {
				return nil
			}
		}
		time.Sleep(pollInterval)
	}
	return fmt.Errorf("the card's memory interface did not step")
}

// trainingSpins is how many status reads before the card is given a moment.
const trainingSpins = 1024

// setStream starts or stops one receiver's share of the transfer. The register
// takes a mask beside the value (bit receiver+8), so one receiver changes
// without knowing what the others are set to.
func (c *card) setStream(receiver int, enabled bool) error {
	value := uint32(1) << uint(receiver+8)
	if enabled {
		value |= 1 << uint(receiver)
	}
	return c.write(regStream, value)
}

// startFrontends brings the four receivers up. Every core is reset first,
// terrestrial before satellite: the two cores of one chip share a reset, so
// resetting a terrestrial one takes its satellite partner down with it.
func (c *card) startFrontends() error {
	for _, satellite := range []bool{false, true} {
		for _, fe := range c.frontends {
			if fe.satellite() != satellite {
				continue
			}
			if err := fe.reset(); err != nil {
				return err
			}
		}
	}
	// Then each is configured and left asleep, to be woken by whoever opens it.
	for _, fe := range c.frontends {
		if err := fe.configure(); err != nil {
			return err
		}
		if err := fe.sleepDemod(); err != nil {
			return err
		}
	}
	return nil
}

// Close gives the card back as it was found: transfer stopped, memory
// interface disabled, supply off, card held in reset.
func (c *card) Close() error {
	for receiver := range receivers {
		_ = c.setStream(receiver, false)
	}
	c.streamMutex.Lock()
	if c.ring != nil {
		_ = c.ring.close()
		c.ring, c.demux = nil, nil
	}
	c.streamMutex.Unlock()
	_ = c.command(cmdDisableRAM)
	for receiver := range receivers {
		_, _ = c.requestSupply(receiver, tuning.Supply{Off: true})
	}
	_ = c.setPower(false, true)
	return c.device.Close()
}
