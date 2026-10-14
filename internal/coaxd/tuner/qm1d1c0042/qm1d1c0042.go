// Copyright (c) 2014 Akihiro Tsukada; 2026 Till0196
// SPDX-License-Identifier: GPL-2.0-only
//
// Ported from the Linux kernel (drivers/media/tuners/qm1d1c0042.c)
// by Akihiro Tsukada.

// Package qm1d1c0042 drives the Sharp QM1D1C0042 satellite tuner over any
// i2c.Bus, which on the boards that carry it is the repeater behind a TC90522.
//
// Very little about the chip is published, so register writes whose meaning is
// unknown keep the order and values they are known to work with. It is one of a
// pair inside a Sharp VA4M6JC2103 can; the board has already set up the
// intermediate frequency, so this driver only tunes and sleeps.
package qm1d1c0042

import (
	"fmt"
	"time"

	"github.com/Till0196/coax/pkg/i2c"
)

// The whole register file is shadowed: registers are written as whole bytes
// built from bits set at different times, and the chip does not read back.
const registerCount = 0x20

// The two register images the chip is known to come up wanting. Register 0
// reads back the first byte of its own row, which is the only thing that
// distinguishes them; a chip answering with neither is unknown.
var initialRegisters = [2][registerCount]byte{{
	0x48, 0x1c, 0xa0, 0x10, 0xbc, 0xc5, 0x20, 0x33,
	0x06, 0x00, 0x00, 0x00, 0x03, 0x00, 0x00, 0x00,
	0x00, 0xff, 0xf3, 0x00, 0x2a, 0x64, 0xa6, 0x86,
	0x8c, 0xcf, 0xb8, 0xf1, 0xa8, 0xf2, 0x89, 0x00,
}, {
	0x68, 0x1c, 0xc0, 0x10, 0xbc, 0xc1, 0x11, 0x33,
	0x03, 0x00, 0x00, 0x00, 0x03, 0x00, 0x00, 0x00,
	0x00, 0xff, 0xf3, 0x00, 0x3f, 0x25, 0x5c, 0xd6,
	0x55, 0xcf, 0x95, 0xf6, 0x36, 0xf2, 0x09, 0x00,
}}

// Config is what the board, rather than the chip, decides.
type Config struct {
	// XtalFreq is the crystal on the board, in kHz; zero takes the 16000 every
	// known board fits. Tune's divider arithmetic is only correct for the
	// crystal actually soldered on.
	XtalFreq uint32
	// LPF selects the low-pass filter path, which is what ISDB-S wants. With it
	// off the chip searches instead, with its own settling times.
	LPF bool
	// FastSearch picks the shorter of the two searches; only consulted when LPF
	// is off.
	FastSearch bool

	// How long to wait for the chip to settle after a tune, one per path.
	// Zero takes the default.
	LPFWait          time.Duration
	FastSearchWait   time.Duration
	NormalSearchWait time.Duration
}

const (
	defaultXtalFreq         = 16000 // kHz
	defaultLPFWait          = 20 * time.Millisecond
	defaultFastSearchWait   = 4 * time.Millisecond
	defaultNormalSearchWait = 15 * time.Millisecond
)

func (config Config) withDefaults() Config {
	if config.XtalFreq == 0 {
		config.XtalFreq = defaultXtalFreq
	}
	if config.LPFWait == 0 {
		config.LPFWait = defaultLPFWait
	}
	if config.FastSearchWait == 0 {
		config.FastSearchWait = defaultFastSearchWait
	}
	if config.NormalSearchWait == 0 {
		config.NormalSearchWait = defaultNormalSearchWait
	}
	return config
}

// Tuner is one QM1D1C0042 reached over an I2C bus.
type Tuner struct {
	bus     i2c.Bus
	address byte
	config  Config
	regs    [registerCount]byte
}

// New addresses one chip. address is the 8-bit (already shifted) I2C address.
func New(bus i2c.Bus, address byte, config Config) *Tuner {
	return &Tuner{bus: bus, address: address, config: config.withDefaults()}
}

// The satellite band the chip will tune, in kHz, as its documentation gives it.
const (
	FrequencyMin = 950000
	FrequencyMax = 2150000
)

func (tuner *Tuner) writeRegister(register, value byte) error {
	return tuner.bus.Write(tuner.address, []byte{register, value})
}

