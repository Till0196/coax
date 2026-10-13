// Copyright (c) 2014 Akihiro Tsukada; 2026 Till0196
// SPDX-License-Identifier: GPL-2.0-only

// Package qm1d1b0004 drives the Sharp QM1D1B0004 satellite tuner, which is the
// tuner half of the Sharp VA1J5JF8007S module an Earthsoft PT1 or PT2 carries
// and the satellite front end of the older PLEX PX-S3U boards.
package qm1d1b0004

import (
	"fmt"
	"time"

	"github.com/Till0196/coax/internal/coaxd/i2c"
)

// Config is what the board, rather than the chip, decides. Both boards this
// serves receive ISDB-S only and agree on everything except the charge pump, so
// the filter, the step and the wait are constants here rather than fields.
type Config struct {
	// Pump is the charge pump the divider is programmed with, a two-bit field
	// the first register byte carries above the divider. Zero takes DefaultPump.
	Pump byte
	// SettledPump, when non-zero, is written once the filter is set: one byte
	// repeating the divider's high bits with the charge pump moved to this
	// value. For a board that drives the loop hard to pull in and then backs it
	// off; zero sends nothing.
	SettledPump byte
	// AwaitLock waits for the loop after tuning by reading the chip's one byte
	// until it reports lock. A board whose bus cannot read from the tuner leaves
	// it clear.
	AwaitLock bool
}

// Tuner is one QM1D1B0004 reached over an I2C bus.
type Tuner struct {
	bus     i2c.Bus
	address byte
	config  Config
}

// New addresses one chip. address is the 8-bit (already shifted) I2C address.
func New(bus i2c.Bus, address byte, config Config) *Tuner {
	return &Tuner{bus: bus, address: address, config: config}
}

// What the chip will tune, in kHz.
const (
	FrequencyMin = 950000
	FrequencyMax = 2150000
)

// The crystal and what the loop steps in.
const (
	crystalKHz = 4000
	stepKHz    = crystalKHz / 4
)

// The charge pump, and where the first register byte carries it.
const (
	DefaultPump          = 2
	pumpShift            = 5
	pumpMask        byte = 0x03
	dividerHighMask byte = 0x1f
)

// filterByte and filterLow are the low-pass filter, which neither board varies:
// the four bits are split across the two bytes of the third write, and both use
// 9 -- a filter at 22 MHz, wide enough for the ISDB-S carrier they receive.
const (
	filterByte byte = 0xe4 | (9&0x0c)<<1
	filterLow  byte = (9 & 0x03) << 2
)

// settling is how long the loop is given before the filter is written; the chip
// reports nothing to poll while it pulls in, so this is a flat wait.
const settling = 10 * time.Millisecond

// The one byte the chip answers with, and how long to wait for it to say the
// loop has locked.
const (
	lockedBit    byte = 1 << 6
	lockPoll          = time.Millisecond
	lockAttempts      = 50
)

// The band the chip is put in, by the frequency being tuned. The byte carries
// the divider, band and prescaler at once, hence a table of whole bytes; bit 4
// is the prescaler and changes how the divider word is packed, so it has to be
// read back out of the byte. A frequency belongs to the first row whose ceiling
// is above it; anything past the last row takes 0xc0.
var bands = [...]struct {
	ceiling uint32 // kHz
	control byte
}{
	{986000, 0xb2},
	{1073000, 0xd2},
	{1154000, 0xe2},
	{1291000, 0x20},
	{1447000, 0x40},
	{1615000, 0x60},
	{1791000, 0x80},
	{1972000, 0xa0},
}

// bandAbove is what a frequency past the last row takes.
const bandAbove = 0xc0

// prescalerBit says the divider is prescaled by sixteen rather than
// thirty-two, which changes how the word below is laid out.
const prescalerBit = 1 << 4

func controlFor(frequency uint32) byte {
	for _, band := range bands {
		if frequency < band.ceiling {
			return band.control
		}
	}
	return bandAbove
}

func (config Config) pump() byte {
	if config.Pump == 0 {
		return DefaultPump
	}
	return config.Pump & pumpMask
}

// ErrNoLock reports that the loop did not pull in.
var ErrNoLock = fmt.Errorf("the QM1D1B0004's loop did not lock")

// Tune sets the chip to a frequency in kHz, in three ordered writes: the second
// starts the loop and the third only takes effect once it has settled. A board
// that asked for a settled charge pump takes a fourth.
func (tuner *Tuner) Tune(frequency uint32) error {
	if frequency < FrequencyMin || frequency > FrequencyMax {
		return fmt.Errorf("QM1D1B0004 cannot tune %d kHz, which is outside %d to %d",
			frequency, FrequencyMin, FrequencyMax)
	}
	word := (frequency + stepKHz/2) / stepKHz
	control := controlFor(frequency)
	if control&prescalerBit != 0 {
		// With the smaller prescaler the divider is split differently: the
		// quotient moves up a bit and the remainder keeps its low nibble.
		word = (word<<1)&^0x1f | word&0x0f
	}
	high := byte(word>>8) & dividerHighMask

	// Step one: divider and band, filter still wide, loop not yet told to settle.
	if err := tuner.bus.Write(tuner.address, []byte{
		tuner.config.pump()<<pumpShift | high, byte(word), 0xe0, control,
	}); err != nil {
		return err
	}
	// Step two: start the loop, and let it settle.
	if err := tuner.bus.Write(tuner.address, []byte{0xe4}); err != nil {
		return err
	}
	time.Sleep(settling)

	// Step three: the low-pass filter, its four bits split across the two bytes,
	// the second of which carries the band again.
	if err := tuner.bus.Write(tuner.address, []byte{filterByte, control | filterLow}); err != nil {
		return err
	}

	// Step four, where asked: the first byte again, same divider, charge pump
	// moved on now that the loop has pulled in.
	if settled := tuner.config.SettledPump; settled != 0 {
		if err := tuner.bus.Write(tuner.address, []byte{
			settled&pumpMask<<pumpShift | high,
		}); err != nil {
			return err
		}
	}

	if !tuner.config.AwaitLock {
		return nil
	}
	return tuner.awaitLock()
}

func (tuner *Tuner) awaitLock() error {
	for attempt := 0; attempt < lockAttempts; attempt++ {
		answer, err := tuner.bus.Read(tuner.address, 1)
		if err != nil {
			return err
		}
		if answer[0]&lockedBit != 0 {
			return nil
		}
		time.Sleep(lockPoll)
	}
	return ErrNoLock
}
