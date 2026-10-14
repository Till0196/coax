// Copyright (c) Luis Alves; TurboSight; 2026 Till0196
// SPDX-License-Identifier: GPL-2.0-only

// Package stv6120 drives the STMicroelectronics STV6120 satellite tuner over
// any i2c.Bus, ported from TBS linux_media (tuners/stv6120.c).
//
// Two tuners in one package sharing a crystal, a register file and an I2C
// address. Each half owns seven consecutive registers -- 0x02..0x08 for the
// first, 0x0c..0x12 for the second -- at base 0x02 + 0x0a*Index. TBS cards put
// one half in front of each of the STV0910's two demodulators, so both are in
// use at once; one transfer brings the whole package up, so
// Config.AlreadyBooted restricts the bring-up to this half's registers.
//
// Left out deliberately: the two table bytes that pick the B or C aerial input,
// and a separate bandwidth entry point -- the channel filter is set on every
// tune.
package stv6120

import (
	"errors"
	"fmt"
	"time"

	"github.com/Till0196/coax/pkg/i2c"
)

// Config describes how the board wired the tuner.
type Config struct {
	// Address is the chip's seven-bit I2C address -- 0x60 on the cards here.
	Address byte
	// Index selects which half of the package this is: 0 for the registers
	// at 0x02, 1 for those at 0x0c.
	Index int
	// XtalKHz is the reference crystal frequency in kilohertz, 30000 on the
	// cards here.
	XtalKHz uint32
	// RDiv is the reference divider the PLL arithmetic divides the crystal by.
	RDiv uint32
	// AlreadyBooted says another driver has already brought this package up and
	// may be holding its other half, so only this half's seven registers may be
	// written; a plain Initialize would drop the other half's PLL mid-lock. On the
	// TBS cards the demodulator's index and this tuner's half are opposite --
	// adapter 0 uses demodulator 0 and tuner half 1.
	AlreadyBooted bool
}

// The seven registers of a half, as offsets from its base.
const (
	regN0 = iota
	regN1F0
	regF1
	regF2ICP
	regCFPDiv
	regCFHF
	regCal
)

// tunerInit is the 25-byte initialization table, written from register 0x00 in
// one transfer: both halves' blocks plus the global registers around and
// between them.
var tunerInit = [25]byte{
	0x77,
	0x33, 0xce, 0x54, 0x55, 0x0d, 0x32, 0x44, 0x0e,
	0xf9, 0x1b,
	0x33, 0xce, 0x54, 0x55, 0x0d, 0x32, 0x44, 0x0e,
	0x00, 0x00, 0x4c, 0x00, 0x00, 0x4c,
}

// Bounds on the wait for the VCO and channel-filter calibrations.
const (
	calibrationAttempts = 10
	calibrationInterval = 5 * time.Millisecond
)

// calibrationBusy is the pair of status-register bits that stay set while the
// two calibrations a tune starts are still running.
const calibrationBusy = 0x06

// Tuner is one half of an STV6120 reached over an I2C bus.
type Tuner struct {
	bus    i2c.Bus
	config Config

	// registers caches this half's seven registers, seeded from the init table:
	// the chip is written a block at a time, so bits a tune does not compute
	// must be remembered rather than read back.
	registers [7]byte
}

// New addresses one half of an STV6120 on the given bus.
func New(bus i2c.Bus, config Config) *Tuner {
	tuner := &Tuner{bus: bus, config: config}
	copy(tuner.registers[:], tunerInit[2:9])
	return tuner
}

func (tuner *Tuner) base() byte { return byte(0x02 + 0x0a*tuner.config.Index) }

// statusRegister is the last register of this half's block.
func (tuner *Tuner) statusRegister() byte { return byte(0x08 + 0x0a*tuner.config.Index) }

// writeBlock writes this half's seven registers as one transfer: the chip
// latches a calibration request from the last of them.
func (tuner *Tuner) writeBlock() error {
	data := make([]byte, 0, 8)
	data = append(data, tuner.base())
	data = append(data, tuner.registers[:]...)
	if err := tuner.bus.Write(tuner.config.Address, data); err != nil {
		return fmt.Errorf("STV6120 block at %#02x: %w", tuner.base(), err)
	}
	return nil
}

func (tuner *Tuner) regRead(register byte) (byte, error) {
	data, err := i2c.WriteRead(tuner.bus, tuner.config.Address, []byte{register}, 1)
	if err != nil {
		return 0, fmt.Errorf("STV6120 read %#02x: %w", register, err)
	}
	return data[0], nil
}

// Initialize loads the fixed table, which brings up both halves at once. Call
// it once, before either half is tuned: a second call resets the other half's
// PLL and drops what that demodulator had locked. AlreadyBooted resumes instead.
func (tuner *Tuner) Initialize() error {
	if tuner.config.AlreadyBooted {
		return tuner.resume()
	}
	data := make([]byte, 0, 26)
	data = append(data, 0x00)
	data = append(data, tunerInit[:]...)
	if err := tuner.bus.Write(tuner.config.Address, data); err != nil {
		return fmt.Errorf("STV6120 initialization table: %w", err)
	}
	copy(tuner.registers[:], tunerInit[2:9])
	return nil
}

// defaultRolloffPercent is a hundred times the 1.35 bandwidth factor of a DVB-S
// carrier at 0.35 rolloff. No per-channel rolloff is available, so the filter
// is always sized for the widest -- too much filter costs a little noise, too
// little cuts into the signal.
const defaultRolloffPercent = 135