func (tuner *Tuner) write(register byte) error {
	return tuner.writeRegister(register, tuner.regs[register])
}

func (tuner *Tuner) readRegister(register byte) (byte, error) {
	data, err := i2c.WriteRead(tuner.bus, tuner.address, []byte{register}, 1)
	if err != nil {
		return 0, err
	}
	if len(data) != 1 {
		return 0, fmt.Errorf("QM1D1C0042 short read: %d bytes", len(data))
	}
	return data[0], nil
}

// Initialize resets the chip, works out which register image it wants, writes
// it, and wakes it up. The board must have powered the chip first; unpowered it
// answers nothing and the identity check is what notices.
func (tuner *Tuner) Initialize() error {
	// Three times: the first two are unchecked because the chip has just been
	// powered and the first transfer after that is allowed to be lost.
	_ = tuner.writeRegister(0x01, 0x0c)
	_ = tuner.writeRegister(0x01, 0x0c)
	if err := tuner.writeRegister(0x01, 0x0c); err != nil { // soft reset on
		return fmt.Errorf("QM1D1C0042 reset: %w", err)
	}
	time.Sleep(2 * time.Millisecond)
	if err := tuner.writeRegister(0x01, 0x1c); err != nil { // soft reset off
		return fmt.Errorf("QM1D1C0042 release reset: %w", err)
	}

	identity, err := tuner.readRegister(0x00)
	if err != nil {
		return fmt.Errorf("QM1D1C0042 identity: %w", err)
	}
	row := -1
	for index := range initialRegisters {
		if identity == initialRegisters[index][0x00] {
			row = index
			break
		}
	}
	if row < 0 {
		return fmt.Errorf("QM1D1C0042 answered %#02x, which is not one it is known to answer", identity)
	}
	tuner.regs = initialRegisters[row]
	time.Sleep(2 * time.Millisecond)

	tuner.regs[0x0c] |= 0x40
	if err := tuner.write(0x0c); err != nil {
		return err
	}
	time.Sleep(tuner.config.LPFWait)

	// Every writable register in order; 0x0d..0x10 are not writable.
	for register := byte(0x01); register <= 0x0c; register++ {
		if err := tuner.write(register); err != nil {
			return err
		}
	}
	for register := byte(0x11); register < registerCount; register++ {
		if err := tuner.write(register); err != nil {
			return err
		}
	}

	if err := tuner.Wake(); err != nil {
		return err
	}
	return tuner.setSearchMode(tuner.config.FastSearch)
}

func (tuner *Tuner) setSearchMode(fast bool) error {
	if fast {
		tuner.regs[0x03] |= 0x01
	} else {
		tuner.regs[0x03] &^= 0x01
	}
	return tuner.write(0x03)
}

// Wake brings the chip out of standby.
func (tuner *Tuner) Wake() error {
	tuner.regs[0x01] |= 1 << 3 // baseband registers enabled
	tuner.regs[0x01] &^= 1 << 0
	tuner.regs[0x05] &^= 1 << 3 // phase detector out of reset
	if err := tuner.write(0x01); err != nil {
		return err
	}
	return tuner.write(0x05)
}

// Sleep puts the chip in standby.
func (tuner *Tuner) Sleep() error {
	tuner.regs[0x01] &^= 1 << 3 // baseband registers disabled
	tuner.regs[0x01] |= 1 << 0  // standby
	tuner.regs[0x05] |= 1 << 3  // phase detector held in reset
	if err := tuner.write(0x05); err != nil {
		return err
	}
	return tuner.write(0x01)
}

// PLLLocked reports whether the tuner's loop has settled. Polling rather than
// waiting a fixed time separates a dead lead from a tuner that never moved.
func (tuner *Tuner) PLLLocked() (bool, error) {
	value, err := tuner.readRegister(0x0d)
	if err != nil {
		return false, err
	}
	return value&0x40 != 0, nil
}

// WaitForPLL waits for the loop to settle, reporting false rather than an error
// on timeout.
func (tuner *Tuner) WaitForPLL(budget time.Duration) (bool, error) {
	deadline := time.Now().Add(budget)
	for {
		locked, err := tuner.PLLLocked()
		if err != nil || locked {
			return locked, err
		}
		if !time.Now().Before(deadline) {
			return false, nil
		}
		time.Sleep(time.Millisecond)
	}
}

