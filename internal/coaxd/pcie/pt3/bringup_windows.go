// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

//go:build windows

package pt3

import (
	"fmt"
	"time"

	"github.com/Till0196/coax/internal/coaxd/pcie/windriver"
)

// Where each channel's demodulator core answers. The address is built by the
// card rather than strapped on the chips: 1<<4 | pin<<2 | tuner<<1 | satellite,
// with pin always zero here, so the four cores are 0x10 to 0x13:
//
//	0x10  tuner 0, terrestrial      0x11  tuner 0, satellite
//	0x12  tuner 1, terrestrial      0x13  tuner 1, satellite
func demodAddress(channel int) byte {
	address := byte(1<<4 | tunerOf(channel)<<1)
	if satelliteChannel(channel) {
		address |= 1
	}
	return address
}

func open(session *windriver.Session, slot windriver.Slot) (*card, error) {
	// The slot comes from a selector, and the driver registers whatever card
	// is there. Only one the scan lists as a PT3 is opened.
	slots, err := session.Scan(pciVendor, pciDevice)
	if err != nil {
		return nil, err
	}
	listed := false
	for _, one := range slots {
		listed = listed || one == slot
	}
	if !listed {
		return nil, fmt.Errorf("there is no PT3 at %s", slot)
	}
	device, err := session.OpenDevice(slot)
	if err != nil {
		return nil, err
	}
	held, err := newCard(device)
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
		device.Close()
		return nil, err
	}
	return held, nil
}

// bringUp checks that this really is the card this package knows and takes it
// through the sequence it needs at probe. The order matters and is not derivable:
// the two ROM programs have to run where they do, the tuners must be powered
// between the core initialisation and the pin configuration, and both halves of
// the can have to be woken and tuned once before any of it is used.
func (c *card) bringUp() error {
	if err := c.identify(); err != nil {
		return err
	}
	// A process that ended without closing the card leaves its transfers
	// running, into memory that is no longer that process's. All four are
	// stopped before anything else, and bus mastering is not given back to a
	// card that will not stop.
	if err := c.stopTransfers(); err != nil {
		c.device.Untrusted()
		_ = c.device.DisableBusMastering()
		return err
	}
	// Without bus mastering the DMA is arranged perfectly and transfers nothing.
	if err := c.device.EnableBusMastering(); err != nil {
		return err
	}
	// The supply on the aerial lead starts off. See tuning.
	if err := c.setSupply(supplyOff); err != nil {
		return err
	}

	for channel := range channels {
		c.frontends[channel] = newFrontend(c, channel)
	}

	// Whoever had the card before may not have let it go tidily.
	if err := c.resetI2C(); err != nil {
		return err
	}
	if err := c.initialiseDemods(); err != nil {
		return err
	}
	for _, fe := range c.frontends {
		if err := fe.start(); err != nil {
			return err
		}
		if err := fe.wakeDemod(); err != nil {
			return err
		}
	}
	time.Sleep(3 * time.Millisecond)

	// Nothing behind the demodulators answers until this has run. The register
	// that powers the tuners is neither on the card nor on each tuner: it is one
	// register of one demodulator core, and it powers the whole can.
	if err := c.setPower(true, false); err != nil {
		return err
	}
	for _, fe := range c.frontends {
		if err := fe.configurePins(); err != nil {
			return err
		}
	}
	time.Sleep(5 * time.Millisecond)

	// The satellite tuners have a driver and are initialised one at a time. The
	// terrestrial ones do not: their sequence is in the card's ROM, is board
	// specific, and runs for both at once.
	for _, fe := range c.frontends {
		if fe.satellite() {
			if err := fe.wakeTuner(); err != nil {
				return err
			}
		}
	}
	if err := c.initialiseTerrestrialTuners(); err != nil {
		return err
	}
	if err := c.setPower(true, true); err != nil {
		return err
	}

	// Wake and tune every tuner once, as the manufacturer asks: four tuners in one
	// can settle away from each other if all are placed before any is used.
	for _, fe := range c.frontends {
		if !fe.satellite() {
			if err := fe.wakeTuner(); err != nil {
				return err
			}
		}
		if err := fe.settle(); err != nil {
			return err
		}
	}
	// And back to sleep, to be woken by whoever opens a channel.
	for _, fe := range c.frontends {
		if err := fe.sleepTuner(); err != nil {
			return err
		}
		if err := fe.sleepDemod(); err != nil {
			return err
		}
	}
	return nil
}

// identify refuses a card this package does not know how to drive: the register
// map is one FPGA revision's, and a card built to another would answer every read
// with something plausible and wrong.
func (c *card) identify() error {
	version, err := c.device.ReadU32(c.registers, regVersion)
	if err != nil {
		return err
	}
	model, fpga := byte(version>>24), byte(version>>8)
	if model != cardModel {
		return fmt.Errorf("the card at %s says it is a PT%d", c.device.Slot(), model)
	}
	if fpga != cardFPGA {
		return fmt.Errorf("the card at %s has FPGA revision %#02x, and this drives %#02x",
			c.device.Slot(), fpga, cardFPGA)
	}
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

// initialiseDemods runs the ROM program that initialises all four demodulator
// cores. Its contents cannot be read out: one transaction holding the chip's
// initialisation and this board's configuration together, which is why the
// sequence is nowhere in this package's source.
func (c *card) initialiseDemods() error {
	if err := c.runROM(romInitDemods); err != nil {
		return fmt.Errorf("run the card's demodulator initialisation: %w", err)
	}
	return nil
}

// initialiseTerrestrialTuners runs the ROM program that is the whole of the two
// MxL301RF tuners' initialisation.
func (c *card) initialiseTerrestrialTuners() error {
	time.Sleep(time.Millisecond)
	if err := c.runROM(romInitTuners); err != nil {
		return fmt.Errorf("run the card's tuner initialisation: %w", err)
	}
	return nil
}

// setPower drives the card's supply for its tuners and its amplifier. Two bits
// each, and the value for "on" is 3 rather than 1, hence the written-out shifts.
func (c *card) setPower(tuner, amplifier bool) error {
	value := byte(0x01<<4 | 0x01)
	if tuner {
		value |= 0x03 << 6
	} else {
		value |= 0x02 << 6
	}
	if amplifier {
		value |= 0x03 << 2
	} else {
		value |= 0x02 << 2
	}
	return c.frontends[powerChannel].demod.WriteRegister(0x1e, value)
}

// powerChannel is the only core the power register exists on: the terrestrial
// core of the second tuner, which the can's power lines are wired to.
const powerChannel = 3

func (c *card) setSupply(setting uint32) error {
	return c.device.WriteU32(c.registers, regSystemW, setting)
}

// stopTransfers tells every channel to stop and waits until each says it has.
func (c *card) stopTransfers() error {
	for channel := range channels {
		r := &ring{card: c, channel: channel}
		if err := r.stop(); err != nil {
			return err
		}
	}
	return nil
}

// halt stops the card in a hurry: every channel told to stop, and bus mastering
// taken away so that one which does not listen writes nowhere. Nothing is
// locked or waited for. See device.OnHalt.
func (c *card) halt() {
	for channel := range channels {
		_ = c.device.WriteU32(c.registers, channelRegister(channel, regDMAControl), dmaStop)
	}
	_ = c.device.DisableBusMastering()
}

// Close gives the card back as it was found: supply off, tuners unpowered.
func (c *card) Close() error {
	_ = c.setSupply(supplyOff)
	_ = c.setPower(false, false)
	return c.device.Close()
}