// Tune moves this half to a frequency in hertz, sizing the channel filter from
// the symbol rate.
func (tuner *Tuner) Tune(frequencyHz, symbolRateSPS uint32) error {
	if tuner.config.XtalKHz == 0 || tuner.config.RDiv == 0 {
		return errors.New("STV6120 needs a crystal frequency and a reference divider")
	}
	cutoffHz := 5_000_000 + mulDiv32(symbolRateSPS, defaultRolloffPercent, 200)
	return tuner.setLO(frequencyHz, cutoffHz)
}

func (tuner *Tuner) setLO(localFrequencyHz, cutoffHz uint32) error {
	// The filter is indexed in megahertz above 5 MHz, in thirty-two steps.
	cutoffIndex := int(cutoffHz/1_000_000) - 5
	if cutoffIndex < 0 {
		cutoffIndex = 0
	}
	if cutoffIndex > 31 {
		cutoffIndex = 31
	}

	frequencyKHz := (localFrequencyHz + 500) / 1000

	// The VCO runs well above the band: pick the prescaler that keeps it there.
	var prescalerSelect, prescaler uint32
	switch {
	case frequencyKHz >= 1_191_000:
		prescalerSelect, prescaler = 0, 2
	case frequencyKHz >= 596_000:
		prescalerSelect, prescaler = 1, 4
	case frequencyKHz >= 299_000:
		prescalerSelect, prescaler = 2, 8
	default:
		prescalerSelect, prescaler = 3, 16
	}

	vcoKHz := frequencyKHz * prescaler
	divider := (vcoKHz * tuner.config.RDiv) / tuner.config.XtalKHz

	// Charge pump current grows with the VCO frequency to keep the loop stable;
	// 4 is skipped -- a gap in the ladder itself.
	var chargePump uint32
	switch {
	case vcoKHz < 2_472_000:
		chargePump = 0
	case vcoKHz < 2_700_000:
		chargePump = 1
	case vcoKHz < 3_021_000:
		chargePump = 2
	case vcoKHz < 3_387_000:
		chargePump = 3
	case vcoKHz < 3_845_000:
		chargePump = 5
	case vcoKHz < 4_394_000:
		chargePump = 6
	default:
		chargePump = 7
	}

	fraction := (vcoKHz * tuner.config.RDiv) % tuner.config.XtalKHz
	fraction = mulDiv32(fraction, 0x40000, tuner.config.XtalKHz)

	tuner.registers[regN0] = byte(divider)
	tuner.registers[regN1F0] = byte((divider>>8)&0x01 | (fraction&0x7f)<<1)
	tuner.registers[regF1] = byte(fraction >> 7)
	tuner.registers[regF2ICP] = tuner.registers[regF2ICP]&0x88 |
		byte(chargePump<<4) | byte((fraction>>15)&0x07)
	// The 0x9f mask keeps the low five bits of what was there and ORs the filter
	// index in, so the index only ever gains bits over a tuner's life. Kept as in
	// the C driver: correcting it would change which filter the chip picks from
	// the second tune on.
	tuner.registers[regCFPDiv] = tuner.registers[regCFPDiv]&0x9f |
		byte(prescalerSelect<<5) | byte(cutoffIndex)
	// Requesting both calibrations is what makes the block write take effect.
	tuner.registers[regCal] = tuner.registers[regCal]&0xf8 | calibrationBusy

	if err := tuner.writeBlock(); err != nil {
		return err
	}
	if err := tuner.waitCalibrated(); err != nil {
		return err
	}
	// Settling time, applied whether or not the calibration bits cleared.
	time.Sleep(10 * time.Millisecond)
	return nil
}

// waitCalibrated polls the two calibration bits until the chip drops them. A
// timeout is reported, not swallowed: it would otherwise hide a tuner that
// never settled behind a demodulator that simply never locks.
func (tuner *Tuner) waitCalibrated() error {
	for attempt := 0; attempt < calibrationAttempts; attempt++ {
		status, err := tuner.regRead(tuner.statusRegister())
		if err != nil {
			return err
		}
		if status&calibrationBusy == 0 {
			return nil
		}
		time.Sleep(calibrationInterval)
	}
	return fmt.Errorf("STV6120 half %d did not finish calibrating", tuner.config.Index)
}

func mulDiv32(a, b, c uint32) uint32 {
	return uint32(uint64(a) * uint64(b) / uint64(c))
}

// resume picks up a package another driver has already brought up, writing the
// seven table bytes at this half's base and nothing else. Both halves' copies
// of those seven bytes are identical, which is why New seeds its cache from the
// first half's regardless of index. Everything global to the package is left
// alone.
func (tuner *Tuner) resume() error {
	// Config.RDiv never reaches a register, so the divider must already be set in
	// the chip; register 0x00 is where the table puts it. Compare the whole
	// register against the table value -- a chip holding anything else is one this
	// driver would compute wrongly for.
	control, err := tuner.regRead(0x00)
	if err != nil {
		return err
	}
	if control != tunerInit[0] {
		return fmt.Errorf("STV6120 was declared already booted but its control register reads %#02x,"+
			" not the %#02x this driver's PLL arithmetic assumes; a cold Initialize is needed,"+
			" which would reset both halves of the package", control, tunerInit[0])
	}

	copy(tuner.registers[:], tunerInit[2:9])
	return tuner.writeBlock()
}