// The divider and band for each part of the range. A frequency belongs to the
// row whose ceiling is above it and whose successor's ceiling is not, so the
// last row is a floor, not an entry. The two values' meaning is unpublished --
// the names are guesses.
var bands = [9]struct {
	ceiling uint32 // kHz
	band    byte
	divider byte
}{
	{2151000, 1, 7},
	{1950000, 1, 6},
	{1800000, 1, 5},
	{1600000, 1, 4},
	{1450000, 1, 3},
	{1250000, 1, 2},
	{1200000, 0, 7},
	{975000, 0, 6},
	{950000, 0, 0},
}

// Tune sets the chip to a frequency in kHz.
func (tuner *Tuner) Tune(frequency uint32) error {
	if frequency < FrequencyMin || frequency > FrequencyMax {
		return fmt.Errorf("QM1D1C0042 cannot tune %d kHz, which is outside %d to %d",
			frequency, FrequencyMin, FrequencyMax)
	}

	tuner.regs[0x08] = tuner.regs[0x08]&0xf0 | 0x09
	tuner.regs[0x13] = tuner.regs[0x13]&0x9f | 0x20

	// A frequency in no row would leave the top nibble as it was; the range
	// check above makes that unreachable.
	value := tuner.regs[0x02] & 0x0f
	for index := 0; index < len(bands)-1; index++ {
		if frequency < bands[index].ceiling && frequency >= bands[index+1].ceiling {
			value |= bands[index].band << 7
			value |= bands[index].divider << 4
			break
		}
	}
	if err := tuner.writeRegister(0x02, value); err != nil {
		return err
	}

	// The whole part of frequency over crystal, and the fraction in 2**-20 of a
	// crystal. The fraction is a 22-bit two's complement number because rounding
	// the whole part to nearest can leave the remainder either side of zero.
	xtal := tuner.config.XtalFreq
	whole := (frequency + xtal/2) / xtal

	tuner.regs[0x06] = tuner.regs[0x06]&0x40 | byte((whole-12)/4)
	if err := tuner.write(0x06); err != nil {
		return err
	}
	tuner.regs[0x07] = tuner.regs[0x07]&0xf0 | byte((whole-4*((whole-12)/4+1)-5)&0x0f)
	if err := tuner.write(0x07); err != nil {
		return err
	}

	// The filter, set before the fraction and again after the chip settles.
	filter := tuner.regs[0x08]
	if tuner.config.LPF {
		filter = filter&0xf0 | 0x02
	}
	if err := tuner.writeRegister(0x08, filter); err != nil {
		return err
	}

	fraction := int64(frequency)<<20/int64(xtal) - int64(whole)<<20
	if fraction < 0 {
		fraction += 1 << 22
	}
	tuner.regs[0x09] = tuner.regs[0x09]&0xc0 | byte(fraction>>16)&0x3f
	tuner.regs[0x0a] = byte(fraction >> 8)
	tuner.regs[0x0b] = byte(fraction)
	for _, register := range []byte{0x09, 0x0a, 0x0b} {
		if err := tuner.write(register); err != nil {
			return err
		}
	}

	if !tuner.config.LPF {
		if err := tuner.write(0x13); err != nil {
			return err
		}
	}

	// Start the calibration by dropping the top bits of 0x0c and putting them
	// back; the filter path calibrates one thing, the search path two.
	mask := byte(0x7f)
	if tuner.config.LPF {
		mask = 0x3f
	}
	if err := tuner.writeRegister(0x0c, tuner.regs[0x0c]&mask); err != nil {
		return err
	}
	time.Sleep(2 * time.Millisecond)
	if err := tuner.writeRegister(0x0c, tuner.regs[0x0c]|^mask); err != nil {
		return err
	}

	switch {
	case tuner.config.LPF:
		time.Sleep(tuner.config.LPFWait)
	case tuner.regs[0x03]&0x01 != 0:
		time.Sleep(tuner.config.FastSearchWait)
	default:
		time.Sleep(tuner.config.NormalSearchWait)
	}

	if tuner.config.LPF {
		if err := tuner.writeRegister(0x08, 0x09); err != nil {
			return err
		}
		if err := tuner.write(0x13); err != nil {
			return err
		}
	}
	return nil
}
